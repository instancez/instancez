//go:build integration

package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

const tsYAML = `
version: 1
tables:
  items:
    timestamps: true
    fields:
      - {name: id, type: bigserial, primary_key: true}
      - {name: title, type: text}
`

func TestIntegration_TimestampsShorthand(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()
	applyYAML(t, app.NewMigrator(owner), tsYAML)

	if _, err := owner.Exec(ctx, "INSERT INTO items (title) VALUES ('a')"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := owner.Exec(ctx, "UPDATE items SET title='b'"); err != nil {
		t.Fatal(err)
	}
	rows, err := owner.Query(ctx, "SELECT updated_at > created_at AS moved FROM items")
	if err != nil || len(rows) != 1 || rows[0]["moved"] != true {
		t.Fatalf("updated_at did not advance: rows=%v err=%v", rows, err)
	}

	mig, err := owner.GetLastMigration(ctx)
	if err != nil || mig == nil {
		t.Fatalf("last migration: %v %v", mig, err)
	}
	if !strings.Contains(mig.ConfigJSON, `"updated_at"`) || strings.Contains(mig.ConfigJSON, "timestamps") {
		t.Fatalf("stored config not expanded: %s", mig.ConfigJSON)
	}
}

func TestIntegration_TimestampsRemovalIsDestructive(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	m := app.NewMigrator(owner)
	applyYAML(t, m, tsYAML)

	cfg, err := config.ParseBytes([]byte("version: 1\ntables:\n  items:\n    fields:\n      - {name: id, type: bigserial, primary_key: true}\n      - {name: title, type: text}\n"), "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(context.Background(), cfg); !errors.Is(err, app.ErrDestructive) {
		t.Fatalf("err = %v, want ErrDestructive", err)
	}
}

func TestIntegration_OwnerShorthandTable(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	applyYAML(t, app.NewMigrator(owner), `
version: 1
tables:
  notes:
    rls_enabled: true
    fields:
      - {name: id, type: bigserial, primary_key: true}
      - {name: user_id, type: uuid, required: true}
    rls:
      - owner: user_id
`)
	ins := func(as, uid string) error {
		return asUser(t, req, as, func(ctx context.Context, tx domain.Tx) error {
			_, err := tx.Exec(ctx, "INSERT INTO notes (user_id) VALUES ($1)", uid)
			return err
		})
	}
	if err := ins(u1, u1); err != nil {
		t.Fatalf("own insert: %v", err)
	}
	if err := ins(u2, u1); err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("insert as other user = %v, want RLS violation", err)
	}
	if _, err := owner.Exec(context.Background(), "INSERT INTO notes (user_id) VALUES ($1)", u1); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = asUser(t, req, u2, func(ctx context.Context, tx domain.Tx) error {
		rows, err := tx.Query(ctx, "SELECT id FROM notes")
		n = len(rows)
		return err
	})
	if n != 0 {
		t.Fatalf("u2 sees %d of u1's rows", n)
	}
}

func TestIntegration_OwnerShorthandBucket(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	applyYAML(t, app.NewMigrator(owner), "version: 1\nauth: {}\nproviders: {storage: {type: local}}\nstorage:\n  b:\n    rls:\n      - {owner: uploaded_by, operations: [insert]}\n")
	seedUsers(t, owner)
	ins := func(as, uploader string) error {
		return asUser(t, req, as, func(ctx context.Context, tx domain.Tx) error {
			_, err := tx.Exec(ctx, "INSERT INTO storage.objects (bucket_id, name, uploaded_by) VALUES ('b', 'f', $1::uuid)", uploader)
			return err
		})
	}
	if err := ins(u1, u1); err != nil {
		t.Fatalf("own insert: %v", err)
	}
	if err := ins(u2, u1); err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("foreign insert = %v, want RLS violation", err)
	}
}

func TestIntegration_RawOwnerPolicyAddsColumnsOnExistingTable(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	m := app.NewMigrator(owner)
	storageYAML := func(rls string) string {
		return "version: 1\nauth: {}\nproviders: {storage: {type: local}}\nstorage:\n  b:\n    rls:\n      - " + rls + "\n"
	}
	applyYAML(t, m, storageYAML(`{operations: [select], using: "true"}`))
	for _, c := range []string{"owner", "owner_id"} {
		// nosemgrep -- c iterates the fixed column names owner and owner_id
		if _, err := owner.Exec(context.Background(), "ALTER TABLE storage.objects DROP COLUMN "+c); err != nil {
			t.Fatal(err)
		}
	}
	applyYAML(t, m, storageYAML(`{operations: [insert], with_check: "owner = auth.uid()"}`))
	seedUsers(t, owner)
	ins := func(as, uploader string) error {
		return asUser(t, req, as, func(ctx context.Context, tx domain.Tx) error {
			_, err := tx.Exec(ctx, "INSERT INTO storage.objects (bucket_id, name, uploaded_by) VALUES ('b', 'f', $1::uuid)", uploader)
			return err
		})
	}
	if err := ins(u1, u1); err != nil {
		t.Fatalf("own insert: %v", err)
	}
	if err := ins(u2, u1); err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("foreign insert = %v, want RLS violation", err)
	}
}

func TestIntegration_IncludeMigrates(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("instancez.yaml", "version: 1\ninclude: [tables/*.yaml]\n")
	write("tables/notes.yaml", "tables:\n  notes:\n    rls_enabled: true\n    fields:\n      - {name: id, type: bigserial, primary_key: true}\n")

	src := &config.FileSource{Path: filepath.Join(dir, "instancez.yaml")}
	cfg, err := src.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m := app.NewMigrator(owner)
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply v1: %v", err)
	}

	write("tables/notes.yaml", "tables:\n  notes:\n    rls_enabled: true\n    fields:\n      - {name: id, type: bigserial, primary_key: true}\n      - {name: body, type: text}\n")
	data, _, err := src.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg2, _, err := config.ParseSourceBytes(src, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, cfg2); err != nil {
		t.Fatalf("apply v2: %v", err)
	}
	rows, err := owner.Query(ctx, "SELECT count(*) AS n FROM information_schema.columns WHERE table_name='notes' AND column_name='body'")
	if err != nil || rows[0]["n"].(int64) != 1 {
		t.Fatalf("body column missing: %v %v", rows, err)
	}
}
