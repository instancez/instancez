//go:build integration

package funcs_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	tc "github.com/testcontainers/testcontainers-go"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/saedx1/instancez/internal/adapter/funcs"
	instancezhttp "github.com/saedx1/instancez/internal/adapter/http"
	"github.com/saedx1/instancez/internal/app"
	"github.com/saedx1/instancez/internal/cli"
	"github.com/saedx1/instancez/internal/domain"
	"github.com/saedx1/instancez/internal/testutil/dbboot"
)

// TestInjectedClientsRLSAndEscalation proves the two injected data-access
// clients behave correctly against ultrabase's own loopback REST API:
//
//   - ctx.supabase carries the CALLER's JWT, so RLS applies as the caller: an
//     authenticated user (U1) can insert/read only its own rows, and a row
//     owned by a different user (U2) is HIDDEN from it.
//   - ctx.serviceClient carries an ultra-minted service_role JWT (BYPASSRLS),
//     so the SAME U2 row IS visible through it — proving explicit escalation.
//
// The function returns both result sets; the Go side asserts the RLS boundary.
//
// Requires: docker daemon, node + npm on PATH, and network access to vendor
// @supabase/supabase-js. Skipped (not failed) when any is unavailable.
func TestInjectedClientsRLSAndEscalation(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// ---- 1. Postgres ----
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
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	dbURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	// ---- 2. Roles + pools ----
	ownerDB, authDB, err := dbboot.Bootstrap(ctx, dbURL, domain.PoolConfig{Max: 4, Min: 1})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	t.Cleanup(func() { ownerDB.Close(); authDB.Close() })
	db := ownerDB.Database

	// rls_secrets: rows are visible/writable only when owner_id = auth.uid().
	cfg := &domain.Config{
		Version: 1,
		Project: domain.Project{Name: "funcs-rls"},
		Server:  domain.Server{Port: 0},
		Auth: &domain.Auth{
			JWTExpiry:     "1h",
			RefreshTokens: false,
			Email:         &domain.AuthEmail{VerifyEmail: false},
		},
		Tables: map[string]domain.Table{
			"rls_secrets": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "owner_id", Type: "uuid", Required: true},
					{Name: "secret", Type: "text", Required: true},
				},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"select", "insert", "update", "delete"}, Check: "owner_id = auth.uid()"},
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
	anonKey, err := mintAnonKey(active)
	if err != nil {
		t.Fatalf("anon key: %v", err)
	}

	// ---- 4. Two real auth.users so RLS FK/uid checks have valid subjects ----
	u1 := "11111111-1111-1111-1111-111111111111"
	u2 := "22222222-2222-2222-2222-222222222222"
	for _, id := range []string{u1, u2} {
		if _, err := db.Exec(ctx,
			`INSERT INTO auth.users (id, email) VALUES ($1::uuid, $2)`,
			id, id[:8]+"@example.com"); err != nil {
			t.Fatalf("seed auth.users %s: %v", id, err)
		}
	}
	// Pre-seed a row owned by U2 (the caller U1 must NOT see this via RLS).
	if _, err := db.Exec(ctx,
		`INSERT INTO rls_secrets (owner_id, secret) VALUES ($1::uuid, $2)`,
		u2, "u2-only-secret"); err != nil {
		t.Fatalf("seed u2 row: %v", err)
	}

	// Caller token: authenticated user U1.
	callerTok, err := mintUserToken(active, u1)
	if err != nil {
		t.Fatalf("caller token: %v", err)
	}

	// ---- 5. Vendor supabase-js + write the function ----
	fnDir := t.TempDir()
	install := exec.CommandContext(ctx, "npm", "install", "--prefix", fnDir,
		"--no-audit", "--no-fund", "--loglevel=error", "@supabase/supabase-js")
	install.Stdout, install.Stderr = os.Stderr, os.Stderr
	if err := install.Run(); err != nil {
		t.Skipf("npm install @supabase/supabase-js failed (no network?): %v", err)
	}

	// The function:
	//   - inserts a U1-owned row via ctx.supabase (RLS must allow it),
	//   - reads back ALL rows via ctx.supabase (RLS hides U2's row → caller sees
	//     only its own),
	//   - reads ALL rows via ctx.serviceClient (BYPASSRLS → sees both U1 + U2).
	fnSrc := `export default async (req, ctx) => {
  const ins = await ctx.supabase
    .from("rls_secrets")
    .insert({ owner_id: "` + u1 + `", secret: "u1-secret" })
    .select();
  const asCaller = await ctx.supabase
    .from("rls_secrets")
    .select("owner_id,secret")
    .order("id");
  const asService = await ctx.serviceClient
    .from("rls_secrets")
    .select("owner_id,secret")
    .order("id");
  return {
    status: 200,
    body: {
      insertError: ins.error ? ins.error.message : null,
      callerOwners: (asCaller.data || []).map(r => r.owner_id),
      callerError: asCaller.error ? asCaller.error.message : null,
      serviceOwners: (asService.data || []).map(r => r.owner_id),
      serviceError: asService.error ? asService.error.message : null,
    },
  };
};
`
	if err := os.WriteFile(filepath.Join(fnDir, "rls.js"), []byte(fnSrc), 0o644); err != nil {
		t.Fatalf("write fn: %v", err)
	}

	// ---- 6. Listener first, so LoopbackURL exists before the server boots ----
	// (httptest can't give us a URL before NewServer needs the runtime.)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	loopback := "http://" + ln.Addr().String()

	rt, err := funcs.New(funcs.Options{
		Dir:         fnDir,
		Functions:   map[string]domain.CodeFunction{"rls": {Runtime: "node", File: "rls.js"}},
		LoopbackURL: loopback,
		MintAnon:    func(context.Context) (string, error) { return anonKey, nil },
		MintService: func(ctx context.Context) (string, error) {
			return app.MintServiceToken(ctx, keys, 30*time.Second)
		},
	})
	if err != nil {
		t.Fatalf("funcs.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	storageDir := filepath.Join(t.TempDir(), "storage")
	localStorage, err := cli.NewLocalStore(storageDir, "")
	if err != nil {
		t.Fatalf("local storage: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	srv := instancezhttp.NewServer(instancezhttp.ServerDeps{
		Config:          cfg,
		DB:              authDB,
		Logger:          logger,
		DevMode:         true,
		JWTKeys:         keys,
		Storage:         localStorage,
		FunctionRuntime: rt,
	})
	httpSrv := &http.Server{Handler: srv.Handler()}
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() { _ = httpSrv.Close() })

	// ---- 7. Invoke the function as U1 ----
	req, err := http.NewRequestWithContext(ctx, "POST", loopback+"/functions/v1/rls", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+callerTok)
	req.Header.Set("apikey", anonKey)

	// Retry briefly while the goroutine server warms up.
	var resp *http.Response
	for i := 0; i < 50; i++ {
		resp, err = http.DefaultClient.Do(req.Clone(ctx))
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("invoke fn: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var b [4096]byte
		n, _ := resp.Body.Read(b[:])
		t.Fatalf("invoke status=%d body=%s", resp.StatusCode, b[:n])
	}

	var out struct {
		InsertError   *string  `json:"insertError"`
		CallerOwners  []string `json:"callerOwners"`
		CallerError   *string  `json:"callerError"`
		ServiceOwners []string `json:"serviceOwners"`
		ServiceError  *string  `json:"serviceError"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	t.Logf("function result: %+v", out)

	if out.InsertError != nil {
		t.Fatalf("ctx.supabase insert (as U1) was rejected: %s", *out.InsertError)
	}
	if out.CallerError != nil {
		t.Fatalf("ctx.supabase select error: %s", *out.CallerError)
	}
	if out.ServiceError != nil {
		t.Fatalf("ctx.serviceClient select error: %s", *out.ServiceError)
	}

	// RLS-as-caller: U1 sees ONLY its own row, never U2's.
	if len(out.CallerOwners) != 1 || out.CallerOwners[0] != u1 {
		t.Fatalf("RLS-as-caller failed: ctx.supabase saw owners %v, want exactly [%s] (U2 row must be hidden)",
			out.CallerOwners, u1)
	}

	// Escalation: serviceClient (BYPASSRLS) sees BOTH the U1 and U2 rows.
	if len(out.ServiceOwners) != 2 {
		t.Fatalf("escalation failed: ctx.serviceClient saw owners %v, want both U1+U2 (2 rows)", out.ServiceOwners)
	}
	sawU1, sawU2 := false, false
	for _, o := range out.ServiceOwners {
		if o == u1 {
			sawU1 = true
		}
		if o == u2 {
			sawU2 = true
		}
	}
	if !sawU1 || !sawU2 {
		t.Fatalf("escalation failed: ctx.serviceClient owners %v, want both %s and %s", out.ServiceOwners, u1, u2)
	}
}

// mintAnonKey signs a long-lived role=anon JWT (the apikey).
func mintAnonKey(key *app.JWTKey) (string, error) {
	now := time.Now()
	return signClaims(key, jwt.MapClaims{
		"iss":  "ultrabase",
		"role": "anon",
		"iat":  now.Unix(),
		"exp":  now.Add(24 * time.Hour).Unix(),
	})
}

// mintUserToken signs an authenticated user JWT for the given subject.
func mintUserToken(key *app.JWTKey, sub string) (string, error) {
	now := time.Now()
	return signClaims(key, jwt.MapClaims{
		"iss":  "ultrabase",
		"sub":  sub,
		"aud":  "authenticated",
		"role": "authenticated",
		"iat":  now.Unix(),
		"exp":  now.Add(1 * time.Hour).Unix(),
	})
}

func signClaims(key *app.JWTKey, claims jwt.MapClaims) (string, error) {
	var method jwt.SigningMethod
	var signingKey any
	switch key.Algorithm {
	case "RS256":
		method, signingKey = jwt.SigningMethodRS256, key.PrivateKey
	default:
		method, signingKey = jwt.SigningMethodHS256, key.Secret
	}
	tok := jwt.NewWithClaims(method, claims)
	tok.Header["kid"] = key.KID
	return tok.SignedString(signingKey)
}
