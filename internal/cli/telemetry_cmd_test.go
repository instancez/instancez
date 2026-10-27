package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type teleEvent struct {
	Event string         `json:"event"`
	Props map[string]any `json:"properties"`
}

func runCLI(t *testing.T, home string, env map[string]string, args ...string) (stderr string, events []teleEvent, code int) {
	t.Helper()
	old := version
	version = "1.0.0"
	t.Cleanup(func() { version = old })
	var buf bytes.Buffer
	code = run(args, teleEnv{
		stderr: &buf,
		getenv: func(k string) string { return env[k] },
		home:   home,
	})
	for _, l := range strings.Split(buf.String(), "\n") {
		var ev teleEvent
		if strings.HasPrefix(l, "{") && json.Unmarshal([]byte(l), &ev) == nil && ev.Event != "" {
			events = append(events, ev)
		}
	}
	return buf.String(), events, code
}

func find(events []teleEvent, name string) *teleEvent {
	for i := range events {
		if events[i].Event == name {
			return &events[i]
		}
	}
	return nil
}

var debugEnv = map[string]string{"INSTANCEZ_TELEMETRY_DEBUG": "1"}

func TestTelemetryCmdDisableEnable(t *testing.T) {
	home := t.TempDir()
	statePath := filepath.Join(home, ".instancez", "telemetry.json")

	if _, _, code := runCLI(t, home, nil, "telemetry", "disable"); code != 0 {
		t.Fatalf("disable exit %d", code)
	}
	b, err := os.ReadFile(statePath)
	if err != nil || !strings.Contains(string(b), `"disabled":true`) {
		t.Fatalf("state after disable = %s, %v", b, err)
	}
	if out := statusOut(t, home); !strings.Contains(out, "disabled (disabled by inz telemetry disable)") {
		t.Errorf("status = %q", out)
	}

	if _, _, code := runCLI(t, home, nil, "telemetry", "enable"); code != 0 {
		t.Fatalf("enable exit %d", code)
	}
	if out := statusOut(t, home); !strings.HasPrefix(out, "enabled") || !strings.Contains(out, "anonymous id: ") {
		t.Errorf("status = %q", out)
	}
}

func statusOut(t *testing.T, home string) string {
	t.Helper()
	old := version
	version = "1.0.0"
	defer func() { version = old }()
	var out bytes.Buffer
	root := newRootCmd(teleEnv{stderr: &bytes.Buffer{}, getenv: func(string) string { return "" }, home: home})
	root.SetOut(&out)
	root.SetArgs([]string{"telemetry", "status"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestTelemetryDisableNoHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, _, code := runCLI(t, "", nil, "telemetry", "disable"); code == 0 {
		t.Error("disable without a home dir must fail")
	}
}

func TestCommandRunEvent(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(home, "nope.yaml")
	_, events, code := runCLI(t, home, debugEnv, "validate", "--config", missing)
	if code != 1 {
		t.Fatalf("exit = %d", code)
	}
	ev := find(events, "command_run")
	if ev == nil {
		t.Fatalf("no command_run in %v", events)
	}
	p := ev.Props
	if p["command"] != "validate" || p["success"] != false || p["expected"] != true || p["error_class"] != "config_missing" {
		t.Errorf("props = %v", p)
	}
	if _, ok := p["duration_ms"]; !ok {
		t.Error("missing duration_ms")
	}
	if strings.Contains(string(mustJSON(p)), home) {
		t.Errorf("props leak the home path: %v", p)
	}
}

func TestFailedCommandSendsException(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	_, events, _ := runCLI(t, t.TempDir(), debugEnv, "validate", "--config", missing)
	ev := find(events, "$exception")
	if ev == nil {
		t.Fatalf("no $exception in %v", events)
	}
	if ev.Props["expected"] != true || ev.Props["command"] != "validate" {
		t.Errorf("props = %v", ev.Props)
	}
	for _, k := range []string{"config_yaml", "function_files"} {
		if _, ok := ev.Props[k]; ok {
			t.Errorf("unexpected key %s", k)
		}
	}
	list, _ := ev.Props["$exception_list"].([]any)
	if len(list) != 1 {
		t.Fatalf("$exception_list = %v", ev.Props["$exception_list"])
	}
	item := list[0].(map[string]any)
	if item["type"] != "config_missing" || item["stacktrace"] != nil {
		t.Errorf("item = %v", item)
	}
}

func TestSuccessSendsNoException(t *testing.T) {
	_, events, code := runCLI(t, t.TempDir(), debugEnv, "version")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if find(events, "$exception") != nil {
		t.Error("success must not send $exception")
	}
	if ev := find(events, "command_run"); ev == nil || ev.Props["success"] != true {
		t.Errorf("command_run = %v", ev)
	}
}

func TestExceptionPropsOmitsStackAndDetail(t *testing.T) {
	err := fmt.Errorf("outer: %w", os.ErrDeadlineExceeded)
	props := exceptionProps("dev", err, "/home/x")
	if props["expected"] != false {
		t.Errorf("props = %v", props)
	}
	item := props["$exception_list"].([]map[string]any)[0]
	if item["stacktrace"] != nil {
		t.Error("reporter must not capture its own stack")
	}
	if _, ok := props["error_detail"]; ok {
		t.Error("error_detail must be omitted when the verbose form equals Error()")
	}
}

func TestServeSendsNoUsage(t *testing.T) {
	home := t.TempDir()
	stderr, events, _ := runCLI(t, home, debugEnv, "serve", "--config", filepath.Join(t.TempDir(), "nope.yaml"))
	if len(events) != 0 {
		t.Errorf("serve sent events: %v", events)
	}
	if strings.Contains(stderr, "anonymous usage data") {
		t.Error("serve printed the notice")
	}
}

func TestNoticeShownOnceAndSkipped(t *testing.T) {
	const notice = "anonymous usage data"
	for _, args := range [][]string{{"version"}, {"telemetry", "status"}, {"--help"}, {"help"}} {
		if out, _, _ := runCLI(t, t.TempDir(), nil, args...); strings.Contains(out, notice) {
			t.Errorf("%v printed the notice", args)
		}
	}
	home := t.TempDir()
	if out, _, _ := runCLI(t, home, nil, "validate", "--config", "/nonexistent.yaml"); !strings.Contains(out, notice) {
		t.Error("first validate must print the notice")
	}
	if out, _, _ := runCLI(t, home, nil, "validate", "--config", "/nonexistent.yaml"); strings.Contains(out, notice) {
		t.Error("second run must not repeat the notice")
	}
}

func TestNoTelemetryFlagSendsNothing(t *testing.T) {
	stderr, events, _ := runCLI(t, t.TempDir(), map[string]string{"INSTANCEZ_TELEMETRY_DEBUG": "1"}, "version", "--no-telemetry")
	if len(events) != 0 || strings.Contains(stderr, "anonymous usage data") {
		t.Errorf("flag leaked: %q", stderr)
	}
}

func TestPreRunFailuresAreReported(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		command string
		class   string
	}{
		{"unknown command", []string{"bogus", "--secret-arg"}, "unknown", "cli_unknown_command"},
		{"unknown flag", []string{"validate", "--bogus-flag"}, "validate", "cli_flag"},
		{"too many args", []string{"validate", "a", "b"}, "validate", "cli_args"},
		{"flag needs value", []string{"validate", "--config"}, "validate", "cli_flag"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stderr, events, code := runCLI(t, t.TempDir(), debugEnv, c.args...)
			if code != 1 {
				t.Fatalf("exit = %d", code)
			}
			run, exc := find(events, "command_run"), find(events, "$exception")
			if run == nil || exc == nil {
				t.Fatalf("want both events, got %v", events)
			}
			for _, ev := range []*teleEvent{run, exc} {
				if ev.Props["command"] != c.command || ev.Props["expected"] != true {
					t.Errorf("%s props = %v", ev.Event, ev.Props)
				}
			}
			if run.Props["error_class"] != c.class {
				t.Errorf("class = %v", run.Props["error_class"])
			}
			if !strings.Contains(stderr, "anonymous usage data") {
				t.Error("notice must show before the first capture")
			}
		})
	}
}

func TestPreRunFailureRespectsOptOut(t *testing.T) {
	_, events, _ := runCLI(t, t.TempDir(), debugEnv, "bogus", "--no-telemetry")
	if len(events) != 0 {
		t.Errorf("sent %v", events)
	}
}

func TestPreRunOptOutForms(t *testing.T) {
	for _, args := range [][]string{
		{"bogus", "--no-telemetry=true"},
		{"bogus", "--no-telemetry=1"},
		{"validate", "a", "b", "--no-telemetry=true"},
	} {
		if _, events, _ := runCLI(t, t.TempDir(), debugEnv, args...); len(events) != 0 {
			t.Errorf("%v sent %v", args, events)
		}
	}
	for _, args := range [][]string{
		{"bogus", "--no-telemetry=false"},
		{"bogus", "--", "--no-telemetry"},
	} {
		if _, events, _ := runCLI(t, t.TempDir(), debugEnv, args...); len(events) == 0 {
			t.Errorf("%v must still report", args)
		}
	}
}

func TestExceptionDetailPresentWhenDiffers(t *testing.T) {
	err := fmt.Errorf("outer: %w", userError("x", errors.New("inner")))
	if _, ok := exceptionProps("dev", detailed{err}, "")["error_detail"]; !ok {
		t.Error("error_detail missing when verbose form differs")
	}
}

type detailed struct{ error }

func (d detailed) Format(f fmt.State, verb rune) {
	if verb == 'v' && f.Flag('+') {
		_, _ = fmt.Fprint(f, "detail: "+d.Error())
		return
	}
	_, _ = fmt.Fprint(f, d.Error())
}

func TestServeLeavesNoState(t *testing.T) {
	home := t.TempDir()
	runCLI(t, home, nil, "serve", "--config", filepath.Join(home, "nope.yaml"))
	runCLI(t, home, nil, "serve", "--bogus-flag")
	if _, err := os.Stat(filepath.Join(home, ".instancez")); !os.IsNotExist(err) {
		t.Errorf("serve touched state: %v", err)
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestTelemetryCommandsReportNothing(t *testing.T) {
	for _, args := range [][]string{{"telemetry", "disable"}, {"telemetry", "enable"}, {"telemetry", "status"}} {
		stderr, events, code := runCLI(t, t.TempDir(), debugEnv, args...)
		if code != 0 || len(events) != 0 || strings.Contains(stderr, "anonymous usage data") {
			t.Errorf("%v: code=%d events=%v stderr=%q", args, code, events, stderr)
		}
	}
	if _, events, _ := runCLI(t, t.TempDir(), debugEnv, "telemetry", "bogus"); len(events) != 0 {
		t.Errorf("failed telemetry subcommand reported: %v", events)
	}
}

func TestTelemetryStatusHonorsFlag(t *testing.T) {
	var out bytes.Buffer
	root := newRootCmd(teleEnv{stderr: &bytes.Buffer{}, getenv: func(string) string { return "" }, home: t.TempDir()})
	old := version
	version = "1.0.0"
	defer func() { version = old }()
	root.SetOut(&out)
	root.SetArgs([]string{"telemetry", "status", "--no-telemetry"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "disabled (--no-telemetry)") {
		t.Errorf("status = %q", out.String())
	}
}

func TestOptedOutRunCreatesNoState(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		args []string
	}{
		"DO_NOT_TRACK": {map[string]string{"DO_NOT_TRACK": "1"}, []string{"version"}},
		"env":          {map[string]string{"INSTANCEZ_TELEMETRY_DISABLED": "1"}, []string{"validate", "--config", "/nonexistent.yaml"}},
		"flag":         {nil, []string{"version", "--no-telemetry"}},
	} {
		home := t.TempDir()
		runCLI(t, home, tc.env, tc.args...)
		if _, err := os.Stat(filepath.Join(home, ".instancez")); !os.IsNotExist(err) {
			t.Errorf("%s created state: %v", name, err)
		}
	}
}
