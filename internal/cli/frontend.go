package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

const frontendMovedMsg = "`inz cloud frontend deploy` was removed. Deploy a built bundle to a static site with:\n\n  inz cloud site deploy <dist-dir> --site <site id or slug>\n\nSee `inz cloud site deploy --help`."

// newFrontendCmd keeps the removed `inz cloud frontend deploy` as a hidden stub
// that explains the replacement and exits non-zero.
func newFrontendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "frontend",
		Short:  "Removed: use `inz cloud site deploy`",
		Hidden: true,
	}
	cmd.AddCommand(&cobra.Command{
		Use:                "deploy",
		Short:              "Removed: use `inz cloud site deploy`",
		Hidden:             true,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		RunE: func(*cobra.Command, []string) error {
			return errors.New(frontendMovedMsg)
		},
	})
	return cmd
}
