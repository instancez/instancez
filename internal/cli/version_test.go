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
