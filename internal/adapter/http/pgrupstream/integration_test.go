//go:build integration

// Package pgrupstream runs postgrest-go against a live Instancez server. This
// file owns the harness (TestMain, schema, seed, buildConfig) and one baseline
// smoke test; behavioral coverage lives in conformance_test.go.
package pgrupstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	instancezhttp "github.com/instancez/instancez/internal/adapter/http"
	"github.com/instancez/instancez/internal/adapter/postgres"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/supabase-community/postgrest-go"
)

var (
	testClient *Client
	testTS     *httptest.Server
	testDB     *postgres.DB
)

const testAdminKey = "pgrupstream-admin-key"

const schemaSQL = `
DROP TABLE IF EXISTS messages CASCADE;
DROP TABLE IF EXISTS channels CASCADE;
DROP TABLE IF EXISTS users CASCADE;

CREATE TABLE users (
    username text PRIMARY KEY,
    status   text NOT NULL DEFAULT 'ONLINE',
    age      int,
    nickname text,
    tags     text[]
);

CREATE TABLE channels (
    id            bigserial PRIMARY KEY,
    slug          text,
    data          jsonb,
    active_during int4range
);

CREATE TABLE messages (
    id         bigserial PRIMARY KEY,
    message    text,
    username   text   NOT NULL REFERENCES users(username) ON UPDATE CASCADE ON DELETE CASCADE,
    channel_id bigint NOT NULL REFERENCES channels(id)   ON UPDATE CASCADE ON DELETE CASCADE
);

-- Stored functions used by TestConf_RPC. Declared here (instead of via
-- the instancez Migrator) so the conformance schema stays self-contained
-- and doesn't couple function DDL generation to the RPC test.
CREATE OR REPLACE FUNCTION public.add_numbers(a int, b int)
RETURNS int LANGUAGE sql IMMUTABLE AS $$ SELECT a + b $$;

CREATE OR REPLACE FUNCTION public.greet(name text DEFAULT 'world')
RETURNS text LANGUAGE sql STABLE AS $$ SELECT 'hello ' || name $$;

CREATE OR REPLACE FUNCTION public.users_by_status(target text)
RETURNS SETOF users LANGUAGE sql STABLE AS $$
  SELECT * FROM users WHERE status = target ORDER BY username
$$;

CREATE OR REPLACE FUNCTION public.touch_nothing()
RETURNS void LANGUAGE plpgsql VOLATILE AS $$ BEGIN RETURN; END $$;

-- Deliberately mis-declared: claims to be STABLE but attempts a write.
-- Used by TestConf_RPC to verify instancez pins stable/immutable RPC
-- transactions to read-only mode as a defense-in-depth guard.
CREATE OR REPLACE FUNCTION public.sneaky_insert()
RETURNS int LANGUAGE plpgsql STABLE AS $$
BEGIN
    INSERT INTO channels (slug) VALUES ('sneaky');
    RETURN 1;
END $$;

-- Returns the request.request_id GUC that the http middleware publishes
-- into every transaction. Used by TestConf_RequestID to verify the header
-- reaches SQL and can be observed from RLS/RPC bodies.
CREATE OR REPLACE FUNCTION public.current_request_id()
RETURNS text LANGUAGE sql STABLE AS $$
  SELECT current_setting('request.request_id', true)
$$;
`

const seedSQL = `
INSERT INTO users (username, status, age, nickname, tags) VALUES
  ('supabot',    'ONLINE',  1,    NULL,   '{bot}'),
  ('kiwicopple', 'OFFLINE', 30,   'kiwi', '{admin,founder}'),
  ('awailas',    'ONLINE',  28,   NULL,   '{contrib}'),
  ('acupofjose', 'OFFLINE', 27,   'jose', '{contrib,speaker}'),
  ('dragarcia',  'ONLINE',  25,   NULL,   '{admin}');

INSERT INTO channels (slug, active_during) VALUES
  ('public',  '[1,10)'),
  ('random',  '[20,30)');

INSERT INTO messages (message, username, channel_id) VALUES
  ('Hello World',    'supabot', 1),
  ('Second message', 'supabot', 2);
`

func buildConfig() *domain.Config {
	return &domain.Config{
		Version: 1,
		Project: domain.Project{Name: "pgrupstream"},
		Server: domain.Server{
			Port:        0,
			MaxBodySize: "10MB",
			MaxLimit:    1000,
		},
		Tables: map[string]domain.Table{
			"users": {
				Fields: []domain.Field{
					{Name: "username", Type: "text", PrimaryKey: true, Required: true},
					{Name: "status", Type: "text"},
					{Name: "age", Type: "int"},
					{Name: "nickname", Type: "text"},
					{Name: "tags", Type: "text[]"},
				},
			},
			"channels": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "slug", Type: "text"},
					{Name: "data", Type: "jsonb"},
					{Name: "active_during", Type: "int4range"},
				},
			},
			"messages": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "message", Type: "text"},
					{Name: "username", Type: "text", Required: true,
						ForeignKey: &domain.ForeignKey{References: "users.username", OnDelete: "cascade"}},
					{Name: "channel_id", Type: "bigint", Required: true,
						ForeignKey: &domain.ForeignKey{References: "channels.id", OnDelete: "cascade"}},
				},
			},
		},
		// Declared here so /rest/v1/rpc/:name routes are mounted. The
		// underlying Postgres functions are created by schemaSQL above
		// (intentionally bypassing the Migrator for this package).
		RPC: map[string]domain.Function{
			"add_numbers": {
				Language: "sql", Volatility: "immutable", Security: "invoker",
				Returns: domain.FuncReturn{Type: "int"}, ReturnCategory: "scalar",
				Args: []domain.FuncArg{
					{Name: "a", Type: "int", Required: true},
					{Name: "b", Type: "int", Required: true},
				},
				Body: "SELECT a + b",
			},
			"greet": {
				Language: "sql", Volatility: "stable", Security: "invoker",
				Returns: domain.FuncReturn{Type: "text"}, ReturnCategory: "scalar",
				Args: []domain.FuncArg{
					{Name: "name", Type: "text", Default: "world"},
				},
				Body: "SELECT 'hello ' || name",
			},
			"users_by_status": {
				Language: "sql", Volatility: "stable", Security: "invoker",
				Returns: domain.FuncReturn{Type: "setof users"}, ReturnCategory: "setof",
				Args: []domain.FuncArg{
					{Name: "target", Type: "text", Required: true},
				},
				Body: "SELECT * FROM users WHERE status = target",
			},
			"touch_nothing": {
				Language: "plpgsql", Volatility: "volatile", Security: "invoker",
				Returns: domain.FuncReturn{Type: "void"}, ReturnCategory: "void",
				Body: "BEGIN RETURN; END",
			},
			// Mis-declared stable function that attempts to insert. The
			// handler pins non-volatile RPC transactions to read-only,
			// so calling this via .rpc() must fail with 25006.
			"sneaky_insert": {
				Language: "plpgsql", Volatility: "stable", Security: "invoker",
				Returns: domain.FuncReturn{Type: "int"}, ReturnCategory: "scalar",
				Body: "BEGIN INSERT INTO channels (slug) VALUES ('sneaky'); RETURN 1; END",
			},
			"current_request_id": {
				Language: "sql", Volatility: "stable", Security: "invoker",
				Returns: domain.FuncReturn{Type: "text"}, ReturnCategory: "scalar",
				Body: "SELECT current_setting('request.request_id', true)",
			},
		},
	}
}

func TestMain(m *testing.M) {
	// Superuser URL — dbboot.Bootstrap creates instancez_owner +
	// authenticator from this connection before tests start.
	dbURL := os.Getenv("INSTANCEZ_TEST_DATABASE_URL")
	if dbURL == "" {
		fmt.Println("pgrupstream: INSTANCEZ_TEST_DATABASE_URL not set, skipping upstream integration tests")
		os.Exit(0)
	}

	os.Setenv("INSTANCEZ_SECRET_KEY", testAdminKey)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ownerDB, authDB, err := dbboot.Bootstrap(ctx, dbURL, domain.PoolConfig{Max: 4, Min: 1})
	if err != nil {
		fmt.Printf("pgrupstream: bootstrap roles: %v\n", err)
		os.Exit(1)
	}
	db := ownerDB.Database.(*postgres.DB)
	testDB = db
	defer ownerDB.Close()
	defer authDB.Close()

	if err := db.ExecDDL(ctx, schemaSQL); err != nil {
		fmt.Printf("pgrupstream: schema: %v\n", err)
		os.Exit(1)
	}
	if err := db.ExecDDL(ctx, seedSQL); err != nil {
		fmt.Printf("pgrupstream: seed: %v\n", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := instancezhttp.NewServer(instancezhttp.ServerDeps{
		Config:  buildConfig(),
		DB:      authDB,
		Logger:  logger,
		DevMode: true,
	})

	testTS = httptest.NewServer(server.Handler())
	defer testTS.Close()

	testClient = NewClient(testTS.URL+"/rest/v1", "public", map[string]string{"apikey": testAdminKey})
	testClient.SetAuthToken(testAdminKey)

	code := m.Run()
	os.Exit(code)
}

// TestIntegration_Smoke is a minimal baseline smoke test: if this fails,
// the harness is broken and nothing in conformance_test.go will run correctly.
func TestIntegration_Smoke(t *testing.T) {
	if testClient == nil {
		t.Skip("Skipping integration test: client not initialized")
	}

	ctx := context.Background()
	resp, err := testClient.From("users").Select("*", nil).Execute(ctx)
	require.NoError(t, err)
	require.Nil(t, resp.Error)

	var users []map[string]interface{}
	dataBytes, _ := json.Marshal(resp.Data)
	json.Unmarshal(dataBytes, &users)
	assert.Equal(t, 5, len(users), "expected 5 seed users")
	assert.Contains(t, users[0], "username")
}
