package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/saedx1/ultrabase/internal/adapter/postgres"
	"github.com/saedx1/ultrabase/internal/config"
	"github.com/spf13/cobra"
)

func newSlotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "slot",
		Short: "WAL replication slot management",
	}

	cmd.AddCommand(newSlotResetCmd())
	return cmd
}

func newSlotResetCmd() *cobra.Command {
	var (
		force      bool
		configPath string
	)

	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Drop and recreate the replication slot",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSlotReset(configPath, force)
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "skip confirmation prompt")
	cmd.Flags().StringVar(&configPath, "config", "ultrabase.yaml", "config file path")
	return cmd
}

func runSlotReset(configPath string, force bool) error {
	if !force {
		fmt.Println("  ⚠ This will drop the replication slot and recreate it.")
		fmt.Println("  ⚠ Events between the last checkpoint and now will be lost.")
		fmt.Print("  Continue? (y/N) ")
		var answer string
		fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" {
			fmt.Println("  Cancelled.")
			return nil
		}
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL not set")
	}

	ctx := context.Background()
	db, err := postgres.New(ctx, dbURL, cfg.Database.Pool)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer db.Close()

	// Drop existing slot (ignore error if it doesn't exist)
	err = postgres.DropSlot(ctx, db.Pool())
	if err != nil {
		// Check if it's a "slot does not exist" error
		errStr := err.Error()
		if strings.Contains(errStr, "does not exist") {
			fmt.Println("  No existing slot found, creating fresh.")
		} else {
			return fmt.Errorf("dropping slot: %w", err)
		}
	} else {
		fmt.Println("  ✓ Slot dropped")
	}

	// Recreate the slot
	_, err = db.Exec(ctx,
		"SELECT pg_create_logical_replication_slot('ultrabase_cdc', 'wal2json')")
	if err != nil {
		return fmt.Errorf("creating slot: %w", err)
	}

	fmt.Println("  ✓ Slot recreated")
	return nil
}

