//go:build integration

package app_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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

// TestServeConsumesBundleEndToEnd proves the serve consumption path: a built
// .tar.gz bundle is FetchAndExtract'd, a funcs.Runtime is created pointing at
// the EXTRACTED tree, the real instancez HTTP handler is booted with that
// runtime, and invoking /functions/v1/<name> returns 200 with the expected
// body. This exercises extract → run → serve without a deploy/S3 round-trip
// (the bundle is built inline and fetched from a local path).
//
// Requires: docker daemon + node on PATH. Skipped (not failed) when node is
// unavailable.
func TestServeConsumesBundleEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// ---- 1. Postgres + pools (for JWT keys / auth) ----
	container, err := pgcontainer.Run(ctx,
		"postgres:16-alpine",
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
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	dbURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	ownerDB, authDB, err := dbboot.Bootstrap(ctx, dbURL, domain.PoolConfig{Max: 4, Min: 1})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	t.Cleanup(func() { ownerDB.Close(); authDB.Close() })
	db := ownerDB.Database

	cfg := &domain.Config{
		Version: 1,
		Project: domain.Project{Name: "funcs-bundle"},
		Server:  domain.Server{Port: 0},
		Auth: &domain.Auth{
			JWTExpiry:     "1h",
			RefreshTokens: false,
			Email:         &domain.AuthEmail{VerifyEmail: false},
		},
		Functions: map[string]domain.CodeFunction{
			"greet": {Runtime: "node", File: "functions/greet.js"},
		},
	}
	if err := app.NewMigrator(db).Apply(ctx, cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	km := app.NewJWTKeyManager(db)
	if _, err := km.Active(ctx); err != nil {
		t.Fatalf("active jwt key: %v", err)
	}

	// ---- 2. Build a bundle INLINE (no npm/deps) and write it to a local path.
	// The function needs no node_modules, so the bundle is just the source +
	// manifest, mirroring the tar layout cli.BuildBundle produces.
	bundle := buildInlineBundle(t, map[string]string{
		"manifest.json":      `{"functions":{"greet":{"file":"functions/greet.js","runtime":"node"}}}`,
		"functions/greet.js": `export default async (req) => ({ status: 200, body: { hello: req.body.who || "world" } });`,
	})
	bundlePath := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := os.WriteFile(bundlePath, bundle, 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	// ---- 3. FetchAndExtract the bundle (the serve consumption path). ----
	extractParent := filepath.Join(t.TempDir(), "extracted")
	dir, version, err := app.FetchAndExtract(ctx, bundlePath+"#v1", extractParent)
	if err != nil {
		t.Fatalf("FetchAndExtract: %v", err)
	}
	if version != "v1" {
		t.Fatalf("version = %q, want v1", version)
	}
	// Sanity: the function source landed where funcs.New will look for it
	// (Dir + CodeFunction.File).
	if _, err := os.Stat(filepath.Join(dir, "functions", "greet.js")); err != nil {
		t.Fatalf("extracted function missing: %v", err)
	}

	// ---- 4. Runtime from the EXTRACTED bundle. ----
	rt, err := funcs.New(funcs.Options{
		Dir:         dir,
		Functions:   cfg.Functions,
		LoopbackURL: "http://127.0.0.1:0",
		MintAnon:    func(c context.Context) (string, error) { return mustAnon(t, c, km), nil },
		MintService: func(ctx context.Context) (string, error) {
			return app.MintServiceToken(ctx, km, 5*time.Minute)
		},
	})
	if err != nil {
		t.Fatalf("funcs.New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	// ---- 5. Boot the real instancez HTTP handler with the runtime. ----
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
		JWTKeys:         km,
		Storage:         localStorage,
		FunctionRuntime: rt,
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	loopback := "http://" + ln.Addr().String()
	httpSrv := &http.Server{Handler: srv.Handler()}
	go func() { _ = httpSrv.Serve(ln) }()
	t.Cleanup(func() { _ = httpSrv.Close() })

	// ---- 6. Invoke /functions/v1/greet → 200 with expected body. ----
	body := bytes.NewReader([]byte(`{"who":"ada"}`))
	req, err := http.NewRequestWithContext(ctx, "POST", loopback+"/functions/v1/greet", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	var resp *http.Response
	for i := 0; i < 50; i++ {
		resp, err = http.DefaultClient.Do(req.Clone(ctx))
		if err == nil {
			break
		}
		req.Body, _ = req.GetBody()
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
		Hello string `json:"hello"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Hello != "ada" {
		t.Fatalf("body hello = %q, want ada", out.Hello)
	}
}

// buildInlineBundle builds a .tar.gz with the given path->content entries plus
// the directory entries their parents imply. Regular files only (no symlinks),
// which is all this test needs.
func buildInlineBundle(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(content)),
			ModTime:  time.Now(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar body %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func mustAnon(t *testing.T, ctx context.Context, km *app.JWTKeyManager) string {
	t.Helper()
	tok, err := app.MintAnonToken(ctx, km, time.Hour)
	if err != nil {
		t.Fatalf("mint anon: %v", err)
	}
	return tok
}
