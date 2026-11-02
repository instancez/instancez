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

func TestExpandTimestamps_EmptyTable(t *testing.T) {
	cfg := tsCfg()
	ExpandShorthands(cfg)
	if len(cfg.Tables["t"].Fields) != 2 {
		t.Fatalf("fields = %+v", cfg.Tables["t"].Fields)
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
