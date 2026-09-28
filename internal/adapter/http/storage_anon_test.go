package http

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	{"HEAD", "/storage/v1/object/authenticated/open/a.txt", "", 404, ""},
	{"HEAD", "/storage/v1/object/info/open/a.txt", "", 404, ""},
	{"GET", "/storage/v1/render/image/authenticated/open/a.png", "", 404, "not_found"},
	{"HEAD", "/storage/v1/render/image/authenticated/open/a.png", "", 404, ""},
	{"POST", "/storage/v1/object/sign/open/a.txt", `{"expiresIn":60}`, 404, "not_found"},
	{"POST", "/storage/v1/object/sign/open", `{"expiresIn":60,"paths":["a.txt"]}`, 200, errNoObjectAccess},
	{"POST", "/storage/v1/object/open/a.txt", "x", 403, "Not authorized to write this object"},
	{"PUT", "/storage/v1/object/open/a.txt", "x", 403, "Not authorized to write this object"},
	{"POST", "/storage/v1/object/upload/sign/open/a.txt", "", 403, "Not authorized to write this object"},
	{"DELETE", "/storage/v1/object/open", `{"prefixes":["a.txt"]}`, 200, "[]"},
	{"POST", "/storage/v1/object/move", `{"bucketId":"open","sourceKey":"a","destinationKey":"b"}`, 404, "not_found"},
	{"POST", "/storage/v1/object/copy", `{"bucketId":"open","sourceKey":"a","destinationKey":"b"}`, 404, "not_found"},
	{"POST", "/storage/v1/object/move", `{"bucketId":"gated","destinationBucket":"open","sourceKey":"a","destinationKey":"b"}`, 404, "not_found"},
	{"POST", "/storage/v1/object/copy", `{"bucketId":"gated","destinationBucket":"open","sourceKey":"a","destinationKey":"b"}`, 404, "not_found"},
	{"POST", "/storage/v1/object/move", `{"bucketId":"open","destinationBucket":"gated","sourceKey":"a","destinationKey":"b"}`, 404, "not_found"},
	{"POST", "/storage/v1/object/copy", `{"bucketId":"open","destinationBucket":"gated","sourceKey":"a","destinationKey":"b"}`, 404, "not_found"},
	{"GET", "/storage/v1/object/open/a%00b", "", 400, "invalid_key"},
	{"GET", "/storage/v1/object/authenticated/open/a%00b", "", 400, "invalid_key"},
	{"GET", "/storage/v1/object/info/open/a%00b", "", 400, "invalid_key"},
	{"GET", "/storage/v1/render/image/authenticated/open/a%00b", "", 400, "invalid_key"},
	{"HEAD", "/storage/v1/object/open/a%00b", "", 400, ""},
	{"DELETE", "/storage/v1/object/open", `not json`, 400, "bad_request"},
	{"POST", "/storage/v1/object/move", `not json`, 400, "bad_request"},
	{"POST", "/storage/v1/object/copy", `{"bucketId":"open","sourceKey":"../x","destinationKey":"b"}`, 400, "invalid_key"},
	{"POST", "/storage/v1/object/sign/open", `not json`, 400, "bad_request"},
	{"POST", "/storage/v1/object/open/a%00b", "x", 400, "invalid_key"},
	{"POST", "/storage/v1/object/sign/open/a%00b", `{}`, 400, "invalid_key"},
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

func TestStorageAnon_RLSUploadStoresNullOwner(t *testing.T) {
	var owner []any
	db := &stubDB{beginFn: func(context.Context) (domain.Tx, error) {
		return &stubTx{execFn: func(_ context.Context, _ string, args ...any) (int64, error) {
			owner = append(owner, args[5])
			return 1, nil
		}}, nil
	}}
	r := mountedStorage(t, db, map[string]domain.Bucket{"drop": {RLS: []domain.RLSPolicy{{Operations: []string{"insert"}, WithCheck: "true"}}}})
	w := serve(r, "POST", "/storage/v1/object/drop/a.txt", "x", map[string]string{"apikey": anonTestPK, "Authorization": "Bearer " + anonTestPK, "Content-Type": "text/plain"})
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Len(t, owner, 2, "probe and real write")
	for _, o := range owner {
		assert.Nil(t, o, "anon upload binds uploaded_by as NULL")
	}
}

func TestStorageAnon_RLSMoveCopyRunAsAnon(t *testing.T) {
	var roles []string
	db := &stubDB{
		withRLSFn: func(ctx context.Context, s domain.Session) (context.Context, error) {
			roles = append(roles, s.Role)
			return ctx, nil
		},
		beginFn: func(context.Context) (domain.Tx, error) {
			return &stubTx{execFn: func(context.Context, string, ...any) (int64, error) { return 1, nil }}, nil
		},
	}
	r := mountedStorage(t, db, map[string]domain.Bucket{"gated": {RLS: []domain.RLSPolicy{{Operations: []string{"select", "update", "insert"}, Using: "true", WithCheck: "true"}}}})
	creds := map[string]string{"apikey": anonTestPK, "Authorization": "Bearer " + anonTestPK}
	for _, op := range []string{"move", "copy"} {
		w := serve(r, "POST", "/storage/v1/object/"+op, `{"bucketId":"gated","sourceKey":"a","destinationKey":"b"}`, creds)
		require.Equal(t, 200, w.Code, "%s: %s", op, w.Body.String())
	}
	require.NotEmpty(t, roles)
	for _, role := range roles {
		assert.Equal(t, "anon", role)
	}
}

func TestStorageAnon_LegacyAnonJWTGated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("INSTANCEZ_PUBLISHABLE_KEY", anonTestPK)
	store := &stubObjectStore{}
	h := &StorageHandler{cfg: &domain.Config{Storage: map[string]domain.Bucket{"open": {}}}, db: touchlessDB(t), storage: store, jwtKeys: stubKeys(t)}
	r := gin.New()
	h.Mount(r.Group("/api"))
	tok := signToken(t, h.jwtKeys, jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001", "role": "anon", "exp": 4102444800})
	auth := map[string]string{"Authorization": "Bearer " + tok}
	for _, c := range []anonCase{
		{"POST", "/api/storage/open/sign", `{"content_type":"text/plain"}`, 403, "42501"},
		{"POST", "/api/storage/open/sign", `not json`, 400, ""},
		{"GET", "/api/storage/open/a.txt", "", 404, "Object not found"},
		{"DELETE", "/api/storage/open/a.txt", "", 404, "Object not found"},
	} {
		w := serve(r, c.method, c.path, c.body, auth)
		require.Equal(t, c.status, w.Code, "%s %s: %s", c.method, c.path, w.Body.String())
		assert.Contains(t, w.Body.String(), c.bodyHas, c.path)
	}
}

func TestLegacySignUpload_RunsInsertUnderCallerRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	type roleKey struct{}
	for _, tc := range []struct {
		name         string
		insertPolicy bool
		status       int
	}{
		{"no insert policy", false, 403},
		{"insert policy", true, 200},
	} {
		var roles []string
		db := &stubDB{
			withRLSFn: func(ctx context.Context, s domain.Session) (context.Context, error) {
				return context.WithValue(ctx, roleKey{}, s.Role), nil
			},
			execFn: func(ctx context.Context, q string, _ ...any) (int64, error) {
				role, _ := ctx.Value(roleKey{}).(string)
				roles = append(roles, role)
				if role != domain.JWTRoleService && !tc.insertPolicy {
					return 0, errors.New(`new row violates row-level security policy for table "objects" (SQLSTATE 42501)`)
				}
				return 1, nil
			},
		}
		store := &stubObjectStore{signUploadFn: func(context.Context, string, string, time.Duration) (string, error) {
			return "https://s3.example/put", nil
		}}
		h := &StorageHandler{cfg: &domain.Config{Storage: map[string]domain.Bucket{"docs": {RLS: []domain.RLSPolicy{{Operations: []string{"select"}, Using: "true"}}}}},
			db: db, storage: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
		r := gin.New()
		r.POST("/sign", asAuthenticated, h.handleSignUpload("docs", h.cfg.Storage["docs"]))
		w := serve(r, "POST", "/sign", `{"content_type":"text/plain"}`, nil)
		require.Equal(t, tc.status, w.Code, "%s: %s", tc.name, w.Body.String())
		assert.Equal(t, tc.status == 200, strings.Contains(w.Body.String(), "upload_url"), tc.name)
		assert.Equal(t, []string{domain.JWTRoleAuthenticated}, roles, "%s: insert must run as the caller", tc.name)
	}
}
