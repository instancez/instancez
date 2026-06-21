package cli

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildDevFuncRuntimeRequiresNode verifies that the `inz dev` runtime build
// fails with the actionable "Node.js" message BEFORE shelling out to npm, even
// when functions/package.json is present, when node is missing from PATH. This
// guards the dev-path seam: npm ships with node, so without an explicit node
// gate a node-less machine would fail with a raw `exec: npm: ... not found`.
// PATH is emptied so exec.LookPath("node") fails deterministically.
func TestBuildDevFuncRuntimeRequiresNode(t *testing.T) {
	t.Setenv("PATH", "")
	dir := t.TempDir()
	fnDir := filepath.Join(dir, "functions")
	require.NoError(t, os.MkdirAll(fnDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fnDir, "package.json"),
		[]byte(`{"name":"x","private":true}`), 0o644))
	configPath := filepath.Join(dir, "instancez.yaml")

	cfg := &domain.Config{Functions: map[string]domain.CodeFunction{
		"hello": {Runtime: "node", File: "functions/hello.js"},
	}}
	km := app.NewJWTKeyManager(nil)

	_, err := buildDevFuncRuntime(context.Background(), cfg, configPath, km, slog.Default())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Node.js")
	assert.Contains(t, err.Error(), "22")
	assert.NotContains(t, err.Error(), "npm")
}

// TestBuildDevFuncRuntimeChecksFunctionFiles verifies the dev startup path
// fails when a declared function's source file is missing — a check that
// previously only ran in `inz validate`/`inz bundle`, not at dev/serve startup.
// A stub node is on PATH so the function-source check (not the node gate) fires.
func TestBuildDevFuncRuntimeChecksFunctionFiles(t *testing.T) {
	fakeNodeOnPath(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "instancez.yaml")
	// functions/hello.js is declared but never created on disk.
	cfg := &domain.Config{Functions: map[string]domain.CodeFunction{
		"hello": {Runtime: "node", File: "functions/hello.js"},
	}}
	km := app.NewJWTKeyManager(nil)

	_, err := buildDevFuncRuntime(context.Background(), cfg, configPath, km, slog.Default())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "functions/hello.js")
}
