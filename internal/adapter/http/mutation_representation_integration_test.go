//go:build integration

package http_test

import (
	"context"
	"io"
	"log/slog"
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

func TestMutationRepresentation_EmptyAndSingular(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	ctx := context.Background()
	on := true
	cfg := &domain.Config{Version: 1, Tables: map[string]domain.Table{
		"items": {Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "tag", Type: "text"},
		}},
		"hidden": {RLSEnabled: &on, Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "tag", Type: "text"},
		}},
	}}
	require.NoError(t, app.NewMigrator(owner).Apply(ctx, cfg))
	keys := app.NewJWTKeyManager(owner)
	_, err := keys.Active(ctx)
	require.NoError(t, err)
	t.Setenv("INSTANCEZ_PUBLISHABLE_KEY", "inz_publishable_mr")
	t.Setenv("INSTANCEZ_SECRET_KEY", "inz_secret_mr")
	srv := instancezhttp.NewServer(instancezhttp.ServerDeps{
		Config: cfg, DB: req, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		DevMode: true, JWTKeys: keys,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	rep := map[string]string{"apikey": "inz_secret_mr", "Prefer": "return=representation"}
	single := map[string]string{"apikey": "inz_secret_mr", "Prefer": "return=representation", "Accept": "application/vnd.pgrst.object+json"}
	singleMin := map[string]string{"apikey": "inz_secret_mr", "Prefer": "return=minimal", "Accept": "application/vnd.pgrst.object+json"}
	minimal := map[string]string{"apikey": "inz_secret_mr", "Prefer": "return=minimal"}
	anon := map[string]string{"apikey": "inz_publishable_mr", "Prefer": "return=representation"}
	u := ts.URL + "/rest/v1/"

	code, _, raw := doJSON(t, "POST", u+"items", `[{"tag":"a"},{"tag":"a"},{"tag":"b"}]`, rep)
	require.Equal(t, 201, code, raw)
	code, _, raw = doJSON(t, "POST", u+"hidden", `{"tag":"x"}`, rep)
	require.Equal(t, 201, code, raw)

	for _, m := range []string{"PATCH", "DELETE"} {
		body := ""
		if m == "PATCH" {
			body = `{"tag":"z"}`
		}
		t.Run(m+" zero rows is []", func(t *testing.T) {
			code, _, raw := doJSON(t, m, u+"items?tag=eq.nope", body, rep)
			assert.Equal(t, 200, code)
			assert.JSONEq(t, `[]`, raw)
		})
		t.Run(m+" RLS-hidden rows is []", func(t *testing.T) {
			code, _, raw := doJSON(t, m, u+"hidden?tag=eq.x", body, anon)
			assert.Equal(t, 200, code)
			assert.JSONEq(t, `[]`, raw)
		})
		t.Run(m+" zero rows singular is 406 PGRST116", func(t *testing.T) {
			code, _, raw := doJSON(t, m, u+"items?tag=eq.nope", body, single)
			assert.Equal(t, 406, code)
			assert.Contains(t, raw, "PGRST116")
		})
		t.Run(m+" many rows singular is 406 and rolls back", func(t *testing.T) {
			code, _, raw := doJSON(t, m, u+"items?tag=eq.a", body, single)
			assert.Equal(t, 406, code, raw)
			code, _, raw = doJSON(t, "GET", u+"items?tag=eq.a", "", rep)
			assert.Equal(t, 200, code)
			assert.Contains(t, raw, `"a"`)
			assert.NotContains(t, raw, `"z"`)
		})
		t.Run(m+" many rows singular return=minimal is 406 and rolls back", func(t *testing.T) {
			code, _, raw := doJSON(t, m, u+"items?tag=eq.a", body, singleMin)
			assert.Equal(t, 406, code, raw)
			assert.Contains(t, raw, "PGRST116")
			assertTagCount(t, u, "a", 2)
			assertTagCount(t, u, "z", 0)
		})
		t.Run(m+" zero rows singular return=minimal is 406", func(t *testing.T) {
			code, _, _ := doJSON(t, m, u+"items?tag=eq.nope", body, singleMin)
			assert.Equal(t, 406, code)
		})
	}

	t.Run("Accept with media-type params still enforces singular", func(t *testing.T) {
		h := map[string]string{"apikey": "inz_secret_mr", "Prefer": "return=representation", "Accept": "application/vnd.pgrst.object+json; nulls=stripped"}
		code, _, raw := doJSON(t, "PATCH", u+"items?tag=eq.a", `{"tag":"z"}`, h)
		assert.Equal(t, 406, code, raw)
		assertTagCount(t, u, "z", 0)
	})
	t.Run("Accept list with json first is not singular", func(t *testing.T) {
		h := map[string]string{"apikey": "inz_secret_mr", "Prefer": "return=representation", "Accept": "application/json, application/vnd.pgrst.object+json"}
		code, _, raw := doJSON(t, "PATCH", u+"items?tag=eq.nope", `{"tag":"z"}`, h)
		assert.Equal(t, 200, code, raw)
		assert.JSONEq(t, `[]`, raw)
	})
	t.Run("Prefer tx=commit does not bypass the rollback", func(t *testing.T) {
		h := map[string]string{"apikey": "inz_secret_mr", "Prefer": "return=representation, tx=commit", "Accept": "application/vnd.pgrst.object+json"}
		code, _, raw := doJSON(t, "PATCH", u+"items?tag=eq.a", `{"tag":"z"}`, h)
		assert.Equal(t, 406, code, raw)
		assertTagCount(t, u, "z", 0)
	})
	t.Run("minimal without singular is unchanged", func(t *testing.T) {
		code, _, _ := doJSON(t, "PATCH", u+"items?tag=eq.nope", `{"tag":"z"}`, minimal)
		assert.Equal(t, 204, code)
	})
	t.Run("one row singular return=minimal is 204", func(t *testing.T) {
		code, _, raw := doJSON(t, "PATCH", u+"items?tag=eq.b", `{"tag":"b"}`, singleMin)
		assert.Equal(t, 204, code, raw)
	})

	for _, m := range []string{"POST", "PUT"} {
		for name, h := range map[string]map[string]string{"representation": single, "minimal": singleMin} {
			t.Run(m+" many rows singular "+name+" is 406 and rolls back", func(t *testing.T) {
				body := `[{"id":901,"tag":"m"},{"id":902,"tag":"m"}]`
				code, _, raw := doJSON(t, m, u+"items", body, h)
				assert.Equal(t, 406, code, raw)
				assert.Contains(t, raw, "PGRST116")
				assertTagCount(t, u, "m", 0)
			})
		}
		t.Run(m+" one row singular returns an object", func(t *testing.T) {
			code, _, raw := doJSON(t, m, u+"items", `{"id":950,"tag":"solo"}`, single)
			assert.Equal(t, map[bool]int{true: 201, false: 200}[m == "POST"], code, raw)
			assert.NotContains(t, raw, "[")
			assertTagCount(t, u, "solo", 1)
		})
	}

	code, _, raw = doJSON(t, "PATCH", u+"items?tag=eq.b", `{"tag":"b2"}`, single)
	assert.Equal(t, 200, code, raw)
	assert.Contains(t, raw, `"b2"`)
	assert.NotContains(t, raw, "[")

	code, _, raw = doJSON(t, "PATCH", u+"items?tag=eq.a", `{"tag":"a2"}`, rep)
	assert.Equal(t, 200, code)
	assert.Contains(t, raw, `"a2"`)

	code, _, raw = doJSON(t, "DELETE", u+"items?tag=eq.a2", "", rep)
	assert.Equal(t, 200, code)
	assert.Equal(t, 2, strings.Count(raw, `"id"`))
}

func assertTagCount(t *testing.T, base, tag string, want int) {
	t.Helper()
	_, _, raw := doJSON(t, "GET", base+"items?tag=eq."+tag, "", map[string]string{"apikey": "inz_secret_mr"})
	assert.Equal(t, want, strings.Count(raw, `"id"`), "rows tagged %q", tag)
}
