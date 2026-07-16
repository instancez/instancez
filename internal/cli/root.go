// Package cli defines the cobra command tree for the instancez binary.
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version/commit are injected at build time via
// -ldflags "-X github.com/instancez/instancez/internal/cli.version=… -X github.com/instancez/instancez/internal/cli.commit=…"
var (
	version = "dev"
	commit  = "unknown"
)

// errReported marks an error whose details were already printed to the user
// (e.g. a formatted validation report). Execute exits non-zero for it without
// printing the bare error again.
var errReported = errors.New("reported")

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "inz",
		Short:         "instancez — declarative backend from a single YAML file",
		Long:          "instancez turns a YAML config into a full backend: Postgres CRUD, auth, storage, events, and more.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		newInitCmd(),
		newValidateCmd(),
		newBundleCmd(),
		newDevCmd(),
		newServeCmd(),
		newVersionCmd(),
		newDoctorCmd(),
		newCloudCmd(),
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
			cmd.Printf("instancez v%s (%s)\n", version, commit)
		},
	}
}
