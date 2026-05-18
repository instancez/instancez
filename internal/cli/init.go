package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "init <name>",
		Short: "Scaffold a new Ultrabase project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if dir == "" {
				dir = filepath.Join(".", name)
			}
			return runInit(name, dir)
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "output directory (default: ./<name>)")
	return cmd
}

func runInit(name, dir string) error {
	fmt.Printf("Creating project in %s ...\n", dir)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "templates"), 0o755); err != nil {
		return fmt.Errorf("create templates directory: %w", err)
	}

	// ultrabase.yaml — minimal working example
	yamlContent := fmt.Sprintf(`version: 1

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

	envContent := `# Database — two-login Supabase-style setup.
#
# Provision once on the Postgres server (run as superuser):
#   CREATE ROLE ultrabase_owner LOGIN PASSWORD 'CHANGE_ME'
#       CREATEROLE CREATEDB BYPASSRLS REPLICATION;
#   CREATE ROLE authenticator LOGIN PASSWORD 'CHANGE_ME' NOINHERIT;
#   ALTER DATABASE ` + name + ` OWNER TO ultrabase_owner;
#   ALTER SCHEMA public OWNER TO ultrabase_owner;
# The three no-login roles (anon, authenticated, service_role) and grants
# to authenticator are created automatically by ultrabase migrations.

ULTRABASE_OWNER_DATABASE_URL=postgres://ultrabase_owner:CHANGE_ME@localhost:5432/` + name + `?sslmode=disable
ULTRABASE_AUTH_DATABASE_URL=postgres://authenticator:CHANGE_ME@localhost:5432/` + name + `?sslmode=disable

# Optional role name overrides (defaults match Supabase / supabase-js).
# ULTRABASE_DB_AUTHENTICATOR_ROLE=authenticator
# ULTRABASE_DB_ANON_ROLE=anon
# ULTRABASE_DB_AUTHENTICATED_ROLE=authenticated
# ULTRABASE_DB_SERVICE_ROLE=service_role

# Optional: Email provider
# ULTRABASE_EMAIL_API_KEY=re_xxxxx

# Optional: Admin key for /api/_admin endpoints
# ULTRABASE_ADMIN_KEY=change-me
`

	gitignoreContent := `.env
uploads/
sdk/
`

	files := map[string]string{
		"ultrabase.yaml": yamlContent,
		".env.example":   envContent,
		".gitignore":     gitignoreContent,
	}

	for filename, content := range files {
		path := filepath.Join(dir, filename)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", filename, err)
		}
		fmt.Printf("  + %s\n", filename)
	}

	fmt.Printf("\nDone! Next steps:\n")
	fmt.Printf("  cd %s\n", dir)
	fmt.Printf("  cp .env.example .env    # configure database\n")
	fmt.Printf("  ultrabase dev            # start dev server\n")
	return nil
}
