//go:build integration

package funcs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/saedx1/instancez/internal/adapter/funcs"
	"github.com/saedx1/instancez/internal/domain"
)

// lockedWriter is a mutex-guarded io.Writer that allows concurrent writes to
// a shared bytes.Buffer from the slog handler goroutines and reads from the
// test goroutine.
type lockedWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// slogRecord is the JSON shape emitted by slog.NewJSONHandler.
// After I1 the user-supplied fields are nested under "fields".
type slogRecord struct {
	Msg       string         `json:"msg"`
	Level     string         `json:"level"`
	RequestID string         `json:"requestId"`
	Fn        string         `json:"fn"`
	Fields    map[string]any `json:"fields"`
}

// TestLogCaptureAttribution verifies that:
//  1. ctx.log.info("hello", { who: "ada" }) is captured and forwarded via slog.
//  2. console.log("plain") — the patched console — is also captured.
//  3. Both log lines carry the requestId "req-123" and fn "noisy".
//  4. The "hello" record carries the field who=="ada" nested under "fields".
func TestLogCaptureAttribution(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}

	var mu sync.Mutex
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&lockedWriter{&mu, &buf}, nil))

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "noisy.js"), []byte(
		`export default async (req, ctx) => { ctx.log.info("hello", { who: "ada" }); console.log("plain"); return { status: 200, body: {} }; };`),
		0o644); err != nil {
		t.Fatal(err)
	}

	rt, err := funcs.New(funcs.Options{
		Dir:    dir,
		Logger: logger,
		Functions: map[string]domain.CodeFunction{
			"noisy": {Runtime: "node", File: "noisy.js"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	_, err = rt.Invoke(context.Background(), domain.FunctionRequest{
		Name:      "noisy",
		Method:    "GET",
		RequestID: "req-123",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Poll with timeout: logs stream asynchronously over the stdout pipe, so
	// they may arrive slightly after Invoke returns (which only waits for the
	// HTTP response over the unix socket).
	deadline := time.Now().Add(2 * time.Second)
	var captured string
	for time.Now().Before(deadline) {
		mu.Lock()
		captured = buf.String()
		mu.Unlock()
		if strings.Contains(captured, "req-123") && strings.Contains(captured, "hello") && strings.Contains(captured, "plain") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	captured = buf.String()
	mu.Unlock()

	// Parse NDJSON lines and collect records for "hello" and "plain".
	var helloRec, plainRec *slogRecord
	for _, line := range strings.Split(strings.TrimSpace(captured), "\n") {
		if line == "" {
			continue
		}
		var rec slogRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue // skip non-JSON lines (e.g. worker_stderr warnings)
		}
		switch rec.Msg {
		case "hello":
			r := rec
			helloRec = &r
		case "plain":
			r := rec
			plainRec = &r
		}
	}

	if helloRec == nil {
		t.Fatalf("no slog record with msg=hello found in output:\n%s", captured)
	}
	if plainRec == nil {
		t.Fatalf("no slog record with msg=plain found in output:\n%s", captured)
	}

	// Both records must carry the trusted top-level requestId and fn attrs.
	for _, rec := range []*slogRecord{helloRec, plainRec} {
		if rec.RequestID != "req-123" {
			t.Errorf("msg=%q: want requestId=req-123, got %q (full record: %+v)", rec.Msg, rec.RequestID, rec)
		}
		if rec.Fn != "noisy" {
			t.Errorf("msg=%q: want fn=noisy, got %q (full record: %+v)", rec.Msg, rec.Fn, rec)
		}
	}

	// The "hello" record must carry who=="ada" nested under "fields".
	if who, ok := helloRec.Fields["who"]; !ok {
		t.Errorf("hello record: want fields.who=ada, fields key absent; full record: %+v", helloRec)
	} else if s, _ := who.(string); s != "ada" {
		t.Errorf("hello record: want fields.who=ada, got %v", who)
	}
}
