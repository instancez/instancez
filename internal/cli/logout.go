package cli

import (
	"fmt"

	"github.com/saedx1/instancez/internal/cloud"
	"github.com/spf13/cobra"
)

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Forget local instancez Cloud credentials",
		Long: `Remove the PAT stored at ~/.instancez/credentials. The token itself
remains valid server-side until you revoke it from the dashboard.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cloud.Delete(); err != nil {
				return fmt.Errorf("removing credentials: %w", err)
			}
			fmt.Println("  ✓ Logged out.")
			return nil
		},
	}
}
