//go:build integration

package http_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

func TestAuthUIDDefault_REST(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	public := false
	cfg := &domain.Config{
		Version: 1,
		Auth:    &domain.Auth{JWTExpiry: "1h", Email: &domain.AuthEmail{}},
		Tables: map[string]domain.Table{
			"notes": {RLSEnabled: &public, Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "body", Type: "text"},
				{Name: "created_by", Type: "uuid", Default: "auth.uid()"},
			}},
			"strict": {RLSEnabled: &public, Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "label", Type: "text"},
				{Name: "owner", Required: true, Default: "auth.uid()", ForeignKey: &domain.ForeignKey{References: "auth.users.id"}},
			}},
		},
	}
	require.Empty(t, config.Validate(cfg))
	v0 := &domain.Config{Version: 1, Auth: cfg.Auth, Tables: map[string]domain.Table{
		"notes": {RLSEnabled: &public, Fields: cfg.Tables["notes"].Fields[:2]},
	}}
	mig := app.NewMigrator(owner)
	require.NoError(t, mig.Apply(context.Background(), v0))
	require.NoError(t, mig.Apply(context.Background(), cfg), "adding a column with an auth.uid() default to an existing table")
	ts := bootServer(t, cfg, owner, req, "authdefault")

	pub := map[string]string{"apikey": "inz_publishable_authdefault", "Prefer": "return=representation"}
	svc := map[string]string{"apikey": "inz_secret_authdefault", "Prefer": "return=representation"}
	code, body, raw := doJSON(t, "POST", ts.URL+"/auth/v1/signup", `{"email":"d@example.com","password":"password123"}`, pub)
	require.Equal(t, 200, code, raw)
	uid := body["user"].(map[string]any)["id"].(string)
	user := map[string]string{"apikey": "inz_publishable_authdefault", "Authorization": "Bearer " + body["access_token"].(string), "Prefer": "return=representation"}

	createdBy := func(h map[string]string, table, payload string) any {
		code, _, raw := doJSON(t, "POST", ts.URL+"/rest/v1/"+table, payload, h)
		require.Equal(t, 201, code, raw)
		rows, err := owner.Query(context.Background(), `SELECT created_by FROM notes ORDER BY id DESC LIMIT 1`)
		require.NoError(t, err)
		return rows[0]["created_by"]
	}

	assert.Equal(t, uid, createdBy(user, "notes", `{"body":"a"}`), "authenticated fills the caller's id")
	assert.Nil(t, createdBy(svc, "notes", `{"body":"b"}`), "service_role has no uid")
	assert.Nil(t, createdBy(pub, "notes", `{"body":"c"}`), "anon has no uid")
	other := "00000000-0000-0000-0000-000000000001"
	assert.Equal(t, other, createdBy(svc, "notes", `{"body":"d","created_by":"`+other+`"}`), "an explicit value beats the default")

	code, _, raw = doJSON(t, "POST", ts.URL+"/rest/v1/strict", `{"label":"x"}`, user)
	assert.Equal(t, 201, code, "authenticated + NOT NULL default: "+raw)
	code, _, raw = doJSON(t, "POST", ts.URL+"/rest/v1/strict", `{"label":"x"}`, svc)
	assert.GreaterOrEqual(t, code, 400, "service_role has no uid, NOT NULL must fail: "+raw)
	assert.Contains(t, raw, "null value")
}
