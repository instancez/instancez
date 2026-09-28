package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func TestGenerateTable_PrimaryKeyShapes(t *testing.T) {
	cases := []struct {
		name   string
		fields []domain.Field
		want   string
	}{
		{"single", []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "title", Type: "text"},
		}, "CREATE TABLE IF NOT EXISTS t (\n  id bigserial PRIMARY KEY,\n  title text\n);"},
		{"two", []domain.Field{
			{Name: "note", Type: "text"},
			{Name: "user_id", Type: "uuid", PrimaryKey: true},
			{Name: "group_id", Type: "bigint", PrimaryKey: true, Required: true},
		}, "CREATE TABLE IF NOT EXISTS t (\n  user_id uuid,\n  group_id bigint NOT NULL,\n  note text,\n  PRIMARY KEY (user_id, group_id)\n);"},
		{"three with fk and unique", []domain.Field{
			{Name: "c", Type: "text", PrimaryKey: true},
			{Name: "a", Type: "int", PrimaryKey: true, Unique: true},
			{Name: "b", ForeignKey: &domain.ForeignKey{References: "p.id"}, PrimaryKey: true},
		}, "CREATE TABLE IF NOT EXISTS t (\n  c text,\n  a int,\n  b BIGINT,\n  UNIQUE (a),\n  FOREIGN KEY (b) REFERENCES public.p(id) ON DELETE RESTRICT,\n  PRIMARY KEY (c, a, b)\n);"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := generateTable("t", domain.Table{Fields: c.fields}, nil)
			if len(got) != 1 || got[0] != c.want {
				t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), c.want)
			}
		})
	}
}

func TestGenerateTable_CompositePKInNonPublicSchema(t *testing.T) {
	got := generateTable("t", domain.Table{Schema: "app", Fields: []domain.Field{
		{Name: "a", Type: "int", PrimaryKey: true},
		{Name: "b", Type: "int", PrimaryKey: true},
	}}, nil)[0]
	if !strings.HasPrefix(got, "CREATE TABLE IF NOT EXISTS app.t") || !strings.Contains(got, "PRIMARY KEY (a, b)") {
		t.Fatalf("got %s", got)
	}
}

func pkTable(fields ...domain.Field) *domain.Config {
	return &domain.Config{Tables: map[string]domain.Table{"t": {Fields: fields}}}
}

func TestPlanStatements_PrimaryKeyChange(t *testing.T) {
	a := domain.Field{Name: "a", Type: "int", PrimaryKey: true}
	b := domain.Field{Name: "b", Type: "int", PrimaryKey: true}
	plainB := domain.Field{Name: "b", Type: "int"}
	c := domain.Field{Name: "c", Type: "int", PrimaryKey: true}
	cases := []struct {
		name     string
		old, new *domain.Config
		wantErr  string
	}{
		{"unchanged composite", pkTable(a, b), pkTable(b, a), ""},
		{"composite to single by unflag", pkTable(a, b), pkTable(a, plainB), "t: (a, b) -> (a)"},
		{"single to composite by flag", pkTable(a, plainB), pkTable(a, b), "t: (a) -> (a, b)"},
		{"add a pk column", pkTable(a), pkTable(a, b), "t: (a) -> (a, b)"},
		{"swap single pk flag", pkTable(a, plainB), pkTable(domain.Field{Name: "a", Type: "int"}, b), "t: (a) -> (b)"},
		{"drop composite member and add new composite", pkTable(a, b), pkTable(a, c), "t: (a, b) -> (a, c)"},
		{"rename pk column", pkTable(a, b), pkTable(a, domain.Field{Name: "bb", Type: "int", PrimaryKey: true, RenamedFrom: "b"}), ""},
		{"replace single pk by drop and add", pkTable(a), pkTable(c), ""},
		{"new table", &domain.Config{}, pkTable(a, b), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMigrator(nil, domain.DefaultRoles()).AllowDestructive(true)
			m.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
			stmts, err := m.PlanStatements(context.Background(), tc.old, tc.new)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrPrimaryKeyChange) {
				t.Fatalf("want ErrPrimaryKeyChange, got %v (stmts %d)", err, len(stmts))
			}
			if errors.Is(err, ErrDestructive) {
				t.Fatal("a primary key change must not look destructive")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q must contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestDiffConfigs_UniqueIndexOnLiveColumnPrecedesNewFK(t *testing.T) {
	devices := func(idx ...domain.Index) domain.Table {
		return domain.Table{Fields: []domain.Field{
			{Name: "tenant_id", Type: "uuid", PrimaryKey: true},
			{Name: "serial", Type: "text", PrimaryKey: true},
		}, Indexes: idx}
	}
	old := &domain.Config{Version: 1, Tables: map[string]domain.Table{"devices": devices()}}
	next := devices(
		domain.Index{Columns: []string{"serial"}, Unique: true},
		domain.Index{Columns: []string{"tenant_id"}},
		domain.Index{Columns: []string{"tenant_id"}, Unique: true, Where: "serial <> ''"},
		domain.Index{Columns: []string{"model"}, Unique: true},
	)
	next.Fields = append(next.Fields, domain.Field{Name: "model", Type: "text"})
	add := strings.Join(diffConfigs(old, &domain.Config{Version: 1, Tables: map[string]domain.Table{
		"devices":  next,
		"readings": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}, {Name: "device_serial", ForeignKey: &domain.ForeignKey{References: "devices.serial"}}}},
	}}).Additions, "\n")
	idx, table := strings.Index(add, "idx_devices_serial"), strings.Index(add, "CREATE TABLE IF NOT EXISTS readings")
	if idx < 0 || table < 0 || idx > table {
		t.Fatalf("unique index must precede the new FK table:\n%s", add)
	}
	if strings.Count(add, "idx_devices_tenant_id") != 0 || strings.Contains(add, "idx_devices_model") {
		t.Fatalf("only plain unique indexes on live columns go early:\n%s", add)
	}
}
