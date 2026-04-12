package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/saedx1/ultrabase/internal/adapter/postgres"
	"github.com/saedx1/ultrabase/internal/config"
	"github.com/spf13/cobra"
)

func newRollbackCmd() *cobra.Command {
	var (
		steps      int
		configPath string
	)

	cmd := &cobra.Command{
		Use:   "rollback",
		Short: "Revert the last migration(s)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRollback(configPath, steps)
		},
	}

	cmd.Flags().IntVar(&steps, "steps", 1, "number of migrations to revert")
	cmd.Flags().StringVar(&configPath, "config", "ultrabase.yaml", "config file path")
	return cmd
}

func runRollback(configPath string, steps int) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	errs := config.Validate(cfg)
	if errs != nil {
		return printPrettyErrors(errs)
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL not set")
	}

	ctx := context.Background()
	db, err := postgres.New(ctx, dbURL, cfg.Server.DB.Pool)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer db.Close()

	fmt.Printf("Rolling back %d migration(s)...\n", steps)

	for i := 0; i < steps; i++ {
		migration, err := db.GetLastMigration(ctx)
		if err != nil {
			return fmt.Errorf("reading migration history: %w", err)
		}
		if migration == nil {
			fmt.Printf("  No more migrations to rollback.\n")
			break
		}

		// Delete the migration record
		_, err = db.Exec(ctx,
			"DELETE FROM _ultrabase_migrations WHERE id = $1", migration.ID)
		if err != nil {
			return fmt.Errorf("deleting migration record %d: %w", migration.ID, err)
		}

		fmt.Printf("  ✓ Rolled back migration #%d (applied at %s)\n",
			migration.ID, migration.AppliedAt.Format("2006-01-02 15:04:05"))
	}

	fmt.Println("\n  Note: Schema changes are not automatically reversed.")
	fmt.Println("  Run 'ultrabase dev' or 'ultrabase serve --migrate' to re-apply from YAML.")
	return nil
}
