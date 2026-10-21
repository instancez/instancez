//go:build integration

package typegen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/instancez/instancez/internal/typegen"
)

// harnessDir returns the supabase-js typegen harness with deps installed and an empty generated dir.
func harnessDir(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH; skipping typegen node checks")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not on PATH; skipping typegen node checks")
	}
	dir, err := filepath.Abs("../../test/integration/typegen")
	if err != nil {
		t.Fatal(err)
	}
	installOnce.Do(func() {
		if !hasBins(dir) {
			run(t, dir, exec.Command("npm", "ci", "--no-audit", "--no-fund", "--loglevel=error"))
		}
	})
	if !hasBins(dir) {
		t.Fatal("typegen harness deps missing after install")
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

var installOnce sync.Once

func hasBins(dir string) bool {
	for _, b := range []string{"tsc", "prettier"} {
		if _, err := os.Stat(filepath.Join(dir, "node_modules", ".bin", b)); err != nil {
			return false
		}
	}
	return true
}

func run(t *testing.T, dir string, cmd *exec.Cmd) {
	t.Helper()
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(cmd.Args, " "), err, out)
	}
}

// generate writes the in-process output of each yaml (empty name = nil config) to generated/<out>.ts.
func generate(t *testing.T, dir string, yamls map[string]string) {
	t.Helper()
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
	}
}

var allConfigs = map[string]string{"full": "full.yaml", "columns": "columns.yaml", "empty": ""}

func TestTypegenTscStrict(t *testing.T) {
	dir := harnessDir(t)
	generate(t, dir, allConfigs)
	run(t, dir, exec.Command("npx", "--no-install", "tsc", "--strict", "--noEmit", "--target", "es2022", "--module", "esnext", "--moduleResolution", "bundler",
		"types/generated/full.ts", "types/generated/columns.ts", "types/generated/empty.ts"))
}

func TestTypegenTypeLevel(t *testing.T) {
	dir := harnessDir(t)
	generate(t, dir, allConfigs)
	if _, err := os.Stat(filepath.Join(dir, "types", "types.test.ts")); err != nil {
		t.Fatalf("type-level assertion file missing: %v", err)
	}
	run(t, dir, exec.Command("npx", "--no-install", "tsc", "-p", "types/tsconfig.json"))
}

func TestTypegenPrettierStable(t *testing.T) {
	dir := harnessDir(t)
	generate(t, dir, allConfigs)
	run(t, dir, exec.Command("npx", "--no-install", "prettier", "--no-config", "--no-editorconfig", "--parser", "typescript", "--no-semi", "--check", "types/generated"))
}
