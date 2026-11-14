package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

const frontendMovedMsg = `inz cloud frontend deploy was removed. It was per-project (it deployed to a project's frontend); frontends are now separate static sites. Deploy a built bundle with:

  inz cloud site deploy <dist-dir> --site <id|slug>

Find the site id and slug with ` + "`inz cloud site list`" + ` (or the Sites page in the console).`

// newFrontendCmd keeps the removed command as a hidden stub that explains the
// replacement and exits non-zero. Unknown flags are tolerated so old
// invocations reach the message.
func newFrontendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "frontend",
		Short:  "Removed: use inz cloud site deploy",
		Hidden: true,
	}
	cmd.AddCommand(&cobra.Command{
		Use:                "deploy",
		Short:              "Removed: use inz cloud site deploy",
		Long:               frontendMovedMsg,
		Hidden:             true,
		Args:               cobra.ArbitraryArgs,
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
		SilenceUsage:       true,
		RunE: func(*cobra.Command, []string) error {
			return errors.New(frontendMovedMsg)
		},
	})
	return cmd
}
