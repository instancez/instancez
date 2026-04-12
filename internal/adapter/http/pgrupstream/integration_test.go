//go:build integration

// Package pgrupstream runs postgrest-go against a live Ultrabase server. This
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

	ultrahttp "github.com/saedx1/ultrabase/internal/adapter/http"
	"github.com/saedx1/ultrabase/internal/adapter/postgres"
	"github.com/saedx1/ultrabase/internal/domain"
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
				Fields: map[string]domain.Field{
					"username": {Type: "text", PrimaryKey: true, Required: true},
					"status":   {Type: "text"},
					"age":      {Type: "int"},
					"nickname": {Type: "text"},
					"tags":     {Type: "text[]"},
				},
			},
			"channels": {
				Fields: map[string]domain.Field{
					"id":            {Type: "bigserial", PrimaryKey: true},
					"slug":          {Type: "text"},
					"data":          {Type: "jsonb"},
					"active_during": {Type: "int4range"},
				},
			},
			"messages": {
				Fields: map[string]domain.Field{
					"id":      {Type: "bigserial", PrimaryKey: true},
					"message": {Type: "text"},
					"username": {Type: "text", Required: true,
						ForeignKey: &domain.ForeignKey{References: "users.username", OnDelete: "cascade"}},
					"channel_id": {Type: "bigint", Required: true,
						ForeignKey: &domain.ForeignKey{References: "channels.id", OnDelete: "cascade"}},
				},
			},
		},
	}
}

func TestMain(m *testing.M) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Println("pgrupstream: DATABASE_URL not set, skipping upstream integration tests")
		os.Exit(0)
	}

	os.Setenv("ULTRABASE_ADMIN_KEY", testAdminKey)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := postgres.New(ctx, dbURL, domain.PoolConfig{Max: 4, Min: 1})
	if err != nil {
		fmt.Printf("pgrupstream: connect: %v\n", err)
		os.Exit(1)
	}
	testDB = db
	defer db.Close()

	if err := db.ExecDDL(ctx, schemaSQL); err != nil {
		fmt.Printf("pgrupstream: schema: %v\n", err)
		os.Exit(1)
	}
	if err := db.ExecDDL(ctx, seedSQL); err != nil {
		fmt.Printf("pgrupstream: seed: %v\n", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := ultrahttp.NewServer(ultrahttp.ServerDeps{
		Config:  buildConfig(),
		DB:      db,
		Logger:  logger,
		DevMode: true,
	})

	testTS = httptest.NewServer(server.Handler())
	defer testTS.Close()

	testClient = NewClient(testTS.URL+"/rest/v1", "public", nil)
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
