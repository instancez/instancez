package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

func newDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Database utilities",
	}

	cmd.AddCommand(
		newDBConsoleCmd(),
		newDBDumpCmd(),
		newDBRestoreCmd(),
	)

	return cmd
}

func newDBConsoleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "console",
		Short: "Open psql connected to DATABASE_URL",
		RunE: func(cmd *cobra.Command, args []string) error {
			dbURL := os.Getenv("DATABASE_URL")
			if dbURL == "" {
				return fmt.Errorf("DATABASE_URL not set")
			}

			psql := exec.Command("psql", dbURL)
			psql.Stdin = os.Stdin
			psql.Stdout = os.Stdout
			psql.Stderr = os.Stderr
			return psql.Run()
		},
	}
}

func newDBDumpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dump",
		Short: "Dump database (pg_dump to stdout)",
		RunE: func(cmd *cobra.Command, args []string) error {
			dbURL := os.Getenv("DATABASE_URL")
			if dbURL == "" {
				return fmt.Errorf("DATABASE_URL not set")
			}

			dump := exec.Command("pg_dump", dbURL)
			dump.Stdout = os.Stdout
			dump.Stderr = os.Stderr
			return dump.Run()
		},
	}
}

func newDBRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <file>",
		Short: "Restore database from dump",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbURL := os.Getenv("DATABASE_URL")
			if dbURL == "" {
				return fmt.Errorf("DATABASE_URL not set")
			}

			restore := exec.Command("psql", dbURL, "-f", args[0])
			restore.Stdout = os.Stdout
			restore.Stderr = os.Stderr
			return restore.Run()
		},
	}
}
