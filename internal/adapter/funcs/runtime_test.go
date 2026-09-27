package funcs

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/domain"
)

// TestTimeoutFor covers the default, the parsed value, and the clamp to
// maxTimeout (which keeps a function's deadline below the app Lambda's 30s
// execution limit).
func TestTimeoutFor(t *testing.T) {
	r := &Runtime{opts: Options{Functions: map[string]domain.CodeFunction{
		"unset":   {},
		"short":   {Timeout: "5s"},
		"bad":     {Timeout: "not-a-duration"},
		"over":    {Timeout: "120s"},
		"atLimit": {Timeout: "25s"},
	}}}
	cases := map[string]time.Duration{
		"unset":   defaultTimeout,
		"short":   5 * time.Second,
		"bad":     defaultTimeout,
		"over":    maxTimeout, // clamped down from 120s
		"atLimit": maxTimeout,
		"missing": defaultTimeout, // not in the map
	}
	for name, want := range cases {
		if got := r.timeoutFor(name); got != want {
			t.Errorf("timeoutFor(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestAsInstancezEnvRef exercises the ref-detection helper at the unit level.
func TestAsInstancezEnvRef(t *testing.T) {
	cases := []struct {
		in      string
		wantRef string
		wantOK  bool
	}{
		{"${INSTANCEZ_ENV_FOO}", "INSTANCEZ_ENV_FOO", true},
		{"${INSTANCEZ_ENV_STRIPE_KEY}", "INSTANCEZ_ENV_STRIPE_KEY", true},
		{"${INSTANCEZ_ENV_A_1_B}", "INSTANCEZ_ENV_A_1_B", true},
		// literals — must not match
		{"https://api.stripe.com", "", false},
		{"${FOO}", "", false},              // missing INSTANCEZ_ENV_ prefix
		{"${INSTANCEZ_DSN}", "", false},    // INSTANCEZ_ not INSTANCEZ_ENV_
		{"INSTANCEZ_ENV_FOO", "", false},       // no ${} wrapper
		{"${INSTANCEZ_ENV_}", "", false},       // empty suffix
		{" ${INSTANCEZ_ENV_FOO}", "", false},   // leading space
		{"${INSTANCEZ_ENV_FOO} ", "", false},   // trailing space
	}
	for _, tc := range cases {
		ref, ok := asInstancezEnvRef(tc.in)
		if ok != tc.wantOK || ref != tc.wantRef {
			t.Errorf("asInstancezEnvRef(%q) = (%q, %v), want (%q, %v)", tc.in, ref, ok, tc.wantRef, tc.wantOK)
		}
	}
}

// TestNewFailsEarlyOnMissingEnvRef verifies that New returns an error when a
// function references a ${INSTANCEZ_ENV_*} key that is absent from EnvMap — and
// does so BEFORE spawning node (no node required to run this test).
func TestNewFailsEarlyOnMissingEnvRef(t *testing.T) {
	opts := Options{
		Dir: t.TempDir(),
		Functions: map[string]domain.CodeFunction{
			"pay": {
				Runtime: "node",
				File:    "pay.js",
				Env:     map[string]string{"STRIPE_KEY": "${INSTANCEZ_ENV_MISSING}"},
			},
		},
		EnvMap: map[string]string{
			// INSTANCEZ_ENV_MISSING is intentionally absent
		},
	}
	_, err := New(opts)
	if err == nil {
		t.Fatal("expected error for missing INSTANCEZ_ENV_ ref, got nil")
	}
	// Error message should name the function and the missing ref.
	errStr := err.Error()
	if !containsAll(errStr, "pay", "INSTANCEZ_ENV_MISSING") {
		t.Fatalf("error message %q did not mention function name and ref", errStr)
	}
}

// TestNewRequiresNodeOnPath verifies that New fails with an actionable error
// when the `node` binary is not on PATH. Code functions cannot run without
// Node.js, so this must fail at startup (New) rather than at first invocation.
// PATH is emptied so exec.LookPath("node") fails deterministically regardless
// of whether the host has node installed. The assertion checks for the
// human-facing "Node.js" / minimum-version text so a future refactor cannot
// silently regress to the raw `exec: "node": ... not found` spawn error.
func TestNewRequiresNodeOnPath(t *testing.T) {
	t.Setenv("PATH", "")
	opts := Options{
		Dir: t.TempDir(),
		Functions: map[string]domain.CodeFunction{
			"hello": {Runtime: "node", File: "hello.js"},
		},
		EnvMap: map[string]string{},
	}
	_, err := New(opts)
	if err == nil {
		t.Fatal("expected error when node is missing from PATH, got nil")
	}
	if !containsAll(err.Error(), "Node.js", "22") {
		t.Fatalf("error %q should name Node.js and the minimum version", err.Error())
	}
}

// TestNewAcceptsLiteralEnvValues verifies that a literal env value (no ${…})
// does not trigger the fail-early check even when EnvMap is empty.
func TestNewAcceptsLiteralEnvValues(t *testing.T) {
	// No node available in unit tests, so we can only verify New does NOT
	// error out on the validation step. We expect it to proceed to spawning
	// node (which may fail) — that's fine; the important thing is the
	// validation itself passes.
	opts := Options{
		Dir: "/nonexistent-dir-that-will-fail-at-node-spawn",
		Functions: map[string]domain.CodeFunction{
			"svc": {
				Runtime: "node",
				File:    "svc.js",
				Env:     map[string]string{"BASE_URL": "https://api.example.com"},
			},
		},
		EnvMap: map[string]string{}, // empty — literals don't need EnvMap
	}
	_, err := New(opts)
	// The error (if any) must NOT be the fail-early validation error —
	// it may be a file-write or node-spawn error.
	if err != nil {
		if containsAll(err.Error(), "not in INSTANCEZ_ENV_ namespace") {
			t.Fatalf("literal env value triggered fail-early: %v", err)
		}
		// Any other error (shim write / node start) is expected in a unit
		// environment without a real functions dir or node binary.
	}
}

// containsAll reports whether s contains all of the given substrings.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// unixWorker returns a worker whose health client dials sock.
func unixWorker(sock string) *worker {
	return &worker{health: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}}
}

func TestWorkerResponsive(t *testing.T) {
	dir := t.TempDir()

	t.Run("answers", func(t *testing.T) {
		sock := filepath.Join(dir, "ok.sock")
		ln, err := net.Listen("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})}
		go func() { _ = srv.Serve(ln) }()
		defer func() { _ = srv.Close() }()
		if !unixWorker(sock).responsive() {
			t.Fatal("want responsive")
		}
	})

	t.Run("accepts but never replies", func(t *testing.T) {
		sock := filepath.Join(dir, "hang.sock")
		ln, err := net.Listen("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ln.Close() }()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				defer func() { _ = c.Close() }()
			}
		}()
		start := time.Now()
		if unixWorker(sock).responsive() {
			t.Fatal("want unresponsive")
		}
		if d := time.Since(start); d < healthProbeTimeout || d > healthProbeTimeout+2*time.Second {
			t.Fatalf("probe took %v, want about %v", d, healthProbeTimeout)
		}
	})

	t.Run("no socket", func(t *testing.T) {
		if unixWorker(filepath.Join(dir, "missing.sock")).responsive() {
			t.Fatal("want unresponsive")
		}
	})
}

func TestTimeoutProbeSkippedAfterClose(t *testing.T) {
	var logs bytes.Buffer
	r := &Runtime{closed: true, done: make(chan struct{}), logger: slog.New(slog.NewTextHandler(&logs, nil))}
	w := unixWorker(filepath.Join(t.TempDir(), "missing.sock"))
	w.cmd = &exec.Cmd{Process: &os.Process{Pid: 1}}
	w.healthy.Store(true)
	ctx := context.Background()
	if err := r.classifyDoErr(ctx, ctx, w, context.DeadlineExceeded); !errors.Is(err, ErrTimeout) {
		t.Fatalf("got %v, want ErrTimeout", err)
	}
	deadline := time.Now().Add(healthProbeTimeout + 2*time.Second)
	for w.probing.Load() {
		if time.Now().After(deadline) {
			t.Fatal("probe never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if strings.Contains(logs.String(), "unresponsive") {
		t.Fatalf("closed runtime logged a replace: %s", logs.String())
	}
	if !w.healthy.Load() {
		t.Fatal("closed runtime marked the worker unhealthy")
	}
}
