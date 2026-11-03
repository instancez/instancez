//go:build integration

package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

func ownerBucketCfg(using, check string) *domain.Config {
	return &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{
		"b": {RLS: []domain.RLSPolicy{{Operations: []string{"select", "insert"}, Using: using, WithCheck: check}}},
	}}
}

func seedUsers(t *testing.T, owner domain.Database) {
	t.Helper()
	for i, id := range []string{u1, u2} {
		if _, err := owner.Exec(context.Background(), `INSERT INTO auth.users (id, email) VALUES ($1::uuid, $2)`, id, string(rune('a'+i))+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIntegration_StorageOwnerColumnsGenerated(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := ownerBucketCfg("owner_id = auth.uid()::text", "owner_id = auth.uid()::text")
	if err := app.NewMigrator(owner).Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}
	seedUsers(t, owner)

	ins := func(as, uploader string) error {
		return asUser(t, req, as, func(ctx context.Context, tx domain.Tx) error {
			_, err := tx.Exec(ctx, "INSERT INTO storage.objects (bucket_id, name, uploaded_by) VALUES ('b', $1, $2::uuid)", uploader+"/x.txt", uploader)
			return err
		})
	}
	if err := ins(u1, u1); err != nil {
		t.Fatalf("own insert: %v", err)
	}
	if err := ins(u1, u2); err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("foreign insert = %v, want RLS violation (WITH CHECK must see generated owner_id)", err)
	}
	if _, err := owner.Exec(ctx, "INSERT INTO storage.objects (bucket_id, name, uploaded_by) VALUES ('b', 'k', $1::uuid)", u1); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, "DELETE FROM auth.users WHERE id = $1::uuid", u1); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	rows, err := owner.Query(ctx, "SELECT count(*) AS n FROM storage.objects WHERE name = 'k' AND owner IS NULL AND owner_id IS NULL")
	if err != nil || rows[0]["n"].(int64) != 1 {
		t.Fatalf("owner columns not nulled after user delete: %v %v", rows, err)
	}
}

func TestIntegration_StorageHelpersMatchSupabase(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	ctx := context.Background()
	if err := app.NewMigrator(owner).Apply(ctx, ownerBucketCfg("true", "")); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Reference expressions are the bodies of supabase/storage tenant migration 0002-storage-schema.sql.
	for _, v := range []string{"", "a", "a/", "file", "a/b/c.png", "a/b.tar.gz"} {
		rows, err := owner.Query(ctx, `SELECT
  storage.foldername($1) IS NOT DISTINCT FROM (string_to_array($1,'/'))[1:array_length(string_to_array($1,'/'),1)-1] AS f,
  storage.filename($1) IS NOT DISTINCT FROM (string_to_array($1,'/'))[array_length(string_to_array($1,'/'),1)] AS n,
  storage.extension($1) IS NOT DISTINCT FROM reverse(split_part(reverse((string_to_array($1,'/'))[array_length(string_to_array($1,'/'),1)]), '.', 1)) AS e`, v)
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		for k, ok := range rows[0] {
			if ok != true {
				t.Errorf("%q: storage.%s differs from supabase definition", v, k)
			}
		}
	}
	var got string
	err := asRole(t, req, "anon", func(ctx context.Context, tx domain.Tx) error {
		rows, err := tx.Query(ctx, "SELECT storage.foldername('a/b/c.png')::text AS v")
		if err == nil {
			got = rows[0]["v"].(string)
		}
		return err
	})
	if err != nil || got != "{a,b}" {
		t.Fatalf("anon foldername = %q, %v", got, err)
	}
}

func TestIntegration_StorageOwnerColumnsAddedOnExistingTable(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()
	m := app.NewMigrator(owner)
	if err := m.Apply(ctx, ownerBucketCfg("true", "")); err != nil {
		t.Fatalf("v1: %v", err)
	}
	for _, c := range []string{"owner", "owner_id"} {
		// nosemgrep -- c iterates the fixed column names owner and owner_id
		if _, err := owner.Exec(ctx, "ALTER TABLE storage.objects DROP COLUMN "+c); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Apply(ctx, ownerBucketCfg("owner = auth.uid()", "")); err != nil {
		t.Fatalf("v2: %v", err)
	}
	count := func() int64 {
		rows, err := owner.Query(ctx, "SELECT count(*) AS n FROM information_schema.columns WHERE table_schema='storage' AND table_name='objects' AND column_name IN ('owner','owner_id')")
		if err != nil {
			t.Fatal(err)
		}
		return rows[0]["n"].(int64)
	}
	if n := count(); n != 2 {
		t.Fatalf("owner columns after v2 = %d, want 2", n)
	}
	if err := m.Apply(ctx, ownerBucketCfg("true", "")); err != nil {
		t.Fatalf("v3: %v", err)
	}
	if n := count(); n != 2 {
		t.Fatalf("owner columns after v3 = %d, want 2 (kept)", n)
	}
}

func TestIntegration_StorageOwnerHealAddsOnlyTheMissingColumn(t *testing.T) {
	owner, _ := dbboot.StartContainer(t)
	ctx := context.Background()
	m := app.NewMigrator(owner)
	if err := m.Apply(ctx, ownerBucketCfg("true", "")); err != nil {
		t.Fatalf("v1: %v", err)
	}
	if _, err := owner.Exec(ctx, "ALTER TABLE storage.objects DROP COLUMN owner_id"); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, ownerBucketCfg("owner_id = auth.uid()::text", "")); err != nil {
		t.Fatalf("v2: %v", err)
	}
	rows, err := owner.Query(ctx, "SELECT count(*) AS n FROM information_schema.columns WHERE table_schema='storage' AND table_name='objects' AND column_name IN ('owner','owner_id')")
	if err != nil || rows[0]["n"].(int64) != 2 {
		t.Fatalf("columns after heal: %v %v", rows, err)
	}
}
