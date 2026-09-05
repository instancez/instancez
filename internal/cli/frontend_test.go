package cli

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func writeDistFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectFrontendFiles(t *testing.T) {
	dir := t.TempDir()
	writeDistFile(t, dir, "index.html", "<!doctype html>")
	writeDistFile(t, dir, "assets/app.abc.js", "console.log(1)")
	writeDistFile(t, dir, "nested/page/data.json", "{}")

	files, err := collectFrontendFiles(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("want 3 files, got %d: %v", len(files), files)
	}
	// forward-slash keys, relative to dir
	for _, want := range []string{"index.html", "assets/app.abc.js", "nested/page/data.json"} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing key %q in %v", want, files)
		}
	}
	// contents are base64-encoded round-trippable
	raw, err := base64.StdEncoding.DecodeString(files["index.html"])
	if err != nil || string(raw) != "<!doctype html>" {
		t.Errorf("index.html decode = %q, %v", raw, err)
	}
}

func TestCollectFrontendFiles_NoIndex(t *testing.T) {
	dir := t.TempDir()
	writeDistFile(t, dir, "assets/app.js", "x")
	if _, err := collectFrontendFiles(dir); err == nil {
		t.Fatal("expected error when index.html is missing")
	}
}

func TestCollectFrontendFiles_NotADir(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "index.html")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := collectFrontendFiles(f); err == nil {
		t.Fatal("expected error when path is a file, not a dir")
	}
}
