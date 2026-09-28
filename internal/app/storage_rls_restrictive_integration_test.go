//go:build integration

package app_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

// bucket_a: permissive allow-all plus two restrictive policies (names under
// "ok/", and no "secret" in the name). bucket_b: permissive allow-all only.
func storageRestrictiveCfg() *domain.Config {
	return &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{
		"bucket_a": {RLS: []domain.RLSPolicy{
			{Operations: []string{"select"}, Using: "true"},
			{Operations: []string{"select"}, Using: "name LIKE 'ok/%'", Type: "restrictive"},
			{Operations: []string{"select"}, Using: "name NOT LIKE '%secret%'", Type: "restrictive"},
			{Operations: []string{"insert"}, WithCheck: "true"},
			{Operations: []string{"insert"}, WithCheck: "name LIKE 'ok/%'", Type: "restrictive"},
		}},
		"bucket_b": {RLS: []domain.RLSPolicy{
			{Operations: []string{"select", "insert"}, Using: "true", WithCheck: "true"},
		}},
	}}
}

func selectObjectKeys(t *testing.T, req domain.RequestDB, role string) []string {
	t.Helper()
	var keys []string
	err := asRole(t, req, role, func(ctx context.Context, tx domain.Tx) error {
		rows, err := tx.Query(ctx, "SELECT bucket_id || '/' || name AS k FROM storage.objects ORDER BY k")
		if err != nil {
			return err
		}
		for _, r := range rows {
			keys = append(keys, r["k"].(string))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s select: %v", role, err)
	}
	return keys
}

// T6: bucket_a's restrictive policies must not deny bucket_b's rows.
func TestIntegration_RestrictiveStoragePolicy_DoesNotDenyOtherBuckets(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	ctx := context.Background()

	if err := app.NewMigrator(owner).Apply(ctx, storageRestrictiveCfg()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	for _, stmt := range []string{
		`INSERT INTO storage.objects (bucket_id, name) VALUES ('bucket_a', 'ok/1')`,
		`INSERT INTO storage.objects (bucket_id, name) VALUES ('bucket_a', 'private/1')`,
		`INSERT INTO storage.objects (bucket_id, name) VALUES ('bucket_a', 'ok/secret')`,
		`INSERT INTO storage.objects (bucket_id, name) VALUES ('bucket_b', 'any/1')`,
	} {
		if _, err := owner.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}

	got := selectObjectKeys(t, req, "authenticated")
	want := []string{"bucket_a/ok/1", "bucket_b/any/1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("visible = %v, want %v", got, want)
	}
}

// T6 review focus: a restrictive with_check on bucket_a must not block bucket_b inserts.
func TestIntegration_RestrictiveStorageWithCheck_DoesNotBlockOtherBucketInserts(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	ctx := context.Background()

	if err := app.NewMigrator(owner).Apply(ctx, storageRestrictiveCfg()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if err := asRole(t, req, "authenticated", func(ctx context.Context, tx domain.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO storage.objects (bucket_id, name) VALUES ('bucket_b', 'new/1')")
		return err
	}); err != nil {
		t.Fatalf("bucket_b insert blocked by bucket_a's restrictive with_check: %v", err)
	}

	if err := asRole(t, req, "authenticated", func(ctx context.Context, tx domain.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO storage.objects (bucket_id, name) VALUES ('bucket_a', 'ok/2')")
		return err
	}); err != nil {
		t.Fatalf("bucket_a matching insert should succeed: %v", err)
	}

	err := asRole(t, req, "authenticated", func(ctx context.Context, tx domain.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO storage.objects (bucket_id, name) VALUES ('bucket_a', 'private/2')")
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("bucket_a non-matching insert = %v, want a row-level security violation", err)
	}
}

// T6 heal: a DB stuck with the pre-fix AND-scoped restrictive policy gets the
// fixed OR-scoping from Harden alone, with no config change.
func TestIntegration_Harden_HealsRestrictiveBucketPolicyOnUnchangedConfig(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := storageRestrictiveCfg()

	if err := app.NewMigrator(owner).Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Overwrite bucket_a's restrictive policies with the pre-fix AND-scoping,
	// simulating a DB migrated before the T6 fix shipped.
	for _, stmt := range []string{
		`DROP POLICY IF EXISTS storage_bucket_a_select_1 ON storage.objects;`,
		`CREATE POLICY storage_bucket_a_select_1 ON storage.objects AS RESTRICTIVE FOR SELECT USING (bucket_id = 'bucket_a' AND (name LIKE 'ok/%'));`,
		`DROP POLICY IF EXISTS storage_bucket_a_select_2 ON storage.objects;`,
		`CREATE POLICY storage_bucket_a_select_2 ON storage.objects AS RESTRICTIVE FOR SELECT USING (bucket_id = 'bucket_a' AND (name NOT LIKE '%secret%'));`,
	} {
		if _, err := owner.Exec(ctx, stmt); err != nil {
			t.Fatalf("simulate pre-fix DDL %q: %v", stmt, err)
		}
	}

	if _, err := owner.Exec(ctx, `INSERT INTO storage.objects (bucket_id, name) VALUES ('bucket_b', 'any/1')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if got := selectObjectKeys(t, req, "authenticated"); len(got) != 0 {
		t.Fatalf("bug not reproduced pre-heal: bucket_b visible = %v, want none (AND-scoped restrictive should hide it)", got)
	}

	if err := app.NewMigrator(owner).Harden(ctx, cfg); err != nil {
		t.Fatalf("harden: %v", err)
	}

	got := selectObjectKeys(t, req, "authenticated")
	want := []string{"bucket_b/any/1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("post-heal visible = %v, want %v", got, want)
	}
}
