package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/instancez/instancez/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/spf13/cobra"
)

type dbResetOpts struct {
	configPath string
	embedded   bool
	yes        bool
	getenv     func(string) string
	tty        func() bool
	confirm    func(string) bool
	out        io.Writer
}

func newDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Local database commands",
	}
	cmd.AddCommand(newDBResetCmd())
	return cmd
}

func newDBResetCmd() *cobra.Command {
	var o dbResetOpts
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Drop all local database data (local only)",
		Long: `Drop every schema instancez manages (public, auth, storage and the schemas
your tables declare) and recreate an empty public schema. Schemas outside that
set are left alone.

Refuses any database host that is not localhost, a loopback address or a unix
socket. Asks for confirmation on a terminal; pass --yes in non-interactive
sessions. It does not migrate: run inz dev afterwards to recreate the schema.

With --embedded-pg it deletes ./pgdata (next to the config file) instead, and
refuses while inz dev --embedded-pg is running.`,
		Example: "  inz db reset\n  inz db reset --embedded-pg --yes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := config.LoadDotenv(".development.env"); err != nil {
				return err
			}
			o.getenv, o.tty, o.confirm, o.out = os.Getenv, stdioIsTTY, promptConfirm, cmd.OutOrStdout()
			return runDBReset(cmd.Context(), o)
		},
	}
	cmd.Flags().StringVar(&o.configPath, "config", "instancez.yaml", "path to instancez.yaml")
	cmd.Flags().BoolVar(&o.embedded, "embedded-pg", false, "delete the embedded Postgres data dir (./pgdata) instead")
	cmd.Flags().BoolVarP(&o.yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func runDBReset(ctx context.Context, o dbResetOpts) error {
	if err := requireLocalConfig(o.configPath); err != nil {
		return err
	}
	if o.embedded {
		return resetEmbedded(o)
	}

	src, err := resolveDBSource(o.getenv, "")
	if err != nil {
		return err
	}
	dsns, dropDSN := []string{src.superuserURL}, src.superuserURL
	if src.ownerURL != "" {
		dsns, dropDSN = []string{src.ownerURL, src.authURL}, src.ownerURL
	}
	var target string
	for i, dsn := range dsns {
		t, err := localDSNHosts(dsn)
		if err != nil {
			return fmt.Errorf("refusing to reset: %w", err)
		}
		if i == 0 {
			target = t
		}
	}
	schemas, err := resetSchemas(o.configPath)
	if err != nil {
		return err
	}
	if err := confirmReset(o, target); err != nil {
		return err
	}
	if err := dropSchemas(ctx, dropDSN, schemas); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			return fmt.Errorf("%w; is inz dev running? stop it and retry", err)
		}
		return err
	}
	_, _ = fmt.Fprintln(o.out, "Database reset. Run inz dev to recreate the schema (restart it if it is running).")
	return nil
}

func confirmReset(o dbResetOpts, target string) error {
	if o.yes {
		return nil
	}
	if !o.tty() {
		return errors.New("db reset needs confirmation; pass --yes in non-interactive sessions")
	}
	if !o.confirm(fmt.Sprintf("Drop all data in %s? [y/N] ", target)) {
		return errors.New("db reset aborted")
	}
	return nil
}

func resetEmbedded(o dbResetOpts) error {
	dir := filepath.Join(filepath.Dir(o.configPath), "pgdata")
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintln(o.out, "nothing to reset")
		return nil
	} else if err != nil {
		return err
	}
	if pid, running := pgdataRunning(dir); running {
		return fmt.Errorf("inz dev appears to be running (PID %d); stop it first", pid)
	}
	if err := confirmReset(o, dir); err != nil {
		return err
	}
	for _, p := range []string{dir, dir + ".runtime"} {
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("remove %s: %w", p, err)
		}
	}
	_, _ = fmt.Fprintln(o.out, "Database reset. Run inz dev --embedded-pg to recreate it.")
	return nil
}

// pgdataRunning reports whether the pid in postmaster.pid is a live process.
func pgdataRunning(dataDir string) (pid int, running bool) {
	b, err := os.ReadFile(filepath.Join(dataDir, "postmaster.pid"))
	if err != nil {
		return 0, false
	}
	first, _, _ := strings.Cut(string(b), "\n")
	pid, err = strconv.Atoi(strings.TrimSpace(first))
	if err != nil || pid <= 0 {
		return 0, false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return pid, false
	}
	err = p.Signal(syscall.Signal(0))
	return pid, err == nil || errors.Is(err, syscall.EPERM)
}

// localDSNHosts returns a password-free host:port/db label, or an error naming the first non-local host.
func localDSNHosts(dsn string) (string, error) {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return "", errors.New("invalid database URL")
	}
	hosts := []string{cfg.Host}
	for _, fb := range cfg.Fallbacks {
		hosts = append(hosts, fb.Host)
	}
	for _, h := range hosts {
		if !isLocalHost(h) {
			return "", fmt.Errorf("database host %q is not local", h)
		}
	}
	return fmt.Sprintf("%s:%d/%s", cfg.Host, cfg.Port, cfg.Database), nil
}

func isLocalHost(h string) bool {
	if h == "localhost" || strings.HasPrefix(h, "/") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// resetSchemas is the closed set reset may drop: the engine schemas plus the ones tables declare.
func resetSchemas(configPath string) ([]string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", configPath, err)
	}
	cfg, _, err := config.ParseBytesLocal(data, configPath)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", configPath, err)
	}
	set := map[string]bool{"public": true, "auth": true, "storage": true}
	for _, t := range cfg.Tables {
		if s := t.EffectiveSchema(); !strings.HasPrefix(s, "pg_") && s != "information_schema" {
			set[s] = true
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

func dropSchemas(ctx context.Context, dsn string, schemas []string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '5s'"); err != nil {
		return err
	}
	for _, s := range schemas {
		if _, err := tx.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{s}.Sanitize()+" CASCADE"); err != nil {
			return fmt.Errorf("drop schema %s: %w", s, err)
		}
	}
	if _, err := tx.Exec(ctx, "CREATE SCHEMA public"); err != nil {
		return fmt.Errorf("create schema public: %w", err)
	}
	return tx.Commit(ctx)
}

