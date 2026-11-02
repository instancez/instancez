package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestCloudSubcommandsRegistered(t *testing.T) {
	root := NewRootCmd()

	// `cloud deploy` must resolve to the deploy command.
	cmd, _, err := root.Find([]string{"cloud", "deploy"})
	if err != nil {
		t.Fatalf("find cloud deploy: %v", err)
	}
	if cmd.Name() != "deploy" {
		t.Errorf("got %q, want deploy", cmd.Name())
	}

	// Every cloud subcommand must be present under `cloud`.
	for _, name := range []string{"login", "logout", "whoami", "deploy", "status"} {
		if c, _, err := root.Find([]string{"cloud", name}); err != nil || c.Name() != name {
			t.Errorf("cloud %s not registered (cmd=%v err=%v)", name, c, err)
		}
	}

	// Bare `inz deploy` must NO LONGER resolve to deploy (hard move).
	if cmd, _, _ := root.Find([]string{"deploy"}); cmd.Name() == "deploy" {
		t.Error("inz deploy still resolves; expected it to be moved under cloud")
	}
}

func TestTopLevelCloudVerbsSuggestCloud(t *testing.T) {
	env := func(buf *bytes.Buffer) teleEnv {
		return teleEnv{stderr: buf, getenv: func(k string) string {
			if k == "INSTANCEZ_TELEMETRY_DISABLED" {
				return "1"
			}
			return ""
		}, home: t.TempDir()}
	}
	for _, verb := range []string{"deploy", "login", "logout", "status", "whoami", "frontend"} {
		t.Run(verb, func(t *testing.T) {
			var buf bytes.Buffer
			if code := run([]string{verb}, env(&buf)); code != 1 {
				t.Fatalf("exit = %d, want 1", code)
			}
			out := buf.String()
			if !strings.Contains(out, `unknown command "`+verb+`"`) || !strings.Contains(out, "Did you mean this?\n\tcloud") {
				t.Errorf("no suggestion in %q", out)
			}
		})
	}
	var buf bytes.Buffer
	run([]string{"bogus"}, env(&buf))
	if strings.Contains(buf.String(), "\tcloud") {
		t.Errorf("bogus suggests cloud: %q", buf.String())
	}
}
