//go:build integration

package pgrupstream

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	instancezhttp "github.com/instancez/instancez/internal/adapter/http"
	"github.com/instancez/instancez/internal/domain"
)

// The old migrator recorded a second primary_key flag without changing the live
// key, so the config claims (a, b) while Postgres has (a).
func TestConf_UpsertUsesLivePrimaryKeyWhenConfigDrifted(t *testing.T) {
	if testClient == nil {
		t.Skip("no client")
	}
	ctx := context.Background()
	require.NoError(t, testDB.ExecDDL(ctx, `DROP TABLE IF EXISTS drifted_pairs;
		CREATE TABLE drifted_pairs (a int PRIMARY KEY, b int NOT NULL, note text);`))
	t.Cleanup(func() { _ = testDB.ExecDDL(context.Background(), `DROP TABLE IF EXISTS drifted_pairs`) })

	cfg := buildConfig()
	cfg.Tables = map[string]domain.Table{"drifted_pairs": {Fields: []domain.Field{
		{Name: "a", Type: "int", PrimaryKey: true},
		{Name: "b", Type: "int", PrimaryKey: true},
		{Name: "note", Type: "text"},
	}}}
	cfg.RPC = nil
	ts := httptest.NewServer(instancezhttp.NewServer(instancezhttp.ServerDeps{
		Config: cfg, DB: testAuthDB, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), DevMode: true,
	}).Handler())
	defer ts.Close()

	send := func(method, body string) (int, string) {
		req, err := http.NewRequest(method, ts.URL+"/rest/v1/drifted_pairs", strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("apikey", testAdminKey)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Prefer", "resolution=merge-duplicates,return=representation")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}

	status, body := send("POST", `{"a":1,"b":1,"note":"first"}`)
	require.Equal(t, 201, status, body)
	status, body = send("POST", `{"a":1,"b":2,"note":"merged"}`)
	require.Equal(t, 201, status, "default upsert must target the live key, not 42P10: %s", body)
	require.Contains(t, body, `"note":"merged"`)
	status, body = send("PUT", `{"a":1,"b":3,"note":"put"}`)
	require.Equal(t, 200, status, "PUT must target the live key: %s", body)
	require.Contains(t, body, `"note":"put"`)

	row, err := testDB.QueryRow(ctx, `SELECT count(*)::int AS n, max(b) AS b FROM drifted_pairs`)
	require.NoError(t, err)
	require.EqualValues(t, 1, row["n"])
	require.EqualValues(t, 3, row["b"])
}

func TestConf_KeyOnlyUpsertReturnsRow(t *testing.T) {
	if testClient == nil {
		t.Skip("no client")
	}
	ctx := context.Background()
	require.NoError(t, testDB.ExecDDL(ctx, `DROP TABLE IF EXISTS key_only;
		CREATE TABLE key_only (a int, b int, PRIMARY KEY (a, b));`))
	t.Cleanup(func() { _ = testDB.ExecDDL(context.Background(), `DROP TABLE IF EXISTS key_only`) })

	cfg := buildConfig()
	cfg.Tables = map[string]domain.Table{"key_only": {Fields: []domain.Field{
		{Name: "a", Type: "int", PrimaryKey: true},
		{Name: "b", Type: "int", PrimaryKey: true},
	}}}
	cfg.RPC = nil
	ts := httptest.NewServer(instancezhttp.NewServer(instancezhttp.ServerDeps{
		Config: cfg, DB: testAuthDB, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), DevMode: true,
	}).Handler())
	defer ts.Close()

	send := func(method, prefer, body string) (int, string) {
		req, err := http.NewRequest(method, ts.URL+"/rest/v1/key_only", strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("apikey", testAdminKey)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Prefer", prefer)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	merge := "resolution=merge-duplicates,return=representation"
	for i := 0; i < 2; i++ {
		status, body := send("POST", merge, `{"a":1,"b":2}`)
		require.Equal(t, 201, status, body)
		require.JSONEq(t, `[{"a":1,"b":2}]`, body, "existing row must still be returned")
	}
	status, body := send("POST", merge, `[{"a":1,"b":2},{"a":3,"b":4}]`)
	require.Equal(t, 201, status, body)
	require.JSONEq(t, `[{"a":1,"b":2},{"a":3,"b":4}]`, body)
	status, body = send("POST", "resolution=ignore-duplicates,return=representation", `{"a":1,"b":2}`)
	require.Equal(t, 201, status, body)
	require.JSONEq(t, `[]`, body)
}
