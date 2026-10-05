//go:build integration

package auth

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

func TestDeleteUserKeepsStorageObjects(t *testing.T) {
	ctx := context.Background()
	owner, req := dbboot.StartContainer(t)
	cfg := &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: map[string]domain.Bucket{"b": {}}}
	if err := app.NewMigrator(owner).Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	s := NewService(req.Database, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	uid := mustUser(t, s, "owner@example.com")
	if _, err := owner.Exec(ctx, `INSERT INTO storage.objects (bucket_id, name, uploaded_by) VALUES ('b', 'f.txt', $1::uuid)`, uid); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, uid); err != nil {
		t.Fatalf("delete user with object: %v", err)
	}
	row, err := owner.QueryRow(ctx, `SELECT uploaded_by IS NULL AS orphaned FROM storage.objects WHERE name = 'f.txt'`)
	if err != nil || row == nil || row["orphaned"] != true {
		t.Fatalf("object must remain with uploaded_by NULL: row=%v err=%v", row, err)
	}
}
