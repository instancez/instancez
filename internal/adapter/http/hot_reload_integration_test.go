//go:build integration

package http_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	instancezhttp "github.com/instancez/instancez/internal/adapter/http"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

func reloadConfig(withPriority, withLabels bool) *domain.Config {
	fields := []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}, {Name: "title", Type: "text"}}
	if withPriority {
		fields = append(fields, domain.Field{Name: "priority", Type: "integer", Default: "0"})
	}
	cfg := &domain.Config{Version: 1, Tables: map[string]domain.Table{"todos": {Fields: fields}}}
	if withLabels {
		cfg.Tables["labels"] = domain.Table{Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}, {Name: "name", Type: "text"}}}
	}
	return cfg
}

// A config reload (file watch or dashboard save) must reach the REST layer:
// new columns and new tables work without restarting the server.
func TestREST_FollowsConfigReload(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg1, cfg2 := reloadConfig(false, false), reloadConfig(true, true)
	mig := app.NewMigrator(owner)
	require.NoError(t, mig.Apply(ctx, cfg1))

	keys := app.NewJWTKeyManager(owner)
	_, err := keys.Active(ctx)
	require.NoError(t, err)
	t.Setenv("INSTANCEZ_PUBLISHABLE_KEY", "inz_publishable_reload")
	t.Setenv("INSTANCEZ_SECRET_KEY", "inz_secret_reload")

	var live atomic.Pointer[domain.Config]
	live.Store(cfg1)
	srv := instancezhttp.NewServer(instancezhttp.ServerDeps{
		Config: cfg1, ConfigFn: func() *domain.Config { return live.Load() }, DB: req,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), DevMode: true, JWTKeys: keys,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	h := map[string]string{"apikey": "inz_secret_reload", "Prefer": "return=representation"}

	code, _, raw := doJSON(t, "GET", ts.URL+"/rest/v1/todos?select=title,priority", "", h)
	assert.Equal(t, 400, code, "priority does not exist yet: "+raw)
	code, _, _ = doJSON(t, "GET", ts.URL+"/rest/v1/labels", "", h)
	assert.Equal(t, 404, code, "labels does not exist yet")

	require.NoError(t, mig.Apply(ctx, cfg2))
	live.Store(cfg2)

	code, _, raw = doJSON(t, "POST", ts.URL+"/rest/v1/todos", `{"title":"a","priority":2}`, h)
	require.Equal(t, 201, code, raw)
	code, _, raw = doJSON(t, "GET", ts.URL+"/rest/v1/todos?select=title,priority", "", h)
	assert.Equal(t, 200, code, raw)
	assert.Contains(t, raw, `"priority":2`)
	code, _, raw = doJSON(t, "POST", ts.URL+"/rest/v1/labels", `{"name":"urgent"}`, h)
	assert.Equal(t, 201, code, "a table added by the reload is routable: "+raw)
}
