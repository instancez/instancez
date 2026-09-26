package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalStore_KeyPrefix(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewLocalStore(dir, "app123")
	if err := s.Upload(context.Background(), "avatars/x", strings.NewReader("hi"), "text/plain", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "app123", "avatars", "x")); err != nil {
		t.Fatalf("expected object under prefixed path: %v", err)
	}
}

func TestLocalStore_SignUpload(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalStore(dir, "")
	if err != nil {
		t.Fatal(err)
	}

	url, err := store.SignUpload(context.Background(), "bucket/file.txt", "text/plain", 0)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(url, "file://") {
		t.Errorf("expected file:// URL, got %q", url)
	}

	expected := filepath.Join(dir, "bucket", "file.txt")
	if !strings.Contains(url, expected) {
		t.Errorf("URL should contain path %q, got %q", expected, url)
	}

	// Parent dir should have been created
	if _, err := os.Stat(filepath.Join(dir, "bucket")); os.IsNotExist(err) {
		t.Error("expected bucket directory to be created")
	}
}

func TestLocalStore_SignDownload(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalStore(dir, "")
	if err != nil {
		t.Fatal(err)
	}

	// Returns path regardless of file existence (consumer handles missing files)
	url, err := store.SignDownload(context.Background(), "bucket/file.txt", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(url, "file://") {
		t.Errorf("expected file:// URL, got %q", url)
	}
}

func TestLocalStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalStore(dir, "")
	if err != nil {
		t.Fatal(err)
	}

	// Create a file to delete
	subdir := filepath.Join(dir, "bucket")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	filePath := filepath.Join(subdir, "test.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	err = store.Delete(context.Background(), "bucket/test.txt")
	if err != nil {
		t.Fatalf("delete error: %v", err)
	}

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}
}

func TestLocalStore_Delete_NonExistent(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalStore(dir, "")
	if err != nil {
		t.Fatal(err)
	}

	// Deleting nonexistent file should not error
	err = store.Delete(context.Background(), "bucket/nonexistent.txt")
	if err != nil {
		t.Fatalf("delete nonexistent should not error, got: %v", err)
	}
}

func TestLocalStore_EnsureBucket(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalStore(dir, "")
	if err != nil {
		t.Fatal(err)
	}

	err = store.EnsureBucket(context.Background(), "mybucket")
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, "mybucket"))
	if err != nil {
		t.Fatal("bucket dir should exist")
	}
	if !info.IsDir() {
		t.Error("bucket should be a directory")
	}
}

func TestLocalStore_ListStripsPrefix(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewLocalStore(dir, "app123")
	if err := s.Upload(context.Background(), "avatars/x", strings.NewReader("hi"), "text/plain", 2); err != nil {
		t.Fatal(err)
	}
	items, err := s.List(context.Background(), "avatars")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Key != "avatars/x" {
		t.Fatalf("expected logical key 'avatars/x' (prefix stripped), got %q", items[0].Key)
	}
}

func TestLocalStore_RejectsEscapingKeys(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "store")
	s, err := NewLocalStore(base, "app1")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Upload(ctx, "avatars/ok.txt", strings.NewReader("ok"), "text/plain", 2); err != nil {
		t.Fatalf("valid upload rejected: %v", err)
	}
	bad := []string{"../escape.txt", "avatars/../../escape.txt", "avatars/../../../escape.txt", "/etc/passwd", ""}
	for _, k := range bad {
		if err := s.Upload(ctx, k, strings.NewReader("x"), "text/plain", 1); err == nil {
			t.Errorf("Upload(%q) succeeded, want error", k)
		}
		if _, _, err := s.Download(ctx, k); err == nil {
			t.Errorf("Download(%q) succeeded, want error", k)
		}
		if err := s.Delete(ctx, k); err == nil {
			t.Errorf("Delete(%q) succeeded, want error", k)
		}
		if err := s.Copy(ctx, "avatars/ok.txt", k); err == nil {
			t.Errorf("Copy(dst=%q) succeeded, want error", k)
		}
		if err := s.Copy(ctx, k, "avatars/dst.txt"); err == nil {
			t.Errorf("Copy(src=%q) succeeded, want error", k)
		}
		if _, err := s.SignDownload(ctx, k, 0); err == nil {
			t.Errorf("SignDownload(%q) succeeded, want error", k)
		}
		if _, err := s.Head(ctx, k); err == nil {
			t.Errorf("Head(%q) succeeded, want error", k)
		}
	}
	// SignUpload/EnsureBucket run against fresh stores: several bad keys resolve to the
	// same on-disk path once keyPrefix cancels "..", and reusing s above would let a file
	// the mutating loop already wrote there mask a missing guard as an unrelated mkdir error.
	for _, k := range bad {
		fresh, err := NewLocalStore(filepath.Join(t.TempDir(), "store"), "app1")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fresh.SignUpload(ctx, k, "", 0); err == nil {
			t.Errorf("SignUpload(%q) succeeded, want error", k)
		}
		if err := fresh.EnsureBucket(ctx, k); err == nil {
			t.Errorf("EnsureBucket(%q) succeeded, want error", k)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("file written outside base: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("file escaped keyPrefix: %v", err)
	}
	if _, err := s.List(ctx, "../"); err == nil {
		t.Error("List(\"../\") succeeded, want error")
	}
}

func TestLocalStore_UnicodeAndDotKeysStayInside(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewLocalStore(dir, "")
	ctx := context.Background()
	for _, k := range []string{"avatars/ünï/çødé 😀.txt", "avatars/a..b.txt", "avatars/x/../y.txt"} {
		if err := s.Upload(ctx, k, strings.NewReader("v"), "text/plain", 1); err != nil {
			t.Errorf("Upload(%q): %v", k, err)
		}
	}
	items, err := s.List(ctx, "")
	if err != nil || len(items) != 3 {
		t.Fatalf("List(\"\") = %v, %v; want 3 items", items, err)
	}
}
