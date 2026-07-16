//go:build integration

package funcs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

// TestResponseBinaryBody: a handler may return a Node Buffer as result.body, and
// the worker writes the exact bytes back instead of JSON-stringifying them. This
// is the response-side counterpart to req.rawBody — it lets functions emit files,
// images, or any binary payload. The test uses bytes that are not valid UTF-8
// (0xFF, 0xFE) so a JSON round-trip would visibly corrupt them.
func TestResponseBinaryBody(t *testing.T) {
	rt := newRawRuntime(t, `export default async () => ({
		status: 200,
		headers: { "content-type": "application/octet-stream" },
		body: Buffer.from([0, 1, 2, 3, 255, 254]),
	});`, nil, nil)

	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{Name: "fn", Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 {
		t.Fatalf("status %d body %x", resp.Status, resp.Body)
	}
	want := []byte{0, 1, 2, 3, 255, 254}
	if !bytes.Equal(resp.Body, want) {
		t.Errorf("body = %x, want %x (binary body was not written raw)", resp.Body, want)
	}
	if ct := http.Header(resp.Headers).Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("content-type = %q, want application/octet-stream", ct)
	}
}

// TestResponseStringAndJSONStillWork is a regression guard: adding Buffer support
// must not change how string and object bodies are serialized.
func TestResponseStringAndJSONStillWork(t *testing.T) {
	rt := newRawRuntime(t, `export default async (req) =>
		req.query.kind === "string"
			? { status: 200, body: "plain" }
			: { status: 200, body: { ok: true } };`, nil, nil)

	str, err := rt.Invoke(context.Background(), domain.FunctionRequest{
		Name: "fn", Method: "GET", Query: map[string][]string{"kind": {"string"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(str.Body) != "plain" {
		t.Errorf("string body = %q, want \"plain\"", str.Body)
	}

	obj, err := rt.Invoke(context.Background(), domain.FunctionRequest{
		Name: "fn", Method: "GET", Query: map[string][]string{"kind": {"json"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(obj.Body) != `{"ok":true}` {
		t.Errorf("json body = %q, want {\"ok\":true}", obj.Body)
	}
}

// TestQueryAndHeadersMultiValue: req.query / req.headers stay first-value (the
// existing contract), and req.queryAll / req.headersAll expose every value for a
// repeated key.
func TestQueryAndHeadersMultiValue(t *testing.T) {
	rt := newRawRuntime(t, `export default async (req) => ({
		status: 200,
		body: {
			qFirst: req.query.tag,
			qAll: req.queryAll.tag,
			hFirst: req.headers["x-multi"],
			hAll: req.headersAll["x-multi"],
		},
	});`, nil, nil)

	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{
		Name: "fn", Method: "GET",
		Query: map[string][]string{"tag": {"a", "b"}},
		Headers: map[string][]string{
			"X-Multi":      {"a", "b"},
			"Content-Type": {"application/json"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 {
		t.Fatalf("status %d body %s", resp.Status, resp.Body)
	}
	var got struct {
		QFirst string   `json:"qFirst"`
		QAll   []string `json:"qAll"`
		HFirst string   `json:"hFirst"`
		HAll   []string `json:"hAll"`
	}
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", resp.Body, err)
	}
	if got.QFirst != "a" {
		t.Errorf("query.tag = %q, want \"a\"", got.QFirst)
	}
	if len(got.QAll) != 2 || got.QAll[0] != "a" || got.QAll[1] != "b" {
		t.Errorf("queryAll.tag = %v, want [a b]", got.QAll)
	}
	if got.HFirst != "a" {
		t.Errorf("headers[x-multi] = %q, want \"a\"", got.HFirst)
	}
	if len(got.HAll) != 2 || got.HAll[0] != "a" || got.HAll[1] != "b" {
		t.Errorf("headersAll[x-multi] = %v, want [a b]", got.HAll)
	}
}

// TestRawQuery: req.rawQuery is the exact unparsed query string, for handlers that
// must verify a signature computed over the query string itself.
func TestRawQuery(t *testing.T) {
	rt := newRawRuntime(t, `export default async (req) => ({
		status: 200,
		body: { rq: req.rawQuery, path: req.path },
	});`, nil, nil)

	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{
		Name: "fn", Method: "GET",
		Path:     "/functions/v1/fn",
		RawQuery: "tag=a&tag=b&sig=abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 {
		t.Fatalf("status %d body %s", resp.Status, resp.Body)
	}
	var got struct {
		RQ   string `json:"rq"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", resp.Body, err)
	}
	if got.RQ != "tag=a&tag=b&sig=abc123" {
		t.Errorf("rawQuery = %q, want \"tag=a&tag=b&sig=abc123\"", got.RQ)
	}
}
