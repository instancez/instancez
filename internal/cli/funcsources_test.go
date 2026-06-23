package cli

import (
	"os"
	"path/filepath"
	"testing"
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
	mustWrite("functions/node_modules/dep/index.js", "module.exports = {}")

	got, err := collectFunctionSources(dir)
	if err != nil {
		t.Fatalf("collectFunctionSources: %v", err)
	}
	want := []string{"functions/hello.js", "functions/package.json", "functions/package-lock.json", "functions/lib/util.js"}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("missing %q", k)
		}
	}
	if _, ok := got["functions/node_modules/dep/index.js"]; ok {
		t.Error("node_modules should be excluded")
	}
}

// TestCollectFunctionSourcesSkipsSymlinks verifies that symlinks inside
// functions/ are not included in the returned source map.
func TestCollectFunctionSourcesSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	fnDir := filepath.Join(dir, "functions")
	if err := os.MkdirAll(fnDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a real file to be the symlink target.
	target := filepath.Join(fnDir, "real.js")
	if err := os.WriteFile(target, []byte("export default () => {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create a symlink pointing at the real file.
	link := filepath.Join(fnDir, "link.js")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks not supported on this platform:", err)
	}
	// Write instancez.yaml so collectFunctionSources can stat the project root.
	if err := os.WriteFile(filepath.Join(dir, "instancez.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := collectFunctionSources(dir)
	if err != nil {
		t.Fatalf("collectFunctionSources: %v", err)
	}
	if _, ok := got["functions/link.js"]; ok {
		t.Error("symlink should not be included in collected sources")
	}
	if _, ok := got["functions/real.js"]; !ok {
		t.Error("real file should be included in collected sources")
	}
}
