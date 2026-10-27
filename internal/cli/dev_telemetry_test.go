package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/telemetry"
)

func debugTele(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := tele
	tele = telemetry.Init(telemetry.Options{
		Version: "1.0.0", Home: t.TempDir(), Stderr: &buf,
		Getenv: func(k string) string { return debugEnv[k] },
	})
	t.Cleanup(func() { tele = old })
	resetDevRuntimeErrors()
	return &buf
}

func events(buf *bytes.Buffer) []teleEvent {
	tele.Close()
	var out []teleEvent
	for _, l := range strings.Split(buf.String(), "\n") {
		var ev teleEvent
		if strings.HasPrefix(l, "{") && json.Unmarshal([]byte(l), &ev) == nil && ev.Event != "" {
			out = append(out, ev)
		}
	}
	return out
}

func TestCaptureDevReady(t *testing.T) {
	buf := debugTele(t)
	captureDevReady(true, 2, 0)
	ev := find(events(buf), "dev_ready")
	if ev == nil {
		t.Fatalf("no dev_ready in %s", buf)
	}
	if ev.Props["embedded_pg"] != true || ev.Props["functions"] != float64(2) || ev.Props["tables"] != float64(0) {
		t.Fatalf("props: %v", ev.Props)
	}
	if d, ok := ev.Props["duration_ms"].(float64); !ok || d < 0 {
		t.Fatalf("duration_ms: %v", ev.Props["duration_ms"])
	}
}

func TestCaptureDevRuntimeError(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		class    string
		expected bool
		msgHas   string
		msgNot   string
	}{
		{"validation is expected", domain.ValidationErrors{{Path: "tables.a", Message: "bad"}}, "config_invalid", true, "tables.a: bad", ""},
		{"migration failure is unexpected", fmt.Errorf("migrate exec: boom postgres://u:secret@h/db"), "go:*errors.errorString", false, "postgres://<redacted>", "secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := debugTele(t)
			captureDevRuntimeError(tc.err)
			ev := find(events(buf), "dev_runtime_error")
			if ev == nil {
				t.Fatalf("no event in %s", buf)
			}
			if ev.Props["expected"] != tc.expected {
				t.Fatalf("expected=%v", ev.Props["expected"])
			}
			if c := ev.Props["error_class"].(string); tc.expected && c != tc.class {
				t.Fatalf("class=%q want %q", c, tc.class)
			}
			m := ev.Props["error_message"].(string)
			if !strings.Contains(m, tc.msgHas) || (tc.msgNot != "" && strings.Contains(m, tc.msgNot)) {
				t.Fatalf("message=%q", m)
			}
		})
	}
}

func TestDevCapturesNilSafe(t *testing.T) {
	old := tele
	tele = nil
	t.Cleanup(func() { tele = old })
	captureDevReady(false, 0, 0)
	captureDevRuntimeError(fmt.Errorf("x"))
	debugTele(t)
	captureDevRuntimeError(nil)
}
