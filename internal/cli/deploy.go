package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/saedx1/ultrabase/internal/cli/preflight"
	"github.com/saedx1/ultrabase/internal/cloud"
	"github.com/spf13/cobra"
)

func newDeployCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Push the current ultrabase.yaml to an Ultrabase Cloud project",
		Long: `Deploy the current project's ultrabase.yaml to the cloud. The
project_id is read from project.cloud.project_id inside ultrabase.yaml. Run
ultra init --with-cloud first if no project is set yet.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeploy(configPath)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "ultrabase.yaml", "path to ultrabase.yaml")
	return cmd
}

// projectIDPresentCheck returns a Check that reads the project_id from
// configPath and fails if it is absent or empty.  Defined in deploy.go
// (package cli) to avoid an import cycle between preflight and cloud.
func projectIDPresentCheck(configPath string) preflight.Check {
	return func() preflight.Result {
		src, err := os.ReadFile(configPath)
		if err != nil {
			// If the file can't be read the ConfigValidCheck will already have
			// failed; report a short message here rather than double-printing.
			return preflight.Result{
				Name:    "project_id present",
				OK:      false,
				Detail:  err.Error(),
				FixHint: "run `ultra init` to create ultrabase.yaml",
			}
		}
		id, err := cloud.ReadProjectID(src)
		if err != nil {
			return preflight.Result{
				Name:    "project_id present",
				OK:      false,
				Detail:  "parse error: " + err.Error(),
				FixHint: "check ultrabase.yaml for YAML syntax errors",
			}
		}
		if id == "" {
			return preflight.Result{
				Name:    "project_id present",
				OK:      false,
				Detail:  "project.cloud.project_id is not set",
				FixHint: "run `ultra init --with-cloud` to link this project to Ultrabase Cloud",
			}
		}
		return preflight.Result{Name: "project_id present", OK: true}
	}
}

func runDeploy(configPath string) error {
	// Preflight: verify config is structurally valid and a project_id is
	// present before touching the network.
	if r, failed := preflight.RunUntilFail([]preflight.Check{
		preflight.ConfigValidCheck(configPath),
		projectIDPresentCheck(configPath),
	}); failed {
		fmt.Fprintf(os.Stderr, "  ✗ %s — %s\n    hint: %s\n", r.Name, r.Detail, r.FixHint)
		return errReported
	}

	if err := requireConfigFile(configPath); err != nil {
		return err
	}

	creds, err := cloud.Load()
	if err != nil {
		return fmt.Errorf("deploy requires authentication: %w", err)
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
		return errors.New("no project.cloud.project_id in ultrabase.yaml; run `ultra init --with-cloud` first")
	}

	apiURL, err := cloud.APIURLFromConfig(configPath)
	if err != nil {
		return err
	}
	c := cloud.NewClient(apiURL, creds.PAT)

	// Push the local YAML to the project's draft Defs before deploying,
	// so the server sees exactly what's on disk.
	fmt.Println("  Uploading ultrabase.yaml...")
	if err := c.UploadYAML(projectID, string(src)); err != nil {
		return fmt.Errorf("upload yaml: %w", err)
	}

	fmt.Println("  Deploying...")
	resp, err := c.Deploy(projectID)
	if err != nil {
		return fmt.Errorf("deploy: %w", err)
	}

	fmt.Printf("  ✓ Deploy queued (version_id: %s)\n", resp.VersionID)
	fmt.Println("  Track progress in the Ultrabase Cloud dashboard.")
	return nil
}
