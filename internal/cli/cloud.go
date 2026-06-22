package cli

import "github.com/spf13/cobra"

// newCloudCmd groups the commands that talk to instancez Cloud. The
// subcommand constructors are unchanged; they are simply reparented from the
// root command to `inz cloud`.
func newCloudCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cloud",
		Short: "Manage and deploy to instancez Cloud",
		Long:  "Commands for authenticating with and deploying to instancez Cloud.",
	}
	cmd.AddCommand(
		newLoginCmd(),
		newLogoutCmd(),
		newWhoamiCmd(),
		newDeployCmd(),
		newStatusCmd(),
	)
	return cmd
}
