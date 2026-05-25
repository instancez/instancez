package cli

import (
	"errors"
	"fmt"
	"os"

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

func runDeploy(configPath string) error {
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
