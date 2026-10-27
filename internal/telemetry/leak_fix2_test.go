package telemetry

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/posthog/posthog-go"
	"gopkg.in/yaml.v3"
)

func TestSanitizeKeepsDiagnosticQuotes(t *testing.T) {
	SetKnownNames([]string{"-p, --port", "inz cloud", "inz cloud login"})
	t.Cleanup(func() { SetKnownNames(nil) })
	for in, keep := range map[string][]string{
		`invalid argument "80a" for "-p, --port" flag`:     {`"-p, --port"`},
		`exec: "node": executable file not found in $PATH`: {`"node"`},
		`syntax error at or near "user"`:                   {`"user"`},
		`unknown command "deplyo" for "inz cloud"`:         {`"deplyo"`, `"inz cloud"`},
		"not logged in: run `inz cloud login` first":       {"`inz cloud login`"},
		`unknown flag "--prot"`:                            {`"--prot"`},
	} {
		got := Sanitize(in, "")
		for _, k := range keep {
			if !strings.Contains(got, k) {
				t.Errorf("%q -> %q, lost %s", in, got, k)
			}
		}
	}
	for _, in := range []string{`invalid argument "80a" for "-p, --port" flag`, `password authentication failed for user "bob"`, `role 'alice' denied`} {
		if got := Sanitize(in, ""); strings.Contains(got, "80a") || strings.Contains(got, "bob") || strings.Contains(got, "alice") {
			t.Errorf("%q -> %q", in, got)
		}
	}
}

func TestPathRuleNeedsSegment(t *testing.T) {
	if got := Sanitize("ratio 3 / 5 bad", ""); got != "ratio 3 / 5 bad" {
		t.Errorf("got %q", got)
	}
}

func TestRedactYAMLScrubsKeysWithoutCollisions(t *testing.T) {
	in := "functions:\n  a:\n    env:\n      sk_live_abcdefghijkl1: x\n      sk_live_abcdefghijkl2: y\n"
	got, err := RedactYAML([]byte(in), "")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := yaml.Unmarshal([]byte(got), &v); err != nil || strings.Contains(got, "abcdefghijkl") {
		t.Errorf("keys: %v\n%s", err, got)
	}
}

func TestRedactSourceStripsCommentsKeepsLines(t *testing.T) {
	src := "a(); // secret hunter2\n/* multi\nline secret */ b();\n"
	got := redactSource(src, "")
	if strings.Contains(got, "secret") || strings.Count(got, "\n") != strings.Count(src, "\n") ||
		!strings.Contains(got, "a(); //\n") || !strings.Contains(got, "/*\n*/ b();") {
		t.Errorf("got %q", got)
	}
}

func TestRedactSourceKeysOnlyAfterBraceOrComma(t *testing.T) {
	got := redactSource(`x = {"long-key-name-here": 1, "another-long-key": 2}; y = c ? "tern-value-secret1" : "z"; switch (q) { case "case-value-secret2": }`, "")
	if !strings.Contains(got, `"long-key-name-here"`) || !strings.Contains(got, `"another-long-key"`) {
		t.Errorf("keys lost: %s", got)
	}
	if strings.Contains(got, "secret1") || strings.Contains(got, "secret2") {
		t.Errorf("ternary/case kept: %s", got)
	}
}

func TestCrashReportFitsPosthogLimit(t *testing.T) {
	for _, kb := range []int{400, 600} {
		crashReportFits(t, kb)
	}
}

func crashReportFits(t *testing.T, kb int) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "instancez.yaml")
	_ = os.WriteFile(cfg, []byte("a: 1\n"), 0o600)
	_ = os.MkdirAll(filepath.Join(dir, "functions"), 0o700)
	// JSON escaping roughly doubles this source, so 400 KB raw is already over the limit.
	big := strings.Repeat("x(\"<\");\n", kb*1024/8)
	_ = os.WriteFile(filepath.Join(dir, "functions", "big.js"), []byte(big), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "functions", "small.js"), []byte("y()"), 0o600)
	class := func(error) (string, bool, string) { return "go:x", false, "boom" }
	r := BuildCrashReport("dev", errors.New("boom"), []posthog.StackFrame{{Filename: "a.go", Function: "f"}}, cfg, map[string]string{"functions/big.js": big, "functions/small.js": "y()"}, class, "")
	b, _ := json.Marshal(r.Props(map[string]any{"command": "dev"}))
	if len(b) >= 450_000 || !strings.Contains(string(b), `"value":"boom"`) || r.Functions["functions/small.js"] == "" {
		t.Errorf("%d KB: size=%d small kept=%v", kb, len(b), r.Functions["functions/small.js"] != "")
	}
	if !strings.Contains(strings.Join(r.Truncated, ","), "functions/big.js") {
		t.Errorf("truncated = %v", r.Truncated)
	}
}

func TestPromptTimesOutToNo(t *testing.T) {
	old := promptTimeout
	promptTimeout = 50 * time.Millisecond
	t.Cleanup(func() { promptTimeout = old })
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	c := Init(Options{Version: "1.0.0", Home: t.TempDir(), Getenv: func(string) string { return "" }})
	var out strings.Builder
	start := time.Now()
	if c.PromptCrash(sampleReport(), nil, pr, &out, true) {
		t.Fatal("timeout accepted")
	}
	if time.Since(start) > 2*time.Second || !strings.Contains(out.String(), "no answer, not sent") {
		t.Errorf("out = %q", out.String())
	}
}

func TestFitTerminatesWhenNothingLeftToDrop(t *testing.T) {
	long := strings.Repeat("functions/very-long-name-", 4) + ".js"
	r := CrashReport{ErrClass: "go:x", ErrMessage: "boom", ConfigYAML: strings.Repeat("a: 1\n", 100_000), Functions: map[string]string{}}
	for i := 0; i < 20_000; i++ {
		r.Truncated = append(r.Truncated, long)
	}
	done := make(chan struct{})
	go func() { r.fit(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("fit did not terminate")
	}
	b, _ := json.Marshal(r.Props(nil))
	if len(b) > maxEventJSON || r.ConfigYAML != "" || len(r.Truncated) != maxTruncated+1 || r.Truncated[maxTruncated] != "…" {
		t.Errorf("size=%d config=%d truncated=%d", len(b), len(r.ConfigYAML), len(r.Truncated))
	}

	huge := CrashReport{ErrMessage: strings.Repeat("x", maxEventJSON+1), Functions: map[string]string{}}
	done2 := make(chan struct{})
	go func() { huge.fit(); close(done2) }()
	select {
	case <-done2:
	case <-time.After(10 * time.Second):
		t.Fatal("fit looped on an unshrinkable event")
	}
}
