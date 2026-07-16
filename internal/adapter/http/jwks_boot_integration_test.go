//go:build integration

package http_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	tc "github.com/testcontainers/testcontainers-go"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	instancezhttp "github.com/instancez/instancez/internal/adapter/http"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

// TestJWKSSeededAtBoot is the regression guard for the seed-after-migrate
// ordering. It boots a real Postgres, then runs the actual engine.Start on a
// fresh database and asserts that /auth/v1/.well-known/jwks.json serves a key
// with NO prior login. This is the exact scenario that was broken when the seed
// ran before migrations: auth.jwt_keys did not exist yet, Active() failed, and
// JWKS stayed empty until the first token was issued.
//
// It has to drive the real engine.Start (not a hand-rolled migrate+seed) or it
// would not catch the seed being moved back before the migrate step.
//
// Enabled with: go test -tags=integration ./internal/adapter/http/...
func TestJWKSSeededAtBoot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	container, err := pgcontainer.Run(ctx,
		dbboot.PostgresImage(),
		pgcontainer.WithDatabase("instancez_test"),
		pgcontainer.WithUsername("instancez"),
		pgcontainer.WithPassword("instancez"),
		tc.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	dbURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	ownerDB, authDB, err := dbboot.Bootstrap(ctx, dbURL, domain.PoolConfig{Max: 4, Min: 1})
	if err != nil {
		t.Fatalf("bootstrap roles: %v", err)
	}
	t.Cleanup(func() {
		ownerDB.Close()
		authDB.Close()
	})

	cfg := &domain.Config{
		Version: 1,
		Project: domain.Project{Name: "jwks-boot"},
		Server:  domain.Server{Port: 0},
		Auth:    &domain.Auth{JWTExpiry: "1h"},
	}

	publishableKey := "inz_publishable_bootseed"
	secretKey := "inz_secret_bootseed"
	t.Setenv("INSTANCEZ_PUBLISHABLE_KEY", publishableKey)
	t.Setenv("INSTANCEZ_SECRET_KEY", secretKey)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// The server's key manager reads the same owner DB the engine seeds into, so
	// it is deliberately left UNSEEDED here — only engine.Start below may create
	// the key. handleJWKS reads the DB per request, so it sees the row the moment
	// the engine writes it.
	km := app.NewJWTKeyManager(ownerDB.Database)
	srv := instancezhttp.NewServer(instancezhttp.ServerDeps{
		Config:  cfg,
		DB:      authDB,
		OwnerDB: ownerDB,
		Logger:  logger,
		DevMode: true,
		JWTKeys: km,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Sanity: before the engine runs, the store is empty (fresh app, no login).
	if n := jwksKeyCount(t, ts.URL, publishableKey); n != 0 {
		t.Fatalf("precondition: want 0 keys before engine.Start, got %d", n)
	}

	// Drive the real engine.Start: migrate (creates auth.jwt_keys) then seed.
	// No HTTP server or watcher on the engine itself — it only owns the
	// migrate+seed here; assertions go through the httptest server above.
	engineCtx, stopEngine := context.WithCancel(ctx)
	defer stopEngine()
	engine := app.NewEngine(cfg, ownerDB, authDB, domain.DefaultRoles(),
		app.WithMode(app.ModeProd),
		app.WithMigrate(true),
		app.WithLogger(logger),
	)
	startErr := make(chan error, 1)
	go func() { startErr <- engine.Start(engineCtx) }()

	// engine.Start runs migrate+seed then blocks in waitForShutdown, so poll the
	// endpoint until the key lands instead of guessing at a fixed sleep.
	deadline := time.Now().Add(30 * time.Second)
	var keys int
	for time.Now().Before(deadline) {
		if keys = jwksKeyCount(t, ts.URL, publishableKey); keys > 0 {
			break
		}
		select {
		case err := <-startErr:
			t.Fatalf("engine.Start returned before JWKS was seeded: %v", err)
		case <-time.After(250 * time.Millisecond):
		}
	}
	if keys == 0 {
		t.Fatal("JWKS still empty after engine.Start: seed did not run after migrate")
	}

	stopEngine()
	select {
	case <-startErr:
	case <-time.After(15 * time.Second):
		t.Fatal("engine.Start did not return after context cancel")
	}
}

// jwksKeyCount GETs the JWKS endpoint and returns the number of published keys.
// A missing table (before migrations) surfaces as a non-200; that is treated as
// zero keys so the poll keeps waiting rather than failing.
func jwksKeyCount(t *testing.T, baseURL, apiKey string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", baseURL+"/auth/v1/.well-known/jwks.json", nil)
	req.Header.Set("apikey", apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET jwks.json: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return 0
	}
	var out struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode jwks (%s): %v", body, err)
	}
	return len(out.Keys)
}
