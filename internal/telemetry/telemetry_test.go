package telemetry

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type safeBuf struct {
	mu sync.Mutex
	sb strings.Builder
}

func (b *safeBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.Write(p)
}

func (b *safeBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.String()
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

type stub struct {
	*httptest.Server
	hits   atomic.Int32
	mu     sync.Mutex
	bodies []string
}

func newStub(t *testing.T, delay time.Duration) *stub {
	t.Helper()
	s := &stub{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		var rd io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			if gz, err := gzip.NewReader(r.Body); err == nil {
				rd = gz
			}
		}
		b, _ := io.ReadAll(rd)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(b))
		s.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
			}
		}
		_, _ = w.Write([]byte(`{"status":1}`))
	}))
	t.Cleanup(s.Close)
	oldHost := Host
	Host = s.URL
	t.Cleanup(func() { Host = oldHost })
	return s
}

func (s *stub) all() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.bodies, "\n")
}

func TestEnabledMatrix(t *testing.T) {
	cases := []struct {
		name    string
		version string
		env     map[string]string
		flag    bool
		want    bool
		reason  string
	}{
		{"source build", "dev", nil, false, false, "source build"},
		{"env disabled", "1.0.0", map[string]string{"INSTANCEZ_TELEMETRY_DISABLED": "1"}, false, false, "INSTANCEZ_TELEMETRY_DISABLED"},
		{"env true upper", "1.0.0", map[string]string{"INSTANCEZ_TELEMETRY_DISABLED": "TRUE"}, false, false, "INSTANCEZ_TELEMETRY_DISABLED"},
		{"dnt", "1.0.0", map[string]string{"DO_NOT_TRACK": "yes"}, false, false, "DO_NOT_TRACK"},
		{"flag", "1.0.0", nil, true, false, "--no-telemetry"},
		{"none", "1.0.0", nil, false, true, ""},
		{"dnt zero", "1.0.0", map[string]string{"DO_NOT_TRACK": "0"}, false, true, ""},
		{"env empty", "1.0.0", map[string]string{"INSTANCEZ_TELEMETRY_DISABLED": ""}, false, true, ""},
		{"env junk", "1.0.0", map[string]string{"INSTANCEZ_TELEMETRY_DISABLED": "maybe"}, false, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Init(Options{Version: tc.version, FlagDisabled: tc.flag, Home: t.TempDir(), Stderr: &safeBuf{},
				Getenv: func(k string) string { return tc.env[k] }})
			got, reason := c.Enabled()
			if got != tc.want || reason != tc.reason {
				t.Fatalf("got (%v, %q) want (%v, %q)", got, reason, tc.want, tc.reason)
			}
		})
	}
}

func TestNoticeOnce(t *testing.T) {
	home := t.TempDir()
	c1, buf := newClient(t, home, nil)
	c1.MaybeShowNotice()
	c1.MaybeShowNotice()
	c2 := Init(Options{Version: "1.0.0", Home: home, Stderr: buf, Getenv: func(string) string { return "" }})
	c2.MaybeShowNotice()
	out := buf.String()
	if n := strings.Count(out, "inz collects anonymous usage data"); n != 1 {
		t.Fatalf("notice count = %d in %q", n, out)
	}
	for _, want := range []string{"Disable: inz telemetry disable  (or INSTANCEZ_TELEMETRY_DISABLED=1)", "Details: https://docs.instancez.ai/telemetry/"} {
		if !contains(out, want) {
			t.Fatalf("missing %q", want)
		}
	}
}

func TestNoticeSkippedWhenDisabled(t *testing.T) {
	c, buf := newClient(t, t.TempDir(), map[string]string{"DO_NOT_TRACK": "1"})
	c.MaybeShowNotice()
	if buf.String() != "" {
		t.Fatalf("printed %q", buf.String())
	}
}

func TestCaptureDebugDoesNotSend(t *testing.T) {
	s := newStub(t, 0)
	c, buf := newClient(t, t.TempDir(), map[string]string{"INSTANCEZ_TELEMETRY_DEBUG": "1"})
	c.Capture("command_run", map[string]any{"command": "init"})
	c.Close()
	var ev struct {
		Event      string         `json:"event"`
		DistinctID string         `json:"distinct_id"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &ev); err != nil {
		t.Fatalf("stderr not json: %q: %v", buf.String(), err)
	}
	if ev.Event != "command_run" || ev.DistinctID != c.AnonymousID() || ev.Properties["command"] != "init" {
		t.Fatalf("bad payload %+v", ev)
	}
	if s.hits.Load() != 0 {
		t.Fatal("debug mode sent")
	}
}

func TestCaptureDebugWorksForSourceBuild(t *testing.T) {
	buf := &safeBuf{}
	c := Init(Options{Version: "dev", Home: t.TempDir(), Stderr: buf,
		Getenv: func(k string) string {
			if k == "INSTANCEZ_TELEMETRY_DEBUG" {
				return "1"
			}
			return ""
		}})
	c.Capture("x", nil)
	if !contains(buf.String(), `"event":"x"`) {
		t.Fatalf("got %q", buf.String())
	}
}

func TestCaptureDebugSilentWhenOptedOut(t *testing.T) {
	c, buf := newClient(t, t.TempDir(), map[string]string{"INSTANCEZ_TELEMETRY_DEBUG": "1", "DO_NOT_TRACK": "1"})
	c.Capture("x", nil)
	if buf.String() != "" {
		t.Fatalf("printed %q", buf.String())
	}
}

func TestCaptureSends(t *testing.T) {
	s := newStub(t, 0)
	c, _ := newClient(t, t.TempDir(), nil)
	c.Capture("command_run", map[string]any{"command": "validate"})
	c.Close()
	body := s.all()
	if !contains(body, "command_run") || !contains(body, c.AnonymousID()) {
		t.Fatalf("body missing event or id: %s", body)
	}
	if !contains(body, "$process_person_profile") || !contains(body, "$geoip_disable") {
		t.Fatalf("missing privacy props: %s", body)
	}
}

func TestCaptureNilProps(t *testing.T) {
	s := newStub(t, 0)
	c, _ := newClient(t, t.TempDir(), nil)
	c.Capture("command_run", nil)
	c.Close()
	if s.hits.Load() != 1 {
		t.Fatalf("hits = %d", s.hits.Load())
	}
}

func TestDisabledSendsNothing(t *testing.T) {
	s := newStub(t, 0)
	c, _ := newClient(t, t.TempDir(), map[string]string{"DO_NOT_TRACK": "1"})
	c.Capture("command_run", nil)
	c.Close()
	if s.hits.Load() != 0 {
		t.Fatal("sent while disabled")
	}
}

func TestCloseWithoutCapture(t *testing.T) {
	c, _ := newClient(t, t.TempDir(), nil)
	c.Close()
}

func TestCloseCapped(t *testing.T) {
	newStub(t, 10*time.Second)
	c, _ := newClient(t, t.TempDir(), nil)
	c.Capture("command_run", nil)
	start := time.Now()
	c.Close()
	if d := time.Since(start); d > 2500*time.Millisecond {
		t.Fatalf("Close took %v", d)
	}
}

func TestCaptureConcurrent(t *testing.T) {
	s := newStub(t, 0)
	c, _ := newClient(t, t.TempDir(), nil)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Capture("command_run", map[string]any{"i": i})
		}()
	}
	wg.Wait()
	c.Close()
	if s.hits.Load() == 0 {
		t.Fatal("nothing sent")
	}
}

func TestInitStateFileCreation(t *testing.T) {
	cases := []struct {
		name string
		o    Options
		env  map[string]string
		want bool
	}{
		{"enabled", Options{Version: "1.0.0"}, nil, true},
		{"do not track", Options{Version: "1.0.0"}, map[string]string{"DO_NOT_TRACK": "1"}, false},
		{"env disabled", Options{Version: "1.0.0"}, map[string]string{"INSTANCEZ_TELEMETRY_DISABLED": "true"}, false},
		{"flag", Options{Version: "1.0.0", FlagDisabled: true}, nil, false},
		{"source build", Options{Version: "dev"}, nil, false},
		{"no persist", Options{Version: "1.0.0", NoPersist: true}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			tc.o.Home = home
			tc.o.Getenv = func(k string) string { return tc.env[k] }
			c := Init(tc.o)
			if c.AnonymousID() == "" {
				t.Fatal("no id")
			}
			_, err := os.Stat(filepath.Join(home, ".instancez"))
			if got := err == nil; got != tc.want {
				t.Fatalf("state dir exists = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCaptureAddsBaseProps(t *testing.T) {
	c, buf := newClient(t, t.TempDir(), map[string]string{"INSTANCEZ_TELEMETRY_DEBUG": "1", "CI": "true", "CLAUDECODE": "1"})
	c.Capture("x", map[string]any{"command": "init", "$geoip_disable": false})
	var ev struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &ev); err != nil {
		t.Fatal(err)
	}
	p := ev.Properties
	if p["is_ci"] != true || p["is_agent"] != true || p["version"] != "1.0.0" || p["os"] == "" || p["arch"] == "" || p["command"] != "init" {
		t.Fatalf("%v", p)
	}
	if p["$process_person_profile"] != false || p["$geoip_disable"] != true {
		t.Fatalf("privacy props not forced: %v", p)
	}
}

func TestCaptureBasePropsNoEnv(t *testing.T) {
	c, buf := newClient(t, t.TempDir(), map[string]string{"INSTANCEZ_TELEMETRY_DEBUG": "1"})
	c.Capture("x", nil)
	if !contains(buf.String(), `"is_ci":false`) || !contains(buf.String(), `"is_agent":false`) {
		t.Fatalf("%s", buf.String())
	}
}

func TestCaptureNeverSendsToProdUnderGoTest(t *testing.T) {
	if Host != prodHost {
		t.Skip("Host overridden")
	}
	c := Init(Options{Version: "1.0.0", Home: t.TempDir(), NoPersist: true})
	c.Capture("evt", map[string]any{"a": 1})
	if c.ph != nil {
		t.Fatal("client created for production host under go test")
	}
	c.Close()
}
