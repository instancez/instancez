package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func TestServeSkipsPromptInContainer(t *testing.T) {
	r := runCrash(t, "serve", func() error { return netErr() }, "y\n", true, dbg("TEST_CONTAINER", "1"))
	if r.prompted() || len(r.events) != 0 {
		t.Errorf("container serve prompted=%v events=%v", r.prompted(), r.events)
	}
	if r := runCrash(t, "dev", func() error { return errors.New("boom") }, "n\n", true, dbg("TEST_CONTAINER", "1")); !r.prompted() {
		t.Error("dev in a container should still ask")
	}
}

func TestInContainer(t *testing.T) {
	none := func(string) string { return "" }
	for name, tc := range map[string]struct {
		getenv func(string) string
		files  []string
		want   bool
	}{
		"bare host":  {none, nil, false},
		"docker":     {none, []string{"/.dockerenv"}, true},
		"podman":     {none, []string{"/run/.containerenv"}, true},
		"kubernetes": {func(k string) string { return map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1"}[k] }, nil, true},
	} {
		exists := func(p string) bool {
			for _, f := range tc.files {
				if f == p {
					return true
				}
			}
			return false
		}
		if got := inContainer(tc.getenv, exists); got != tc.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
}

func TestValidationErrorsSendFirstThree(t *testing.T) {
	errs := domain.ValidationErrors{
		{Path: "tables.a", Message: "bad type"}, {Path: "tables.b", Message: `unknown "Hunter2X" value`},
		{Path: "rpc.c", Message: "missing sql"}, {Path: "storage.d", Message: "fourth"},
	}
	for _, err := range []error{errs, reportedValidation(errs)} {
		class, exp, msg := classifyErr(err)
		if class != "config_invalid" || !exp {
			t.Errorf("class=%q exp=%v", class, exp)
		}
		if !strings.Contains(msg, "tables.a: bad type") || !strings.Contains(msg, "rpc.c: missing sql") ||
			strings.Contains(msg, "fourth") || strings.Contains(msg, "Hunter2X") {
			t.Errorf("msg = %q", msg)
		}
	}
}

func TestBareRefusedIsNotDB(t *testing.T) {
	if class, _, _ := classifyErr(realRefused(t)); class != "env:connect_refused" {
		t.Errorf("class = %q", class)
	}
}

func TestKnownNamesComeFromCommandTree(t *testing.T) {
	names := commandNames(newRootCmd(teleEnv{}))
	for _, want := range []string{"inz cloud", "inz cloud login", "-y, --yes", "--no-telemetry"} {
		found := false
		for _, n := range names {
			found = found || n == want
		}
		if !found {
			t.Errorf("missing %q", want)
		}
	}
}
