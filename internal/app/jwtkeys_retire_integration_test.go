//go:build integration

package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
)

func TestJWTKeys_RetiredKeyCutoff(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	if err := app.NewMigrator(db).Apply(ctx, &domain.Config{Version: 1, Auth: &domain.Auth{}}); err != nil {
		t.Fatal(err)
	}
	m := app.NewJWTKeyManager(db)
	m.SetMaxTokenLifetime(15 * time.Minute)
	old, err := m.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	current, err := m.RotateActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(ctx, old.KID); err != nil {
		t.Fatalf("freshly retired key must still verify: %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE auth.jwt_keys SET retired_at = now() - interval '1 hour' WHERE kid = $1`, old.KID); err != nil {
		t.Fatal(err)
	}

	fresh := app.NewJWTKeyManager(db)
	fresh.SetMaxTokenLifetime(15 * time.Minute)
	if _, err := fresh.Get(ctx, old.KID); err == nil {
		t.Fatal("key retired past the token lifetime still verifies")
	}
	if _, err := fresh.Get(ctx, current.KID); err != nil {
		t.Fatalf("active key: %v", err)
	}
	if _, err := fresh.Get(ctx, "deadbeefdeadbeef"); err == nil {
		t.Fatal("unknown kid accepted")
	}

	// A longer lifetime keeps the same key verifying.
	lenient := app.NewJWTKeyManager(db)
	lenient.SetMaxTokenLifetime(2 * time.Hour)
	if _, err := lenient.Get(ctx, old.KID); err != nil {
		t.Fatalf("key retired within a 2h lifetime: %v", err)
	}
}

func TestJWTKeys_ActiveMintsOnceOnEmptyTable(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	if err := app.NewMigrator(db).Apply(ctx, &domain.Config{Version: 1, Auth: &domain.Auth{}}); err != nil {
		t.Fatal(err)
	}
	first, err := app.NewJWTKeyManager(db).Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.NewJWTKeyManager(db).Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.KID != first.KID {
		t.Fatalf("second manager minted %s instead of loading %s", second.KID, first.KID)
	}
	row, err := db.QueryRow(ctx, `SELECT count(*) AS n FROM auth.jwt_keys`)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := row["n"].(int64); n != 1 {
		t.Fatalf("auth.jwt_keys has %v rows, want 1", row["n"])
	}
}
