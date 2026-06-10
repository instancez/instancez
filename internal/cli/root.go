// Package cli defines the cobra command tree for the ultrabase binary.
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "dev"

// errReported marks an error whose details were already printed to the user
// (e.g. a formatted validation report). Execute exits non-zero for it without
// printing the bare error again.
var errReported = errors.New("reported")

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "inz",
		Short:         "Ultrabase — declarative backend from a single YAML file",
		Long:          "Ultrabase turns a YAML config into a full backend: Postgres CRUD, auth, storage, events, and more.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		newInitCmd(),
		newValidateCmd(),
		newDevCmd(),
		newServeCmd(),
		newVersionCmd(),
		newLoginCmd(),
		newLogoutCmd(),
		newDeployCmd(),
		newWhoamiCmd(),
		newDoctorCmd(),
		newStatusCmd(),
	)

	return root
}

func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		if !errors.Is(err, errReported) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("ultrabase v%s\n", version)
		},
	}
}
