//go:build integration

package funcs_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/instancez/instancez/internal/adapter/funcs"
	"github.com/instancez/instancez/internal/domain"
)

// newRawRuntime spins up a single-function runtime from inline JS source.
func newRawRuntime(t *testing.T, src string, env, envMap map[string]string) *funcs.Runtime {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fn.js"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, err := funcs.New(funcs.Options{
		Dir:       dir,
		Functions: map[string]domain.CodeFunction{"fn": {Runtime: "node", File: "fn.js", Env: env}},
		EnvMap:    envMap,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close() })
	return rt
}

// TestRawBodyExactBytesJSON: for a JSON request, req.rawBody is a Buffer holding
// the exact posted bytes, while req.body is the parsed object. Both coexist.
func TestRawBodyExactBytesJSON(t *testing.T) {
	rt := newRawRuntime(t, `export default async (req) => ({
		status: 200,
		body: {
			isBuffer: Buffer.isBuffer(req.rawBody),
			raw: req.rawBody.toString(),
			rawLen: req.rawBody.length,
			parsedName: req.body.name,
		},
	});`, nil, nil)

	posted := []byte(`{"name":"ada","n":2}`)
	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{
		Name: "fn", Method: "POST",
		Headers: map[string][]string{"Content-Type": {"application/json"}},
		Body:    posted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 {
		t.Fatalf("status %d body %s", resp.Status, resp.Body)
	}
	var got struct {
		IsBuffer   bool   `json:"isBuffer"`
		Raw        string `json:"raw"`
		RawLen     int    `json:"rawLen"`
		ParsedName string `json:"parsedName"`
	}
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", resp.Body, err)
	}
	if !got.IsBuffer {
		t.Error("req.rawBody is not a Buffer")
	}
	if got.Raw != string(posted) {
		t.Errorf("rawBody = %q, want %q", got.Raw, posted)
	}
	if got.RawLen != len(posted) {
		t.Errorf("rawBody length = %d, want %d", got.RawLen, len(posted))
	}
	if got.ParsedName != "ada" {
		t.Errorf("parsed body.name = %q, want \"ada\"", got.ParsedName)
	}
}

// TestRawBodySignatureFidelity is the regression that protects the webhook use
// case. It reproduces exactly how Stripe verifies a webhook (HMAC-SHA256 over the
// raw request bytes) without depending on the stripe package. It proves two
// things at once:
//   - an HMAC computed over req.rawBody matches one computed over the bytes the
//     client actually sent (so signature verification succeeds), and
//   - an HMAC computed over a re-serialized req.body does NOT match (which is the
//     whole reason rawBody has to exist: JSON.parse → JSON.stringify is lossy for
//     whitespace, so the parsed body can never reproduce the signed bytes).
//
// If a future change ever routes rawBody through the JSON round-trip, the first
// assertion fails and this test catches the regression.
func TestRawBodySignatureFidelity(t *testing.T) {
	const secret = "whsec_test_secret"

	rt := newRawRuntime(t, `import { createHmac } from "node:crypto";
	export default async (req, ctx) => {
		const sign = (buf) => createHmac("sha256", ctx.env.SIGNING_SECRET).update(buf).digest("hex");
		return {
			status: 200,
			body: {
				isBuffer: Buffer.isBuffer(req.rawBody),
				rawLen: req.rawBody.length,
				rawSig: sign(req.rawBody),
				bodySig: sign(Buffer.from(JSON.stringify(req.body))),
			},
		};
	};`, map[string]string{"SIGNING_SECRET": secret}, nil)

	// Non-canonical spacing and a multibyte character ('á' is two UTF-8 bytes).
	// JSON.stringify(parsed) collapses the spacing, so its bytes differ from these.
	posted := []byte("{ \"amount\": 1000,\n  \"name\": \"adá\",\n  \"nested\": {\"a\": 1} }")

	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{
		Name: "fn", Method: "POST",
		Headers: map[string][]string{"Content-Type": {"application/json; charset=utf-8"}},
		Body:    posted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 {
		t.Fatalf("status %d body %s", resp.Status, resp.Body)
	}
	var got struct {
		IsBuffer bool   `json:"isBuffer"`
		RawLen   int    `json:"rawLen"`
		RawSig   string `json:"rawSig"`
		BodySig  string `json:"bodySig"`
	}
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", resp.Body, err)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(posted)
	want := hex.EncodeToString(mac.Sum(nil))

	if !got.IsBuffer {
		t.Error("req.rawBody is not a Buffer")
	}
	if got.RawLen != len(posted) {
		t.Errorf("rawBody length = %d, want %d (byte length, not char length)", got.RawLen, len(posted))
	}
	if got.RawSig != want {
		t.Errorf("signature over req.rawBody = %s, want %s — rawBody is NOT byte-identical to the request", got.RawSig, want)
	}
	if got.BodySig == want {
		t.Error("signature over re-serialized req.body matched the raw signature; the test payload is not actually lossy, so it no longer guards the regression")
	}
}

// TestRawBodyEmpty: with no request body, req.rawBody is an empty Buffer (length 0)
// and req.body is undefined. (Stripe never sends an empty webhook, but a handler
// must not crash on req.rawBody for a bodyless request.)
func TestRawBodyEmpty(t *testing.T) {
	rt := newRawRuntime(t, `export default async (req) => ({
		status: 200,
		body: {
			isBuffer: Buffer.isBuffer(req.rawBody),
			rawLen: req.rawBody.length,
			bodyUndefined: req.body === undefined,
		},
	});`, nil, nil)

	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{Name: "fn", Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 {
		t.Fatalf("status %d body %s", resp.Status, resp.Body)
	}
	var got struct {
		IsBuffer      bool `json:"isBuffer"`
		RawLen        int  `json:"rawLen"`
		BodyUndefined bool `json:"bodyUndefined"`
	}
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", resp.Body, err)
	}
	if !got.IsBuffer {
		t.Error("req.rawBody is not a Buffer on an empty request")
	}
	if got.RawLen != 0 {
		t.Errorf("rawBody length = %d, want 0", got.RawLen)
	}
	if !got.BodyUndefined {
		t.Error("req.body should be undefined for an empty request")
	}
}

// TestRawBodyNonJSON: for a non-JSON content type, req.body is already the raw
// string, and req.rawBody holds the identical bytes as a Buffer.
func TestRawBodyNonJSON(t *testing.T) {
	rt := newRawRuntime(t, `export default async (req) => ({
		status: 200,
		body: {
			isBuffer: Buffer.isBuffer(req.rawBody),
			raw: req.rawBody.toString(),
			bodyIsString: typeof req.body === "string",
			equal: req.rawBody.toString() === req.body,
		},
	});`, nil, nil)

	posted := []byte("plain text body, not json")
	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{
		Name: "fn", Method: "POST",
		Headers: map[string][]string{"Content-Type": {"text/plain"}},
		Body:    posted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 {
		t.Fatalf("status %d body %s", resp.Status, resp.Body)
	}
	var got struct {
		IsBuffer     bool   `json:"isBuffer"`
		Raw          string `json:"raw"`
		BodyIsString bool   `json:"bodyIsString"`
		Equal        bool   `json:"equal"`
	}
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", resp.Body, err)
	}
	if !got.IsBuffer {
		t.Error("req.rawBody is not a Buffer")
	}
	if got.Raw != string(posted) {
		t.Errorf("rawBody = %q, want %q", got.Raw, posted)
	}
	if !got.BodyIsString {
		t.Error("req.body should be a string for a non-JSON content type")
	}
	if !got.Equal {
		t.Error("req.rawBody bytes do not match req.body string for non-JSON request")
	}
}
