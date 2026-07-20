package app

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

// Drops are gated in serve but not in dev, where iterating on the schema
// (and losing scratch data) is the normal workflow.
func TestEngineGatesDestructiveByMode(t *testing.T) {
	tests := []struct {
		name       string
		opts       []EngineOption
		wantAllows bool
	}{
		{"dev permits drops", []EngineOption{WithMode(ModeDev)}, true},
		{"serve gates drops", []EngineOption{WithMode(ModeProd)}, false},
		{"serve with opt-in permits drops", []EngineOption{WithMode(ModeProd), WithAllowDestructive(true)}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewEngine(&domain.Config{}, domain.OwnerDB{}, domain.RequestDB{}, domain.DefaultRoles(), tt.opts...)
			if got := e.migrator.allowDestructive; got != tt.wantAllows {
				t.Errorf("allowDestructive = %v, want %v", got, tt.wantAllows)
			}
		})
	}
}

// In dev the gate is open, so the log is the only warning that data is about
// to be dropped. It must name the objects.
func TestPermittedDestructivePlanIsLogged(t *testing.T) {
	var buf bytes.Buffer
	m := NewMigrator(nil, domain.DefaultRoles()).AllowDestructive(true)
	m.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	old := parentChild("id", "parent_id")
	if _, err := m.PlanStatements(context.Background(), old, parentChild("uid", "owner_id")); err != nil {
		t.Fatalf("plan: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"parent.id", "child.parent_id"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning must name %q, got: %s", want, out)
		}
	}
}

// A plan that destroys nothing must stay quiet.
func TestAdditivePlanLogsNoWarning(t *testing.T) {
	var buf bytes.Buffer
	m := NewMigrator(nil, domain.DefaultRoles()).AllowDestructive(true)
	m.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	old := &domain.Config{Tables: map[string]domain.Table{
		"todos": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
	}}
	updated := &domain.Config{Tables: map[string]domain.Table{
		"todos": {Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "title", Type: "text"},
		}},
	}}

	if _, err := m.PlanStatements(context.Background(), old, updated); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no warning for additive plan, got: %s", buf.String())
	}
}
