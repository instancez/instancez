// Package cli defines the cobra command tree for the ultrabase binary.
package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const version = "0.1.0"

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "ultrabase",
		Short:         "Ultrabase — declarative backend from a single YAML file",
		Long:          "Ultrabase turns a YAML config into a full backend: Postgres CRUD, auth, storage, events, and more.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		applyEnvFlags(cmd)
		return nil
	}

	root.AddCommand(
		newInitCmd(),
		newValidateCmd(),
		newDevCmd(),
		newServeCmd(),
		newRollbackCmd(),
		newSlotCmd(),
		newVersionCmd(),
	)

	return root
}

// applyEnvFlags sets any unset flag from ULTRABASE_<FLAG_UPPER_SNAKE> env vars.
func applyEnvFlags(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Changed {
			return
		}
		envKey := "ULTRABASE_" + strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))
		if v := os.Getenv(envKey); v != "" {
			_ = cmd.Flags().Set(f.Name, v)
		}
	})
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
