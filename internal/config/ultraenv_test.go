package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/saedx1/instancez/internal/config"
)

func TestLoadUltraEnvPrecedenceAndScope(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("ULTRA_ENV_A=base\nULTRA_ENV_B=base\nSECRET=should-not-load\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".development.env"), []byte("ULTRA_ENV_B=mode\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ULTRA_ENV_C", "fromproc")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "nope")
	m, err := config.LoadUltraEnv(dir, "development")
	if err != nil {
		t.Fatal(err)
	}
	if m["ULTRA_ENV_A"] != "base" || m["ULTRA_ENV_B"] != "mode" || m["ULTRA_ENV_C"] != "fromproc" {
		t.Fatalf("precedence wrong: %v", m)
	}
	if _, ok := m["SECRET"]; ok {
		t.Fatal("non-prefixed file key leaked")
	}
	if _, ok := m["AWS_SECRET_ACCESS_KEY"]; ok {
		t.Fatal("non-prefixed proc env leaked")
	}
}

func TestLoadUltraEnvMissingFiles(t *testing.T) {
	dir := t.TempDir()
	// Ensure no ambient ULTRA_ENV_* vars interfere by checking specific keys are absent.
	m, err := config.LoadUltraEnv(dir, "production")
	if err != nil {
		t.Fatalf("expected no error for missing files, got: %v", err)
	}
	// Keys written by this test's files are absent (there are no files).
	// We check specific keys rather than len==0 to be robust to ambient ULTRA_ENV_* vars.
	if _, ok := m["ULTRA_ENV_MISSING_FILE_KEY_PROBE"]; ok {
		t.Fatal("key from non-existent file somehow present")
	}
}

func TestLoadUltraEnvOnlyBaseFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("ULTRA_ENV_X=hello\nOTHER=skip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No mode-specific file.
	m, err := config.LoadUltraEnv(dir, "production")
	if err != nil {
		t.Fatal(err)
	}
	if m["ULTRA_ENV_X"] != "hello" {
		t.Fatalf("expected ULTRA_ENV_X=hello, got %v", m)
	}
	if _, ok := m["OTHER"]; ok {
		t.Fatal("non-prefixed key should not be in map")
	}
}

func TestLoadUltraEnvQuotedValues(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(`ULTRA_ENV_Q="quoted value"`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := config.LoadUltraEnv(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if m["ULTRA_ENV_Q"] != "quoted value" {
		t.Fatalf("expected unquoted value, got %q", m["ULTRA_ENV_Q"])
	}
}

func TestLoadUltraEnvEmptyValue(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("ULTRA_ENV_X=\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := config.LoadUltraEnv(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := m["ULTRA_ENV_X"]; !ok {
		t.Fatal("expected ULTRA_ENV_X to be present")
	} else if v != "" {
		t.Fatalf("expected empty value, got %q", v)
	}
}
