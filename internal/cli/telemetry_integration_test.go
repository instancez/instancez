//go:build integration

package cli

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

type posthogStub struct {
	mu     sync.Mutex
	events []map[string]any
}

func newPosthogStub(t *testing.T) (*posthogStub, string) {
	t.Helper()
	s := &posthogStub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				return
			}
			body = gz
		}
		var payload struct {
			Batch []map[string]any `json:"batch"`
		}
		if err := json.NewDecoder(body).Decode(&payload); err == nil {
			s.mu.Lock()
			s.events = append(s.events, payload.Batch...)
			s.mu.Unlock()
		}
		_, _ = w.Write([]byte(`{"status":1}`))
	}))
	t.Cleanup(srv.Close)
	return s, srv.URL
}

func (s *posthogStub) take() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev := s.events
	s.events = nil
	return ev
}

func TestTelemetryBinary(t *testing.T) {
	stub, host := newPosthogStub(t)
	bin := filepath.Join(t.TempDir(), "inz")
	ldflags := "-X github.com/instancez/instancez/internal/cli.version=1.0.0 -X github.com/instancez/instancez/internal/telemetry.Host=" + host
	build := exec.Command("go", "build", "-ldflags", ldflags, "-o", bin, "../../cmd/inz")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	cfg := filepath.Join(t.TempDir(), "instancez.yaml")
	if err := os.WriteFile(cfg, []byte(reloadBaseYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	runBin := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}, env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	runBin(nil, "validate", "--config", cfg)
	events := stub.take()
	if len(events) != 1 || events[0]["event"] != "command_run" {
		t.Fatalf("events = %v", events)
	}
	props, _ := events[0]["properties"].(map[string]any)
	if props["command"] != "validate" || props["success"] != true || props["version"] != "1.0.0" {
		t.Errorf("props = %v", props)
	}

	for name, c := range map[string]struct {
		env  []string
		args []string
	}{
		"DO_NOT_TRACK":                 {[]string{"DO_NOT_TRACK=1"}, nil},
		"INSTANCEZ_TELEMETRY_DISABLED": {[]string{"INSTANCEZ_TELEMETRY_DISABLED=1"}, nil},
		"--no-telemetry":               {nil, []string{"--no-telemetry"}},
	} {
		runBin(c.env, append([]string{"validate", "--config", cfg}, c.args...)...)
		if ev := stub.take(); len(ev) != 0 {
			t.Errorf("%s: sent %v", name, ev)
		}
	}
}
