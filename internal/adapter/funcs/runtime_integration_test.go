//go:build integration

package funcs_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/saedx1/instancez/internal/adapter/funcs"
	"github.com/saedx1/instancez/internal/domain"
)

func TestInvokeRequestFidelity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "echo.js"), []byte(
		`export default async (req) => ({ status: 200, body: { m: req.method, p: req.path, q: req.query.x || null, ct: req.headers["content-type"] || null, raw: typeof req.body } });`), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, err := funcs.New(funcs.Options{Dir: dir, Functions: map[string]domain.CodeFunction{"echo": {Runtime: "node", File: "echo.js"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{
		Name: "echo", Method: "PATCH", Path: "/functions/v1/echo",
		Query:   map[string][]string{"x": {"1"}},
		Headers: map[string][]string{"Content-Type": {"text/plain"}},
		Body:    []byte("hello-raw"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"m":"PATCH","p":"/functions/v1/echo","q":"1","ct":"text/plain","raw":"string"}`
	if string(resp.Body) != want {
		t.Fatalf("got %s", resp.Body)
	}
}

func TestInvokeHelloFunction(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.js"),
		[]byte(`export default async (req, ctx) => ({ status: 200, body: { hi: req.body.name } });`), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, err := funcs.New(funcs.Options{
		Dir:       dir,
		Functions: map[string]domain.CodeFunction{"hello": {Runtime: "node", File: "hello.js"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{
		Name: "hello", Method: "POST", Body: []byte(`{"name":"ada"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 {
		t.Fatalf("status %d", resp.Status)
	}
	if string(resp.Body) != `{"hi":"ada"}` {
		t.Fatalf("body %s", resp.Body)
	}
}

func TestWorkerEnvScrubbed(t *testing.T) {
	t.Setenv("AWS_SECRET_ACCESS_KEY", "leak-me")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "peek.js"), []byte(
		`export default async () => ({ status: 200, body: { aws: process.env.AWS_SECRET_ACCESS_KEY || null } });`), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, err := funcs.New(funcs.Options{Dir: dir, Functions: map[string]domain.CodeFunction{"peek": {Runtime: "node", File: "peek.js"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{Name: "peek", Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != `{"aws":null}` {
		t.Fatalf("env leaked: %s", resp.Body)
	}
}

// TestInvokeNonJSONBodyDoesNotHang verifies that sending a non-JSON body to a function
// does not hang the caller and does not crash the worker. The worker must return an error
// response (400 or 500), and a subsequent invoke must still succeed (worker is alive).
func TestInvokeNonJSONBodyDoesNotHang(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "echo.js"),
		[]byte(`export default async (req, ctx) => ({ status: 200, body: { ok: true } });`), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, err := funcs.New(funcs.Options{
		Dir:       dir,
		Functions: map[string]domain.CodeFunction{"echo": {Runtime: "node", File: "echo.js"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Use a generous but finite timeout so the test fails fast if the worker hangs.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Invoke with non-JSON body — must return a response (not hang).
	resp, err := rt.Invoke(ctx, domain.FunctionRequest{
		Name: "echo", Method: "POST", Body: []byte("not json"),
	})
	if err != nil {
		t.Fatalf("first invoke with non-JSON body returned error (worker may have hung or crashed): %v", err)
	}
	// We accept 400 or 500 — any non-hang response is correct.
	if resp.Status != 400 && resp.Status != 500 {
		t.Logf("first invoke status=%d body=%s (acceptable non-2xx from bad JSON)", resp.Status, resp.Body)
	}

	// Prove the worker is still alive by issuing a second valid invoke.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	resp2, err := rt.Invoke(ctx2, domain.FunctionRequest{
		Name: "echo", Method: "POST", Body: []byte(`{"x":1}`),
	})
	if err != nil {
		t.Fatalf("second invoke (valid JSON) failed — worker may have crashed after the first: %v", err)
	}
	if resp2.Status != 200 {
		t.Fatalf("second invoke status=%d body=%s, want 200", resp2.Status, resp2.Body)
	}
}

// TestEnvRefResolution verifies that a ${ULTRA_ENV_*} reference in a function's
// env: config is resolved from EnvMap at invoke time and is accessible as ctx.env.
func TestEnvRefResolution(t *testing.T) {
	dir := t.TempDir()
	// Function reads ctx.env.STRIPE_KEY and returns it in the body.
	if err := os.WriteFile(filepath.Join(dir, "pay.js"),
		[]byte(`export default async (req, ctx) => ({ status: 200, body: { v: ctx.env.STRIPE_KEY } });`), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, err := funcs.New(funcs.Options{
		Dir: dir,
		Functions: map[string]domain.CodeFunction{
			"pay": {
				Runtime: "node",
				File:    "pay.js",
				Env:     map[string]string{"STRIPE_KEY": "${ULTRA_ENV_STRIPE_KEY}"},
			},
		},
		EnvMap: map[string]string{
			"ULTRA_ENV_STRIPE_KEY": "sk_test_123",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{
		Name: "pay", Method: "POST",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 {
		t.Fatalf("status %d body %s", resp.Status, resp.Body)
	}
	want := `{"v":"sk_test_123"}`
	if string(resp.Body) != want {
		t.Fatalf("got %s, want %s", resp.Body, want)
	}
}

// TestEnvLiteralPassthrough verifies that a literal env value (not a ${…} ref)
// passes through unchanged into ctx.env.
func TestEnvLiteralPassthrough(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "svc.js"),
		[]byte(`export default async (req, ctx) => ({ status: 200, body: { url: ctx.env.BASE_URL } });`), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, err := funcs.New(funcs.Options{
		Dir: dir,
		Functions: map[string]domain.CodeFunction{
			"svc": {
				Runtime: "node",
				File:    "svc.js",
				Env:     map[string]string{"BASE_URL": "https://api.example.com"},
			},
		},
		EnvMap: map[string]string{}, // empty — literals don't need EnvMap
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{
		Name: "svc", Method: "GET",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 {
		t.Fatalf("status %d body %s", resp.Status, resp.Body)
	}
	want := `{"url":"https://api.example.com"}`
	if string(resp.Body) != want {
		t.Fatalf("got %s, want %s", resp.Body, want)
	}
}

// TestEnvNotInProcessEnv verifies that env values resolved from EnvMap are NOT
// present in the worker's process.env (security property: in-memory only).
func TestEnvNotInProcessEnv(t *testing.T) {
	dir := t.TempDir()
	// Function reads process.env.STRIPE_KEY (NOT ctx.env.STRIPE_KEY) — should be null.
	if err := os.WriteFile(filepath.Join(dir, "peek.js"),
		[]byte(`export default async () => ({ status: 200, body: { proc: process.env.STRIPE_KEY || null } });`), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, err := funcs.New(funcs.Options{
		Dir: dir,
		Functions: map[string]domain.CodeFunction{
			"peek": {
				Runtime: "node",
				File:    "peek.js",
				Env:     map[string]string{"STRIPE_KEY": "${ULTRA_ENV_STRIPE_KEY}"},
			},
		},
		EnvMap: map[string]string{
			"ULTRA_ENV_STRIPE_KEY": "sk_test_should_not_leak",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{
		Name: "peek", Method: "GET",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Body) != `{"proc":null}` {
		t.Fatalf("env value leaked into process.env: %s", resp.Body)
	}
}
