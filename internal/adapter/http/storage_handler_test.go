package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newLegacyStorageHandler(db domain.Database, store domain.ObjectStore) *StorageHandler {
	return &StorageHandler{
		cfg:     &domain.Config{},
		db:      db,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		storage: store,
	}
}

func TestHandleSignUpload_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotKey, gotContentType string
	store := &stubObjectStore{
		signUploadFn: func(ctx context.Context, key, contentType string, size int64, expiry time.Duration) (string, error) {
			gotKey, gotContentType = key, contentType
			return "https://example.com/upload?sig=abc", nil
		},
	}
	db := &stubDB{execFn: func(ctx context.Context, q string, args ...any) (int64, error) { return 1, nil }}
	h := newLegacyStorageHandler(db, store)
	bucket := domain.Bucket{}

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/avatars/sign", func(c *gin.Context) {
		setTestSession(c, domain.Session{Role: "authenticated", UserID: "u1", IsAuthenticated: true})
		h.handleSignUpload("avatars", bucket)(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/storage/avatars/sign", strings.NewReader(`{"content_type":"image/png"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotContentType != "image/png" {
		t.Errorf("SignUpload contentType = %q", gotContentType)
	}
	if !strings.HasPrefix(gotKey, "avatars/") {
		t.Errorf("SignUpload key = %q, want avatars/ prefix", gotKey)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["upload_url"] != "https://example.com/upload?sig=abc" {
		t.Errorf("upload_url = %v", resp["upload_url"])
	}
}

func TestHandleSignUpload_MissingContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newLegacyStorageHandler(&stubDB{}, &stubObjectStore{})

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/avatars/sign", h.handleSignUpload("avatars", domain.Bucket{}))

	req := httptest.NewRequest(http.MethodPost, "/storage/avatars/sign", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleSignUpload_MimeTypeRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newLegacyStorageHandler(&stubDB{}, &stubObjectStore{})
	bucket := domain.Bucket{Types: []string{"image/*"}}

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/avatars/sign", h.handleSignUpload("avatars", bucket))

	req := httptest.NewRequest(http.MethodPost, "/storage/avatars/sign", strings.NewReader(`{"content_type":"text/plain"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 422 {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleSignUpload_SizeExceedsMax(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newLegacyStorageHandler(&stubDB{}, &stubObjectStore{})
	bucket := domain.Bucket{MaxSize: "1KB"}

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/avatars/sign", h.handleSignUpload("avatars", bucket))

	req := httptest.NewRequest(http.MethodPost, "/storage/avatars/sign", strings.NewReader(`{"content_type":"image/png","size":2048}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 422 {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleSignDownload_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
		return map[string]any{"id": "obj1"}, nil
	}}
	store := &stubObjectStore{
		signDownloadFn: func(ctx context.Context, key string, expiry time.Duration, _ domain.DownloadOptions) (string, error) {
			return "https://example.com/download?sig=xyz", nil
		},
	}
	h := newLegacyStorageHandler(db, store)

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/storage/avatars/:id", asAuthenticated, h.handleSignDownload("avatars", domain.Bucket{}))

	req := httptest.NewRequest(http.MethodGet, "/storage/avatars/photo.jpg", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["url"] != "https://example.com/download?sig=xyz" {
		t.Errorf("url = %v", resp["url"])
	}
}

func TestHandleSignDownload_ObjectNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
		return nil, nil
	}}
	h := newLegacyStorageHandler(db, &stubObjectStore{})

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/storage/avatars/:id", asAuthenticated, h.handleSignDownload("avatars", domain.Bucket{}))

	req := httptest.NewRequest(http.MethodGet, "/storage/avatars/missing.jpg", nil)
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleSignDownload_PassesSafeHeaderOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, public := range []bool{false, true} {
		var got domain.DownloadOptions
		db := &stubDB{queryRowFn: func(context.Context, string, ...any) (map[string]any, error) {
			return map[string]any{"mime": "text/html"}, nil
		}}
		store := &stubObjectStore{signDownloadFn: func(_ context.Context, _ string, _ time.Duration, o domain.DownloadOptions) (string, error) {
			got = o
			return "https://example.com/download", nil
		}}
		h := newLegacyStorageHandler(db, store)

		w := httptest.NewRecorder()
		r := gin.New()
		r.GET("/storage/avatars/:id", asAuthenticated, h.handleSignDownload("avatars", domain.Bucket{Public: public}))
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/storage/avatars/page.html", nil))

		require.Equal(t, 200, w.Code, w.Body.String())
		cache := "private, max-age=3600"
		if public {
			cache = "public, max-age=3600"
		}
		assert.Equal(t, domain.DownloadOptions{ContentType: "text/html", ContentDisposition: "attachment", CacheControl: cache}, got)
	}
}

func TestHandleDelete_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var deletedKey string
	store := &stubObjectStore{
		deleteFn: func(ctx context.Context, key string) error { deletedKey = key; return nil },
	}
	db := &stubDB{queryFn: func(context.Context, string, ...any) ([]map[string]any, error) {
		return []map[string]any{{"name": "photo.jpg"}}, nil
	}}
	h := newLegacyStorageHandler(db, store)

	w := httptest.NewRecorder()
	r := gin.New()
	r.DELETE("/storage/avatars/:id", asAuthenticated, h.handleDelete("avatars", domain.Bucket{}))

	req := httptest.NewRequest(http.MethodDelete, "/storage/avatars/photo.jpg", nil)
	r.ServeHTTP(w, req)

	if w.Code != 204 {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if deletedKey != "avatars/photo.jpg" {
		t.Errorf("Delete key = %q", deletedKey)
	}
}

func TestHandleDelete_StoreError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &stubObjectStore{
		deleteFn: func(ctx context.Context, key string) error { return errors.New("store unavailable") },
	}
	db := &stubDB{queryFn: func(context.Context, string, ...any) ([]map[string]any, error) {
		return []map[string]any{{"name": "photo.jpg"}}, nil
	}}
	h := newLegacyStorageHandler(db, store)

	w := httptest.NewRecorder()
	r := gin.New()
	r.DELETE("/storage/avatars/:id", asAuthenticated, h.handleDelete("avatars", domain.Bucket{}))

	req := httptest.NewRequest(http.MethodDelete, "/storage/avatars/photo.jpg", nil)
	r.ServeHTTP(w, req)

	if w.Code != 204 {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestLegacyMount_PublicReadBypassesRLSOtherRoutesNeedAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var queried []string
	h := newLegacyStorageHandler(roleTrackingDB(&queried), &stubObjectStore{})
	h.cfg.Storage = map[string]domain.Bucket{"pub": {Public: true}, "priv": {}}
	r := gin.New()
	h.Mount(r.Group(""))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/storage/pub/a.txt", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, []string{"service_role[a.txt pub]"}, queried, "a public download is a scoped lookup that bypasses RLS, like /object/public")

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/storage/priv/a.txt", nil))
	assert.Equal(t, 401, w.Code)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/storage/pub/a.txt", nil))
	assert.Equal(t, 401, w.Code)
}

func TestHandleSignDownload_UsesCallerSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var got domain.Session
	db := &stubDB{
		withRLSFn:  func(ctx context.Context, s domain.Session) (context.Context, error) { got = s; return ctx, nil },
		queryRowFn: func(context.Context, string, ...any) (map[string]any, error) { return nil, nil },
	}
	h := newLegacyStorageHandler(db, &stubObjectStore{})
	r := gin.New()
	r.GET("/storage/avatars/:id", func(c *gin.Context) {
		setTestSession(c, domain.Session{Role: "authenticated", UserID: "u1", IsAuthenticated: true})
		h.handleSignDownload("avatars", domain.Bucket{})(c)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/storage/avatars/other-users.jpg", nil))
	assert.Equal(t, 404, w.Code)
	assert.Equal(t, "authenticated", got.Role)
	assert.Equal(t, "u1", got.UserID)
}

func TestHandleDelete_RLSHiddenIs404AndKeepsBytes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deleted := false
	store := &stubObjectStore{deleteFn: func(context.Context, string) error { deleted = true; return nil }}
	db := &stubDB{queryFn: func(_ context.Context, q string, _ ...any) ([]map[string]any, error) {
		require.Contains(t, q, "RETURNING name")
		return nil, nil
	}}
	h := newLegacyStorageHandler(db, store)
	r := gin.New()
	r.DELETE("/storage/avatars/:id", h.handleDelete("avatars", domain.Bucket{}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/storage/avatars/victim.jpg", nil))
	assert.Equal(t, 404, w.Code)
	assert.False(t, deleted, "bytes deleted without an authorized metadata delete")
}

func TestHandleSignUpload_RLSDeniedIs403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{execFn: func(context.Context, string, ...any) (int64, error) {
		return 0, errors.New(`new row violates row-level security policy for table "objects"`)
	}}
	h := newLegacyStorageHandler(db, &stubObjectStore{})
	r := gin.New()
	r.POST("/storage/avatars/sign", h.handleSignUpload("avatars", domain.Bucket{}))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/storage/avatars/sign", strings.NewReader(`{"content_type":"image/png"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, 403, w.Code)
}

func TestHandleSignDownload_InvalidKeyRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryRowFn: func(context.Context, string, ...any) (map[string]any, error) {
		t.Fatal("query must not run for an invalid key")
		return nil, nil
	}}
	store := &stubObjectStore{signDownloadFn: func(context.Context, string, time.Duration, domain.DownloadOptions) (string, error) {
		t.Fatal("sign must not run for an invalid key")
		return "", nil
	}}
	h := newLegacyStorageHandler(db, store)
	r := gin.New()
	r.GET("/storage/avatars/:id", h.handleSignDownload("avatars", domain.Bucket{}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/storage/avatars/..", nil))
	assert.Equal(t, 400, w.Code)
}

func TestHandleDelete_InvalidKeyRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryFn: func(context.Context, string, ...any) ([]map[string]any, error) {
		t.Fatal("query must not run for an invalid key")
		return nil, nil
	}}
	store := &stubObjectStore{deleteFn: func(context.Context, string) error {
		t.Fatal("delete must not run for an invalid key")
		return nil
	}}
	h := newLegacyStorageHandler(db, store)
	r := gin.New()
	r.DELETE("/storage/avatars/:id", h.handleDelete("avatars", domain.Bucket{}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/storage/avatars/..", nil))
	assert.Equal(t, 400, w.Code)
}

func TestHandleDelete_AnotherUsersPrivateObjectDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var capturedRole string
	deleted := false
	store := &stubObjectStore{deleteFn: func(context.Context, string) error { deleted = true; return nil }}
	db := &stubDB{
		withRLSFn: func(ctx context.Context, s domain.Session) (context.Context, error) {
			capturedRole = s.Role
			return ctx, nil
		},
		// RLS hides another user's row, so RETURNING is empty.
		queryFn: func(context.Context, string, ...any) ([]map[string]any, error) { return nil, nil },
	}
	h := newLegacyStorageHandler(db, store)
	r := gin.New()
	r.DELETE("/storage/avatars/:id", func(c *gin.Context) {
		setTestSession(c, domain.Session{Role: "authenticated", UserID: "attacker", IsAuthenticated: true})
		h.handleDelete("avatars", domain.Bucket{})(c)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/storage/avatars/victim-private.jpg", nil))
	assert.Equal(t, 404, w.Code)
	assert.False(t, deleted, "bytes must not be deleted when the caller's RLS can't see the row")
	assert.Equal(t, "authenticated", capturedRole, "delete must run under the caller's role, not service_role")
}

func TestHandleSignDownload_AnotherUsersPrivateObjectDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var capturedRole string
	signed := false
	store := &stubObjectStore{signDownloadFn: func(context.Context, string, time.Duration, domain.DownloadOptions) (string, error) {
		signed = true
		return "https://example.com/download?sig=leak", nil
	}}
	db := &stubDB{
		withRLSFn: func(ctx context.Context, s domain.Session) (context.Context, error) {
			capturedRole = s.Role
			return ctx, nil
		},
		// RLS hides another user's row, so the lookup returns nothing.
		queryRowFn: func(context.Context, string, ...any) (map[string]any, error) { return nil, nil },
	}
	h := newLegacyStorageHandler(db, store)
	r := gin.New()
	r.GET("/storage/avatars/:id", func(c *gin.Context) {
		setTestSession(c, domain.Session{Role: "authenticated", UserID: "attacker", IsAuthenticated: true})
		h.handleSignDownload("avatars", domain.Bucket{})(c)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/storage/avatars/victim-private.jpg", nil))
	assert.Equal(t, 404, w.Code)
	assert.False(t, signed, "no signed URL must be issued for a row the caller's RLS can't see")
	assert.Equal(t, "authenticated", capturedRole, "sign-download must run under the caller's role, not service_role")
}

func TestStorageHandler_MountsAtRootOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("INSTANCEZ_SECRET_KEY", "test-key")
	db := &stubDB{queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
		return map[string]any{"id": "obj1"}, nil
	}}
	store := &stubObjectStore{
		signDownloadFn: func(ctx context.Context, key string, expiry time.Duration, _ domain.DownloadOptions) (string, error) {
			return "https://example.com/download?sig=xyz", nil
		},
	}
	r := NewServer(ServerDeps{
		Config: &domain.Config{
			Project: domain.Project{Name: "test"},
			Storage: map[string]domain.Bucket{"pub": {Public: true}, "priv": {}},
		},
		DB:            domain.RequestDB{Database: db},
		Storage:       store,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		DashboardMode: DashboardDisabled,
	}).Handler()

	cases := []struct {
		name, method, path string
		wantStatus         int
	}{
		{"storage sign wired at root", http.MethodPost, "/storage/priv/sign", 401},
		{"admin at root requires key", http.MethodGet, "/_admin/config/env-vars", 401},
		{"api admin removed", http.MethodGet, "/api/_admin/config/env-vars", 404},
		{"api storage sign removed", http.MethodPost, "/api/storage/priv/sign", 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.wantStatus {
				t.Fatalf("%s %s: status = %d, want %d: %s", tc.method, tc.path, w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantStatus == 404 {
				if loc := w.Header().Get("Location"); loc != "" {
					t.Fatalf("unexpected redirect to %q", loc)
				}
				if strings.Contains(strings.ToLower(w.Body.String()), "<html") {
					t.Fatalf("404 served HTML: %s", w.Body.String())
				}
			}
		})
	}
}

type signCall struct {
	key       string
	size      int64
	signed    bool
	execQuery string
	execArgs  []any
	execs     int
}

func doSign(t *testing.T, bucket domain.Bucket, body string, hdr map[string]string, execErr error) (*httptest.ResponseRecorder, *signCall) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	call := &signCall{}
	store := &stubObjectStore{signUploadFn: func(_ context.Context, key, _ string, size int64, _ time.Duration) (string, error) {
		call.key, call.size, call.signed = key, size, true
		return "https://s3.example/put", nil
	}}
	db := &stubDB{execFn: func(_ context.Context, q string, args ...any) (int64, error) {
		call.execs++
		call.execQuery, call.execArgs = q, args
		return 1, execErr
	}}
	h := newLegacyStorageHandler(db, store)
	r := gin.New()
	r.POST("/sign", func(c *gin.Context) {
		setTestSession(c, domain.Session{Role: "authenticated", UserID: "u1", IsAuthenticated: true})
		h.handleSignUpload("docs", bucket)(c)
	})
	req := httptest.NewRequest(http.MethodPost, "/sign", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w, call
}

func TestHandleSignUpload_WithoutPathKeepsRandomID(t *testing.T) {
	w, call := doSign(t, domain.Bucket{}, `{"content_type":"application/pdf"}`, nil, nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["id"])
	assert.Equal(t, resp["id"], resp["path"])
	assert.Equal(t, "docs/"+resp["id"], call.key)
	assert.Equal(t, resp["id"], call.execArgs[1])
}

func TestHandleSignUpload_WithPathUsesItForKeyAndRow(t *testing.T) {
	name := "p1/Sham ZainAlabideen All Info 2026.pdf"
	body, _ := json.Marshal(map[string]any{"content_type": "application/pdf", "size": 1234, "path": name})
	w, call := doSign(t, domain.Bucket{}, string(body), nil, nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, name, resp["id"])
	assert.Equal(t, name, resp["path"])
	assert.Equal(t, "https://s3.example/put", resp["upload_url"])
	assert.Equal(t, "docs/"+name, call.key)
	assert.EqualValues(t, 1234, call.size)
	assert.Equal(t, name, call.execArgs[1], "RLS insert sees the real path")
	assert.EqualValues(t, 1234, call.execArgs[2], "declared size recorded")
	assert.NotContains(t, call.execQuery, "ON CONFLICT")
}

func TestHandleSignUpload_UnicodePath(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"content_type": "text/plain", "path": "ünï/çødé 😀.txt"})
	w, call := doSign(t, domain.Bucket{}, string(body), nil, nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, "docs/ünï/çødé 😀.txt", call.key)
}

func TestHandleSignUpload_InvalidPaths(t *testing.T) {
	long := strings.Repeat("a", maxKeyBytes)
	for name, p := range map[string]string{
		"dotdot": "../x", "nested dotdot": "a/../b", "leading slash": "/a.pdf", "empty": "",
		"empty segment": "a//b", "trailing slash": "a/", "dot segment": "a/./b", "nul": "a\x00b",
		"too long": long, "root": ".",
	} {
		t.Run(name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"content_type": "text/plain", "path": p})
			w, call := doSign(t, domain.Bucket{}, string(body), nil, nil)
			assert.Equal(t, 400, w.Code, w.Body.String())
			assert.False(t, call.signed, "no URL minted")
			assert.Zero(t, call.execs, "no row created")
		})
	}
}

func TestHandleSignUpload_PathAtKeyLimit(t *testing.T) {
	p := strings.Repeat("a", maxKeyBytes-len("docs/"))
	body, _ := json.Marshal(map[string]any{"content_type": "text/plain", "path": p})
	w, _ := doSign(t, domain.Bucket{}, string(body), nil, nil)
	assert.Equal(t, 200, w.Code, w.Body.String())
}

func TestHandleSignUpload_ExistingPathIs409(t *testing.T) {
	dup := errors.New(`duplicate key value violates unique constraint "objects_pkey" (SQLSTATE 23505)`)
	w, _ := doSign(t, domain.Bucket{}, `{"content_type":"text/plain","path":"a.txt"}`, nil, dup)
	assert.Equal(t, 409, w.Code, w.Body.String())
}

func TestHandleSignUpload_UpsertHeader(t *testing.T) {
	w, call := doSign(t, domain.Bucket{}, `{"content_type":"text/plain","path":"a.txt"}`, map[string]string{"x-upsert": "true"}, nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, call.execQuery, "ON CONFLICT")
}

func TestHandleSignUpload_RLSDeniedWithPath(t *testing.T) {
	deny := errors.New(`new row violates row-level security policy for table "objects"`)
	w, _ := doSign(t, domain.Bucket{}, `{"content_type":"text/plain","path":"other/a.txt"}`, nil, deny)
	assert.Equal(t, 403, w.Code)
}

func TestHandleSignUpload_SizeRules(t *testing.T) {
	capped := domain.Bucket{MaxSize: "1KB"}
	cases := []struct {
		name   string
		bucket domain.Bucket
		body   string
		code   int
	}{
		{"over max", capped, `{"content_type":"text/plain","size":2048}`, 422},
		{"exactly max", capped, `{"content_type":"text/plain","size":1024}`, 200},
		{"missing with max_size", capped, `{"content_type":"text/plain"}`, 400},
		{"zero with max_size", capped, `{"content_type":"text/plain","size":0}`, 400},
		{"negative", capped, `{"content_type":"text/plain","size":-1}`, 400},
		{"missing without max_size", domain.Bucket{}, `{"content_type":"text/plain"}`, 200},
		{"mime rejected", domain.Bucket{Types: []string{"image/*"}}, `{"content_type":"text/plain","size":1}`, 422},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, call := doSign(t, tc.bucket, tc.body, nil, nil)
			assert.Equal(t, tc.code, w.Code, w.Body.String())
			if tc.code != 200 {
				assert.False(t, call.signed)
				assert.Zero(t, call.execs, "no orphan row")
			}
		})
	}
}

func TestHandleSignUpload_PassesDeclaredSizeToSigner(t *testing.T) {
	_, call := doSign(t, domain.Bucket{MaxSize: "1MB"}, `{"content_type":"text/plain","size":777}`, nil, nil)
	assert.EqualValues(t, 777, call.size)
}

func TestHandleSignUpload_NoRowWhenSigningFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	execs := 0
	store := &stubObjectStore{signUploadFn: func(context.Context, string, string, int64, time.Duration) (string, error) {
		return "", errors.New("boom")
	}}
	db := &stubDB{execFn: func(context.Context, string, ...any) (int64, error) { execs++; return 1, nil }}
	h := newLegacyStorageHandler(db, store)
	r := gin.New()
	r.POST("/sign", func(c *gin.Context) {
		setTestSession(c, domain.Session{Role: "authenticated", UserID: "u1", IsAuthenticated: true})
		h.handleSignUpload("docs", domain.Bucket{})(c)
	})
	req := httptest.NewRequest(http.MethodPost, "/sign", strings.NewReader(`{"content_type":"text/plain","path":"a.txt"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, 500, w.Code)
	assert.Zero(t, execs)
}

func TestHandleSignUpload_AnonymousRejectedWithPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	execs := 0
	db := &stubDB{execFn: func(context.Context, string, ...any) (int64, error) { execs++; return 1, nil }}
	h := newLegacyStorageHandler(db, &stubObjectStore{})
	r := gin.New()
	r.POST("/sign", h.handleSignUpload("docs", domain.Bucket{}))
	req := httptest.NewRequest(http.MethodPost, "/sign", strings.NewReader(`{"content_type":"text/plain","path":"a.txt"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, 403, w.Code)
	assert.Zero(t, execs)
}

func TestLegacyRoutes_AcceptNestedIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotName any
	db := &stubDB{
		queryRowFn: func(_ context.Context, _ string, args ...any) (map[string]any, error) {
			gotName = args[0]
			return map[string]any{"mime": "text/plain"}, nil
		},
		queryFn: func(_ context.Context, _ string, args ...any) ([]map[string]any, error) {
			gotName = args[0]
			return []map[string]any{{"name": args[0]}}, nil
		},
	}
	store := &stubObjectStore{signDownloadFn: func(context.Context, string, time.Duration, domain.DownloadOptions) (string, error) {
		return "https://s3.example/get", nil
	}}
	h := newLegacyStorageHandler(db, store)
	r := gin.New()
	r.Use(func(c *gin.Context) { setTestSession(c, domain.Session{Role: "authenticated", UserID: "u1", IsAuthenticated: true}) })
	r.GET("/storage/docs/*id", h.handleSignDownload("docs", domain.Bucket{}))
	r.DELETE("/storage/docs/*id", h.handleDelete("docs", domain.Bucket{}))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/storage/docs/p1/Sham%20Zain.pdf", nil))
	assert.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, "p1/Sham Zain.pdf", gotName)

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/storage/docs/p1/Sham%20Zain.pdf", nil))
	assert.Equal(t, 204, w.Code, w.Body.String())
	assert.Equal(t, "p1/Sham Zain.pdf", gotName)
}
