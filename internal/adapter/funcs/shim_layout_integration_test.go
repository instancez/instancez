//go:build integration

package funcs_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/adapter/funcs"
	"github.com/instancez/instancez/internal/domain"
)

// TestSupabaseJSImportResolvesUnderRealLayout is the regression test for the
// shim placement bug: with opts.Dir = config root and node_modules vendored at
// <root>/functions/node_modules (the canonical layout), the worker shim must
// sit inside <root>/functions/ for Node's bare-specifier ESM resolution to
// find @supabase/supabase-js. A shim placed at <root> (one level up) cannot
// descend into functions/node_modules and would fail with ERR_MODULE_NOT_FOUND,
// surfaced as a per-invocation 500 containing "@supabase/supabase-js not vendored".
//
// This test FAILS without the fix (shim at opts.Dir) and PASSES with the fix
// (shim at opts.Dir/functions/).
//
// Requires: node and npm on PATH, and network access for `npm i`. Skipped (not
// failed) when either is unavailable or the npm install fails offline.
func TestSupabaseJSImportResolvesUnderRealLayout(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not installed")
	}

	// ---- 1. Set up the real config-root + functions/ layout ----
	// root/               ← opts.Dir (config root)
	//   functions/
	//     realclient.js   ← CodeFunction.File = "functions/realclient.js"
	//     node_modules/   ← populated by npm i below
	root := t.TempDir()
	fnDir := filepath.Join(root, "functions")
	if err := os.MkdirAll(fnDir, 0o755); err != nil {
		t.Fatalf("mkdir functions: %v", err)
	}

	// ---- 2. Vendor @supabase/supabase-js into functions/node_modules ----
	install := exec.CommandContext(context.Background(), "npm", "install",
		"--prefix", fnDir,
		"--no-audit", "--no-fund", "--loglevel=error",
		"@supabase/supabase-js",
	)
	install.Stdout = os.Stderr
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		t.Skipf("npm install @supabase/supabase-js failed (no network?): %v", err)
	}

	// ---- 3. Write the function that probes ctx.supabase ----
	// The function accesses ctx.supabase, which triggers the lazy getter in
	// worker.js. If @supabase/supabase-js wasn't resolved at load time,
	// mk() throws "not vendored" and the invocation returns 500. If the
	// import resolved successfully, createClient is defined, the client is
	// constructed, and typeof ctx.supabase.from === "function" → 200.
	fnSrc := `export default async (req, ctx) => {
  const hasFn = typeof ctx.supabase.from === "function";
  return { status: 200, body: { hasFn } };
};
`
	fnPath := filepath.Join(fnDir, "realclient.js")
	if err := os.WriteFile(fnPath, []byte(fnSrc), 0o644); err != nil {
		t.Fatalf("write function: %v", err)
	}

	// ---- 4. Build the runtime with opts.Dir = root (config-root layout) ----
	rt, err := funcs.New(funcs.Options{
		Dir: root,
		Functions: map[string]domain.CodeFunction{
			"realclient": {Runtime: "node", File: "functions/realclient.js"},
		},
		LoopbackURL: "http://127.0.0.1:0",
		MintAnon:    func(context.Context) (string, error) { return "test-anon-key", nil },
	})
	if err != nil {
		t.Fatalf("funcs.New: %v", err)
	}
	defer rt.Close()

	// ---- 5. Invoke and assert the import resolved ----
	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{
		Name:   "realclient",
		Method: "GET",
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}

	body := string(resp.Body)

	// The key assertion: the invocation must NOT be the "not vendored" 500.
	// If the shim is in the wrong directory (above functions/), the import
	// fails and every invocation returns 500 with "@supabase/supabase-js not vendored".
	if resp.Status != 200 {
		t.Fatalf("import did not resolve: status=%d body=%s\n"+
			"(if body contains '@supabase/supabase-js not vendored', the shim is in the wrong directory)",
			resp.Status, body)
	}
	if !strings.Contains(body, `"hasFn":true`) {
		t.Fatalf("expected hasFn:true in body, got: %s", body)
	}
}
