package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/telemetry"
	"github.com/spf13/cobra"
)

type crashRun struct {
	stderr string
	events []teleEvent
	code   int
	panic  any
	home   string
}

var crashTestVersion = "1.0.0"

func (r crashRun) prompted() bool { return strings.Contains(r.stderr, "Send? [y/N/v=view]") }

func runCrash(t *testing.T, cmdName string, runE func() error, stdin string, tty bool, env map[string]string, args ...string) crashRun {
	t.Helper()
	old := version
	version = crashTestVersion
	t.Cleanup(func() { version = old })
	home := t.TempDir()
	var buf bytes.Buffer
	e := teleEnv{
		stderr:    &buf,
		getenv:    func(k string) string { return env[k] },
		home:      home,
		stdin:     strings.NewReader(stdin),
		tty:       func() bool { return tty },
		container: func() bool { return env["TEST_CONTAINER"] == "1" },
	}
	root := newRootCmd(e)
	c, _, err := root.Find([]string{cmdName})
	if err != nil || c == root {
		t.Fatalf("find %s: %v", cmdName, err)
	}
	c.RunE = func(*cobra.Command, []string) error { return runE() }
	out := crashRun{home: home}
	func() {
		defer func() { out.panic = recover() }()
		out.code = runWith(root, append([]string{cmdName}, args...), e)
	}()
	out.stderr = buf.String()
	for _, l := range strings.Split(out.stderr, "\n") {
		var ev teleEvent
		if i := strings.Index(l, `{"distinct_id"`); i >= 0 && json.Unmarshal([]byte(l[i:]), &ev) == nil && ev.Event != "" {
			out.events = append(out.events, ev)
		}
	}
	return out
}

func netErr() error { return &net.OpError{Op: "dial", Err: errors.New("refused")} }

func dbg(extra ...string) map[string]string {
	m := map[string]string{"INSTANCEZ_TELEMETRY_DEBUG": "1"}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i]] = extra[i+1]
	}
	return m
}

func writeProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfg := "functions:\n  hello:\n    file: functions/hello.js\nauth:\n  smtp:\n    password: hunter2\n"
	if err := os.MkdirAll(filepath.Join(dir, "functions"), 0o755); err != nil {
		t.Fatal(err)
	}
	for p, body := range map[string]string{"instancez.yaml": cfg, "functions/hello.js": "exports.h=1", "functions/package.json": "{}"} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCrashPromptOnlyUnexpected(t *testing.T) {
	cases := []struct {
		name, cmd string
		err       error
		tty       bool
		env       map[string]string
		prompt    bool
	}{
		{"expected on dev", "dev", reported("preflight:x"), true, dbg(), false},
		{"unexpected on dev", "dev", netErr(), true, dbg(), true},
		{"unexpected on validate", "validate", netErr(), true, dbg(), false},
		{"no tty", "dev", netErr(), false, dbg(), false},
		{"CI", "dev", netErr(), true, dbg("CI", "true"), false},
		{"env opt-out", "dev", netErr(), true, dbg("INSTANCEZ_TELEMETRY_DISABLED", "1"), false},
		{"DO_NOT_TRACK", "dev", netErr(), true, dbg("DO_NOT_TRACK", "1"), false},
	}
	for _, tc := range cases {
		r := runCrash(t, tc.cmd, func() error { return tc.err }, "y\n", tc.tty, tc.env)
		if r.prompted() != tc.prompt {
			t.Errorf("%s: prompted = %v\n%s", tc.name, r.prompted(), r.stderr)
		}
		if r.code != 1 {
			t.Errorf("%s: exit = %d", tc.name, r.code)
		}
	}
}

func TestCrashOptOutFlagNoPrompt(t *testing.T) {
	r := runCrash(t, "dev", func() error { return netErr() }, "y\n", true, dbg(), "--no-telemetry")
	if r.prompted() {
		t.Errorf("prompted despite --no-telemetry:\n%s", r.stderr)
	}
}

func TestCrashAcceptedAddsAttachmentsToSameEvent(t *testing.T) {
	dir := writeProject(t)
	r := runCrash(t, "dev", func() error { return netErr() }, "y\n", true, dbg(), "--config", filepath.Join(dir, "instancez.yaml"))
	var exc []teleEvent
	for _, e := range r.events {
		if e.Event == "$exception" {
			exc = append(exc, e)
		}
	}
	if len(exc) != 1 {
		t.Fatalf("want one $exception, got %d: %v", len(exc), r.events)
	}
	p := exc[0].Props
	yaml, _ := p["config_yaml"].(string)
	if !strings.Contains(yaml, "<redacted>") || strings.Contains(yaml, "hunter2") {
		t.Errorf("config_yaml = %q", yaml)
	}
	files, _ := p["function_files"].(map[string]any)
	if len(files) != 2 || files["functions/hello.js"] == nil || files["functions/package.json"] == nil {
		t.Errorf("function_files = %v", p["function_files"])
	}
	if _, ok := p["truncated_files"]; !ok || p["command"] != "dev" || p["expected"] != false {
		t.Errorf("props = %v", p)
	}
	if find(r.events, "command_run") == nil {
		t.Error("command_run missing")
	}
	if b, _ := json.Marshal(r.events); strings.Contains(string(b), "hunter2") || strings.Contains(r.stderr, "hunter2") {
		t.Error("secret hunter2 present in payload")
	}
}

func TestCrashDeclinedSendsPlainException(t *testing.T) {
	dir := writeProject(t)
	for _, in := range []string{"n\n", "\n", ""} {
		r := runCrash(t, "dev", func() error { return netErr() }, in, true, dbg(), "--config", filepath.Join(dir, "instancez.yaml"))
		ev := find(r.events, "$exception")
		if ev == nil || !r.prompted() {
			t.Fatalf("input %q: ev=%v prompted=%v", in, ev, r.prompted())
		}
		for _, k := range []string{"config_yaml", "function_files", "truncated_files"} {
			if _, ok := ev.Props[k]; ok {
				t.Errorf("input %q: declined event has %s", in, k)
			}
		}
	}
}

func TestCrashMissingFunctionsDirIsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "instancez.yaml"), []byte("tables: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := runCrash(t, "dev", func() error { return netErr() }, "y\n", true, dbg(), "--config", filepath.Join(dir, "instancez.yaml"))
	ev := find(r.events, "$exception")
	if ev == nil {
		t.Fatal("no $exception")
	}
	files, ok := ev.Props["function_files"].(map[string]any)
	if !ok || len(files) != 0 || ev.Props["config_yaml"] == "" {
		t.Errorf("props = %v", ev.Props)
	}
}

func TestCrashRemoteSourceAttachesNothing(t *testing.T) {
	for _, args := range [][]string{{"--config", "s3://b/k.yaml"}, {"--bundle", "s3://b/k"}} {
		r := runCrash(t, "serve", func() error { return netErr() }, "y\n", true, dbg(), args...)
		ev := find(r.events, "$exception")
		if ev == nil || ev.Props["config_yaml"] != "" || !strings.Contains(r.stderr, "not included (remote source)") {
			t.Errorf("%v: ev=%v\n%s", args, ev, r.stderr)
		}
	}
	r := runCrash(t, "serve", func() error { return netErr() }, "y\n", true, dbg("INSTANCEZ_BUNDLE", "s3://b/k"))
	if !strings.Contains(r.stderr, "not included (remote source)") {
		t.Errorf("env bundle not treated as remote:\n%s", r.stderr)
	}
}

func TestServeCrashSendsNothingUnlessAccepted(t *testing.T) {
	r := runCrash(t, "serve", func() error { return netErr() }, "n\n", true, dbg())
	if len(r.events) != 0 || !r.prompted() {
		t.Errorf("declined serve: events=%v prompted=%v", r.events, r.prompted())
	}
	if strings.Contains(r.stderr, "anonymous usage and error data") {
		t.Error("serve printed the first-run notice")
	}
	if _, err := os.Stat(filepath.Join(r.home, ".instancez")); err == nil {
		t.Error("declined serve prompt wrote state")
	}
	r = runCrash(t, "serve", func() error { return netErr() }, "y\n", true, dbg())
	if len(r.events) != 1 || r.events[0].Event != "$exception" {
		t.Errorf("accepted serve events = %v", r.events)
	}
}

func TestServeNonInteractiveNeverTouchesDisk(t *testing.T) {
	for _, tc := range []struct {
		tty bool
		env map[string]string
	}{{false, dbg()}, {true, dbg("CI", "1")}} {
		r := runCrash(t, "serve", func() error { return netErr() }, "y\n", tc.tty, tc.env)
		if _, err := os.Stat(filepath.Join(r.home, ".instancez")); err == nil {
			t.Errorf("serve touched disk (tty=%v)", tc.tty)
		}
		if len(r.events) != 0 || r.prompted() {
			t.Errorf("serve sent or asked: %v", r.events)
		}
	}
}

func TestPanicReportedThenRepanics(t *testing.T) {
	r := runCrash(t, "dev", func() error { panic("boom postgres://u:pw@h/db") }, "n\n", true, dbg())
	if r.panic == nil {
		t.Fatal("panic was swallowed")
	}
	if !r.prompted() {
		t.Errorf("no prompt on dev panic:\n%s", r.stderr)
	}
	ev := find(r.events, "$exception")
	if ev == nil {
		t.Fatalf("no $exception: %v", r.events)
	}
	item := ev.Props["$exception_list"].([]any)[0].(map[string]any)
	st, _ := item["stacktrace"].(map[string]any)
	if item["type"] != "panic" || len(st["frames"].([]any)) == 0 || strings.Contains(item["value"].(string), "u:pw") {
		t.Errorf("item = %v", item)
	}
	if ev.Props["expected"] != false {
		t.Errorf("expected = %v", ev.Props["expected"])
	}
}

func TestPanicOtherCommandReportsWithoutPrompt(t *testing.T) {
	r := runCrash(t, "validate", func() error { panic("boom") }, "y\n", true, dbg())
	ev := find(r.events, "$exception")
	if r.panic == nil || r.prompted() || ev == nil {
		t.Fatalf("panic=%v prompted=%v ev=%v", r.panic, r.prompted(), ev)
	}
	if _, ok := ev.Props["config_yaml"]; ok {
		t.Error("config attached without a prompt")
	}
}

func TestPanicServeNonInteractiveSendsNothing(t *testing.T) {
	r := runCrash(t, "serve", func() error { panic("boom") }, "y\n", false, dbg())
	if r.panic == nil || len(r.events) != 0 {
		t.Errorf("panic=%v events=%v", r.panic, r.events)
	}
	if _, err := os.Stat(filepath.Join(r.home, ".instancez")); err == nil {
		t.Error("serve touched disk")
	}
}

func TestPanicServeAcceptedSendsException(t *testing.T) {
	r := runCrash(t, "serve", func() error { panic("boom") }, "y\n", true, dbg())
	if r.panic == nil || len(r.events) != 1 || r.events[0].Event != "$exception" {
		t.Errorf("panic=%v events=%v", r.panic, r.events)
	}
}

func TestPanicNilValue(t *testing.T) {
	r := runCrash(t, "validate", func() error { panic(errors.New("")) }, "", false, dbg())
	if r.panic == nil || find(r.events, "$exception") == nil {
		t.Errorf("panic=%v events=%v", r.panic, r.events)
	}
}

func TestServeOptOutNeverTouchesDisk(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		args []string
	}{
		"disabled": {dbg("INSTANCEZ_TELEMETRY_DISABLED", "1"), nil},
		"dnt":      {dbg("DO_NOT_TRACK", "true"), nil},
		"flag":     {dbg(), []string{"--no-telemetry"}},
		"flag=":    {dbg(), []string{"--no-telemetry=true"}},
	} {
		r := runCrash(t, "serve", func() error { return netErr() }, "y\n", true, tc.env, tc.args...)
		if _, err := os.Stat(filepath.Join(r.home, ".instancez")); err == nil {
			t.Errorf("%s: touched disk", name)
		}
		if r.prompted() || len(r.events) != 0 {
			t.Errorf("%s: prompted=%v events=%v", name, r.prompted(), r.events)
		}
	}
}

// panicAfterPrompt panics once the crash prompt has been written.
type panicAfterPrompt struct{ buf *bytes.Buffer }

func (w panicAfterPrompt) Write(p []byte) (int, error) {
	w.buf.Write(p)
	if strings.Contains(string(p), "Send? [y/N/v=view]") {
		panic("prompt boom")
	}
	return len(p), nil
}

func TestPanicInsideFinishDoesNotReportTwice(t *testing.T) {
	old := version
	version = "1.0.0"
	defer func() { version = old }()
	var buf bytes.Buffer
	e := teleEnv{stderr: panicAfterPrompt{&buf}, getenv: func(k string) string { return dbg()[k] }, home: t.TempDir(),
		stdin: strings.NewReader(""), tty: func() bool { return true }}
	root := newRootCmd(e)
	c, _, _ := root.Find([]string{"dev"})
	c.RunE = func(*cobra.Command, []string) error { return netErr() }
	var got any
	func() {
		defer func() { got = recover() }()
		runWith(root, []string{"dev"}, e)
	}()
	if got == nil {
		t.Fatal("panic swallowed")
	}
	if n := strings.Count(buf.String(), "Send? [y/N/v=view]"); n != 1 {
		t.Errorf("prompted %d times", n)
	}
	if strings.Count(buf.String(), `"event":"$exception"`) > 1 {
		t.Error("duplicate exception")
	}
}

func TestServeStateDisabledFileNotRewritten(t *testing.T) {
	old := version
	version = "1.0.0"
	defer func() { version = old }()
	home := t.TempDir()
	if err := telemetry.SetDisabled(home, true); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(home, ".instancez", "telemetry.json")
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	_ = os.Chtimes(p, past, past)
	before, _ := os.ReadFile(p)
	var buf bytes.Buffer
	e := teleEnv{stderr: &buf, getenv: func(string) string { return "" }, home: home,
		stdin: strings.NewReader("y\n"), tty: func() bool { return true }}
	root := newRootCmd(e)
	c, _, _ := root.Find([]string{"serve"})
	c.RunE = func(*cobra.Command, []string) error { return netErr() }
	runWith(root, []string{"serve"}, e)
	after, _ := os.ReadFile(p)
	fi, _ := os.Stat(p)
	if !bytes.Equal(before, after) || !fi.ModTime().Equal(past) {
		t.Errorf("state rewritten: %s -> %s", before, after)
	}
	if strings.Contains(buf.String(), "Send?") {
		t.Error("prompted while disabled")
	}
}

func TestSourceBuildSkipsCrashPromptUnlessDebug(t *testing.T) {
	old := crashTestVersion
	crashTestVersion = "dev"
	t.Cleanup(func() { crashTestVersion = old })
	for _, cmd := range []string{"dev", "serve"} {
		if r := runCrash(t, cmd, func() error { return netErr() }, "y\n", true, map[string]string{}); r.prompted() {
			t.Errorf("%s: source build prompted without debug:\n%s", cmd, r.stderr)
		}
		if r := runCrash(t, cmd, func() error { return netErr() }, "y\n", true, dbg()); !r.prompted() {
			t.Errorf("%s: source build debug did not prompt:\n%s", cmd, r.stderr)
		}
	}
}

func TestServeCrashOptOutsLeaveNoState(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"DO_NOT_TRACK": dbg("DO_NOT_TRACK", "1"),
		"disabled":     dbg("INSTANCEZ_TELEMETRY_DISABLED", "yes"),
	} {
		r := runCrash(t, "serve", func() error { return netErr() }, "y\n", true, env)
		if r.prompted() || len(r.events) != 0 {
			t.Errorf("%s: prompted=%v events=%v", name, r.prompted(), r.events)
		}
		if _, err := os.Stat(filepath.Join(r.home, ".instancez")); err == nil {
			t.Errorf("%s: wrote state", name)
		}
	}
}

func TestPanicStackIsStructuredFrames(t *testing.T) {
	r := runCrash(t, "dev", func() error { panic("boom") }, "y\n", true, dbg())
	ev := find(r.events, "$exception")
	if ev == nil {
		t.Fatalf("no $exception: %v", r.events)
	}
	st := ev.Props["$exception_list"].([]any)[0].(map[string]any)["stacktrace"].(map[string]any)
	if _, ok := st["frames_raw"]; ok || st["type"] != "raw" {
		t.Fatalf("stacktrace = %v", st)
	}
	frames := st["frames"].([]any)
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	home, _ := os.UserHomeDir()
	found := false
	for _, f := range frames {
		fr := f.(map[string]any)
		found = found || strings.Contains(fr["function"].(string), "TestPanicStackIsStructuredFrames")
		if home != "" && strings.Contains(fr["filename"].(string), home) {
			t.Errorf("home leaked in %v", fr)
		}
	}
	top := frames[len(frames)-1].(map[string]any)
	if !found || strings.Contains(top["function"].(string), "internal/telemetry") || strings.Contains(top["function"].(string), "reportPanic") {
		t.Errorf("found=%v top=%v", found, top)
	}
	t.Logf("top frame: %v", top)
	if !strings.Contains(r.stderr, `"frames"`) {
		t.Error("view/debug output must show structured frames")
	}
}
