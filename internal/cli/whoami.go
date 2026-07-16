package cli

import (
	"fmt"

	"github.com/instancez/instancez/internal/cloud"
	"github.com/spf13/cobra"
)

func newWhoamiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Print the currently logged-in instancez Cloud user",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWhoami()
		},
	}
	return cmd
}

func runWhoami() error {
	creds, err := cloud.Load()
	if err != nil {
		return fmt.Errorf("not logged in: %w", err)
	}

	c := cloud.NewClient(cloud.APIURL(), creds.PAT)
	resp, err := c.Whoami()
	if err != nil {
		return fmt.Errorf("whoami: %w", err)
	}
	fmt.Println(resp.Email)
	return nil
}
