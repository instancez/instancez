package http

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/instancez/instancez/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const anonTestPK = "pk-anon-test"

func mountedStorage(t *testing.T, db domain.Database, buckets map[string]domain.Bucket) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("INSTANCEZ_PUBLISHABLE_KEY", anonTestPK)
	h := newStorageHandler(db, &stubObjectStore{}, buckets)
	h.jwtKeys = stubKeys(t)
	r := gin.New()
	h.Mount(r.Group(""))
	return r
}

func touchlessDB(t *testing.T) *stubDB {
	fail := func() { t.Helper(); t.Error("anon on a bucket without rls: must not reach the database") }
	return &stubDB{
		queryFn:    func(context.Context, string, ...any) ([]map[string]any, error) { fail(); return nil, nil },
		queryRowFn: func(context.Context, string, ...any) (map[string]any, error) { fail(); return nil, nil },
		execFn:     func(context.Context, string, ...any) (int64, error) { fail(); return 0, nil },
		beginFn:    func(context.Context) (domain.Tx, error) { fail(); return &stubTx{}, nil },
	}
}

type anonCase struct {
	method, path, body string
	status             int
	bodyHas            string
}

var noRLSAnonCases = []anonCase{
	{"POST", "/storage/v1/object/list/open", `{"prefix":""}`, 200, "[]"},
	{"POST", "/storage/v1/object/list/nope", `{"prefix":""}`, 200, "[]"},
	{"POST", "/storage/v1/object/list-v2/open", `{}`, 200, `"objects":[]`},
	{"GET", "/storage/v1/object/info/open/a.txt", "", 404, "not_found"},
	{"GET", "/storage/v1/object/info/authenticated/open/a.txt", "", 404, "not_found"},
	{"GET", "/storage/v1/object/authenticated/open/a.txt", "", 404, "not_found"},
	{"GET", "/storage/v1/object/open/a.txt", "", 404, "not_found"},
	{"HEAD", "/storage/v1/object/open/a.txt", "", 404, ""},
	{"POST", "/storage/v1/object/sign/open/a.txt", `{"expiresIn":60}`, 404, "not_found"},
	{"POST", "/storage/v1/object/sign/open", `{"expiresIn":60,"paths":["a.txt"]}`, 200, errNoObjectAccess},
	{"POST", "/storage/v1/object/open/a.txt", "x", 401, "Missing or invalid Authorization header"},
	{"PUT", "/storage/v1/object/open/a.txt", "x", 401, "Missing or invalid Authorization header"},
	{"POST", "/storage/v1/object/upload/sign/open/a.txt", "", 401, "Missing or invalid Authorization header"},
	{"DELETE", "/storage/v1/object/open", `{"prefixes":["a.txt"]}`, 200, "[]"},
	{"POST", "/storage/v1/object/move", `{"bucketId":"open","sourceKey":"a","destinationKey":"b"}`, 401, ""},
	{"POST", "/storage/v1/object/copy", `{"bucketId":"open","sourceKey":"a","destinationKey":"b"}`, 401, ""},
}

func serve(r *gin.Engine, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if method == "POST" && strings.Contains(path, "/object/open/") || method == "PUT" {
		req.Header.Set("Content-Type", "text/plain")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestStorageAnon_NoRLSBucketNeverTouchesDB(t *testing.T) {
	for _, bucketsCase := range map[string]map[string]domain.Bucket{
		"rls off table-wide": {"open": {}},
		"default_all shim":   {"open": {}, "gated": {RLS: []domain.RLSPolicy{{Operations: []string{"select"}, Using: "true"}}}},
	} {
		r := mountedStorage(t, touchlessDB(t), bucketsCase)
		for _, creds := range []map[string]string{
			{"apikey": anonTestPK, "Authorization": "Bearer " + anonTestPK},
			{"apikey": anonTestPK},
		} {
			for _, c := range noRLSAnonCases {
				w := serve(r, c.method, c.path, c.body, creds)
				require.Equal(t, c.status, w.Code, "%s %s: %s", c.method, c.path, w.Body.String())
				if c.bodyHas != "" {
					assert.Contains(t, w.Body.String(), c.bodyHas, c.path)
				}
			}
		}
	}
}

func TestStorageAnon_AnonRoleJWTIsGatedToo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("INSTANCEZ_PUBLISHABLE_KEY", anonTestPK)
	h := newStorageHandler(touchlessDB(t), &stubObjectStore{}, map[string]domain.Bucket{"open": {}})
	h.jwtKeys = stubKeys(t)
	r := gin.New()
	h.Mount(r.Group(""))
	tok := signToken(t, h.jwtKeys, jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001", "role": "anon", "exp": 4102444800})
	w := serve(r, "POST", "/storage/v1/object/list/open", `{}`, map[string]string{"apikey": anonTestPK, "Authorization": "Bearer " + tok})
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, "[]", strings.TrimSpace(w.Body.String()))
}

func TestStorageAnon_RLSBucketRunsAsAnon(t *testing.T) {
	var roles []string
	db := &stubDB{
		withRLSFn: func(ctx context.Context, s domain.Session) (context.Context, error) {
			roles = append(roles, s.Role)
			return ctx, nil
		},
		queryFn: func(context.Context, string, ...any) ([]map[string]any, error) {
			return []map[string]any{{"name": "a.txt"}}, nil
		},
		beginFn: func(context.Context) (domain.Tx, error) {
			return &stubTx{execFn: func(context.Context, string, ...any) (int64, error) {
				return 0, errors.New("new row violates row-level security policy for table \"objects\"")
			}}, nil
		},
	}
	r := mountedStorage(t, db, map[string]domain.Bucket{"gated": {RLS: []domain.RLSPolicy{{Operations: []string{"select"}, Using: "true"}}}})
	creds := map[string]string{"apikey": anonTestPK, "Authorization": "Bearer " + anonTestPK}

	w := serve(r, "POST", "/storage/v1/object/list/gated", `{"prefix":""}`, creds)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "a.txt", "anon sees what the select policy allows")

	w = serve(r, "POST", "/storage/v1/object/gated/b.txt", "x", map[string]string{"apikey": anonTestPK, "Authorization": "Bearer " + anonTestPK, "Content-Type": "text/plain"})
	require.Equal(t, 403, w.Code, "RLS, not route auth, denies the anon upload: %s", w.Body.String())
	require.NotEmpty(t, roles)
	for _, role := range roles {
		assert.Equal(t, "anon", role)
	}
}
