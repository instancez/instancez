package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func tsCfg(fields ...domain.Field) *domain.Config {
	return &domain.Config{Tables: map[string]domain.Table{"t": {Timestamps: true, Fields: fields}}}
}

func TestExpandTimestamps_AppendsFields(t *testing.T) {
	cfg := tsCfg(domain.Field{Name: "id", Type: "bigserial", PrimaryKey: true})
	ExpandShorthands(cfg)

	tb := cfg.Tables["t"]
	if tb.Timestamps {
		t.Fatal("Timestamps must be cleared")
	}
	want := []domain.Field{
		{Name: "id", Type: "bigserial", PrimaryKey: true},
		{Name: "created_at", Type: "timestamptz", Required: true, Default: "now()"},
		{Name: "updated_at", Type: "timestamptz", Required: true, Default: "now()", AutoUpdatedAt: true},
	}
	if !reflect.DeepEqual(tb.Fields, want) {
		t.Fatalf("fields = %+v", tb.Fields)
	}
	if len(cfg.ExpandErrors) != 0 {
		t.Fatalf("errors: %v", cfg.ExpandErrors)
	}
}

func TestExpandTimestamps_Idempotent(t *testing.T) {
	cfg := tsCfg(domain.Field{Name: "id", Type: "bigserial", PrimaryKey: true})
	ExpandShorthands(cfg)
	first := reflect.ValueOf(cfg.Tables["t"].Fields).Len()
	ExpandShorthands(cfg)
	if got := len(cfg.Tables["t"].Fields); got != first || len(cfg.ExpandErrors) != 0 {
		t.Fatalf("second run changed fields (%d -> %d) or errored: %v", first, got, cfg.ExpandErrors)
	}
}

func TestExpandTimestamps_ConflictWithDeclaredField(t *testing.T) {
	cfg := tsCfg(
		domain.Field{Name: "id", Type: "bigserial", PrimaryKey: true},
		domain.Field{Name: "updated_at", Type: "timestamptz"},
	)
	ExpandShorthands(cfg)

	if len(cfg.ExpandErrors) != 1 || cfg.ExpandErrors[0].Path != "tables.t.timestamps" ||
		!strings.Contains(cfg.ExpandErrors[0].Message, "updated_at") {
		t.Fatalf("errors: %v", cfg.ExpandErrors)
	}
	if len(cfg.Tables["t"].Fields) != 2 || !cfg.Tables["t"].Timestamps {
		t.Fatal("conflicting table must stay unexpanded")
	}
	// Re-running must not duplicate the error.
	ExpandShorthands(cfg)
	if len(cfg.ExpandErrors) != 1 {
		t.Fatalf("duplicate errors: %v", cfg.ExpandErrors)
	}
}

func TestExpandShorthands_NilAndEmpty(t *testing.T) {
	ExpandShorthands(&domain.Config{})
	cfg := &domain.Config{Tables: map[string]domain.Table{"e": {}, "f": {Timestamps: false, Fields: []domain.Field{{Name: "id"}}}}}
	ExpandShorthands(cfg)
	if len(cfg.Tables["e"].Fields) != 0 || len(cfg.Tables["f"].Fields) != 1 || len(cfg.ExpandErrors) != 0 {
		t.Fatalf("unexpected change: %+v %v", cfg.Tables, cfg.ExpandErrors)
	}
}

func TestParseBytes_TimestampsExpands(t *testing.T) {
	cfg, err := ParseBytes([]byte("version: 1\ntables:\n  t:\n    timestamps: true\n    fields:\n      - {name: id, type: bigserial, primary_key: true}\n"), "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tables["t"].Fields) != 3 || len(cfg.UnknownKeys) != 0 {
		t.Fatalf("got %+v unknown=%v", cfg.Tables["t"].Fields, cfg.UnknownKeys)
	}
}

func TestUnmarshalConfigJSON_TimestampsExpands(t *testing.T) {
	cfg, err := UnmarshalConfigJSON([]byte(`{"version":1,"tables":{"t":{"timestamps":true,"fields":[{"name":"id","type":"bigserial","primary_key":true}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tables["t"].Fields) != 3 || cfg.Tables["t"].Timestamps {
		t.Fatalf("got %+v", cfg.Tables["t"])
	}
}

func TestValidate_SurfacesExpandErrors(t *testing.T) {
	cfg := validBaseConfig()
	tb := cfg.Tables["todos"]
	tb.Timestamps = true
	tb.Fields = append(tb.Fields, domain.Field{Name: "created_at", Type: "timestamptz"})
	cfg.Tables["todos"] = tb
	ExpandShorthands(cfg)
	assertHasErrorAt(t, Validate(cfg), "tables.todos.timestamps")
}

func ownerCfg(pol domain.RLSPolicy, fields ...domain.Field) *domain.Config {
	if fields == nil {
		fields = []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}, {Name: "user_id", Type: "uuid"}}
	}
	return &domain.Config{Tables: map[string]domain.Table{"t": {Fields: fields, RLS: []domain.RLSPolicy{pol}}}}
}

func TestExpandOwner_DefaultsAllFourOps(t *testing.T) {
	cfg := ownerCfg(domain.RLSPolicy{Owner: "user_id"})
	ExpandShorthands(cfg)
	p := cfg.Tables["t"].RLS[0]
	if !reflect.DeepEqual(p.Operations, []string{"select", "insert", "update", "delete"}) ||
		p.Using != "user_id = auth.uid()" || p.WithCheck != "user_id = auth.uid()" || p.Owner != "" {
		t.Fatalf("got %+v", p)
	}
	if len(cfg.ExpandErrors) != 0 {
		t.Fatal(cfg.ExpandErrors)
	}
}

func TestExpandOwner_InsertOnly(t *testing.T) {
	cfg := ownerCfg(domain.RLSPolicy{Operations: []string{"insert"}, Owner: "user_id"})
	ExpandShorthands(cfg)
	if p := cfg.Tables["t"].RLS[0]; p.Using != "" || p.WithCheck != "user_id = auth.uid()" {
		t.Fatalf("got %+v", p)
	}
}

func TestExpandOwner_SelectOnly(t *testing.T) {
	cfg := ownerCfg(domain.RLSPolicy{Operations: []string{"select"}, Owner: "user_id"})
	ExpandShorthands(cfg)
	if p := cfg.Tables["t"].RLS[0]; p.WithCheck != "" || p.Using != "user_id = auth.uid()" {
		t.Fatalf("got %+v", p)
	}
}

func expectOwnerErr(t *testing.T, cfg *domain.Config, path, msg string) {
	t.Helper()
	ExpandShorthands(cfg)
	if len(cfg.ExpandErrors) != 1 || cfg.ExpandErrors[0].Path != path || !strings.Contains(cfg.ExpandErrors[0].Message, msg) {
		t.Fatalf("errors = %v, want %s containing %q", cfg.ExpandErrors, path, msg)
	}
}

func TestExpandOwner_ConflictsWithUsing(t *testing.T) {
	cfg := ownerCfg(domain.RLSPolicy{Owner: "user_id", Using: "true"})
	expectOwnerErr(t, cfg, "tables.t.rls[0].owner", "cannot be combined")
	if cfg.Tables["t"].RLS[0].Owner != "user_id" {
		t.Fatal("conflicting policy must stay unexpanded")
	}
}

func TestExpandOwner_UnknownColumn(t *testing.T) {
	expectOwnerErr(t, ownerCfg(domain.RLSPolicy{Owner: "uid"}), "tables.t.rls[0].owner", `column "uid" not found`)
}

func TestExpandOwner_NonUUIDColumn(t *testing.T) {
	cfg := ownerCfg(domain.RLSPolicy{Owner: "user_id"}, domain.Field{Name: "id", Type: "bigserial", PrimaryKey: true}, domain.Field{Name: "user_id", Type: "text"})
	expectOwnerErr(t, cfg, "tables.t.rls[0].owner", "must be uuid")
}

func TestExpandOwner_FKToAuthUsersUntyped(t *testing.T) {
	cfg := ownerCfg(domain.RLSPolicy{Owner: "user_id"},
		domain.Field{Name: "id", Type: "bigserial", PrimaryKey: true},
		domain.Field{Name: "user_id", ForeignKey: &domain.ForeignKey{References: "auth.users.id"}})
	ExpandShorthands(cfg)
	if len(cfg.ExpandErrors) != 0 || cfg.Tables["t"].RLS[0].Using == "" {
		t.Fatalf("errors=%v policy=%+v", cfg.ExpandErrors, cfg.Tables["t"].RLS[0])
	}
}

func TestExpandOwner_InvalidIdent(t *testing.T) {
	expectOwnerErr(t, ownerCfg(domain.RLSPolicy{Owner: "x; drop"}), "tables.t.rls[0].owner", "invalid identifier")
}

func TestExpandOwner_Bucket(t *testing.T) {
	cfg := &domain.Config{Storage: map[string]domain.Bucket{"a": {RLS: []domain.RLSPolicy{{Owner: "uploaded_by", Operations: []string{"select"}}}}}}
	ExpandShorthands(cfg)
	r := cfg.Storage["a"].RLS
	if len(cfg.ExpandErrors) != 0 || r[0].Using != "uploaded_by = auth.uid()" || r[0].Owner != "" {
		t.Fatalf("errors=%v rls=%+v", cfg.ExpandErrors, r)
	}

	bad := &domain.Config{Storage: map[string]domain.Bucket{"avatars": {RLS: []domain.RLSPolicy{{Owner: "user_id"}}}}}
	expectOwnerErr(t, bad, "storage.avatars.rls[0].owner", "must be uploaded_by")

	alias := &domain.Config{Storage: map[string]domain.Bucket{"a": {RLS: []domain.RLSPolicy{{Owner: "owner"}}}}}
	expectOwnerErr(t, alias, "storage.a.rls[0].owner", "must be uploaded_by")
}

func TestExpandOwner_Idempotent(t *testing.T) {
	cfg := ownerCfg(domain.RLSPolicy{Owner: "user_id"})
	ExpandShorthands(cfg)
	first := cfg.Tables["t"].RLS[0]
	ExpandShorthands(cfg)
	if !reflect.DeepEqual(first, cfg.Tables["t"].RLS[0]) || len(cfg.ExpandErrors) != 0 {
		t.Fatal("second expansion changed the policy")
	}
}

func TestExpandOwner_DoesNotMutateSharedSlice(t *testing.T) {
	orig := []domain.RLSPolicy{{Owner: "user_id"}}
	cfg := &domain.Config{Tables: map[string]domain.Table{"t": {Fields: []domain.Field{{Name: "user_id", Type: "uuid"}}, RLS: orig}}}
	ExpandShorthands(cfg)
	if orig[0].Owner != "user_id" {
		t.Fatal("expansion must copy the policy slice, not mutate the caller's")
	}
}
