package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saedx1/ultrabase/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestInitFlagsCloudAndGenerateLike(t *testing.T) {
	cmd := newInitCmd()
	assert.NotNil(t, cmd.Flags().Lookup("with-cloud"))
	assert.NotNil(t, cmd.Flags().Lookup("generate-like"))
}

func TestInitWithDockerAndCloudCompose(t *testing.T) {
	// --with-cloud and --with-docker are NOT mutually exclusive — they're
	// orthogonal axes (cloud target vs local dev DB).
	opts := initOptions{useDock: true, withCloud: true}
	assert.NoError(t, validateInitFlags(opts))
}

func TestInitWithDSNAndDockerStillMutuallyExclusive(t *testing.T) {
	opts := initOptions{withDSN: "x", useDock: true}
	assert.Error(t, validateInitFlags(opts))
}

func TestInitGenerateLikeRequiresLogin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir) // no credentials in this HOME

	opts := initOptions{dir: dir, generateLike: "twitter"}
	err := runInit(context.Background(), opts)
	assert.ErrorContains(t, err, "ultra login")
}

// TestRunInitScaffoldStartsCleanly guards the generated project: it must both
// validate AND describe migratable DDL. The todos.user_id FK must reference
// auth.users.id (3-part) — a 2-part `users.id` validates fine but the migrator
// resolves it to a nonexistent public.users table, so `ultra dev` would
// die at migration time.
func TestRunInitScaffoldStartsCleanly(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(context.Background(), initOptions{name: "demo", dir: dir}); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	cfg, err := config.Load(filepath.Join(dir, "ultrabase.yaml"))
	if err != nil {
		t.Fatalf("load scaffolded config: %v", err)
	}
	if errs := config.Validate(cfg); errs != nil {
		t.Fatalf("scaffolded config failed validation: %v", errs)
	}

	todos, ok := cfg.Tables["todos"]
	if !ok {
		t.Fatal("scaffold missing todos table")
	}
	var fk string
	for _, f := range todos.Fields {
		if f.Name == "user_id" && f.ForeignKey != nil {
			fk = f.ForeignKey.References
		}
	}
	if fk != "auth.users.id" {
		t.Fatalf("todos.user_id FK references %q, want auth.users.id "+
			"(2-part users.id resolves to a nonexistent public.users table)", fk)
	}
}

// TestRunInitWritesProductionEnvExample verifies the prod template lands.
// It's the only handhold a user has for "where do I put prod config?".
func TestRunInitWritesProductionEnvExample(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(context.Background(), initOptions{name: "demo", dir: dir}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	for _, name := range []string{".production.env.example", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	// .development.env must NOT exist for a no-flag init — that file is only
	// written by --with-dsn / --with-docker. If it appeared here, somebody
	// added a hidden default-bootstrap step that would silently call out to
	// a Postgres on every plain `ultra init`.
	if _, err := os.Stat(filepath.Join(dir, ".development.env")); !os.IsNotExist(err) {
		t.Errorf("plain init wrote .development.env (expected only with --with-dsn / --with-docker)")
	}
}

// TestRunInitRefusesOverwrite stops a re-run from blowing away an in-progress
// project. --force is the documented escape hatch.
func TestRunInitRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(context.Background(), initOptions{name: "demo", dir: dir}); err != nil {
		t.Fatalf("first runInit: %v", err)
	}
	err := runInit(context.Background(), initOptions{name: "demo", dir: dir})
	if err == nil {
		t.Fatal("expected error on re-init without --force, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error %q should mention 'already exists'", err.Error())
	}

	// With --force, re-init succeeds.
	if err := runInit(context.Background(), initOptions{name: "demo", dir: dir, force: true}); err != nil {
		t.Fatalf("--force should overwrite: %v", err)
	}
}

// TestRunInitMutuallyExclusiveBootstrapFlags prevents the contradictory
// "use my DSN AND start docker" invocation.
func TestRunInitMutuallyExclusiveBootstrapFlags(t *testing.T) {
	dir := t.TempDir()
	err := runInit(context.Background(), initOptions{
		dir:     dir,
		withDSN: "postgres://example",
		useDock: true,
	})
	if err == nil {
		t.Fatal("expected error when --with-dsn and --with-docker are both set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error %q should mention 'mutually exclusive'", err.Error())
	}
}

// TestRunInitDockerStubbed guards the stub message until the docker path
// is fully implemented. When that lands, this test should be deleted or
// repurposed to assert the real docker behavior.
func TestRunInitDockerStubbed(t *testing.T) {
	dir := t.TempDir()
	err := runInit(context.Background(), initOptions{dir: dir, useDock: true})
	if err == nil {
		t.Fatal("expected stub error for --with-docker")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("error %q should mention 'not yet implemented'", err.Error())
	}
}

// TestMergeEnvFile pins the dotenv merge semantics that protect user edits
// in .development.env (and the example file) across re-runs of `ultra init`.
func TestMergeEnvFile(t *testing.T) {
	cases := []struct {
		name     string
		existing string
		updates  []envKV
		want     string
	}{
		{
			name:     "empty existing serializes updates in caller order",
			existing: "",
			updates:  []envKV{{"FOO", "1"}, {"BAR", "2"}},
			want:     "FOO=1\nBAR=2\n",
		},
		{
			name:     "in-place value update preserves line order",
			existing: "FOO=old\nBAR=keep\n",
			updates:  []envKV{{"FOO", "new"}},
			want:     "FOO=new\nBAR=keep\n",
		},
		{
			name:     "comments and blank lines survive",
			existing: "# header\n\nFOO=old\n# notes\nBAR=2\n",
			updates:  []envKV{{"FOO", "new"}},
			want:     "# header\n\nFOO=new\n# notes\nBAR=2\n",
		},
		{
			name:     "missing keys append after existing content",
			existing: "FOO=1\n",
			updates:  []envKV{{"BAR", "2"}},
			want:     "FOO=1\nBAR=2\n",
		},
		{
			name:     "user-added line between our keys is preserved",
			existing: "ULTRABASE_OWNER_DATABASE_URL=oldowner\nMY_CUSTOM=value\nULTRABASE_AUTH_DATABASE_URL=oldauth\n",
			updates: []envKV{
				{"ULTRABASE_OWNER_DATABASE_URL", "newowner"},
				{"ULTRABASE_AUTH_DATABASE_URL", "newauth"},
			},
			want: "ULTRABASE_OWNER_DATABASE_URL=newowner\nMY_CUSTOM=value\nULTRABASE_AUTH_DATABASE_URL=newauth\n",
		},
		{
			name:     "missing trailing newline gets normalized",
			existing: "FOO=1",
			updates:  []envKV{{"FOO", "2"}},
			want:     "FOO=2\n",
		},
		{
			name:     "export prefix is recognized and rewritten canonical",
			existing: "export FOO=old\n",
			updates:  []envKV{{"FOO", "new"}},
			want:     "FOO=new\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeEnvFile(tc.existing, tc.updates)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMergeGitignore pins the append-only semantics for .gitignore so user
// patterns are never reordered or removed across re-runs.
func TestMergeGitignore(t *testing.T) {
	cases := []struct {
		name     string
		existing string
		template string
		want     string
	}{
		{
			name:     "empty existing returns template verbatim",
			existing: "",
			template: "a\nb\n",
			want:     "a\nb\n",
		},
		{
			name:     "all template entries present returns existing untouched",
			existing: "a\nb\nc\n",
			template: "a\nb\n",
			want:     "a\nb\nc\n",
		},
		{
			name:     "appends only the missing entries",
			existing: "a\n",
			template: "a\nb\nc\n",
			want:     "a\nb\nc\n",
		},
		{
			name:     "user comments survive",
			existing: "# my notes\nfoo/\n",
			template: "uploads/\nfoo/\n",
			want:     "# my notes\nfoo/\nuploads/\n",
		},
		{
			name:     "blank lines and comments in template are skipped",
			existing: "a\n",
			template: "a\n\n# header\nb\n",
			want:     "a\nb\n",
		},
		{
			name:     "missing trailing newline normalizes",
			existing: "a",
			template: "a\nb\n",
			want:     "a\nb\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeGitignore(tc.existing, tc.template)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRunInitPreservesUserGitignore guards the regression that
// `ultra init [--force]` would clobber a user-edited .gitignore. Re-running
// init must add any missing entries from our template but never reorder or
// remove the user's lines.
func TestRunInitPreservesUserGitignore(t *testing.T) {
	dir := t.TempDir()
	gitignorePath := filepath.Join(dir, ".gitignore")
	userContent := "# my notes\nnode_modules/\n.env.local\n"
	if err := os.WriteFile(gitignorePath, []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runInit(context.Background(), initOptions{name: "demo", dir: dir}); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{"# my notes", "node_modules/", ".env.local", "uploads/", "pgdata/"} {
		if !strings.Contains(got, want) {
			t.Errorf(".gitignore missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestRunInitPreservesProductionEnvExample guards the regression that
// `ultra init --force` would clobber a user-edited .production.env.example.
// Once the user has touched the example file we treat their copy as
// authoritative — re-init must not rewrite it.
func TestRunInitPreservesProductionEnvExample(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(context.Background(), initOptions{name: "demo", dir: dir}); err != nil {
		t.Fatalf("first runInit: %v", err)
	}

	examplePath := filepath.Join(dir, ".production.env.example")
	customContent := "# my custom production notes\nULTRABASE_OWNER_DATABASE_URL=postgres://i-edited-this\n"
	if err := os.WriteFile(examplePath, []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runInit(context.Background(), initOptions{name: "demo", dir: dir, force: true}); err != nil {
		t.Fatalf("re-runInit: %v", err)
	}

	data, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != customContent {
		t.Errorf(".production.env.example was overwritten\n--- got ---\n%s--- want ---\n%s", data, customContent)
	}
}
