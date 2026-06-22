package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/cloud"
	"github.com/instancez/instancez/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitFlagsCloudAndGenerateLike(t *testing.T) {
	cmd := newInitCmd()
	assert.NotNil(t, cmd.Flags().Lookup("with-cloud"))
	assert.NotNil(t, cmd.Flags().Lookup("generate-like"))
}

func TestInitGenerateLikeRequiresLogin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir) // no credentials in this HOME

	opts := initOptions{dir: dir, generateLike: "twitter"}
	err := runInit(opts)
	assert.ErrorContains(t, err, "inz cloud login")
}

func TestInitWithCloudRequiresLogin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	opts := initOptions{dir: dir, withCloud: true, name: "myapp"}
	err := runInit(opts)
	assert.ErrorContains(t, err, "inz cloud login")
}

// TestRunInitScaffoldStartsCleanly guards the generated project: it must both
// validate AND describe migratable DDL. The todos.user_id FK must reference
// auth.users.id (3-part) — a 2-part `users.id` validates fine but the migrator
// resolves it to a nonexistent public.users table, so `inz dev` would
// die at migration time.
func TestRunInitScaffoldStartsCleanly(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(initOptions{name: "demo", dir: dir}); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	cfg, err := config.Load(filepath.Join(dir, "instancez.yaml"))
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

// TestRunInitScaffoldsFunctions verifies init drops a working starter code
// function (package.json + handler) and wires it into instancez.yaml so
// `inz dev` can serve it immediately.
func TestRunInitScaffoldsFunctions(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(initOptions{name: "demo", dir: dir}); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	// package.json declares the deps the scaffolded handler needs.
	pkg, err := os.ReadFile(filepath.Join(dir, "functions", "package.json"))
	require.NoError(t, err, "functions/package.json should exist")
	assert.Contains(t, string(pkg), "@supabase/supabase-js", "ctx.supabase needs supabase-js")
	assert.Contains(t, string(pkg), "zod")

	// The handler is a real one (uses ctx.supabase + default export).
	fn, err := os.ReadFile(filepath.Join(dir, "functions", "todos.js"))
	require.NoError(t, err, "functions/todos.js should exist")
	assert.Contains(t, string(fn), "export default")
	assert.Contains(t, string(fn), "ctx.supabase")

	// Wired into the config as a code function.
	cfg, err := config.Load(filepath.Join(dir, "instancez.yaml"))
	require.NoError(t, err)
	if errs := config.Validate(cfg); errs != nil {
		t.Fatalf("scaffolded config failed validation: %v", errs)
	}
	todosFn, ok := cfg.Functions["todos"]
	require.True(t, ok, "instancez.yaml should declare the todos code function")
	assert.Equal(t, "node", todosFn.Runtime)
	assert.Equal(t, "functions/todos.js", todosFn.File)
	assert.True(t, todosFn.AuthRequired)

	// Storage bucket scaffolded so the example exercises the storage feature.
	bucket, ok := cfg.Storage["avatars"]
	require.True(t, ok, "scaffold should declare the avatars storage bucket")
	assert.True(t, bucket.Public, "avatars bucket should be public")
	assert.Equal(t, "5MB", bucket.MaxSize)

	// node_modules ignored.
	gi, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	assert.Contains(t, string(gi), "functions/node_modules/")
}

func TestRunInitWritesProductionEnvExample(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(initOptions{name: "demo", dir: dir}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	for _, name := range []string{".production.env.example", ".development.env.example", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	// init never touches a database, so it must NOT write a live .development.env
	// (that is now written by `inz dev` after bootstrapping). Only the example
	// template is written here.
	if _, err := os.Stat(filepath.Join(dir, ".development.env")); !os.IsNotExist(err) {
		t.Errorf("init wrote a live .development.env (expected only .development.env.example)")
	}
}

// TestRunInitDevelopmentEnvExampleDocumentsSuperuser verifies the dev example
// points users at the single superuser DSN that `inz dev` bootstraps from.
func TestRunInitDevelopmentEnvExampleDocumentsSuperuser(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(initOptions{name: "demo", dir: dir}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".development.env.example"))
	if err != nil {
		t.Fatalf("read .development.env.example: %v", err)
	}
	if !strings.Contains(string(data), "INSTANCEZ_DATABASE_URL=") {
		t.Errorf(".development.env.example should document INSTANCEZ_DATABASE_URL\n--- got ---\n%s", data)
	}
}

// TestRunInitIdempotent verifies that re-running init without --force is safe:
// it succeeds (no error), leaves the existing instancez.yaml bytes unchanged,
// and still completes the rest of the init steps (gitignore, env example, etc).
func TestRunInitIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(initOptions{name: "demo", dir: dir}); err != nil {
		t.Fatalf("first runInit: %v", err)
	}

	yamlPath := filepath.Join(dir, "instancez.yaml")
	originalBytes, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read yaml after first init: %v", err)
	}

	// Re-run without --force: must succeed and leave yaml bytes unchanged.
	if err := runInit(initOptions{name: "demo", dir: dir}); err != nil {
		t.Fatalf("re-runInit without --force should succeed, got: %v", err)
	}

	afterBytes, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read yaml after re-init: %v", err)
	}
	if string(afterBytes) != string(originalBytes) {
		t.Errorf("instancez.yaml was modified on re-run without --force\n--- before ---\n%s--- after ---\n%s",
			originalBytes, afterBytes)
	}
}

// TestRunInitForceOverwrites confirms that --force does overwrite the yaml.
func TestRunInitForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(initOptions{name: "demo", dir: dir}); err != nil {
		t.Fatalf("first runInit: %v", err)
	}

	// Inject custom content so we can detect whether it was replaced.
	yamlPath := filepath.Join(dir, "instancez.yaml")
	customContent := "# custom content that --force should replace\n"
	if err := os.WriteFile(yamlPath, []byte(customContent), 0o644); err != nil {
		t.Fatalf("write custom yaml: %v", err)
	}

	if err := runInit(initOptions{name: "demo", dir: dir, force: true}); err != nil {
		t.Fatalf("--force should overwrite: %v", err)
	}

	afterBytes, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read yaml after --force re-init: %v", err)
	}
	if string(afterBytes) == customContent {
		t.Error("instancez.yaml was NOT overwritten despite --force")
	}
}

// TestRunInitGenerateLikeRefusesWhenYAMLExists verifies that --generate-like
// over an existing yaml fails fast with an error — before any login/network
// call — when --force is not set. This prevents wasting cloud tokens to
// generate output that would immediately be discarded.
func TestRunInitGenerateLikeRefusesWhenYAMLExists(t *testing.T) {
	dir := t.TempDir()

	// Write an existing yaml directly (no credentials needed for this step).
	yamlPath := filepath.Join(dir, "instancez.yaml")
	existingContent := scaffoldYAML("demo")
	if err := os.WriteFile(yamlPath, []byte(existingContent), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	// Use a fresh HOME with no credentials — if the guard fires before the
	// login check, the error must NOT mention "inz login".
	t.Setenv("HOME", t.TempDir())

	opts := initOptions{dir: dir, generateLike: "twitter"}
	err := runInit(opts)
	if err == nil {
		t.Fatal("expected error when --generate-like over existing yaml without --force, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error %q should mention 'already exists'", err.Error())
	}
	// The guard must fire before the login check — verify the error is NOT
	// the login error.
	if strings.Contains(err.Error(), "inz login") {
		t.Errorf("guard should fire before login check, but got login error: %v", err)
	}

	// yaml must be untouched.
	afterBytes, err2 := os.ReadFile(yamlPath)
	if err2 != nil {
		t.Fatalf("read yaml after failed generate-like: %v", err2)
	}
	if string(afterBytes) != existingContent {
		t.Error("instancez.yaml was modified despite the guard error")
	}
}

// TestRunInitWithCloudSkipsCreateWhenAlreadyLinked guards against duplicate
// cloud-project creation: re-running `inz init --with-cloud` over a yaml that
// already carries project.cloud.project_id must NOT call CreateProject.
//
// The proof is twofold and network-free:
//   - We point INSTANCEZ_CLOUD_API at a dead address. If the guard regressed
//     and CreateProject were reached, it would hit that address and runInit
//     would return an error. So err==nil IS the evidence the guard fired.
//   - The existing project_id read is purely local, so valid creds-on-disk let
//     ensureLoggedIn return early without TTY or network.
//
// We also assert the yaml bytes are unchanged — the no-id path would inject a
// freshly-created id and rewrite the file.
func TestRunInitWithCloudSkipsCreateWhenAlreadyLinked(t *testing.T) {
	dir := t.TempDir()

	// Fresh HOME with valid creds so ensureLoggedIn returns at login.go:124
	// (no TTY prompt, no device-code flow).
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "test-pat"}))

	// Any CreateProject call would hit this unreachable address and error.
	t.Setenv("INSTANCEZ_CLOUD_API", "http://127.0.0.1:1")

	// Seed a yaml that already declares project.cloud.project_id. Use the real
	// helper so the field lands exactly where ReadProjectID looks for it.
	linked, err := cloud.WriteProjectID([]byte(scaffoldYAML("demo")), "proj_existing")
	require.NoError(t, err)
	yamlPath := filepath.Join(dir, "instancez.yaml")
	require.NoError(t, os.WriteFile(yamlPath, linked, 0o644))

	err = runInit(initOptions{name: "demo", dir: dir, withCloud: true})
	require.NoError(t, err, "guard should skip CreateProject; any creation hits the dead API and errors")

	after, err := os.ReadFile(yamlPath)
	require.NoError(t, err)
	assert.Equal(t, string(linked), string(after), "yaml must be untouched when already linked")
}

// TestReadProjectIDGuardDecision unit-tests the guard predicate in isolation:
// a yaml with a project_id yields a non-empty id (→ skip), and one without
// yields "" (→ create). This is the local, network-free read the guard branches
// on inside the --with-cloud block.
func TestReadProjectIDGuardDecision(t *testing.T) {
	linked, err := cloud.WriteProjectID([]byte(scaffoldYAML("demo")), "proj_abc")
	require.NoError(t, err)
	id, err := cloud.ReadProjectID(linked)
	require.NoError(t, err)
	assert.Equal(t, "proj_abc", id, "linked yaml should report its project_id (guard → skip)")

	id, err = cloud.ReadProjectID([]byte(scaffoldYAML("demo")))
	require.NoError(t, err)
	assert.Empty(t, id, "scaffold yaml has no project_id (guard → create)")
}

// TestMergeEnvFile pins the dotenv merge semantics that protect user edits
// in .development.env (and the example file) across re-runs of `inz init`.
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
			existing: "INSTANCEZ_OWNER_DATABASE_URL=oldowner\nMY_CUSTOM=value\nINSTANCEZ_AUTH_DATABASE_URL=oldauth\n",
			updates: []envKV{
				{"INSTANCEZ_OWNER_DATABASE_URL", "newowner"},
				{"INSTANCEZ_AUTH_DATABASE_URL", "newauth"},
			},
			want: "INSTANCEZ_OWNER_DATABASE_URL=newowner\nMY_CUSTOM=value\nINSTANCEZ_AUTH_DATABASE_URL=newauth\n",
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
// `inz init [--force]` would clobber a user-edited .gitignore. Re-running
// init must add any missing entries from our template but never reorder or
// remove the user's lines.
func TestRunInitPreservesUserGitignore(t *testing.T) {
	dir := t.TempDir()
	gitignorePath := filepath.Join(dir, ".gitignore")
	userContent := "# my notes\nnode_modules/\n.env.local\n"
	if err := os.WriteFile(gitignorePath, []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runInit(initOptions{name: "demo", dir: dir}); err != nil {
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
// `inz init --force` would clobber a user-edited .production.env.example.
// Once the user has touched the example file we treat their copy as
// authoritative — re-init must not rewrite it.
func TestRunInitPreservesProductionEnvExample(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(initOptions{name: "demo", dir: dir}); err != nil {
		t.Fatalf("first runInit: %v", err)
	}

	examplePath := filepath.Join(dir, ".production.env.example")
	customContent := "# my custom production notes\nINSTANCEZ_OWNER_DATABASE_URL=postgres://i-edited-this\n"
	if err := os.WriteFile(examplePath, []byte(customContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runInit(initOptions{name: "demo", dir: dir, force: true}); err != nil {
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
