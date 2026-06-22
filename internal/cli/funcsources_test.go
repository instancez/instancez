package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectFunctionSources(t *testing.T) {
	dir := t.TempDir()
	fns := filepath.Join(dir, "functions")
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
	_ = fns

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
