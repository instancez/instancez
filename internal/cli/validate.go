package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/instancez/instancez/internal/adapter/postgres"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/cloud"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
	"github.com/spf13/cobra"
)

func newValidateCmd() *cobra.Command {
	var (
		configPath string
		jsonOutput bool
		useDSN     string
		project    string
		branch     string
	)

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate config without starting the server",
		Long: `Validate instancez.yaml structure and references.

With --use-dsn, validate also connects to the given Postgres and prints
the migration that would bring the database in sync with the yaml. The
migration is planned but never applied — this is a dry-run.

With --project, validate previews the local yaml against the named branch
(--branch draft|production, default draft) of the cloud project and prints
the diff. Bare --project reads the project id from project.cloud.project_id
in instancez.yaml; --project <id> or --project=<id> targets that project
instead, the same as 'inz cloud deploy --project'. validate never creates a
project and never writes anything server-side, for either branch; use
'inz cloud deploy --new' to link one first.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := applyEnvDefaults(cmd.Flags(), nil, os.Getenv); err != nil {
				return err
			}
			if !cmd.Flags().Changed("project") {
				if len(args) > 0 {
					return fmt.Errorf("validate does not take positional arguments")
				}
				return runValidate(cmd.Context(), configPath, jsonOutput, useDSN)
			}
			override := project
			if len(args) == 1 {
				override = args[0]
			}
			return planAgainstProject(cmd.Context(), configPath, jsonOutput, override, branch)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "config source (env: INSTANCEZ_CONFIG)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output errors as JSON (for CI)")
	cmd.Flags().StringVar(&useDSN, "use-dsn", "", "after syntax check, plan a migration against this owner-class DSN")
	cmd.Flags().StringVar(&project, "project", "", "preview against a cloud project (bare --project uses instancez.yaml's project_id; --project <id> overrides it)")
	cmd.Flags().Lookup("project").NoOptDefVal = useFileProjectID
	cmd.Flags().StringVar(&branch, "branch", "draft", "branch to preview against: draft or production")
	return cmd
}

// useFileProjectID is the sentinel NoOptDefVal for --project, so bare
// --project (no =value and no following token) is distinguishable from an
// explicit override in planAgainstProject. Not a value any real project id
// can equal, since ids are Mongo ObjectID hex strings.
const useFileProjectID = "\x00use-file-project-id\x00"

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

	raw, _ := os.ReadFile(configPath)
	errs := config.Validate(cfg)
	errs = append(errs, config.ValidateEnvNamespace(raw)...)
	if errs != nil {
		if jsonOutput {
			return printJSONErrors(errs)
		}
		return printPrettyErrors(errs)
	}

	if fileErrs := config.ValidateFunctionFiles(cfg, filepath.Dir(configPath)); fileErrs != nil {
		if jsonOutput {
			return printJSONErrors(fileErrs)
		}
		return printPrettyErrors(fileErrs)
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
// _instancez_migrations (if present), and prints the DDL diff. Errors from
// the planner surface back so the user sees concretely what would break.
func planAgainstDSN(ctx context.Context, cfg *domain.Config, dsn string, jsonOutput bool) error {
	owner, err := postgres.NewOwner(ctx, dsn, cfg.Database.Pool)
	if err != nil {
		return fmt.Errorf("connect owner DSN: %w", err)
	}
	defer func() { _ = owner.Close() }()

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
// _instancez_migrations, or nil if the table does not yet exist (fresh DB)
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
		`SELECT COUNT(*) AS n FROM pg_tables WHERE schemaname = current_schema() AND tablename = '_instancez_migrations'`,
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
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return errReported
}

func printJSONError(err error) error {
	out := []jsonError{{Path: "", Message: err.Error()}}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if encErr := enc.Encode(out); encErr != nil {
		return fmt.Errorf("encode json error: %w", encErr)
	}
	return errReported
}

// planAgainstProject previews the local yaml against the named branch of the
// cloud project. This is a pure read: nothing is written server-side, for
// either branch. Backs `inz validate --project`.
//
// projectOverride is either useFileProjectID (bare --project, read the id
// from configPath) or an explicit id from --project=<id>. Either way, an
// unresolved project id is always an error here: validate never creates a
// project, unlike `inz cloud deploy --new`.
func planAgainstProject(ctx context.Context, configPath string, jsonOutput bool, projectOverride, branch string) error {
	if err := requireLocalConfig(configPath); err != nil {
		return err
	}
	if branch == "" {
		branch = "draft"
	}
	if branch != "draft" && branch != "production" {
		return fmt.Errorf("--branch must be draft or production, got %q", branch)
	}
	creds, err := cloud.Load()
	if err != nil {
		return fmt.Errorf("--project requires authentication: %w", err)
	}

	src, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}

	projectID := projectOverride
	if projectID == useFileProjectID {
		projectID, err = cloud.ReadProjectID(src)
		if err != nil {
			return fmt.Errorf("parse %s: %w", configPath, err)
		}
	}
	if projectID == "" {
		return errors.New("no project linked; pass --project=<id>, or link one first with `inz cloud deploy --new`")
	}

	apiURL, err := cloud.APIURLFromConfig(configPath)
	if err != nil {
		return err
	}
	c := cloud.NewClient(apiURL, creds.PAT)

	dropped, diff, err := c.PreviewBranchConfig(projectID, branch, string(src))
	if err != nil {
		return reportCloudErr("preview config", err)
	}

	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"dropped": dropped,
			"diff":    diff,
		})
	}
	printDropped(dropped)
	fmt.Println(renderConfigDiff(diff))
	_ = ctx // reserved for future timeout/cancellation
	return nil
}
