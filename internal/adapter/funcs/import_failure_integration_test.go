//go:build integration

package funcs_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/adapter/funcs"
	"github.com/instancez/instancez/internal/domain"
)

func invokeStatus(t *testing.T, rt *funcs.Runtime, name string) (int, map[string]any) {
	t.Helper()
	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{Name: name, Method: "POST"})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal(resp.Body, &body)
	return resp.Status, body
}

// Regression: a function that failed to import answered a bare "unknown fn".
func TestImportFailureNamesFunctionAndCause(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("ok.js", `export default async () => ({ status: 200, body: { ok: true } })`)
	write("broken.js", `import { g } from "./lib/google.js"; export default async () => g`)
	rt, err := funcs.New(funcs.Options{
		Dir: dir,
		Functions: map[string]domain.CodeFunction{
			"ok":     {Runtime: "node", File: "ok.js"},
			"broken": {Runtime: "node", File: "broken.js"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close() })

	if status, body := invokeStatus(t, rt, "ok"); status != 200 || body["ok"] != true {
		t.Fatalf("control: status %d body %v", status, body)
	}

	status, body := invokeStatus(t, rt, "broken")
	if status != 404 {
		t.Fatalf("status = %d, want 404 (Supabase parity)", status)
	}
	if body["message"] != "unknown fn" {
		t.Errorf("message = %v, want unchanged \"unknown fn\"", body["message"])
	}
	detail, _ := body["detail"].(string)
	if !strings.Contains(detail, "broken") || !strings.Contains(detail, "google.js") {
		t.Errorf("detail = %q, want function name and import error", detail)
	}
}
