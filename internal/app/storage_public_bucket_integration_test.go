//go:build integration

package app_test

import (
	"context"
	"reflect"
	"testing"

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

func TestIntegration_Harden_PublicBucket_NoStorageSchema(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	cfg := &domain.Config{Storage: map[string]domain.Bucket{"pub": {Public: true}}}
	if err := app.NewMigrator(owner).Harden(context.Background(), cfg); err != nil {
		t.Fatalf("Harden without storage.objects must not fail: %v", err)
	}
}
