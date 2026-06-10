package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/saedx1/instancez/internal/cloud"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the linked Ultrabase Cloud project's state",
		Long: `Show the cloud project's current state: name, id, and url; the
production deploy status; and whether the local draft has unpublished changes
relative to production.

The project_id is read from project.cloud.project_id inside ultrabase.yaml. Run
ultra init --with-cloud first if no project is linked yet.

This is distinct from ultra doctor, which checks local environment health.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(configPath)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "ultrabase.yaml", "path to ultrabase.yaml")
	return cmd
}

func runStatus(configPath string) error {
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
		return errors.New("no project.cloud.project_id in ultrabase.yaml; run `ultra init --with-cloud` to link this project to Ultrabase Cloud")
	}

	// Inline login: returns existing creds, prompts on a TTY, or hard-errors
	// in a non-interactive session pointing at `ultra login`.
	creds, err := ensureLoggedIn(ensureLoginOpts{})
	if err != nil {
		return err
	}

	apiURL, err := cloud.APIURLFromConfig(configPath)
	if err != nil {
		return err
	}
	c := cloud.NewClient(apiURL, creds.PAT)

	app, err := c.GetApp(projectID)
	if err != nil {
		return fmt.Errorf("get app: %w", err)
	}

	renderStatus(os.Stdout, app)
	return nil
}

// renderStatus writes a human-readable summary of the cloud project's state to
// w. It renders the project identity, the PRODUCTION deploy status (from
// app.Deployment — NOT the top-level project Status), and a draft line derived
// from app.DraftDirty. Pure (no I/O beyond w) so it can be unit-tested.
func renderStatus(w io.Writer, app *cloud.GetAppResponse) {
	fmt.Fprintf(w, "Project:    %s\n", app.Name)
	fmt.Fprintf(w, "ID:         %s\n", app.ID)
	if app.URL != "" {
		fmt.Fprintf(w, "URL:        %s\n", app.URL)
	}

	fmt.Fprintln(w)

	// Production deploy state. Render the raw status string as the server
	// reports it (e.g. build_done, not_ready) — no friendly remapping.
	status := app.Deployment.Status
	if status == "" {
		status = "unknown"
	}
	fmt.Fprintf(w, "Production: %s\n", status)
	if app.Deployment.DeployedAt != nil && *app.Deployment.DeployedAt != "" {
		fmt.Fprintf(w, "Deployed:   %s\n", *app.Deployment.DeployedAt)
	}
	if app.Deployment.Error != "" {
		fmt.Fprintf(w, "Error:      %s\n", app.Deployment.Error)
	}

	draft := "clean"
	if app.DraftDirty {
		draft = "has unpublished changes"
	}
	fmt.Fprintf(w, "Draft:      %s\n", draft)
}
