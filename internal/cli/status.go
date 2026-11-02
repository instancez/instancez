package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/instancez/instancez/internal/cloud"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var configPath string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the linked instancez Cloud project's state",
		Long: `Show the cloud project's current state: name, id, url, and deploy status.

The project_id is read from project.cloud.project_id inside instancez.yaml. If
no project is linked yet, run inz cloud deploy --new to create one, or set
project.cloud.project_id by hand.

This is distinct from inz doctor, which checks local environment health.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd.OutOrStdout(), configPath, jsonOut)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "path to instancez.yaml")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the project state as JSON (never prompts for login)")
	return cmd
}

// statusLogin is the login behaviour for status; tests replace it.
var statusLogin = ensureLoginOpts{}

func runStatus(w io.Writer, configPath string, jsonOut bool) error {
	if err := requireLocalConfig(configPath); err != nil {
		return err
	}
	src, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}
	projectID, err := cloud.ReadProjectID(src)
	if err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}
	if projectID == "" {
		return errors.New("no project.cloud.project_id in instancez.yaml; run `inz cloud deploy --new` to create and link one")
	}

	// Inline login: returns existing creds, prompts on a TTY, or hard-errors
	// in a non-interactive session pointing at `inz cloud login`.
	login := statusLogin
	if jsonOut {
		login.isTTY = func() bool { return false }
	}
	creds, err := ensureLoggedIn(login)
	if err != nil {
		return err
	}

	c := cloud.NewClient(cloud.APIURL(), creds.PAT)

	app, err := c.GetApp(projectID)
	if err != nil {
		return fmt.Errorf("get app: %w", err)
	}

	if jsonOut {
		return json.NewEncoder(w).Encode(app)
	}
	renderStatus(w, app)
	return nil
}

// renderStatus writes a human-readable summary of the cloud project's state to
// w. It renders the project identity and the deploy status (from
// app.Deployment — NOT the top-level project Status). Pure (no I/O beyond w)
// so it can be unit-tested.
func renderStatus(w io.Writer, app *cloud.GetAppResponse) {
	_, _ = fmt.Fprintf(w, "Project:    %s\n", app.Name)
	_, _ = fmt.Fprintf(w, "ID:         %s\n", app.ID)
	if app.URL != "" {
		_, _ = fmt.Fprintf(w, "URL:        %s\n", app.URL)
	}
	if app.PublishableKey != "" {
		_, _ = fmt.Fprintf(w, "Publishable: %s\n", app.PublishableKey)
	}

	_, _ = fmt.Fprintln(w)

	// Render the raw status string as the server reports it (e.g. build_done,
	// not_ready) — no friendly remapping.
	status := app.Deployment.Status
	if status == "" {
		status = "unknown"
	}
	_, _ = fmt.Fprintf(w, "Deploy:     %s\n", status)
	if app.Deployment.DeployedAt != nil && *app.Deployment.DeployedAt != "" {
		_, _ = fmt.Fprintf(w, "Deployed:   %s\n", *app.Deployment.DeployedAt)
	}
	if app.Deployment.Error != "" {
		_, _ = fmt.Fprintf(w, "Error:      %s\n", app.Deployment.Error)
	}
}
