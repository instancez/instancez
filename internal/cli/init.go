package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/saedx1/ultrabase/internal/cloud"
	"github.com/spf13/cobra"
)

// initOptions captures the resolved positional + flags for `ultra init`.
type initOptions struct {
	name         string
	dir          string
	withDSN      string
	useDock      bool
	withCloud    bool
	generateLike string
	force        bool
}

func newInitCmd() *cobra.Command {
	var opts initOptions

	cmd := &cobra.Command{
		Use:   "init [name]",
		Short: "Scaffold a new Ultrabase project in the current directory",
		Long: `Scaffold a new Ultrabase project.

The project is created in the current directory by default. The project name
defaults to the directory's basename when not given as a positional argument.

Optional bootstrap flags also wire up a database so 'ultra dev' works
immediately:
  --with-dsn <url>   Bootstrap roles on a Postgres you supply
  --with-docker      Start a local Docker Postgres (not yet implemented)

Without a flag, init only writes scaffolding files; you can configure a data
source later.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.name = args[0]
			}
			return runInit(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.dir, "dir", ".", "output directory")
	cmd.Flags().StringVar(&opts.withDSN, "with-dsn", "", "bootstrap roles on this privileged Postgres DSN")
	cmd.Flags().BoolVar(&opts.useDock, "with-docker", false, "start a local Docker Postgres and bootstrap it")
	cmd.Flags().BoolVar(&opts.withCloud, "with-cloud", false, "create a project in Ultrabase Cloud (requires `ultra login`)")
	cmd.Flags().StringVar(&opts.generateLike, "generate-like", "", "generate ultrabase.yaml from a free-form prompt (requires `ultra login`)")
	cmd.Flags().BoolVar(&opts.force, "force", false, "overwrite existing scaffolding files")
	return cmd
}

// validateInitFlags enforces mutual exclusions between init's flags.
// --with-dsn and --with-docker are mutually exclusive (both are local dev
// DB sources). --with-cloud is orthogonal — it specifies the cloud target.
// --generate-like is also orthogonal (it shapes the scaffold YAML).
func validateInitFlags(opts initOptions) error {
	if opts.withDSN != "" && opts.useDock {
		return errors.New("--with-dsn and --with-docker are mutually exclusive")
	}
	return nil
}

func runInit(ctx context.Context, opts initOptions) error {
	if err := validateInitFlags(opts); err != nil {
		return err
	}

	// Cloud-dependent flags require credentials. Fail fast.
	if opts.withCloud || opts.generateLike != "" {
		if _, err := cloud.Load(); err != nil {
			return fmt.Errorf("--with-cloud / --generate-like require `ultra login`: %w", err)
		}
	}

	dir, err := filepath.Abs(opts.dir)
	if err != nil {
		return fmt.Errorf("resolve dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	name := opts.name
	if name == "" {
		name = filepath.Base(dir)
	}

	yamlPath := filepath.Join(dir, "ultrabase.yaml")
	if _, err := os.Stat(yamlPath); err == nil && !opts.force {
		return fmt.Errorf("ultrabase.yaml already exists in %s (use --force to overwrite)", dir)
	}

	// Bootstrap a DB first if requested — fail fast before writing any files
	// so a bad DSN doesn't leave the project half-scaffolded.
	var ownerDSN, authDSN string
	switch {
	case opts.useDock:
		return errors.New("--with-docker is not yet implemented in this build; use --with-dsn or omit the flag for now")
	case opts.withDSN != "":
		fmt.Println("  Bootstrapping roles on the supplied DSN...")
		ownerDSN, authDSN, err = bootstrapDB(ctx, opts.withDSN)
		if err != nil {
			return fmt.Errorf("bootstrap: %w", err)
		}
		fmt.Println("  ✓ Roles provisioned (ultrabase_owner + authenticator + anon/authenticated/service_role)")
		fmt.Println()
		fmt.Println("  Note: ultrabase only manages tables/schemas it creates from ultrabase.yaml.")
		fmt.Println("  Existing tables in your database are not imported or modified.")
		fmt.Println()
	}

	// If --generate-like is set, fetch the YAML from the cloud AI service
	// instead of using the static scaffold.
	var generatedYAML string
	if opts.generateLike != "" {
		fmt.Println("  Generating ultrabase.yaml from prompt...")
		creds, _ := cloud.Load()
		c := cloud.NewClient(cloud.APIURL(), creds.PAT)
		resp, err := c.GenerateYAML(opts.generateLike)
		if err != nil {
			return fmt.Errorf("generate-yaml: %w", err)
		}
		generatedYAML = resp.YAML
		fmt.Printf("  ✓ Generated (%d input + %d output tokens)\n", resp.Tokens.Input, resp.Tokens.Output)
	}

	// ultrabase.yaml: existence already gated above (errors without --force).
	if err := applyWrite(dir, "ultrabase.yaml", func(_ string) (string, writeAction) {
		if generatedYAML != "" {
			return generatedYAML, actionCreate
		}
		return scaffoldYAML(name), actionCreate
	}); err != nil {
		return err
	}

	// .gitignore: append-only — preserve user customizations, only add our
	// entries that aren't already present.
	if err := applyWrite(dir, ".gitignore", func(existing string) (string, writeAction) {
		merged := mergeGitignore(existing, scaffoldGitignore())
		switch {
		case existing == "":
			return merged, actionCreate
		case merged == existing:
			return existing, actionSkip
		default:
			return merged, actionUpdate
		}
	}); err != nil {
		return err
	}

	// .production.env.example: write once. After that, treat user edits as
	// authoritative — the file is a static example, we have no live values to
	// inject, and the user may have hand-tuned it for their environment.
	if err := applyWrite(dir, ".production.env.example", func(existing string) (string, writeAction) {
		if existing != "" {
			return existing, actionSkip
		}
		return scaffoldProductionEnvExample(), actionCreate
	}); err != nil {
		return err
	}

	// .development.env: key-preserving merge. Rotated DSNs go in, user-added
	// custom lines (extra env vars, comments) stay put.
	if ownerDSN != "" && authDSN != "" {
		if err := applyWrite(dir, ".development.env", func(existing string) (string, writeAction) {
			if existing == "" {
				return scaffoldDevelopmentEnv(ownerDSN, authDSN), actionCreate
			}
			merged := mergeEnvFile(existing, []envKV{
				{Key: "ULTRABASE_OWNER_DATABASE_URL", Val: ownerDSN},
				{Key: "ULTRABASE_AUTH_DATABASE_URL", Val: authDSN},
			})
			if merged == existing {
				return existing, actionSkip
			}
			return merged, actionUpdate
		}); err != nil {
			return err
		}
	}

	// Create cloud project and bake project_id into ultrabase.yaml.
	if opts.withCloud {
		fmt.Println("  Creating Ultrabase Cloud project...")
		creds, _ := cloud.Load()
		c := cloud.NewClient(cloud.APIURL(), creds.PAT)
		resp, err := c.CreateProject(name)
		if err != nil {
			return fmt.Errorf("creating cloud project: %w", err)
		}
		fmt.Printf("  ✓ Project created (id: %s)\n", resp.ProjectID)

		existing, err := os.ReadFile(yamlPath)
		if err != nil {
			return fmt.Errorf("re-reading ultrabase.yaml: %w", err)
		}
		updated, err := cloud.WriteProjectID(existing, resp.ProjectID)
		if err != nil {
			return fmt.Errorf("injecting project_id: %w", err)
		}
		if err := os.WriteFile(yamlPath, updated, 0o644); err != nil {
			return fmt.Errorf("writing ultrabase.yaml: %w", err)
		}
		fmt.Println("  ~ ultrabase.yaml (added project.cloud.project_id)")
	}

	fmt.Println()
	fmt.Println("Done! Next steps:")
	if dir != mustCwd() {
		fmt.Printf("  cd %s\n", opts.dir)
	}
	switch {
	case opts.withCloud:
		fmt.Println("  ultra deploy            # push your YAML to the cloud project")
	case opts.withDSN != "":
		fmt.Println("  ultra dev --use-dsn")
	case opts.useDock:
		fmt.Println("  ultra dev --use-docker")
	default:
		fmt.Println("  # Configure a data source, then:")
		fmt.Println("  ultra dev --use-dsn        # point at your own Postgres")
	}
	return nil
}

func mustCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return cwd
	}
	return abs
}

func scaffoldYAML(name string) string {
	return fmt.Sprintf(`version: 1

project:
  name: %q

auth:
  jwt_expiry: 15m
  refresh_tokens: true
  refresh_token_expiry: 7d

  email:
    verify_email: false

tables:
  todos:
    fields:
      - name: id
        type: bigserial
        primary_key: true
      - name: user_id
        foreign_key:
          references: auth.users.id
          on_delete: cascade
      - name: title
        type: text
        required: true
      - name: status
        type: text
        required: true
        enum: [pending, active, done]
        default: pending
      - name: created_at
        type: timestamptz
        required: true
        default: now()

    rls:
      - operations: [select, insert, update, delete]
        check: "user_id = auth.uid()"
`, name)
}

func scaffoldGitignore() string {
	return `.development.env
.production.env
uploads/
sdk/
pgdata/
`
}

func scaffoldProductionEnvExample() string {
	return `# Production runtime config — copy to .production.env (gitignored) before
# running 'ultra serve'. Shell env vars always take precedence over this file.
#
# Two-pool layout: the owner DSN runs migrations/seeding; the authenticator
# DSN handles request traffic (NOINHERIT login that SET LOCAL ROLEs per query).

ULTRABASE_OWNER_DATABASE_URL=postgres://ultrabase_owner:CHANGE_ME@host:5432/dbname?sslmode=require
ULTRABASE_AUTH_DATABASE_URL=postgres://authenticator:CHANGE_ME@host:5432/dbname?sslmode=require

# Optional: admin key for /api/_admin endpoints
# ULTRABASE_ADMIN_KEY=CHANGE_ME

# Optional: email provider
# ULTRABASE_EMAIL_API_KEY=re_xxx
`
}

func scaffoldDevelopmentEnv(ownerDSN, authDSN string) string {
	return fmt.Sprintf(`# Auto-generated by 'ultra init --with-dsn'. Refresh with the same command.
ULTRABASE_OWNER_DATABASE_URL=%s
ULTRABASE_AUTH_DATABASE_URL=%s
`, ownerDSN, authDSN)
}

// writeAction tells the file-write dispatcher what happened, so the printed
// log shows "+" for new files, "~" for updates, and nothing for skips.
type writeAction int

const (
	actionSkip writeAction = iota
	actionCreate
	actionUpdate
)

// envKV is a single key/value update for mergeEnvFile. A slice preserves
// caller order so the output is deterministic — map iteration would not be.
type envKV struct{ Key, Val string }

// applyWrite reads any existing file at dir/name (empty string if missing),
// runs the planner to decide the new content + action, then writes (or stays
// silent on skip). The planner owns the per-file merge policy; applyWrite
// is just the IO + logging shell.
func applyWrite(dir, name string, plan func(existing string) (string, writeAction)) error {
	path := filepath.Join(dir, name)
	var existing string
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", name, err)
	}
	newContent, action := plan(existing)
	if action == actionSkip {
		return nil
	}
	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	sym := "+"
	if action == actionUpdate {
		sym = "~"
	}
	fmt.Printf("  %s %s\n", sym, name)
	return nil
}

// mergeEnvFile preserves the existing dotenv file's structure (lines, order,
// comments, blank lines, foreign keys) while updating any KEY=val line whose
// KEY appears in updates. Keys from updates not found in existing are
// appended at the end. When existing is empty, returns updates serialized in
// caller order.
//
// Key matching is lenient about leading whitespace and an optional "export "
// prefix; on replacement, the rewritten line uses canonical "KEY=val" form.
func mergeEnvFile(existing string, updates []envKV) string {
	applied := make(map[string]bool, len(updates))
	var b strings.Builder
	if existing != "" {
		lines := strings.Split(strings.TrimRight(existing, "\n"), "\n")
		for _, line := range lines {
			key := extractEnvKey(line)
			if key != "" && !applied[key] {
				if val, ok := lookupKV(updates, key); ok {
					b.WriteString(key)
					b.WriteByte('=')
					b.WriteString(val)
					b.WriteByte('\n')
					applied[key] = true
					continue
				}
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	for _, kv := range updates {
		if !applied[kv.Key] {
			b.WriteString(kv.Key)
			b.WriteByte('=')
			b.WriteString(kv.Val)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// extractEnvKey returns the key portion of a dotenv line, or "" if the line
// isn't a KEY=val assignment. Tolerant of leading whitespace and an optional
// "export " prefix, both of which dotenv loaders commonly accept.
func extractEnvKey(line string) string {
	s := strings.TrimLeft(line, " \t")
	s = strings.TrimPrefix(s, "export ")
	s = strings.TrimLeft(s, " \t")
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return ""
	}
	return s[:eq]
}

func lookupKV(updates []envKV, key string) (string, bool) {
	for _, kv := range updates {
		if kv.Key == key {
			return kv.Val, true
		}
	}
	return "", false
}

// mergeGitignore appends to existing any non-blank, non-comment line from
// template that isn't already a literal line in existing. Existing lines are
// never reordered or removed. Returns existing verbatim when nothing is
// missing — that makes the "no-change" signal cheap for the caller.
func mergeGitignore(existing, template string) string {
	if existing == "" {
		return template
	}
	have := make(map[string]bool)
	for _, line := range strings.Split(existing, "\n") {
		have[line] = true
	}
	var missing []string
	for _, line := range strings.Split(strings.TrimRight(template, "\n"), "\n") {
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !have[line] {
			missing = append(missing, line)
		}
	}
	if len(missing) == 0 {
		return existing
	}
	return strings.TrimRight(existing, "\n") + "\n" + strings.Join(missing, "\n") + "\n"
}
