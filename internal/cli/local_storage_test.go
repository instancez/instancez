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
	os.MkdirAll(subdir, 0o755)
	filePath := filepath.Join(subdir, "test.txt")
	os.WriteFile(filePath, []byte("hello"), 0o644)

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
