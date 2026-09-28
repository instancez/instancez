//go:build integration

package app_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

// pub is public but only lets authenticated callers see rows; open is public with no rls.
func publicBucketCfg() *domain.Config {
	return &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{
		"pub": {Public: true, RLS: []domain.RLSPolicy{
			{Operations: []string{"select"}, Using: "auth.role() = 'authenticated'"},
		}},
		"open": {Public: true},
	}}
}

func seedPublicBuckets(t *testing.T, owner domain.Database) {
	t.Helper()
	for _, stmt := range []string{
		`INSERT INTO storage.objects (bucket_id, name) VALUES ('pub', 'a.txt')`,
		`INSERT INTO storage.objects (bucket_id, name) VALUES ('open', 'b.txt')`,
	} {
		if _, err := owner.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
}

func TestIntegration_PublicBucket_GrantsNoAnonSelect(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	if err := app.NewMigrator(owner).Apply(context.Background(), publicBucketCfg()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	seedPublicBuckets(t, owner)

	if got, want := selectObjectKeys(t, req, "anon"), []string{"open/b.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("anon visible = %v, want %v", got, want)
	}
	if got, want := selectObjectKeys(t, req, "authenticated"), []string{"open/b.txt", "pub/a.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("authenticated visible = %v, want %v", got, want)
	}
}

// Live DBs still carry the old <bucket>_public_select; Harden drops it with no config change.
func TestIntegration_Harden_DropsLegacyPublicSelect(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := publicBucketCfg()
	if err := app.NewMigrator(owner).Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}
	seedPublicBuckets(t, owner)
	for _, stmt := range []string{
		`DROP POLICY IF EXISTS pub_public_select ON storage.objects`,
		`CREATE POLICY pub_public_select ON storage.objects FOR SELECT USING (bucket_id = 'pub')`,
	} {
		if _, err := owner.Exec(ctx, stmt); err != nil {
			t.Fatalf("simulate legacy policy %q: %v", stmt, err)
		}
	}
	if got := selectObjectKeys(t, req, "anon"); len(got) != 2 {
		t.Fatalf("legacy leak not reproduced: anon visible = %v", got)
	}

	for i := range 2 {
		if err := app.NewMigrator(owner).Harden(ctx, cfg); err != nil {
			t.Fatalf("harden #%d: %v", i+1, err)
		}
	}

	if got, want := selectObjectKeys(t, req, "anon"), []string{"open/b.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("post-heal anon visible = %v, want %v", got, want)
	}
}

// Both storage heals are guarded, so Harden survives an applied config whose storage.objects is gone.
func TestIntegration_Harden_StorageHeals_NoStorageSchema(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{
		"pub": {Public: true},
		"secrets": {RLS: []domain.RLSPolicy{
			{Operations: []string{"select"}, Using: "name LIKE 'ok/%'", Type: "restrictive"},
		}},
	}}
	if err := app.NewMigrator(owner).Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := owner.Exec(ctx, "DROP TABLE storage.objects"); err != nil {
		t.Fatalf("drop storage.objects: %v", err)
	}
	if err := app.NewMigrator(owner).Harden(ctx, cfg); err != nil {
		t.Fatalf("Harden without storage.objects must not fail: %v", err)
	}
}

// A bucket switched to private must not keep its old listing policy.
func TestIntegration_Harden_DropsLegacyPublicSelect_PrivateBucket(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{
		"was_pub": {RLS: []domain.RLSPolicy{{Operations: []string{"select"}, Using: "auth.role() = 'authenticated'"}}},
	}}
	if err := app.NewMigrator(owner).Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO storage.objects (bucket_id, name) VALUES ('was_pub', 'a.txt')`,
		`CREATE POLICY was_pub_public_select ON storage.objects FOR SELECT USING (bucket_id = 'was_pub')`,
	} {
		if _, err := owner.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	if got := selectObjectKeys(t, req, "anon"); len(got) != 1 {
		t.Fatalf("legacy leak not reproduced: anon visible = %v", got)
	}
	if err := app.NewMigrator(owner).Harden(ctx, cfg); err != nil {
		t.Fatalf("harden: %v", err)
	}
	if got := selectObjectKeys(t, req, "anon"); len(got) != 0 {
		t.Fatalf("post-heal anon visible = %v, want none", got)
	}
}

// Once healed, a boot must not wait on storage.objects: DDL there would queue behind any open reader.
func TestIntegration_Harden_HealedDBTakesNoStorageLock(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := storageRestrictiveCfg()
	cfg.Storage["pub"] = domain.Bucket{Public: true}
	if err := app.NewMigrator(owner).Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := app.NewMigrator(owner).Harden(ctx, cfg); err != nil {
		t.Fatalf("first harden: %v", err)
	}

	reader, err := owner.Begin(ctx)
	if err != nil {
		t.Fatalf("begin reader: %v", err)
	}
	defer func() { _ = reader.Rollback(ctx) }()
	if _, err := reader.Exec(ctx, "LOCK TABLE storage.objects IN ACCESS SHARE MODE"); err != nil {
		t.Fatalf("reader lock: %v", err)
	}

	if err := app.NewMigrator(owner).LockTimeout(time.Second).Harden(ctx, cfg); err != nil {
		t.Fatalf("second harden took a storage.objects lock: %v", err)
	}
}

// Postgres truncates "<60-char bucket>_public_select" to 63 bytes, so the heal must match the truncated name.
func TestIntegration_Harden_DropsLegacyPublicSelect_LongBucketName(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	ctx := context.Background()
	long := strings.Repeat("b", 60)
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{
		long: {Public: true, RLS: []domain.RLSPolicy{{Operations: []string{"select"}, Using: "auth.role() = 'authenticated'"}}},
	}}
	if err := app.NewMigrator(owner).Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO storage.objects (bucket_id, name) VALUES ($1, 'a.txt')`, long); err != nil {
		t.Fatalf("seed object: %v", err)
	}
	// nosemgrep -- test DDL; long is a constant bucket name and DDL can't take bind params
	if _, err := owner.Exec(ctx, `CREATE POLICY `+long+`_public_select ON storage.objects FOR SELECT USING (bucket_id = '`+long+`')`); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	if got := selectObjectKeys(t, req, "anon"); len(got) != 1 {
		t.Fatalf("legacy leak not reproduced: anon visible = %v", got)
	}

	if err := app.NewMigrator(owner).Harden(ctx, cfg); err != nil {
		t.Fatalf("harden: %v", err)
	}

	if got := selectObjectKeys(t, req, "anon"); len(got) != 0 {
		t.Fatalf("post-heal anon visible = %v, want none", got)
	}
}
