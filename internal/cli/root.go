// Package cli defines the cobra command tree for the ultrabase binary.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

const version = "0.1.0"

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "ultrabase",
		Short: "Ultrabase — declarative backend from a single YAML file",
		Long:  "Ultrabase turns a YAML config into a full backend: Postgres CRUD, auth, storage, events, and more.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		newInitCmd(),
		newValidateCmd(),
		newDevCmd(),
		newServeCmd(),
		newRollbackCmd(),
		newDBCmd(),
		newGenerateCmd(),
		newSlotCmd(),
		newVersionCmd(),
	)

	return root
}

func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
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
