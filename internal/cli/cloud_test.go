package cli

import "testing"

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
