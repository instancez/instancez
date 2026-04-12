//go:build integration

package http_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	tc "github.com/testcontainers/testcontainers-go"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	ultrahttp "github.com/saedx1/ultrabase/internal/adapter/http"
	"github.com/saedx1/ultrabase/internal/adapter/postgres"
	"github.com/saedx1/ultrabase/internal/app"
	"github.com/saedx1/ultrabase/internal/domain"
)

// TestSupabaseJSCompat boots a real Postgres in a container, runs migrations,
// starts the ultrabase HTTP handler via httptest, then shells out to a Node
// harness that drives @supabase/supabase-js against the URL. The harness exits
// non-zero on any assertion failure and streams its output to the test log.
//
// Prerequisites: docker daemon running, node + npm on PATH. Skipped otherwise.
// Enabled with: go test -tags=integration ./internal/adapter/http/...
func TestSupabaseJSCompat(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed; skipping supabase-js integration test")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not installed; skipping supabase-js integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// ---- 1. Spin up Postgres ----
	container, err := pgcontainer.Run(ctx,
		"postgres:16-alpine",
		pgcontainer.WithDatabase("ultrabase_test"),
		pgcontainer.WithUsername("ultrabase"),
		pgcontainer.WithPassword("ultrabase"),
		tc.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	dbURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	// ---- 2. Connect and migrate ----
	db, err := postgres.New(ctx, dbURL, domain.PoolConfig{Max: 4, Min: 1})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	verifyEmail := false
	cfg := &domain.Config{
		Version: 1,
		Project: domain.Project{Name: "integration"},
		Server:  domain.Server{Port: 0},
		Auth: &domain.Auth{
			JWTExpiry:     "1h",
			RefreshTokens: true,
			Email:         &domain.AuthEmail{VerifyEmail: verifyEmail},
			Fields: map[string]domain.Field{
				"display_name": {Type: "text"},
			},
		},
		Tables: map[string]domain.Table{
			"todos": {
				AllowAnon: true,
				Fields: map[string]domain.Field{
					"id":      {Type: "bigserial", PrimaryKey: true},
					"title":   {Type: "text", Required: true},
					"done":    {Type: "boolean", Default: false},
					"user_id": {ForeignKey: &domain.ForeignKey{References: "users.id", OnDelete: "cascade"}},
				},
			},
		},
	}

	if err := app.NewMigrator(db).Apply(ctx, cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// ---- 3. JWT keys + anon key ----
	keys := app.NewJWTKeyManager(db)
	active, err := keys.Active(ctx)
	if err != nil {
		t.Fatalf("active jwt key: %v", err)
	}
	anonKey, err := signAnonKey(active)
	if err != nil {
		t.Fatalf("sign anon key: %v", err)
	}

	// ---- 4. Start HTTP server ----
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	srv := ultrahttp.NewServer(ultrahttp.ServerDeps{
		Config:  cfg,
		DB:      db,
		Logger:  logger,
		DevMode: true,
		JWTKeys: keys,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// ---- 5. Run Node harness ----
	harnessDir, err := filepath.Abs("../../../test/integration/supabase-js")
	if err != nil {
		t.Fatalf("harness path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(harnessDir, "node_modules")); os.IsNotExist(err) {
		t.Logf("installing supabase-js harness deps in %s", harnessDir)
		install := exec.CommandContext(ctx, "npm", "install", "--no-audit", "--no-fund", "--loglevel=error")
		install.Dir = harnessDir
		install.Stdout = os.Stderr
		install.Stderr = os.Stderr
		if err := install.Run(); err != nil {
			t.Fatalf("npm install: %v", err)
		}
	}

	cmd := exec.CommandContext(ctx, "node", "run.mjs")
	cmd.Dir = harnessDir
	cmd.Env = append(os.Environ(),
		"ULTRABASE_URL="+ts.URL,
		"ULTRABASE_ANON_KEY="+anonKey,
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("supabase-js harness failed: %v", err)
	}
}

// signAnonKey mints a long-lived JWT with role=anon using the active key.
// supabase-js will send this as the `apikey` header (and Bearer on anonymous
// requests) the same way it does against Supabase.
func signAnonKey(key *app.JWTKey) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":  "ultrabase",
		"role": "anon",
		"iat":  now.Unix(),
		"exp":  now.Add(24 * time.Hour).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["kid"] = key.KID
	signed, err := tok.SignedString(key.Secret)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	return signed, nil
}
