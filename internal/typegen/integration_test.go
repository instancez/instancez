//go:build integration

package typegen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/typegen"
)

// harnessDir returns the supabase-js harness with deps installed and an empty generated dir.
func harnessDir(t *testing.T) string {
	t.Helper()
	for _, bin := range []string{"node", "npm"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH; skipping typegen node checks", bin)
		}
	}
	dir, err := filepath.Abs("../../test/integration/supabase-js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules", ".bin", "tsc")); err != nil {
		run(t, dir, "npm", "ci", "--no-audit", "--no-fund", "--loglevel=error")
	}
	gen := filepath.Join(dir, "types", "generated")
	if err := os.RemoveAll(gen); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(gen, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(gen) })
	return dir
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// generate writes the in-process output of each yaml (empty name = nil config) to generated/<out>.ts.
func generate(t *testing.T, dir string, yamls map[string]string) []string {
	t.Helper()
	var files []string
	for out, yaml := range yamls {
		var data []byte
		if yaml == "" {
			data = typegen.Generate(nil)
		} else {
			data = typegen.Generate(loadYAML(t, yaml))
		}
		path := filepath.Join(dir, "types", "generated", out+".ts")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, path)
	}
	return files
}

var allConfigs = map[string]string{"full": "full.yaml", "columns": "columns.yaml", "empty": ""}

func TestTypegenTscStrict(t *testing.T) {
	dir := harnessDir(t)
	files := generate(t, dir, allConfigs)
	args := append([]string{"tsc", "--strict", "--noEmit", "--target", "es2022", "--module", "esnext", "--moduleResolution", "bundler"}, files...)
	run(t, dir, "npx", args...)
}

func TestTypegenTypeLevel(t *testing.T) {
	dir := harnessDir(t)
	generate(t, dir, allConfigs)
	if _, err := os.Stat(filepath.Join(dir, "types", "types.test.ts")); err != nil {
		t.Fatalf("type-level assertion file missing: %v", err)
	}
	run(t, dir, "npx", "tsc", "-p", "types/tsconfig.json")
}

func TestTypegenPrettierStable(t *testing.T) {
	dir := harnessDir(t)
	files := generate(t, dir, allConfigs)
	args := append([]string{"prettier", "--no-config", "--no-editorconfig", "--parser", "typescript", "--no-semi", "--check"}, files...)
	run(t, dir, "npx", args...)
}
