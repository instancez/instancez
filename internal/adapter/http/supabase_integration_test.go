//go:build integration

package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	tc "github.com/testcontainers/testcontainers-go"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	ultrahttp "github.com/saedx1/ultrabase/internal/adapter/http"
	"github.com/saedx1/ultrabase/internal/adapter/postgres"
	"github.com/saedx1/ultrabase/internal/app"
	"github.com/saedx1/ultrabase/internal/cli"
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
		},
		Tables: map[string]domain.Table{
			"users": {
				Fields: []domain.Field{
					{Name: "display_name", Type: "text"},
				},
			},
			"todos": {
				AllowAnon: true,
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "title", Type: "text", Required: true},
					{Name: "done", Type: "boolean", Default: false},
					{Name: "priority", Type: "int", Default: 0},
					{Name: "user_id", ForeignKey: &domain.ForeignKey{References: "users.id", OnDelete: "cascade"}},
				},
			},
			"comments": {
				AllowAnon: true,
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "body", Type: "text", Required: true},
					{Name: "todo_id", ForeignKey: &domain.ForeignKey{References: "todos.id", OnDelete: "cascade"}},
					{Name: "user_id", ForeignKey: &domain.ForeignKey{References: "users.id", OnDelete: "cascade"}},
				},
			},
		},
		Storage: map[string]domain.Bucket{
			"avatars": {
				MaxSize: "5MB",
				Types:   []string{"image/*", "application/octet-stream", "text/plain"},
				Public:  true,
			},
			"documents": {
				MaxSize: "10MB",
				Public:  false,
			},
		},
		// rpc functions drive the supabase-js .rpc() compat checks in
		// run.mjs. Creation goes through the real Migrator so this path
		// also exercises generateRPCFunction → CREATE OR REPLACE FUNCTION.
		Functions: map[string]domain.Function{
			"add_two": {
				Language:   "sql",
				Volatility: "immutable",
				Security:   "invoker",
				Returns:    domain.FuncReturn{Type: "int"},
				Body:       "SELECT a + b",
				Args: []domain.FuncArg{
					{Name: "a", Type: "int", Required: true},
					{Name: "b", Type: "int", Required: true},
				},
			},
			"echo_text": {
				Language:   "sql",
				Volatility: "stable",
				Security:   "invoker",
				Returns:    domain.FuncReturn{Type: "text"},
				Body:       "SELECT msg",
				Args: []domain.FuncArg{
					{Name: "msg", Type: "text", Required: true},
				},
			},
			// Single jsonb-arg function so the harness can exercise
			// supabase-js's .rpc('name', body, { params: 'single' })
			// codepath end-to-end. The function just returns the input
			// jsonb, which is the easiest roundtrip to assert on.
			"echo_json": {
				Language:   "sql",
				Volatility: "stable",
				Security:   "invoker",
				Returns:    domain.FuncReturn{Type: "jsonb"},
				Body:       "SELECT payload",
				Args: []domain.FuncArg{
					{Name: "payload", Type: "jsonb", Required: true},
				},
			},
			"noop_void": {
				Language:   "sql",
				Volatility: "volatile",
				Security:   "invoker",
				Returns:    domain.FuncReturn{Type: "void"},
				Body:       "SELECT",
				Args:       []domain.FuncArg{},
			},
			"list_todos": {
				Language:       "sql",
				Volatility:     "stable",
				Security:       "invoker",
				Returns:        domain.FuncReturn{Type: "setof todos"},
				ReturnCategory: "setof",
				Body:           "SELECT * FROM todos",
				Args:           []domain.FuncArg{},
			},
			"double_it": {
				Language:   "sql",
				Volatility: "stable",
				Security:   "invoker",
				Returns:    domain.FuncReturn{Type: "int"},
				Body:       "SELECT n * 2",
				Args: []domain.FuncArg{
					{Name: "n", Type: "int", Required: true},
				},
			},
		},
	}
	// Ensure ReturnCategory is populated; normally set by the YAML
	// loader's applyDefaults, but this test constructs Config directly.
	for k, fn := range cfg.Functions {
		if fn.ReturnCategory == "" {
			if fn.Returns.Type == "void" {
				fn.ReturnCategory = "void"
			} else {
				fn.ReturnCategory = "scalar"
			}
		}
		cfg.Functions[k] = fn
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
	storageDir := filepath.Join(t.TempDir(), "storage")
	localStorage, err := cli.NewLocalStore(storageDir)
	if err != nil {
		t.Fatalf("local storage: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	capturedEmail := &captureEmailSender{}
	srv := ultrahttp.NewServer(ultrahttp.ServerDeps{
		Config:  cfg,
		DB:      db,
		Logger:  logger,
		DevMode: true,
		JWTKeys: keys,
		Email:   capturedEmail,
		Storage: localStorage,
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

	// Admin key enables the /auth/v1/admin/* routes (generate_link). The
	// server middleware reads this from the process env, so set it for the
	// harness subprocess *and* the in-process server goroutine.
	adminKey := "test-admin-key-" + fmt.Sprintf("%d", time.Now().UnixNano())
	t.Setenv("ULTRABASE_ADMIN_KEY", adminKey)

	cmd := exec.CommandContext(ctx, "node", "run.mjs")
	cmd.Dir = harnessDir
	cmd.Env = append(os.Environ(),
		"ULTRABASE_URL="+ts.URL,
		"ULTRABASE_ANON_KEY="+anonKey,
		"ULTRABASE_ADMIN_KEY="+adminKey,
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("supabase-js harness failed: %v", err)
	}

	// ---- 6. Email OTP flow (Go-driven) ----
	// supabase-js calls /otp → email delivers a 6-digit code → client
	// submits {type:'email', email, token} to /verify and receives a
	// session. We exercise the whole loop here because the Node harness
	// has no mailbox; the capturing EmailSender wired above lets us
	// extract the code directly from the sent message body.
	runEmailOTPFlow(t, ts.URL, capturedEmail)

	// ---- 7. Password reset flow (Go-driven) ----
	// Like the email OTP flow, we need the capturing EmailSender to
	// extract the recovery token from the email.
	runPasswordResetFlow(t, ts.URL, capturedEmail)
}

// captureEmailSender records every email the auth handler asks to send
// so tests can assert on content (e.g. extract a 6-digit OTP code).
type captureEmailSender struct {
	mu    sync.Mutex
	sent  []domain.EmailMessage
}

func (c *captureEmailSender) Send(_ context.Context, msg domain.EmailMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, msg)
	return nil
}

func (c *captureEmailSender) latestTo(addr string) (domain.EmailMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.sent) - 1; i >= 0; i-- {
		for _, to := range c.sent[i].To {
			if to == addr {
				return c.sent[i], true
			}
		}
	}
	return domain.EmailMessage{}, false
}

var otpCodeRE = regexp.MustCompile(`\b\d{6}\b`)

func runEmailOTPFlow(t *testing.T, baseURL string, emails *captureEmailSender) {
	t.Helper()
	email := fmt.Sprintf("otp_%d_%d@example.com", time.Now().UnixNano(), rand.Int63())
	body := bytes.NewBufferString(fmt.Sprintf(`{"email":%q}`, email))
	resp, err := http.Post(baseURL+"/auth/v1/otp", "application/json", body)
	if err != nil {
		t.Fatalf("email OTP: /otp request failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("email OTP: /otp status = %d, want 200", resp.StatusCode)
	}

	// The dispatch happens in a goroutine (handleOTP uses `go h.sendMagicLinkEmail`),
	// so poll briefly for the captured email.
	var msg domain.EmailMessage
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m, ok := emails.latestTo(email)
		if ok {
			msg = m
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if msg.Subject == "" {
		t.Fatalf("email OTP: no email captured for %s", email)
	}
	match := otpCodeRE.FindString(msg.Text)
	if match == "" {
		t.Fatalf("email OTP: no 6-digit code in body:\n%s", msg.Text)
	}

	// Now submit the code via /verify as {type:'email', email, token:<code>}.
	verifyBody := bytes.NewBufferString(fmt.Sprintf(`{"type":"email","email":%q,"token":%q}`, email, match))
	verifyResp, err := http.Post(baseURL+"/auth/v1/verify", "application/json", verifyBody)
	if err != nil {
		t.Fatalf("email OTP: /verify request failed: %v", err)
	}
	defer verifyResp.Body.Close()
	if verifyResp.StatusCode != 200 {
		buf, _ := io.ReadAll(verifyResp.Body)
		t.Fatalf("email OTP: /verify status = %d, body = %s", verifyResp.StatusCode, buf)
	}
	var session map[string]any
	if err := json.NewDecoder(verifyResp.Body).Decode(&session); err != nil {
		t.Fatalf("email OTP: decode session: %v", err)
	}
	if _, ok := session["access_token"].(string); !ok {
		t.Fatalf("email OTP: missing access_token in session: %v", session)
	}
	user, _ := session["user"].(map[string]any)
	if user == nil || user["email"] != email {
		t.Fatalf("email OTP: user.email mismatch: %v", session)
	}

	// Reuse must fail (single-use).
	reuseBody := bytes.NewBufferString(fmt.Sprintf(`{"type":"email","email":%q,"token":%q}`, email, match))
	reuseResp, err := http.Post(baseURL+"/auth/v1/verify", "application/json", reuseBody)
	if err != nil {
		t.Fatalf("email OTP: /verify reuse request failed: %v", err)
	}
	_ = reuseResp.Body.Close()
	if reuseResp.StatusCode == 200 {
		t.Fatalf("email OTP: reused code should not succeed")
	}
}

var tokenRE = regexp.MustCompile(`token=([a-f0-9]{64})`)

func runPasswordResetFlow(t *testing.T, baseURL string, emails *captureEmailSender) {
	t.Helper()
	// Step 1: sign up a user so there's an account to recover.
	email := fmt.Sprintf("reset_%d_%d@example.com", time.Now().UnixNano(), rand.Int63())
	signupBody := bytes.NewBufferString(fmt.Sprintf(`{"email":%q,"password":"oldpassword123"}`, email))
	resp, err := http.Post(baseURL+"/auth/v1/signup", "application/json", signupBody)
	if err != nil {
		t.Fatalf("password reset: signup failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("password reset: signup status = %d, want 200", resp.StatusCode)
	}

	// Step 2: request password reset.
	recoverBody := bytes.NewBufferString(fmt.Sprintf(`{"email":%q,"redirect_to":"http://app.local/reset"}`, email))
	resp, err = http.Post(baseURL+"/auth/v1/recover", "application/json", recoverBody)
	if err != nil {
		t.Fatalf("password reset: /recover failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("password reset: /recover status = %d, want 200", resp.StatusCode)
	}

	// Step 3: extract the recovery token from the captured email.
	var msg domain.EmailMessage
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m, ok := emails.latestTo(email)
		if ok && m.Subject != "" {
			msg = m
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if msg.Subject == "" {
		t.Fatalf("password reset: no email captured for %s", email)
	}
	match := tokenRE.FindStringSubmatch(msg.Text)
	if len(match) < 2 {
		t.Fatalf("password reset: no token found in email body:\n%s", msg.Text)
	}
	token := match[1]

	// Step 4: click the recovery link (GET /auth/v1/verify).
	verifyURL := fmt.Sprintf("%s/auth/v1/verify?token=%s&type=recovery&redirect_to=http://app.local/reset", baseURL, token)
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse // don't follow redirects
	}}
	verifyResp, err := client.Get(verifyURL)
	if err != nil {
		t.Fatalf("password reset: GET /verify failed: %v", err)
	}
	_ = verifyResp.Body.Close()
	if verifyResp.StatusCode != 303 {
		t.Fatalf("password reset: GET /verify status = %d, want 303 redirect", verifyResp.StatusCode)
	}
	loc := verifyResp.Header.Get("Location")
	if loc == "" {
		t.Fatal("password reset: redirect missing Location header")
	}
	if !regexp.MustCompile(`^http://app\.local/reset#`).MatchString(loc) {
		t.Fatalf("password reset: unexpected redirect: %s", loc)
	}
	if !regexp.MustCompile(`access_token=`).MatchString(loc) {
		t.Fatalf("password reset: redirect missing access_token: %s", loc)
	}
	if !regexp.MustCompile(`type=recovery`).MatchString(loc) {
		t.Fatalf("password reset: redirect missing type=recovery: %s", loc)
	}

	// Step 5: extract the access token from the fragment and use it to
	// update the password via PUT /auth/v1/user.
	fragment := loc[len("http://app.local/reset#"):]
	var accessToken string
	for _, part := range regexp.MustCompile(`&`).Split(fragment, -1) {
		kv := regexp.MustCompile(`=`).Split(part, 2)
		if len(kv) == 2 && kv[0] == "access_token" {
			accessToken = kv[1]
		}
	}
	if accessToken == "" {
		t.Fatalf("password reset: could not extract access_token from fragment: %s", fragment)
	}

	updateBody := bytes.NewBufferString(`{"password":"newpassword456"}`)
	req, _ := http.NewRequest("PUT", baseURL+"/auth/v1/user", updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	updateResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("password reset: PUT /user failed: %v", err)
	}
	_ = updateResp.Body.Close()
	if updateResp.StatusCode != 200 {
		t.Fatalf("password reset: PUT /user status = %d, want 200", updateResp.StatusCode)
	}

	// Step 6: verify the new password works.
	loginBody := bytes.NewBufferString(fmt.Sprintf(`{"email":%q,"password":"newpassword456"}`, email))
	loginResp, err := http.Post(baseURL+"/auth/v1/token?grant_type=password", "application/json", loginBody)
	if err != nil {
		t.Fatalf("password reset: login with new password failed: %v", err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode != 200 {
		buf, _ := io.ReadAll(loginResp.Body)
		t.Fatalf("password reset: login status = %d, body = %s", loginResp.StatusCode, buf)
	}

	// Step 7: verify the old password no longer works.
	oldBody := bytes.NewBufferString(fmt.Sprintf(`{"email":%q,"password":"oldpassword123"}`, email))
	oldResp, err := http.Post(baseURL+"/auth/v1/token?grant_type=password", "application/json", oldBody)
	if err != nil {
		t.Fatalf("password reset: login with old password failed: %v", err)
	}
	_ = oldResp.Body.Close()
	if oldResp.StatusCode == 200 {
		t.Fatal("password reset: old password should not work after reset")
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
