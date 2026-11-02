//go:build integration

package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

const (
	u1 = "11111111-1111-1111-1111-111111111111"
	u2 = "22222222-2222-2222-2222-222222222222"
)

func asUser(t *testing.T, req domain.RequestDB, uid string, fn func(ctx context.Context, tx domain.Tx) error) error {
	t.Helper()
	ctx, err := req.WithRLS(context.Background(), domain.Session{Role: "authenticated", UserID: uid, IsAuthenticated: true})
	if err != nil {
		t.Fatalf("WithRLS: %v", err)
	}
	tx, err := req.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	return fn(ctx, tx)
}

func applyYAML(t *testing.T, m *app.Migrator, y string) *domain.Config {
	t.Helper()
	cfg, err := config.ParseBytes([]byte(y), "test")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatalf("validate: %v", errs[0])
	}
	if err := m.Apply(context.Background(), cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return cfg
}

func TestIntegration_PartialOpsPolicy(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	applyYAML(t, app.NewMigrator(owner), `
version: 1
tables:
  notes:
    rls_enabled: true
    fields:
      - {name: id, type: bigserial, primary_key: true}
      - {name: user_id, type: uuid}
    rls:
      - operations: [select]
        using: "true"
      - operations: [insert, update]
        using: "user_id = auth.uid()"
        with_check: "user_id = auth.uid()"
`)
	err := asUser(t, req, u1, func(ctx context.Context, tx domain.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO notes (user_id) VALUES ($1)", u1)
		return err
	})
	if err != nil {
		t.Fatalf("own insert: %v", err)
	}
	err = asUser(t, req, u1, func(ctx context.Context, tx domain.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO notes (user_id) VALUES ($1)", u2)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("foreign insert = %v, want RLS violation", err)
	}
	if _, err := owner.Exec(context.Background(), "INSERT INTO notes (user_id) VALUES ($1)", u1); err != nil {
		t.Fatal(err)
	}
	var n int64
	err = asUser(t, req, u1, func(ctx context.Context, tx domain.Tx) error {
		var err error
		n, err = tx.Exec(ctx, "DELETE FROM notes")
		return err
	})
	if err != nil || n != 0 {
		t.Fatalf("delete affected %d (err %v), want 0 with no delete policy", n, err)
	}
}
