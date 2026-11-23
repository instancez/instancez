package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
}

func fnCfg(files ...string) *domain.Config {
	cfg := &domain.Config{Functions: map[string]domain.CodeFunction{}}
	for _, f := range files {
		cfg.Functions[strings.TrimSuffix(filepath.Base(f), ".js")] = domain.CodeFunction{File: f}
	}
	return cfg
}

func fileKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Regression: functions/lib/* was skipped, so deployed handlers failed to import it.
func TestCollectFunctionSourcesBundlesNestedImports(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"functions/slots.js":              `import { g } from "./lib/google.js"; export default () => g`,
		"functions/lib/google.js":         `import { h } from "./deep/http.js"; export const g = h`,
		"functions/lib/deep/http.js":      `export const h = 1`,
		"functions/package.json":          "{}",
		"functions/package-lock.json":     "{}",
		"functions/.env":                  "SECRET=shh",
		"functions/node_modules/d/i.js":   "module.exports = {}",
		"functions/lib/node_modules/x.js": "x",
	})
	got, err := collectFunctionSources(dir, fnCfg("functions/slots.js"))
	require.NoError(t, err)
	assert.Equal(t, []string{
		"functions/lib/deep/http.js", "functions/lib/google.js",
		"functions/package-lock.json", "functions/package.json", "functions/slots.js",
	}, fileKeys(got))
}

func TestCollectFunctionSourcesFailsOnMissingRelativeImport(t *testing.T) {
	cases := map[string]string{
		"esm static":        `import a from "./lib/missing.js"`,
		"esm side effect":   `import "./lib/missing.js"`,
		"esm reexport":      `export * from "./lib/missing.js"`,
		"dynamic import":    `const m = await import("./lib/missing.js")`,
		"parent traversal":  `import a from "../outside.js"`,
		"extensionless esm": `import a from "./lib/google"`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, dir, map[string]string{
				"functions/h.js":          body,
				"functions/lib/google.js": "export const g = 1",
			})
			_, err := collectFunctionSources(dir, fnCfg("functions/h.js"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "functions/h.js")
		})
	}
}

func TestCollectFunctionSourcesIgnoresBareAndCommentedImports(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"functions/h.js": "import x from \"@supabase/supabase-js\"\nimport fs from \"node:fs\"\n// import y from \"./gone.js\"\nconst s = 'not an import'\n",
	})
	_, err := collectFunctionSources(dir, fnCfg("functions/h.js"))
	require.NoError(t, err)
}

func TestCollectFunctionSourcesCJSExtensionProbing(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"functions/h.cjs":         `const g = require("./lib/google"); const i = require("./lib")`,
		"functions/lib/google.js": "module.exports = 1",
		"functions/lib/index.js":  "module.exports = 2",
	})
	got, err := collectFunctionSources(dir, fnCfg("functions/h.cjs"))
	require.NoError(t, err)
	assert.Contains(t, got, "functions/lib/index.js")
}

func TestCollectFunctionSourcesSymlinks(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeTree(t, dir, map[string]string{
		"functions/h.js":        `import a from "./lib/real.js"; import b from "./alias.js"`,
		"functions/lib/real.js": "export default 1",
	})
	writeTree(t, outside, map[string]string{"secret.js": "export default 2"})
	require.NoError(t, os.Symlink(filepath.Join(dir, "functions/lib/real.js"), filepath.Join(dir, "functions/alias.js")))
	got, err := collectFunctionSources(dir, fnCfg("functions/h.js"))
	require.NoError(t, err)
	assert.Equal(t, "export default 1", got["functions/alias.js"])

	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.js"), filepath.Join(dir, "functions/leak.js")))
	_, err = collectFunctionSources(dir, fnCfg("functions/h.js"))
	require.Error(t, err, "symlink escaping the project must not be bundled")
	assert.Contains(t, err.Error(), "leak.js")
}

func TestCollectFunctionSourcesSymlinkedDirIsSkippedNotLooped(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"functions/h.js": "export default 1"})
	require.NoError(t, os.Symlink(filepath.Join(dir, "functions"), filepath.Join(dir, "functions/self")))
	got, err := collectFunctionSources(dir, fnCfg("functions/h.js"))
	require.NoError(t, err)
	assert.Equal(t, []string{"functions/h.js"}, fileKeys(got))
}

func TestCollectFunctionSourcesMissingEntryFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "functions"), 0o755))
	_, err := collectFunctionSources(dir, fnCfg("functions/ghost.js"))
	require.Error(t, err)
}

func TestCollectFunctionSourcesNoFunctionsDir(t *testing.T) {
	_, err := collectFunctionSources(t.TempDir(), fnCfg("functions/a.js"))
	require.Error(t, err)
}

func TestCollectFunctionSourcesEmptyFileAndNoFunctions(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"functions/h.js": ""})
	got, err := collectFunctionSources(dir, fnCfg("functions/h.js"))
	require.NoError(t, err)
	assert.Contains(t, got, "functions/h.js")
	_, err = collectFunctionSources(dir, &domain.Config{})
	require.NoError(t, err)
}
