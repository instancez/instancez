package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequireLocalConfig_RejectsRemote(t *testing.T) {
	err := requireLocalConfig("s3://bucket/backend/instancez.yaml")
	if err == nil {
		t.Fatal("expected error for s3:// spec, got nil")
	}
	if !strings.Contains(err.Error(), "s3://bucket/backend/instancez.yaml") {
		t.Fatalf("error %q should name the offending spec", err)
	}
}

func TestRequireLocalConfig_AcceptsExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instancez.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := requireLocalConfig(path); err != nil {
		t.Fatalf("expected nil for existing local file, got %v", err)
	}
}

func TestRequireLocalConfig_MissingFile(t *testing.T) {
	if err := requireLocalConfig(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected error for missing local file, got nil")
	}
}
