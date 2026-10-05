//go:build integration

package http_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	instancezhttp "github.com/instancez/instancez/internal/adapter/http"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

func rpcRoleServer(t *testing.T) *httptest.Server {
	t.Helper()
	owner, req := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := &domain.Config{
		Version: 1,
		Auth:    &domain.Auth{JWTExpiry: "1h", Email: &domain.AuthEmail{}},
		RPC: map[string]domain.Function{
			"whoami": {
				Language: "sql", Volatility: "stable", Security: "invoker",
				Returns: domain.FuncReturn{Type: "text"}, ReturnCategory: "scalar",
				Body: "SELECT auth.role() || '|' || current_user || '|' || coalesce(auth.uid()::text, 'null')",
			},
			"needs_two": {
				Language: "sql", Volatility: "stable", Security: "invoker",
				Returns: domain.FuncReturn{Type: "int"}, ReturnCategory: "scalar",
				Body: "SELECT a + b",
				Args: []domain.FuncArg{{Name: "a", Type: "int", Required: true}, {Name: "b", Type: "int", Required: true}},
			},
		},
	}
	require.NoError(t, app.NewMigrator(owner).Apply(ctx, cfg))
	keys := app.NewJWTKeyManager(owner)
	_, err := keys.Active(ctx)
	require.NoError(t, err)
	t.Setenv("INSTANCEZ_PUBLISHABLE_KEY", "inz_publishable_rolecheck")
	t.Setenv("INSTANCEZ_SECRET_KEY", "inz_secret_rolecheck")
	srv := instancezhttp.NewServer(instancezhttp.ServerDeps{
		Config: cfg, DB: req, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		DevMode: true, JWTKeys: keys,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func doJSON(t *testing.T, method, url, body string, h map[string]string) (int, map[string]any, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m, string(raw)
}

func TestRPC_RunsAsCallerRole(t *testing.T) {
	ts := rpcRoleServer(t)
	pub := map[string]string{"apikey": "inz_publishable_rolecheck"}

	code, body, raw := doJSON(t, "POST", ts.URL+"/auth/v1/signup", `{"email":"r@example.com","password":"password123"}`, pub)
	require.Equal(t, 200, code, raw)
	token := body["access_token"].(string)
	require.NotEmpty(t, token)
	uid := body["user"].(map[string]any)["id"].(string)

	call := func(h map[string]string) string {
		code, _, raw := doJSON(t, "POST", ts.URL+"/rest/v1/rpc/whoami", `{}`, h)
		require.Equal(t, 200, code, raw)
		var s string
		require.NoError(t, json.Unmarshal([]byte(raw), &s))
		return s
	}

	assert.Equal(t, "service_role|service_role|null", call(map[string]string{"apikey": "inz_secret_rolecheck"}), "secret key")
	assert.Equal(t, "anon|anon|null", call(pub), "publishable key")
	assert.Equal(t, "authenticated|authenticated|"+uid, call(map[string]string{"apikey": "inz_publishable_rolecheck", "Authorization": "Bearer " + token}), "user jwt")
}

func TestRPC_MissingArgHint(t *testing.T) {
	ts := rpcRoleServer(t)
	code, body, raw := doJSON(t, "POST", ts.URL+"/rest/v1/rpc/needs_two", `{"a":1}`, map[string]string{"apikey": "inz_publishable_rolecheck"})
	require.Equal(t, 404, code, raw)
	assert.Equal(t, "PGRST202", body["code"])
	assert.Equal(t, "argument b has no default", body["hint"])
}
