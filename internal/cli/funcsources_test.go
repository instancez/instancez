package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func TestCollectFunctionSources(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("functions/hello.js", "export default () => {}")
	mustWrite("functions/package.json", "{}")
	mustWrite("functions/package-lock.json", "{}")
	mustWrite("functions/lib/util.js", "export const x = 1")
	mustWrite("functions/.env", "SECRET=shh")
	mustWrite("functions/node_modules/dep/index.js", "module.exports = {}")

	cfg := &domain.Config{Functions: map[string]domain.CodeFunction{
		"hello": {File: "functions/hello.js"},
	}}

	got, err := collectFunctionSources(dir, cfg)
	if err != nil {
		t.Fatalf("collectFunctionSources: %v", err)
	}

	// Declared entry file + package*.json only.
	want := []string{"functions/hello.js", "functions/package.json", "functions/package-lock.json"}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("missing %q", k)
		}
	}
	// Everything not declared and not a package file stays local — including
	// a stray .env, local helper modules, and node_modules.
	for _, k := range []string{
		"functions/lib/util.js",
		"functions/.env",
		"functions/node_modules/dep/index.js",
	} {
		if _, ok := got[k]; ok {
			t.Errorf("%q should not be uploaded (not a declared entry or package file)", k)
		}
	}
	if len(got) != len(want) {
		t.Errorf("uploaded %d files, want exactly %d: %v", len(got), len(want), keys(got))
	}
}

// A declared file that does not exist surfaces a clear error rather than
// silently uploading a partial set.
func TestCollectFunctionSourcesMissingFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "functions"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &domain.Config{Functions: map[string]domain.CodeFunction{
		"ghost": {File: "functions/ghost.js"},
	}}
	if _, err := collectFunctionSources(dir, cfg); err == nil {
		t.Fatal("expected an error for a declared file that does not exist")
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
