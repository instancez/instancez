package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newRollbackCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rollback",
		Short: "Revert the last migration(s)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("rollback is not yet implemented")
		},
	}
}
