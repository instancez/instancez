package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/saedx1/instancez/internal/adapter/postgres"
	"github.com/saedx1/instancez/internal/app"
	"github.com/saedx1/instancez/internal/cloud"
	"github.com/saedx1/instancez/internal/config"
	"github.com/saedx1/instancez/internal/domain"
	"github.com/spf13/cobra"
)

func newValidateCmd() *cobra.Command {
	var (
		configPath string
		jsonOutput bool
		useDSN     string
		useProject bool
	)

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate config without starting the server",
		Long: `Validate instancez.yaml structure and references.

With --use-dsn, validate also connects to the given Postgres and prints
the migration that would bring the database in sync with the yaml. The
migration is planned but never applied — this is a dry-run.

With --project, validate uploads the local yaml to the cloud project (from
project.cloud.project_id) and prints the diff vs. the deployed version.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := applyEnvDefaults(cmd.Flags(), nil, os.Getenv); err != nil {
				return err
			}
			if useProject {
				return planAgainstProject(cmd.Context(), configPath, jsonOutput)
			}
			return runValidate(cmd.Context(), configPath, jsonOutput, useDSN)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "config source (env: INSTANCEZ_CONFIG)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output errors as JSON (for CI)")
	cmd.Flags().StringVar(&useDSN, "use-dsn", "", "after syntax check, plan a migration against this owner-class DSN")
	cmd.Flags().BoolVar(&useProject, "project", false, "preview migration against the cloud project from instancez.yaml")
	return cmd
}

func runValidate(ctx context.Context, configPath string, jsonOutput bool, useDSN string) error {
	if err := requireLocalConfig(configPath); err != nil {
		return err
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		if jsonOutput {
			return printJSONError(err)
		}
		return err
	}

	errs := config.Validate(cfg)
	if errs != nil {
		if jsonOutput {
			return printJSONErrors(errs)
		}
		return printPrettyErrors(errs)
	}

	if !jsonOutput {
		fmt.Println("  ✓ Schema valid")
	}

	if useDSN == "" {
		return nil
	}

	// Plan a migration against the live DB. We never apply — this is a
	// preview that proves the YAML can be migrated to from current state.
	return planAgainstDSN(ctx, cfg, useDSN, jsonOutput)
}

// planAgainstDSN opens an owner pool, reads the last applied config from
// _ultrabase_migrations (if present), and prints the DDL diff. Errors from
// the planner surface back so the user sees concretely what would break.
func planAgainstDSN(ctx context.Context, cfg *domain.Config, dsn string, jsonOutput bool) error {
	owner, err := postgres.NewOwner(ctx, dsn, cfg.Database.Pool)
	if err != nil {
		return fmt.Errorf("connect owner DSN: %w", err)
	}
	defer owner.Close()

	oldCfg, err := loadAppliedConfig(ctx, owner)
	if err != nil {
		return fmt.Errorf("read prior migration state: %w", err)
	}

	migrator := app.NewMigrator(owner.Database, domain.DefaultRoles())
	ddl, err := migrator.Plan(ctx, oldCfg, cfg)
	if err != nil {
		return fmt.Errorf("plan migration: %w", err)
	}

	if jsonOutput {
		out := map[string]any{
			"fresh_database": oldCfg == nil,
			"ddl":            ddl,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if ddl == "" {
		fmt.Println("  ✓ Database is in sync — no migration needed")
		return nil
	}
	if oldCfg == nil {
		fmt.Println("  ! Fresh database — full schema will be created on first dev/serve")
	} else {
		fmt.Println("  ! Migration plan (would run on next dev/serve):")
	}
	fmt.Println()
	fmt.Println(ddl)
	return nil
}

// loadAppliedConfig returns the last applied config from
// _ultrabase_migrations, or nil if the table does not yet exist (fresh DB)
// or has no rows. Any other error is fatal because we cannot safely plan
// without knowing prior state.
func loadAppliedConfig(ctx context.Context, owner domain.OwnerDB) (*domain.Config, error) {
	exists, err := migrationsTableExists(ctx, owner)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	last, err := owner.GetLastMigration(ctx)
	if err != nil {
		return nil, err
	}
	if last == nil || last.ConfigJSON == "" || last.ConfigJSON == "{}" {
		return nil, nil
	}
	var cfg domain.Config
	if err := json.Unmarshal([]byte(last.ConfigJSON), &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal stored config: %w", err)
	}
	return &cfg, nil
}

// migrationsTableExists checks pg_tables rather than catching a
// undefined_table error from GetLastMigration — the affirmative check is
// cheaper to reason about and never aborts a transaction.
func migrationsTableExists(ctx context.Context, owner domain.OwnerDB) (bool, error) {
	row, err := owner.QueryRow(ctx,
		`SELECT COUNT(*) AS n FROM pg_tables WHERE schemaname = current_schema() AND tablename = '_ultrabase_migrations'`,
	)
	if err != nil {
		return false, err
	}
	n, _ := row["n"].(int64)
	return n > 0, nil
}

// printPrettyErrors writes a formatted validation report to stderr and returns
// errReported so the caller exits non-zero without re-printing a bare error.
func printPrettyErrors(errs domain.ValidationErrors) error {
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "\n  ✗ Error: %s\n", e.Path)
		if e.Line > 0 {
			fmt.Fprintf(os.Stderr, "    at instancez.yaml:%d\n", e.Line)
		}
		fmt.Fprintf(os.Stderr, "    %s\n", e.Message)
		if e.Suggestion != "" {
			fmt.Fprintf(os.Stderr, "    Suggestion: %s\n", e.Suggestion)
		}
	}
	fmt.Fprintf(os.Stderr, "\n  Found %d error(s)\n", len(errs))
	return errReported
}

type jsonError struct {
	Path       string `json:"path"`
	Message    string `json:"message"`
	Line       int    `json:"line,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

func printJSONErrors(errs domain.ValidationErrors) error {
	out := make([]jsonError, len(errs))
	for i, e := range errs {
		out[i] = jsonError{
			Path:       e.Path,
			Message:    e.Message,
			Line:       e.Line,
			Suggestion: e.Suggestion,
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(out)
	return errReported
}

func printJSONError(err error) error {
	out := []jsonError{{Path: "", Message: err.Error()}}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(out)
	return errReported
}

// planAgainstProject uploads the local yaml to the cloud project's draft Defs
// (so the server-side diff reflects what's actually on disk) and then fetches
// the migration preview. Pure side-effect-aware — no local DB connection.
func planAgainstProject(ctx context.Context, configPath string, jsonOutput bool) error {
	if err := requireLocalConfig(configPath); err != nil {
		return err
	}
	creds, err := cloud.Load()
	if err != nil {
		return fmt.Errorf("--project requires authentication: %w", err)
	}

	src, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}
	projectID, err := cloud.ReadProjectID(src)
	if err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}
	if projectID == "" {
		return errors.New("no project.cloud.project_id in instancez.yaml; run `inz init --with-cloud` first")
	}

	apiURL, err := cloud.APIURLFromConfig(configPath)
	if err != nil {
		return err
	}
	c := cloud.NewClient(apiURL, creds.PAT)

	// Push the local YAML to the project's draft so the diff reflects
	// what's actually on disk, not whatever stale draft was uploaded last.
	if err := c.UploadYAML(projectID, string(src)); err != nil {
		return fmt.Errorf("upload yaml: %w", err)
	}

	resp, err := c.MigrationPreview(projectID)
	if err != nil {
		return fmt.Errorf("migration preview: %w", err)
	}

	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(resp)
	}
	if resp.Diff == "" {
		fmt.Println("  ✓ No pending changes.")
		return nil
	}
	fmt.Println(resp.Diff)
	_ = ctx // reserved for future timeout/cancellation
	return nil
}
