package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// initOptions captures the resolved positional + flags for `inz init`.
type initOptions struct {
	name  string
	dir   string
	force bool
}

func newInitCmd() *cobra.Command {
	var opts initOptions

	cmd := &cobra.Command{
		Use:   "init [name]",
		Short: "Scaffold a new instancez project in the current directory",
		Long: `Scaffold a new instancez project.

The project is created in the current directory by default. The project name
defaults to the directory's basename when not given as a positional argument.

init only writes scaffolding files; it never touches a database. A
'.development.env.example' is written documenting the single superuser DSN that
'inz dev' uses to provision roles on first run.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.name = args[0]
			}
			return runInit(opts)
		},
	}

	cmd.Flags().StringVar(&opts.dir, "dir", ".", "output directory")
	cmd.Flags().BoolVar(&opts.force, "force", false, "overwrite existing scaffolding files")
	return cmd
}

func runInit(opts initOptions) error {
	dir, err := filepath.Abs(opts.dir)
	if err != nil {
		return fmt.Errorf("resolve dir: %w", err)
	}
	yamlPath := filepath.Join(dir, "instancez.yaml")
	_, statErr := os.Stat(yamlPath)
	yamlExists := statErr == nil

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	name := opts.name
	if name == "" {
		name = filepath.Base(dir)
	}

	// instancez.yaml: keep existing content when the file is already present
	// and --force is not set. The rest of init (functions, env examples,
	// next-steps) still runs so a re-run is fully idempotent.
	if yamlExists && !opts.force {
		fmt.Println("  = instancez.yaml (unchanged)")
	} else {
		if err := applyWrite(dir, "instancez.yaml", func(_ string) (string, writeAction) {
			return scaffoldYAML(name), actionCreate
		}); err != nil {
			return err
		}
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

	// functions/: a working starter code function (served at /functions/v1/todos)
	// plus its shared package.json. Written once; user edits are preserved on
	// re-run. `inz dev` runs `npm ci` here on boot.
	if err := os.MkdirAll(filepath.Join(dir, "functions"), 0o755); err != nil {
		return fmt.Errorf("creating functions dir: %w", err)
	}
	if err := applyWrite(dir, filepath.Join("functions", "package.json"), func(existing string) (string, writeAction) {
		if existing != "" {
			return existing, actionSkip
		}
		return scaffoldFunctionsPackageJSON(), actionCreate
	}); err != nil {
		return err
	}
	if err := applyWrite(dir, filepath.Join("functions", "todos.js"), func(existing string) (string, writeAction) {
		if existing != "" {
			return existing, actionSkip
		}
		return scaffoldTodosFunction(), actionCreate
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

	// .development.env.example: write once. Documents the superuser DSN that
	// `inz dev` bootstraps roles from. Treated as authoritative after first
	// write — user edits are preserved on re-run.
	if err := applyWrite(dir, ".development.env.example", func(existing string) (string, writeAction) {
		if existing != "" {
			return existing, actionSkip
		}
		return scaffoldDevelopmentEnvExample(), actionCreate
	}); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Done! Next steps:")
	if dir != mustCwd() {
		fmt.Printf("  cd %s\n", opts.dir)
	}
	fmt.Println("  cp .development.env.example .development.env   # set INSTANCEZ_DATABASE_URL")
	fmt.Println("  inz dev")
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

# Providers back the features declared below: a storage provider is required
# whenever you declare storage buckets, and an email provider is required when
# auth.email.verify_email is true. "local" writes uploads to ./uploads.
providers:
  storage:
    type: local
  # To send auth emails, add an email provider and set verify_email: true below:
  # email:
  #   type: resend
  #   api_key: $INSTANCEZ_RESEND_API_KEY

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

# Storage buckets: file uploads served at /storage/v1/object/<bucket>/<path>.
# Access is governed by RLS, exactly like tables.
storage:
  avatars:
    public: true          # objects are world-readable by URL
    max_size: 5MB
    types: [image/*]
    rls:
      # Only signed-in users may upload, replace, or remove avatars.
      - operations: [insert, update, delete]
        check: "auth.is_authenticated()"

# Code functions: JavaScript HTTP handlers served at /functions/v1/<name>.
# Source + shared deps live in ./functions (run by Node workers). See
# functions/todos.js — a working CRUD handler over the todos table above.
functions:
  todos:
    runtime: node
    file: functions/todos.js
    auth_required: true   # instancez returns 401 for anonymous callers
`, name)
}

func scaffoldGitignore() string {
	return `.development.env
.production.env
uploads/
sdk/
pgdata/
functions/node_modules/
`
}

// scaffoldFunctionsPackageJSON is the shared manifest for the project's code
// functions. @supabase/supabase-js is required for ctx.supabase/serviceClient;
// zod is used by the scaffolded todos handler for request validation.
func scaffoldFunctionsPackageJSON() string {
	return `{
  "name": "functions",
  "private": true,
  "type": "module",
  "dependencies": {
    "@supabase/supabase-js": "^2.107.0",
    "zod": "^3.23.8"
  }
}
`
}

// scaffoldTodosFunction is a working starter code function over the scaffolded
// todos table: list + create the signed-in user's todos, authorized by RLS.
func scaffoldTodosFunction() string {
	return `/**
 * todos — a REST-ish handler over the scaffolded "todos" table.
 *
 * Served at /functions/v1/todos. auth_required: true, so anonymous callers get
 * a 401 before this runs.
 *
 *   GET  /functions/v1/todos?status=active   → list the caller's todos
 *   POST /functions/v1/todos  {"title":"..."} → create one
 *
 * ctx.supabase carries the CALLER's JWT, so the table's RLS policy
 * (user_id = auth.uid()) authorizes every query as that user — you never check
 * ownership in JS. Body validation uses the "zod" dependency.
 */
import { z } from "zod";

const NewTodo = z.object({
  title: z.string().min(1).max(200),
  status: z.enum(["pending", "active", "done"]).optional(),
});

export default async function handler(req, ctx) {
  if (req.method === "GET") {
    let q = ctx.supabase
      .from("todos")
      .select("id, title, status, created_at")
      .order("created_at", { ascending: false });
    if (req.query.status) q = q.eq("status", req.query.status);
    const { data, error } = await q;
    if (error) return { status: 400, body: { error: error.message } };
    return { status: 200, body: { todos: data } };
  }

  if (req.method === "POST") {
    const parsed = NewTodo.safeParse(req.body);
    if (!parsed.success) {
      return { status: 400, body: { error: "invalid body", issues: parsed.error.issues } };
    }
    // user_id is stamped from the verified JWT; the RLS policy also enforces
    // user_id = auth.uid(), so it can't be spoofed.
    const { data, error } = await ctx.supabase
      .from("todos")
      .insert({ ...parsed.data, user_id: ctx.claims.sub })
      .select()
      .single();
    if (error) return { status: 400, body: { error: error.message } };
    ctx.log.info("todo created", { id: data.id });
    return { status: 201, body: { todo: data } };
  }

  return { status: 405, body: { error: "method not allowed" } };
}
`
}

func scaffoldProductionEnvExample() string {
	return `# Production runtime config — copy to .production.env (gitignored) before
# running 'inz serve'. Shell env vars always take precedence over this file.
#
# Two-pool layout: the owner DSN runs migrations; the authenticator
# DSN handles request traffic (NOINHERIT login that SET LOCAL ROLEs per query).

INSTANCEZ_OWNER_DATABASE_URL=postgres://instancez_owner:CHANGE_ME@host:5432/dbname?sslmode=require
INSTANCEZ_AUTH_DATABASE_URL=postgres://authenticator:CHANGE_ME@host:5432/dbname?sslmode=require

# Admin key for the dashboard and /api/_admin endpoints. Leave unset to disable
# them entirely (they return 404). Set a strong, unique value before deploying.
INSTANCEZ_ADMIN_KEY=CHANGE_ME

# Optional: email provider
# INSTANCEZ_EMAIL_API_KEY=re_xxx
`
}

func scaffoldDevelopmentEnvExample() string {
	return `# Local development config — copy to .development.env (gitignored), then set a
# superuser/privileged Postgres DSN below. 'inz dev' reads this DSN on every
# startup to provision instancez_owner + authenticator + the API roles.
#
# 'inz dev' also generates a random INSTANCEZ_ADMIN_KEY into .development.env
# on first run (printed to the console, used to log into the dashboard). Set one
# here yourself to pin a known value instead.

INSTANCEZ_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres
# INSTANCEZ_ADMIN_KEY=CHANGE_ME
`
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

// hasActiveEnvKey reports whether existing contains an active (non-comment)
// assignment for key. A commented "# KEY=…" line counts as absent, since
// extractEnvKey treats the leading "#" as part of the key.
func hasActiveEnvKey(existing, key string) bool {
	for _, line := range strings.Split(existing, "\n") {
		if extractEnvKey(line) == key {
			return true
		}
	}
	return false
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
