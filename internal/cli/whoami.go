package cli

import (
	"fmt"

	"github.com/saedx1/instancez/internal/cloud"
	"github.com/spf13/cobra"
)

func newWhoamiCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Print the currently logged-in Ultrabase Cloud user",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWhoami(configPath)
		},
	}
	// configPath is optional: whoami works outside a project too. When
	// provided (and the file exists), we honor project.cloud.api_url.
	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "path to instancez.yaml (used to honor project.cloud.api_url; ignored if missing)")
	return cmd
}

func runWhoami(configPath string) error {
	creds, err := cloud.Load()
	if err != nil {
		return fmt.Errorf("not logged in: %w", err)
	}

	// Project-pinned api_url wins if present, else env, else default.
	apiURL, err := cloud.APIURLFromConfig(configPath)
	if err != nil {
		// Bad yaml shouldn't break whoami. Fall back to env/default.
		apiURL = cloud.APIURL()
	}

	c := cloud.NewClient(apiURL, creds.PAT)
	resp, err := c.Whoami()
	if err != nil {
		return fmt.Errorf("whoami: %w", err)
	}
	fmt.Println(resp.Email)
	return nil
}
