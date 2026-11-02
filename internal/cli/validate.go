package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		configPath    string
		jsonOutput    bool
		useDSN        string
		project       string
		dryRunDDL     bool
		cloudValidate bool
	)

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate config without starting the server",
		Long: `Validate instancez.yaml structure and references. Follows include:.

With --use-dsn, validate also connects to the given Postgres and prints
the migration that would bring the database in sync with the yaml. The
migration is planned but never applied.

With --use-dsn and --dry-run-ddl, validate also runs the migration inside a
transaction that is always rolled back, so unknown columns in RLS policies
and bad rpc bodies surface as real Postgres errors. This takes the
migration advisory lock and DDL locks until the rollback (lock timeout 2s,
statement timeout 30s). It is opt-in because it touches the live database;
plain --use-dsn never runs DDL.

With --project, validate previews the local yaml against the cloud project
and prints the diff. Bare --project reads the project id from
project.cloud.project_id in instancez.yaml; --project <id> or --project=<id>
targets that project instead, the same as 'inz cloud deploy --project'.
validate never creates a project and never writes anything server-side; use
'inz cloud deploy --new' to link one first.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := applyEnvDefaults(cmd.Flags(), nil, os.Getenv); err != nil {
				return err
			}
			if cloudValidate {
				if cmd.Flags().Changed("project") {
					return errors.New("--cloud and --project are mutually exclusive (--cloud is projectless)")
				}
				if len(args) > 0 {
					return fmt.Errorf("validate does not take positional arguments")
				}
				return validateAgainstCloud(cmd.Context(), configPath, jsonOutput)
			}
			if dryRunDDL && useDSN == "" {
				return errors.New("--dry-run-ddl requires --use-dsn")
			}
			if !cmd.Flags().Changed("project") {
				if len(args) > 0 {
					return fmt.Errorf("validate does not take positional arguments")
				}
				return runValidate(cmd.Context(), cmd.ErrOrStderr(), configPath, jsonOutput, useDSN, dryRunDDL)
			}
			override := project
			if len(args) == 1 {
				override = args[0]
			}
			return planAgainstProject(cmd.Context(), configPath, jsonOutput, override)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "instancez.yaml", "config source (env: INSTANCEZ_CONFIG)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output errors as JSON (for CI)")
	cmd.Flags().StringVar(&useDSN, "use-dsn", "", "after syntax check, plan a migration against this owner-class DSN")
	cmd.Flags().BoolVar(&dryRunDDL, "dry-run-ddl", false, "with --use-dsn, also run the migration in a rolled-back transaction (takes DDL locks; catches unknown columns and bad rpc bodies)")
	cmd.Flags().StringVar(&project, "project", "", "preview against a cloud project (bare --project uses instancez.yaml's project_id; --project <id> overrides it)")
	cmd.Flags().Lookup("project").NoOptDefVal = useFileProjectID
	cmd.Flags().BoolVar(&cloudValidate, "cloud", false, "validate against the cloud's policy rules server-side (projectless; the same rules a deploy enforces)")
	return cmd
}

// validateAgainstCloud validates the local yaml against the cloud's policy rules
// server-side, without a project and without writing anything. Backs `inz
// validate --cloud`. Blocking problems exit non-zero; dropped entries are
// non-blocking warnings.
func validateAgainstCloud(ctx context.Context, configPath string, jsonOutput bool) error {
	_ = ctx
	if err := requireLocalConfig(configPath); err != nil {
		return err
	}
	creds, err := cloud.Load()
	if err != nil {
		return fmt.Errorf("--cloud requires authentication: %w", err)
	}
	src, err := config.ReadConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}
	c := cloud.NewClient(cloud.APIURL(), creds.PAT)
	problems, dropped, err := c.ValidateConfigCloud(string(src))
	if err != nil {
		return reportCloudErr("validate config", err)
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"problems": problems, "dropped": dropped,
		})
	}
	if perr := reportCloudProblems("validate", problems); perr != nil {
		return perr
	}
	printDropped(dropped)
	fmt.Println("  ✓ Cloud valid")
	return nil
}

// useFileProjectID is the sentinel NoOptDefVal for --project, so bare
// --project (no =value and no following token) is distinguishable from an
// explicit override in planAgainstProject. Not a value any real project id
// can equal, since ids are Mongo ObjectID hex strings.
const useFileProjectID = "\x00use-file-project-id\x00"

// missingEnvReport turns a missing-env load error into one report, leaving out errors caused by the placeholder for an unset var, or nil.
func missingEnvReport(raw []byte, err error) domain.ValidationErrors {
	var me *domain.MissingEnvError
	if !errors.As(err, &me) {
		return nil
	}
	errs := config.ValidateEnvNamespace(raw)
	if len(errs) == 0 {
		return nil
	}
	lenient, lerr := config.ParseBytesLenient(raw, "")
	if lerr != nil {
		return errs
	}
	unset := map[string]bool{}
	locs := config.EnvRefLocations(raw)
	for _, v := range me.Vars {
		for _, l := range locs[v] {
			unset[l.Path] = true
		}
	}
	for _, e := range config.ValidateLocal(lenient) {
		if !unset[e.Path] {
			errs = append(errs, e)
		}
	}
	return errs
}

// cfgSrc is a config file path with its raw bytes; a zero value means no file (bundle, s3), so no positions are shown.
type cfgSrc struct {
	file string
	raw  []byte
}

func readSrc(file string) cfgSrc {
	raw, _ := os.ReadFile(file)
	return cfgSrc{file: file, raw: raw}
}

func (s cfgSrc) at(e *domain.ValidationError) string {
	switch {
	case s.file == "" || e.Line == 0:
		return ""
	case e.Column > 0:
		return fmt.Sprintf("%s:%d:%d", s.file, e.Line, e.Column)
	}
	return fmt.Sprintf("%s:%d", s.file, e.Line)
}

// warnBlanked tells the user which unset ${VAR}s were treated as empty, and where they are used.
func warnBlanked(errOut io.Writer, src cfgSrc, names []string) {
	locs := config.EnvRefLocations(src.raw)
	for _, name := range names {
		at := ""
		if l := locs[name]; len(l) > 0 {
			at = " at " + l[0].Path
		}
		_, _ = fmt.Fprintf(errOut, "  ! unset env %s (using empty)%s\n", name, at)
	}
}

func runValidate(ctx context.Context, errOut io.Writer, configPath string, jsonOutput bool, useDSN string, dryRunDDL bool) error {
	if err := requireLocalConfig(configPath); err != nil {
		return err
	}

	cfg, blanked, err := config.LoadLocalWithDotenv(configPath, ".development.env")
	src := readSrc(configPath)
	raw := src.raw
	warnBlanked(errOut, src, blanked)
	if errs := missingEnvReport(raw, err); errs != nil {
		if jsonOutput {
			return printJSONErrors(src, errs)
		}
		return printPrettyErrors(src, errs)
	}
	if err != nil {
		if jsonOutput {
			return printJSONError(err)
		}
		return err
	}

	errs := config.ValidateLocal(cfg)
	errs = append(errs, config.ValidateEnvNamespace(raw)...)
	if errs != nil {
		if jsonOutput {
			return printJSONErrors(src, errs)
		}
		return printPrettyErrors(src, errs)
	}

	printWarnings(errOut, src, config.Warnings(cfg))
	printWarnings(errOut, src, config.LocalWarnings(cfg))

	if fileErrs := config.ValidateFunctionFiles(cfg, filepath.Dir(configPath)); fileErrs != nil {
		if jsonOutput {
			return printJSONErrors(src, fileErrs)
		}
		return printPrettyErrors(src, fileErrs)
	}

	if !jsonOutput {
		fmt.Println("  ✓ Schema valid")
	}

	if useDSN == "" {
		return nil
	}

	// Plan a migration against the live DB. We never apply — this is a
	// preview that proves the YAML can be migrated to from current state.
	return planAgainstDSN(ctx, cfg, useDSN, jsonOutput, dryRunDDL)
}

// planAgainstDSN opens an owner pool, reads the last applied config from
// _instancez_migrations (if present), and prints the DDL diff. Errors from
// the planner surface back so the user sees concretely what would break.
func planAgainstDSN(ctx context.Context, cfg *domain.Config, dsn string, jsonOutput bool, dryRunDDL bool) error {
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

	var dryErr error
	if dryRunDDL {
		dryErr = migrator.DryRun(ctx, cfg)
	}

	if jsonOutput {
		out := map[string]any{
			"fresh_database": oldCfg == nil,
			"ddl":            ddl,
		}
		if dryRunDDL {
			out["dry_run"] = "ok"
			if dryErr != nil {
				out["dry_run"] = dryErr.Error()
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return err
		}
		return dryErr
	}

	switch {
	case ddl == "":
		fmt.Println("  ✓ Database is in sync — no migration needed")
	case oldCfg == nil:
		fmt.Println("  ! Fresh database — full schema will be created on first dev/serve")
		fmt.Println()
		fmt.Println(ddl)
	default:
		fmt.Println("  ! Migration plan (would run on next dev/serve):")
		fmt.Println()
		fmt.Println(ddl)
	}
	if dryErr != nil {
		return dryErr
	}
	if dryRunDDL {
		fmt.Println("  ✓ Dry run passed (rolled back)")
	}
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
func printPrettyErrors(src cfgSrc, errs domain.ValidationErrors) error {
	config.Annotate(src.raw, errs)
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "\n  ✗ Error: %s\n", e.Path)
		if at := src.at(e); at != "" {
			fmt.Fprintf(os.Stderr, "    at %s\n", at)
		}
		fmt.Fprintf(os.Stderr, "    %s\n", e.Message)
		if e.Suggestion != "" {
			fmt.Fprintf(os.Stderr, "    Suggestion: %s\n", e.Suggestion)
		}
	}
	fmt.Fprintf(os.Stderr, "\n  Found %d error(s)\n", len(errs))
	return reportedValidation(errs)
}

// printWarnings writes non-blocking findings; it never fails the command.
func printWarnings(w io.Writer, src cfgSrc, ws domain.ValidationErrors) {
	config.Annotate(src.raw, ws)
	for _, e := range ws {
		_, _ = fmt.Fprintf(w, "\n  ! Warning: %s\n", e.Path)
		if at := src.at(e); at != "" {
			_, _ = fmt.Fprintf(w, "    at %s\n", at)
		}
		_, _ = fmt.Fprintf(w, "    %s\n", e.Message)
		if e.Suggestion != "" {
			_, _ = fmt.Fprintf(w, "    Suggestion: %s\n", e.Suggestion)
		}
	}
}

type jsonError struct {
	Path       string `json:"path"`
	Message    string `json:"message"`
	Line       int    `json:"line,omitempty"`
	Column     int    `json:"column,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

func printJSONErrors(src cfgSrc, errs domain.ValidationErrors) error {
	config.Annotate(src.raw, errs)
	out := make([]jsonError, len(errs))
	for i, e := range errs {
		out[i] = jsonError{
			Path:       e.Path,
			Message:    e.Message,
			Line:       e.Line,
			Column:     e.Column,
			Suggestion: e.Suggestion,
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return reportedValidation(errs)
}

func printJSONError(err error) error {
	out := []jsonError{{Path: "", Message: err.Error()}}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if encErr := enc.Encode(out); encErr != nil {
		return fmt.Errorf("encode json error: %w", encErr)
	}
	class, _, _ := classifyErr(err)
	return reported(class)
}

// planAgainstProject previews the local yaml against the cloud project. This
// is a pure read: nothing is written server-side. Backs `inz validate
// --project`.
//
// projectOverride is either useFileProjectID (bare --project, read the id
// from configPath) or an explicit id from --project=<id>. Either way, an
// unresolved project id is always an error here: validate never creates a
// project, unlike `inz cloud deploy --new`.
func planAgainstProject(ctx context.Context, configPath string, jsonOutput bool, projectOverride string) error {
	if err := requireLocalConfig(configPath); err != nil {
		return err
	}
	creds, err := cloud.Load()
	if err != nil {
		return fmt.Errorf("--project requires authentication: %w", err)
	}

	src, err := config.ReadConfigFile(configPath)
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

	c := cloud.NewClient(cloud.APIURL(), creds.PAT)

	dropped, diff, err := c.PreviewConfig(projectID, string(src))
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
