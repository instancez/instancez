//go:build integration

package http_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	instancezhttp "github.com/instancez/instancez/internal/adapter/http"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

func guardsConfig(guarded bool) *domain.Config {
	return &domain.Config{Version: 1, Tables: map[string]domain.Table{"notes": {
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "owner", Type: "text", Immutable: guarded},
			{Name: "body", Type: "text"},
			{Name: "updated_at", Type: "timestamptz", Default: "now()", AutoUpdatedAt: guarded},
		},
		Indexes: []domain.Index{{Columns: []string{"body"}, Method: "hash"}},
	}}}
}

func TestFieldGuards_REST(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := guardsConfig(true)
	mig := app.NewMigrator(owner)
	require.NoError(t, mig.Apply(ctx, cfg))
	require.NoError(t, mig.Apply(ctx, cfg), "re-apply is idempotent")

	keys := app.NewJWTKeyManager(owner)
	_, err := keys.Active(ctx)
	require.NoError(t, err)
	t.Setenv("INSTANCEZ_PUBLISHABLE_KEY", "inz_publishable_guards")
	t.Setenv("INSTANCEZ_SECRET_KEY", "inz_secret_guards")
	srv := instancezhttp.NewServer(instancezhttp.ServerDeps{
		Config: cfg, DB: req, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		DevMode: true, JWTKeys: keys,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	h := map[string]string{"apikey": "inz_secret_guards", "Prefer": "return=representation"}

	am, err := owner.Query(ctx, `SELECT am.amname FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid JOIN pg_am am ON am.oid = c.relam WHERE c.relname = 'idx_notes_body'`)
	require.NoError(t, err)
	require.Len(t, am, 1)
	assert.Equal(t, "hash", am[0]["amname"])

	code, _, raw := doJSON(t, "POST", ts.URL+"/rest/v1/notes", `{"owner":"alice","body":"a","updated_at":"2000-01-01T00:00:00Z"}`, h)
	require.Equal(t, 201, code, raw)

	code, rows := patchNotes(t, ts.URL, `{"body":"b"}`, h)
	require.Equal(t, 200, code)
	got, err := time.Parse(time.RFC3339, rows[0]["updated_at"].(string))
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), got, time.Minute, "updated_at bumped")

	code, rows = patchNotes(t, ts.URL, `{"owner":"alice","body":"c"}`, h)
	assert.Equal(t, 200, code, "unchanged immutable value is allowed")
	assert.Equal(t, "c", rows[0]["body"])

	code, _, raw = doJSON(t, "PATCH", ts.URL+"/rest/v1/notes?id=eq.1", `{"owner":"bob"}`, h)
	assert.Equal(t, 400, code, raw)
	assert.Contains(t, raw, "immutable")

	code, _, raw = doJSON(t, "PATCH", ts.URL+"/rest/v1/notes?id=eq.1", `{"owner":null}`, h)
	assert.Equal(t, 400, code, "value -> NULL is a change: "+raw)

	require.NoError(t, mig.Apply(ctx, guardsConfig(false)))
	tr, err := owner.Query(ctx, `SELECT count(*) AS n FROM pg_trigger WHERE tgname = 'inz_guard' AND NOT tgisinternal`)
	require.NoError(t, err)
	assert.EqualValues(t, 0, tr[0]["n"], "trigger dropped with the options")
	code, _, raw = doJSON(t, "PATCH", ts.URL+"/rest/v1/notes?id=eq.1", `{"owner":"bob"}`, h)
	assert.Equal(t, 200, code, raw)
}

func patchNotes(t *testing.T, base, body string, h map[string]string) (int, []map[string]any) {
	t.Helper()
	code, _, raw := doJSON(t, "PATCH", base+"/rest/v1/notes?id=eq.1", body, h)
	var rows []map[string]any
	if code == 200 {
		require.NoError(t, json.Unmarshal([]byte(raw), &rows))
		require.Len(t, rows, 1)
	}
	return code, rows
}
