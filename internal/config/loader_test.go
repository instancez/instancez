package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
)

func TestLoad_MinimalValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ultrabase.yaml")
	content := `
version: 1
project:
  name: "test"
tables:
  todos:
    fields:
      - name: id
        type: bigserial
        primary_key: true
      - name: title
        type: text
        required: true
`
	os.WriteFile(path, []byte(content), 0o644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("version = %d, want 1", cfg.Version)
	}
	if cfg.Project.Name != "test" {
		t.Errorf("project.name = %q, want %q", cfg.Project.Name, "test")
	}
	if len(cfg.Tables) != 1 {
		t.Errorf("tables count = %d, want 1", len(cfg.Tables))
	}
}

func TestLoad_Defaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ultrabase.yaml")
	content := `
version: 1
project:
  name: "test"
`
	os.WriteFile(path, []byte(content), 0o644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("default port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Server.MaxBodySize != "1MB" {
		t.Errorf("default max_body_size = %q, want %q", cfg.Server.MaxBodySize, "1MB")
	}
	if cfg.Server.MaxLimit != 100 {
		t.Errorf("default max_limit = %d, want 100", cfg.Server.MaxLimit)
	}
	if cfg.Server.Timeouts.Request != "30s" {
		t.Errorf("default request timeout = %q, want %q", cfg.Server.Timeouts.Request, "30s")
	}
	if cfg.Database.Pool.Max != 20 {
		t.Errorf("default pool max = %d, want 20", cfg.Database.Pool.Max)
	}
}

func TestLoad_EnvVarInterpolation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ultrabase.yaml")
	content := `
version: 1
project:
  name: "${TEST_PROJECT_NAME}"
`
	os.WriteFile(path, []byte(content), 0o644)

	t.Setenv("TEST_PROJECT_NAME", "my-app")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project.Name != "my-app" {
		t.Errorf("project.name = %q, want %q", cfg.Project.Name, "my-app")
	}
}

func TestLoad_EnvVarDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ultrabase.yaml")
	content := `
version: 1
project:
  name: "${MISSING_VAR:-fallback}"
`
	os.WriteFile(path, []byte(content), 0o644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project.Name != "fallback" {
		t.Errorf("project.name = %q, want %q", cfg.Project.Name, "fallback")
	}
}

func TestLoad_MissingEnvVar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ultrabase.yaml")
	content := `
version: 1
project:
  name: "${TOTALLY_MISSING_VAR}"
`
	os.WriteFile(path, []byte(content), 0o644)

	// Make sure the var is not set
	os.Unsetenv("TOTALLY_MISSING_VAR")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing env var")
	}
	if _, ok := err.(*domain.MissingEnvError); !ok {
		// Check if it's our domain type
		t.Logf("error type: %T, message: %v", err, err)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/ultrabase.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ultrabase.yaml")
	os.WriteFile(path, []byte("{{invalid yaml"), 0o644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadDotenv(t *testing.T) {
	dir := t.TempDir()

	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte(`
# comment
MY_TEST_KEY=hello
MY_QUOTED="world"
`), 0o644)

	configPath := filepath.Join(dir, "ultrabase.yaml")
	os.WriteFile(configPath, []byte(`
version: 1
project:
  name: "${MY_TEST_KEY}"
`), 0o644)

	// Clear just in case
	os.Unsetenv("MY_TEST_KEY")
	os.Unsetenv("MY_QUOTED")

	cfg, err := LoadWithDotenv(configPath, envPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project.Name != "hello" {
		t.Errorf("project.name = %q, want %q", cfg.Project.Name, "hello")
	}

	// Check .env doesn't override real env vars
	t.Cleanup(func() {
		os.Unsetenv("MY_TEST_KEY")
		os.Unsetenv("MY_QUOTED")
	})
}

func TestLoadDotenv_RealEnvTakesPriority(t *testing.T) {
	dir := t.TempDir()

	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("MY_PRIO_TEST=from-dotenv\n"), 0o644)

	configPath := filepath.Join(dir, "ultrabase.yaml")
	os.WriteFile(configPath, []byte(`
version: 1
project:
  name: "${MY_PRIO_TEST}"
`), 0o644)

	t.Setenv("MY_PRIO_TEST", "from-real-env")

	cfg, err := LoadWithDotenv(configPath, envPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project.Name != "from-real-env" {
		t.Errorf("project.name = %q, want %q", cfg.Project.Name, "from-real-env")
	}
}

func TestLoad_EnvVarDefaultEmpty(t *testing.T) {
	// ${VAR:-} with empty default should resolve to empty string
	dir := t.TempDir()
	path := filepath.Join(dir, "ultrabase.yaml")
	content := `
version: 1
project:
  name: "${UNSET_VAR_EMPTY:-}"
`
	os.WriteFile(path, []byte(content), 0o644)
	os.Unsetenv("UNSET_VAR_EMPTY")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project.Name != "" {
		t.Errorf("project.name = %q, want empty string", cfg.Project.Name)
	}
}

func TestLoad_EnvVarDefaultOverriddenByReal(t *testing.T) {
	// ${VAR:-default} should use real env var if set, not the default
	dir := t.TempDir()
	path := filepath.Join(dir, "ultrabase.yaml")
	content := `
version: 1
project:
  name: "${DEFAULT_OVERRIDE_TEST:-fallback}"
`
	os.WriteFile(path, []byte(content), 0o644)
	t.Setenv("DEFAULT_OVERRIDE_TEST", "real-value")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project.Name != "real-value" {
		t.Errorf("project.name = %q, want %q", cfg.Project.Name, "real-value")
	}
}

func TestLoad_MultipleEnvVarsInOneString(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ultrabase.yaml")
	content := `
version: 1
project:
  name: "${MULTI_A}-${MULTI_B:-world}"
`
	os.WriteFile(path, []byte(content), 0o644)
	t.Setenv("MULTI_A", "hello")
	os.Unsetenv("MULTI_B")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project.Name != "hello-world" {
		t.Errorf("project.name = %q, want %q", cfg.Project.Name, "hello-world")
	}
}

func TestLoad_MultipleMissingEnvVarsReportedAtOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ultrabase.yaml")
	content := `
version: 1
project:
  name: "${MISS_A}"
  description: "${MISS_B}"
`
	os.WriteFile(path, []byte(content), 0o644)
	os.Unsetenv("MISS_A")
	os.Unsetenv("MISS_B")

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing env vars")
	}
	envErr, ok := err.(*domain.MissingEnvError)
	if !ok {
		t.Fatalf("expected *MissingEnvError, got %T: %v", err, err)
	}
	if len(envErr.Vars) != 2 {
		t.Errorf("expected 2 missing vars, got %d: %v", len(envErr.Vars), envErr.Vars)
	}
}

func TestLoad_EnvVarDefaultWithSpecialChars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ultrabase.yaml")
	content := `
version: 1
project:
  name: "${SPECIAL_DEFAULT:-admin123}"
`
	os.WriteFile(path, []byte(content), 0o644)
	os.Unsetenv("SPECIAL_DEFAULT")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project.Name != "admin123" {
		t.Errorf("project.name = %q, want %q", cfg.Project.Name, "admin123")
	}
}

func TestParseBytesLenient_MissingEnvDoesNotFail(t *testing.T) {
	// ${MISSING_VAR} would make ParseBytes return MissingEnvError; lenient must not.
	yamlSrc := []byte("version: 1\nproject:\n  name: test\nproviders:\n  email:\n    type: resend\n    api_key: ${MISSING_VAR}\n")
	cfg, err := ParseBytesLenient(yamlSrc, "test")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg == nil {
		t.Fatal("expected a config")
	}
}

func TestEnvRefs_ReturnsUniqueNames(t *testing.T) {
	data := []byte("a: ${FOO}\nb: ${BAR:-x}\nc: ${FOO}\n")
	got := EnvRefs(data)
	want := map[string]bool{"FOO": true, "BAR": true}
	if len(got) != 2 {
		t.Fatalf("expected 2 unique names, got %v", got)
	}
	for _, n := range got {
		if !want[n] {
			t.Errorf("unexpected name %q", n)
		}
	}
}
