package cli

import (
	"bytes"
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
