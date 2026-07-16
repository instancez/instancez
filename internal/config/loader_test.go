package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

// LoadDotenv must never clobber a variable already set in the real
// environment; injected/orchestrator env always wins over a local .env.
func TestLoadDotenvDoesNotOverrideRealEnv(t *testing.T) {
	t.Setenv("INSTANCEZ_ENV_X", "from-real-env")
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	if err := os.WriteFile(p, []byte("INSTANCEZ_ENV_X=from-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadDotenv(p); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("INSTANCEZ_ENV_X"); got != "from-real-env" {
		t.Errorf("real env must win, got %q", got)
	}
}

// mustWriteFile writes data to path and fatals the test on error.
func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

// mustUnsetenv unsets an env var and fatals the test on error.
func mustUnsetenv(t *testing.T, key string) {
	t.Helper()
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unsetenv %s: %v", key, err)
	}
}

func TestLoad_MinimalValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instancez.yaml")
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
	mustWriteFile(t, path, []byte(content))

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
	path := filepath.Join(dir, "instancez.yaml")
	content := `
version: 1
project:
  name: "test"
`
	mustWriteFile(t, path, []byte(content))

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
	if cfg.Server.Timeouts.Request != "25s" {
		t.Errorf("default request timeout = %q, want %q", cfg.Server.Timeouts.Request, "25s")
	}
	if cfg.Database.Pool.Max != 20 {
		t.Errorf("default pool max = %d, want 20", cfg.Database.Pool.Max)
	}
}

func TestLoad_EnvVarInterpolation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instancez.yaml")
	content := `
version: 1
project:
  name: "${TEST_PROJECT_NAME}"
`
	mustWriteFile(t, path, []byte(content))

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
	path := filepath.Join(dir, "instancez.yaml")
	content := `
version: 1
project:
  name: "${MISSING_VAR:-fallback}"
`
	mustWriteFile(t, path, []byte(content))

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
	path := filepath.Join(dir, "instancez.yaml")
	content := `
version: 1
project:
  name: "${TOTALLY_MISSING_VAR}"
`
	mustWriteFile(t, path, []byte(content))

	// Make sure the var is not set
	mustUnsetenv(t, "TOTALLY_MISSING_VAR")

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
	_, err := Load("/nonexistent/instancez.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instancez.yaml")
	mustWriteFile(t, path, []byte("{{invalid yaml"))

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadDotenv(t *testing.T) {
	dir := t.TempDir()

	envPath := filepath.Join(dir, ".env")
	mustWriteFile(t, envPath, []byte(`
# comment
MY_TEST_KEY=hello
MY_QUOTED="world"
`))

	configPath := filepath.Join(dir, "instancez.yaml")
	mustWriteFile(t, configPath, []byte(`
version: 1
project:
  name: "${MY_TEST_KEY}"
`))

	// Clear just in case
	mustUnsetenv(t, "MY_TEST_KEY")
	mustUnsetenv(t, "MY_QUOTED")

	cfg, err := LoadWithDotenv(configPath, envPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project.Name != "hello" {
		t.Errorf("project.name = %q, want %q", cfg.Project.Name, "hello")
	}

	// Check .env doesn't override real env vars
	t.Cleanup(func() {
		mustUnsetenv(t, "MY_TEST_KEY")
		mustUnsetenv(t, "MY_QUOTED")
	})
}

func TestLoadDotenv_RealEnvTakesPriority(t *testing.T) {
	dir := t.TempDir()

	envPath := filepath.Join(dir, ".env")
	mustWriteFile(t, envPath, []byte("MY_PRIO_TEST=from-dotenv\n"))

	configPath := filepath.Join(dir, "instancez.yaml")
	mustWriteFile(t, configPath, []byte(`
version: 1
project:
  name: "${MY_PRIO_TEST}"
`))

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
	path := filepath.Join(dir, "instancez.yaml")
	content := `
version: 1
project:
  name: "${UNSET_VAR_EMPTY:-}"
`
	mustWriteFile(t, path, []byte(content))
	mustUnsetenv(t, "UNSET_VAR_EMPTY")

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
	path := filepath.Join(dir, "instancez.yaml")
	content := `
version: 1
project:
  name: "${DEFAULT_OVERRIDE_TEST:-fallback}"
`
	mustWriteFile(t, path, []byte(content))
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
	path := filepath.Join(dir, "instancez.yaml")
	content := `
version: 1
project:
  name: "${MULTI_A}-${MULTI_B:-world}"
`
	mustWriteFile(t, path, []byte(content))
	t.Setenv("MULTI_A", "hello")
	mustUnsetenv(t, "MULTI_B")

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
	path := filepath.Join(dir, "instancez.yaml")
	content := `
version: 1
project:
  name: "${MISS_A}"
  description: "${MISS_B}"
`
	mustWriteFile(t, path, []byte(content))
	mustUnsetenv(t, "MISS_A")
	mustUnsetenv(t, "MISS_B")

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
	path := filepath.Join(dir, "instancez.yaml")
	content := `
version: 1
project:
  name: "${SPECIAL_DEFAULT:-admin123}"
`
	mustWriteFile(t, path, []byte(content))
	mustUnsetenv(t, "SPECIAL_DEFAULT")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project.Name != "admin123" {
		t.Errorf("project.name = %q, want %q", cfg.Project.Name, "admin123")
	}
}

func TestParseBytesLenient_MissingEnvDoesNotFail(t *testing.T) {
	// A missing ${VAR} must not error; it should be replaced by the placeholder.
	yamlSrc := []byte("version: 1\nproject:\n  name: ${MISSING_VAR}\n")
	cfg, err := ParseBytesLenient(yamlSrc, "test")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg == nil {
		t.Fatal("expected a config")
	}
	if cfg.Project.Name != "placeholder" {
		t.Fatalf("expected missing var replaced by placeholder, got %q", cfg.Project.Name)
	}
}

func TestParseBytesLenient_EmptyDefault(t *testing.T) {
	// ${VAR:-} with a missing VAR yields the empty default, not the placeholder.
	yamlSrc := []byte("version: 1\nproject:\n  name: \"${MISSING:-}\"\n")
	cfg, err := ParseBytesLenient(yamlSrc, "test")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.Project.Name != "" {
		t.Fatalf("expected empty default, got %q", cfg.Project.Name)
	}
}

func TestParseBytesRaw_PreservesEnvRefs(t *testing.T) {
	t.Setenv("TEST_API_KEY", "actual_secret")

	yamlData := `
version: 1
project:
  name: test
providers:
  email:
    type: resend
    api_key: ${TEST_API_KEY}
`
	cfg, err := ParseBytesRaw([]byte(yamlData), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Providers.Email == nil {
		t.Fatal("expected email provider, got nil")
	}
	if cfg.Providers.Email.APIKey != "${TEST_API_KEY}" {
		t.Errorf("api_key = %q, want %q", cfg.Providers.Email.APIKey, "${TEST_API_KEY}")
	}
}

func TestParseBytesRaw_PreservesDefaultSyntax(t *testing.T) {
	yamlData := `
version: 1
project:
  name: test
providers:
  email:
    type: resend
    api_key: ${TEST_KEY:-fallback}
`
	cfg, err := ParseBytesRaw([]byte(yamlData), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Providers.Email.APIKey != "${TEST_KEY:-fallback}" {
		t.Errorf("api_key = %q, want literal ref string", cfg.Providers.Email.APIKey)
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
