package cli

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"
)

func TestVersionCmd(t *testing.T) {
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute version: %v", err)
	}
	got := out.String()
	// Defaults: version="dev", commit="unknown"; release builds override via ldflags.
	if !strings.Contains(got, "instancez vdev (unknown)") {
		t.Errorf("version output = %q, want it to contain %q", got, "instancez vdev (unknown)")
	}
}

func TestVersionFlagMatchesSubcommand(t *testing.T) {
	run := func(arg string) string {
		root := NewRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetArgs([]string{arg})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute %s: %v", arg, err)
		}
		return out.String()
	}
	flag, sub := run("--version"), run("version")
	if flag != sub {
		t.Errorf("--version = %q, version = %q; want identical", flag, sub)
	}
	if !strings.Contains(flag, "instancez vdev (unknown)") {
		t.Errorf("--version output = %q", flag)
	}
}

func TestResolveVersion(t *testing.T) {
	bi := func(ver, rev string) *debug.BuildInfo {
		b := &debug.BuildInfo{Main: debug.Module{Version: ver}}
		if rev != "" {
			b.Settings = []debug.BuildSetting{{Key: "vcs.revision", Value: rev}}
		}
		return b
	}
	cases := []struct {
		name, ver, com string
		info           *debug.BuildInfo
		wantV, wantC   string
	}{
		{"nil info", "dev", "unknown", nil, "dev", "unknown"},
		{"devel", "dev", "unknown", bi("(devel)", ""), "dev", "unknown"},
		{"empty main version", "dev", "unknown", bi("", ""), "dev", "unknown"},
		{"tagged", "dev", "unknown", bi("v0.0.12", ""), "0.0.12", "unknown"},
		{"pseudo dirty", "dev", "unknown", bi("v0.0.13-0.20261102120000-abcdef123456+dirty", "abcdef1234567890"), "0.0.13-0.20261102120000-abcdef123456+dirty", "abcdef1"},
		{"devel with short rev", "dev", "unknown", bi("(devel)", "abc"), "dev", "abc"},
		{"empty rev setting", "dev", "unknown", &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision"}}}, "dev", "unknown"},
		{"ldflags win", "0.0.12", "1a2b3c4", bi("v9.9.9", "ffff"), "0.0.12", "1a2b3c4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, com := resolveVersion(c.ver, c.com, c.info)
			if v != c.wantV || com != c.wantC {
				t.Errorf("got (%q,%q), want (%q,%q)", v, com, c.wantV, c.wantC)
			}
		})
	}
}
