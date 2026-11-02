//go:build integration

package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
	"github.com/jackc/pgx/v5/pgconn"
)

func pkField() domain.Field { return domain.Field{Name: "id", Type: "bigserial", PrimaryKey: true} }

func fkCfg(ref string, extra map[string]domain.Table) *domain.Config {
	tables := map[string]domain.Table{"a": {Fields: []domain.Field{
		pkField(), {Name: "f", Type: "uuid", ForeignKey: &domain.ForeignKey{References: ref}},
	}}}
	for k, v := range extra {
		tables[k] = v
	}
	return &domain.Config{Version: 1, Auth: &domain.Auth{}, Tables: tables}
}

func rlsExprCfg(expr string) *domain.Config {
	on := true
	return &domain.Config{Version: 1, Tables: map[string]domain.Table{"a": {
		RLSEnabled: &on,
		Fields:     []domain.Field{pkField(), {Name: "u1", Type: "uuid"}},
		RLS:        []domain.RLSPolicy{{Operations: []string{"select"}, Using: expr}},
	}}}
}

// Every config validate newly rejects must be one Postgres rejects at migrate.
func TestValidateRejectsWhatMigrateRejects(t *testing.T) {
	appX := domain.Table{Schema: "app", Fields: []domain.Field{{Name: "id", Type: "uuid", PrimaryKey: true}}}
	cases := []struct {
		name      string
		cfg       *domain.Config
		wantPath  string
		wantState string
	}{
		{"bare users fk", fkCfg("users.id", nil), "tables.a.fields.f.foreign_key.references", "42P01"},
		{"fk to table in other schema", fkCfg("x.id", map[string]domain.Table{"x": appX}), "tables.a.fields.f.foreign_key.references", "42P01"},
		{"fk to non-unique auth column", fkCfg("auth.users.created_at", nil), "tables.a.fields.f.foreign_key.references", "42830"},
		{"fk to non-unique storage column", fkCfg("storage.objects.name", nil), "tables.a.fields.f.foreign_key.references", "42830"},
		{"fk to unknown schema", fkCfg("auht.users.id", nil), "tables.a.fields.f.foreign_key.references", "3F000"},
		{"duplicate field", &domain.Config{Version: 1, Tables: map[string]domain.Table{"a": {Fields: []domain.Field{
			pkField(), {Name: "x", Type: "text"}, {Name: "x", Type: "text"},
		}}}}, "tables.a.fields[2]", "42701"},
		{"rls doubled operator", rlsExprCfg("u1 = = auth.uid()"), "tables.a.rls[0].using", "42601"},
		{"rls unbalanced paren", rlsExprCfg("(u1 is null"), "tables.a.rls[0].using", "42601"},
		{"rls extra paren", rlsExprCfg("u1 is null)"), "tables.a.rls[0].using", "42601"},
		{"rls unterminated string", rlsExprCfg("u1::text = 'x"), "tables.a.rls[0].using", "42601"},
		{"rls trailing and", rlsExprCfg("u1 is null and"), "tables.a.rls[0].using", "42601"},
		{"rls leading or", rlsExprCfg("or u1 is null"), "tables.a.rls[0].using", "42601"},
		{"rls doubled and", rlsExprCfg("u1 is null and and u1 is null"), "tables.a.rls[0].using", "42601"},
		{"rls whitespace only", rlsExprCfg("   "), "tables.a.rls[0].using", "42601"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var found bool
			for _, e := range config.Validate(tc.cfg) {
				if strings.HasPrefix(e.Path, tc.wantPath) {
					found = true
				}
			}
			if !found {
				t.Fatalf("validate did not flag %s", tc.wantPath)
			}
			db := startPostgres(t)
			err := app.NewMigrator(db).Apply(context.Background(), tc.cfg)
			var pg *pgconn.PgError
			if !errors.As(err, &pg) || pg.Code != tc.wantState {
				t.Fatalf("migrate error = %v, want SQLSTATE %s", err, tc.wantState)
			}
		})
	}
}
