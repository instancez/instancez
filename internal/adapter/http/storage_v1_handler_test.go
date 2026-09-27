package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- stubObjectStore ---

type stubObjectStore struct {
	signDownloadFn func(ctx context.Context, key string, expiry time.Duration) (string, error)
	signUploadFn   func(ctx context.Context, key, contentType string, expiry time.Duration) (string, error)
	deleteFn       func(ctx context.Context, key string) error
	uploadFn       func(ctx context.Context, key string, r io.Reader, contentType string, size int64) error
	downloadFn     func(ctx context.Context, key string) (io.ReadCloser, string, error)
	copyFn         func(ctx context.Context, srcKey, dstKey string) error
}

func (s *stubObjectStore) SignUpload(ctx context.Context, key, contentType string, expiry time.Duration) (string, error) {
	if s.signUploadFn != nil {
		return s.signUploadFn(ctx, key, contentType, expiry)
	}
	return "", nil
}
func (s *stubObjectStore) SignDownload(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if s.signDownloadFn != nil {
		return s.signDownloadFn(ctx, key, expiry)
	}
	return "", nil
}
func (s *stubObjectStore) Delete(ctx context.Context, key string) error {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, key)
	}
	return nil
}
func (s *stubObjectStore) EnsureBucket(ctx context.Context, bucket string) error { return nil }
func (s *stubObjectStore) Upload(ctx context.Context, key string, r io.Reader, contentType string, size int64) error {
	if s.uploadFn != nil {
		return s.uploadFn(ctx, key, r, contentType, size)
	}
	return nil
}
func (s *stubObjectStore) Download(ctx context.Context, key string) (io.ReadCloser, string, error) {
	if s.downloadFn != nil {
		return s.downloadFn(ctx, key)
	}
	return io.NopCloser(strings.NewReader("")), "application/octet-stream", nil
}
func (s *stubObjectStore) Copy(ctx context.Context, srcKey, dstKey string) error {
	if s.copyFn != nil {
		return s.copyFn(ctx, srcKey, dstKey)
	}
	return nil
}
func (s *stubObjectStore) Head(ctx context.Context, key string) (domain.ObjectInfo, error) {
	return domain.ObjectInfo{}, nil
}
func (s *stubObjectStore) List(ctx context.Context, prefix string) ([]domain.ObjectInfo, error) {
	return nil, nil
}

// --- helper to build a StorageV1Handler with a stub DB and store ---

func newStorageHandler(db domain.Database, store domain.ObjectStore, storage map[string]domain.Bucket) *StorageV1Handler {
	if storage == nil {
		storage = map[string]domain.Bucket{}
	}
	return &StorageV1Handler{
		cfg:     &domain.Config{Storage: storage},
		db:      db,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		storage: store,
		// jwtKeys intentionally nil — bucket/object tests don't exercise JWT path
	}
}

// setTestSession injects a domain.Session into the gin context so that
// getSession() returns it, bypassing the jwtAuth middleware.
func setTestSession(c *gin.Context, s domain.Session) {
	c.Set(contextKeySession, s)
}

// --- Bucket handler tests ---

func TestListBuckets_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, nil)

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/storage/v1/bucket", h.listBuckets)

	req := httptest.NewRequest(http.MethodGet, "/storage/v1/bucket", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body []any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 0 {
		t.Fatalf("expected empty array, got %v", body)
	}
}

func TestListBuckets_NonEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buckets := map[string]domain.Bucket{
		"avatars": {Public: true},
		"docs":    {Public: false},
	}
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/storage/v1/bucket", h.listBuckets)

	req := httptest.NewRequest(http.MethodGet, "/storage/v1/bucket", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 {
		t.Fatalf("expected 2 buckets, got %d: %s", len(body), w.Body.String())
	}
}

func TestGetBucket_Found(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buckets := map[string]domain.Bucket{
		"avatars": {Public: true},
	}
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/storage/v1/bucket/:id", h.getBucket)

	req := httptest.NewRequest(http.MethodGet, "/storage/v1/bucket/avatars", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["id"] != "avatars" {
		t.Errorf("expected id=avatars, got %v", body["id"])
	}
}

func TestGetBucket_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, nil)

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/storage/v1/bucket/:id", h.getBucket)

	req := httptest.NewRequest(http.MethodGet, "/storage/v1/bucket/missing", nil)
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateBucket_NotSupported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, nil)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/bucket", h.createBucket)

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/bucket", strings.NewReader(`{"name":"new"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["error"] != "not_supported" {
		t.Errorf("expected error=not_supported, got %v", body["error"])
	}
}

func TestUpdateBucket_NotSupported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, nil)

	w := httptest.NewRecorder()
	r := gin.New()
	r.PUT("/storage/v1/bucket/:id", h.updateBucket)

	req := httptest.NewRequest(http.MethodPut, "/storage/v1/bucket/avatars", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["error"] != "not_supported" {
		t.Errorf("expected error=not_supported, got %v", body["error"])
	}
}

func TestDeleteBucket_NotSupported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, nil)

	w := httptest.NewRecorder()
	r := gin.New()
	r.DELETE("/storage/v1/bucket/:id", h.deleteBucket)

	req := httptest.NewRequest(http.MethodDelete, "/storage/v1/bucket/avatars", nil)
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["error"] != "not_supported" {
		t.Errorf("expected error=not_supported, got %v", body["error"])
	}
}

func TestEmptyBucket_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, nil)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/bucket/:id/empty", h.emptyBucket)

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/bucket/missing/empty", nil)
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// SELECT sees the row but RLS filters the DELETE: bytes must survive.
func TestEmptyBucket_ReadOnlyCallerDeletesNothing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deleted := 0
	store := &stubObjectStore{deleteFn: func(context.Context, string) error { deleted++; return nil }}
	db := &stubDB{queryFn: func(_ context.Context, q string, _ ...any) ([]map[string]any, error) {
		if strings.HasPrefix(strings.TrimSpace(q), "SELECT") {
			return []map[string]any{{"name": "photo.jpg"}}, nil
		}
		return nil, nil
	}}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {}})
	r := gin.New()
	r.POST("/storage/v1/bucket/:id/empty", h.emptyBucket)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/storage/v1/bucket/avatars/empty", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Zero(t, deleted, "S3 bytes deleted for rows RLS did not let us delete")
}

func TestEmptyBucket_DeletesReturnedRows(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var deleted []string
	store := &stubObjectStore{deleteFn: func(_ context.Context, k string) error {
		deleted = append(deleted, k)
		if strings.HasSuffix(k, "b.jpg") {
			return errors.New("s3 flake")
		}
		return nil
	}}
	var gotQ string
	db := &stubDB{queryFn: func(_ context.Context, q string, args ...any) ([]map[string]any, error) {
		gotQ = q
		return []map[string]any{{"name": "a.jpg"}, {"name": "b.jpg"}, {"name": "ünï/c.png"}}, nil
	}}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {}})
	r := gin.New()
	r.POST("/storage/v1/bucket/:id/empty", h.emptyBucket)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/storage/v1/bucket/avatars/empty", nil))
	require.Equal(t, 200, w.Code)
	assert.Contains(t, gotQ, "DELETE FROM storage.objects")
	assert.Contains(t, gotQ, "RETURNING name")
	assert.Equal(t, []string{"avatars/a.jpg", "avatars/b.jpg", "avatars/ünï/c.png"}, deleted, "one S3 failure must not stop the rest")
}

func TestEmptyBucket_DBErrorSkipsStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deleted := 0
	store := &stubObjectStore{deleteFn: func(context.Context, string) error { deleted++; return nil }}
	db := &stubDB{queryFn: func(context.Context, string, ...any) ([]map[string]any, error) {
		return nil, errors.New("permission denied")
	}}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {}})
	r := gin.New()
	r.POST("/storage/v1/bucket/:id/empty", h.emptyBucket)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/storage/v1/bucket/avatars/empty", nil))
	assert.Equal(t, 500, w.Code)
	assert.Zero(t, deleted)
}

// --- Signed URL handler tests ---

func TestCreateSignedURL_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	store := &stubObjectStore{
		signDownloadFn: func(ctx context.Context, key string, expiry time.Duration) (string, error) {
			return "https://example.com/signed?token=abc123", nil
		},
	}

	db := &stubDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			// Object found
			return map[string]any{"id": "some-id"}, nil
		},
	}

	buckets := map[string]domain.Bucket{
		"avatars": {Public: false},
	}
	h := newStorageHandler(db, store, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/sign/:bucket/*path", func(c *gin.Context) {
		setTestSession(c, domain.Session{Role: "service_role"})
		h.createSignedURL(c)
	})

	body := strings.NewReader(`{"expiresIn":3600}`)
	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/sign/avatars/photo.jpg", body)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["signedURL"] != "https://example.com/signed?token=abc123" {
		t.Errorf("expected signedURL in response, got %v", resp)
	}
}

// A signed upload URL is a capability: once minted, the holder can write the
// object with no further auth (the redemption runs as service_role). So the
// authorization check must happen at mint time. These two tests pin that the
// caller's INSERT policy is probed under their role before any token is issued,
// mirroring Supabase's storage-api (signUploadObjectUrl → canUpload).

func TestCreateSignedUploadURL_DeniedByInsertRLS(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var rolledBack, committed bool
	tx := &stubTx{
		execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
			return 0, errors.New(`new row violates row-level security policy for table "objects"`)
		},
		rollbackFn: func(ctx context.Context) error { rolledBack = true; return nil },
		commitFn:   func(ctx context.Context) error { committed = true; return nil },
	}
	db := &stubDB{beginFn: func(ctx context.Context) (domain.Tx, error) { return tx, nil }}

	h := newStorageHandler(db, &stubObjectStore{}, map[string]domain.Bucket{"avatars": {}})
	h.jwtKeys = stubKeys(t)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/upload/sign/:bucket/*path", func(c *gin.Context) {
		setTestSession(c, domain.Session{Role: "authenticated", UserID: "11111111-1111-1111-1111-111111111111", IsAuthenticated: true})
		h.createSignedUploadURL(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/upload/sign/avatars/photo.jpg", nil)
	r.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["token"]; ok {
		t.Errorf("no token may be minted when the insert is RLS-denied, got %v", resp)
	}
	if !rolledBack {
		t.Errorf("the permission-probe transaction must be rolled back")
	}
	if committed {
		t.Errorf("the permission-probe transaction must never be committed")
	}
}

func TestCreateSignedUploadURL_AllowedMintsTokenAndRollsBack(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var rolledBack, committed bool
	tx := &stubTx{
		execFn:     func(ctx context.Context, q string, args ...any) (int64, error) { return 1, nil },
		rollbackFn: func(ctx context.Context) error { rolledBack = true; return nil },
		commitFn:   func(ctx context.Context) error { committed = true; return nil },
	}
	db := &stubDB{beginFn: func(ctx context.Context) (domain.Tx, error) { return tx, nil }}

	h := newStorageHandler(db, &stubObjectStore{}, map[string]domain.Bucket{"avatars": {}})
	h.jwtKeys = stubKeys(t)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/upload/sign/:bucket/*path", func(c *gin.Context) {
		setTestSession(c, domain.Session{Role: "authenticated", UserID: "11111111-1111-1111-1111-111111111111", IsAuthenticated: true})
		h.createSignedUploadURL(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/upload/sign/avatars/photo.jpg", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if tok, _ := resp["token"].(string); tok == "" {
		t.Errorf("expected a signed upload token, got %v", resp)
	}
	if !rolledBack {
		t.Errorf("the permission-probe transaction must be rolled back, never persisted")
	}
	if committed {
		t.Errorf("the permission-probe transaction must never be committed")
	}
}

func TestUploadToken_RoundTripsOwner(t *testing.T) {
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, map[string]domain.Bucket{"avatars": {}})
	h.jwtKeys = stubKeys(t)

	owner := "11111111-1111-1111-1111-111111111111"
	token := h.signUploadToken("avatars", "photo.png", owner)
	if token == "" {
		t.Fatal("expected a token")
	}

	gotOwner, ok := h.verifyUploadToken(token, "avatars", "photo.png")
	if !ok {
		t.Fatal("token should verify for the path it was signed for")
	}
	if gotOwner != owner {
		t.Fatalf("owner not recovered from token: got %q, want %q", gotOwner, owner)
	}

	if _, ok := h.verifyUploadToken(token, "avatars", "other.png"); ok {
		t.Error("token must not verify for a different path")
	}
}

func TestUploadToSignedURL_PersistsOwnerFromToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var gotArgs []any
	db := &stubDB{
		execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
			gotArgs = args
			return 1, nil
		},
	}
	h := newStorageHandler(db, &stubObjectStore{}, map[string]domain.Bucket{"avatars": {}})
	h.jwtKeys = stubKeys(t)

	owner := "11111111-1111-1111-1111-111111111111"
	token := h.signUploadToken("avatars", "photo.png", owner)

	w := httptest.NewRecorder()
	r := gin.New()
	r.PUT("/storage/v1/object/upload/sign/:bucket/*path", h.uploadToSignedURL)
	req := httptest.NewRequest(http.MethodPut, "/storage/v1/object/upload/sign/avatars/photo.png?token="+token, strings.NewReader("hi"))
	req.Header.Set("Content-Type", "text/plain")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// The owner carried in the token must be written to uploaded_by so that
	// owner-scoped RLS policies match the row the redemption persists.
	found := false
	for _, a := range gotArgs {
		if s, ok := a.(string); ok && s == owner {
			found = true
		}
	}
	if !found {
		t.Errorf("expected uploaded_by=%q threaded into the metadata write, got args %v", owner, gotArgs)
	}
}

func TestCreateSignedURL_ObjectNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := &stubDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			// Object not found — return nil row
			return nil, nil
		},
	}

	buckets := map[string]domain.Bucket{
		"avatars": {Public: false},
	}
	h := newStorageHandler(db, &stubObjectStore{}, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/sign/:bucket/*path", func(c *gin.Context) {
		setTestSession(c, domain.Session{Role: "service_role"})
		h.createSignedURL(c)
	})

	body := strings.NewReader(`{"expiresIn":3600}`)
	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/sign/avatars/missing.jpg", body)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// --- Upload / update object tests ---

func TestUploadObject_BucketNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, nil)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/:bucket/*path", h.uploadObject)

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/missing/photo.jpg", strings.NewReader("data"))
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUploadObject_MimeTypeRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buckets := map[string]domain.Bucket{
		"avatars": {Types: []string{"image/png"}},
	}
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/:bucket/*path", h.uploadObject)

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/avatars/photo.txt", strings.NewReader("data"))
	req.Header.Set("Content-Type", "text/plain")
	r.ServeHTTP(w, req)

	if w.Code != 422 {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "invalid_mime_type" {
		t.Errorf("expected error=invalid_mime_type, got %v", body["error"])
	}
}

func TestUploadObject_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var gotKey, gotContentType string
	var gotSize int64
	store := &stubObjectStore{
		uploadFn: func(ctx context.Context, key string, r io.Reader, contentType string, size int64) error {
			gotKey = key
			gotContentType = contentType
			gotSize = size
			b, _ := io.ReadAll(r)
			if string(b) != "hello world" {
				t.Errorf("upload body = %q, want %q", b, "hello world")
			}
			return nil
		},
	}
	tx := &stubTx{execFn: func(ctx context.Context, q string, args ...any) (int64, error) { return 1, nil }}
	db := &stubDB{beginFn: func(ctx context.Context) (domain.Tx, error) { return tx, nil }}
	buckets := map[string]domain.Bucket{"avatars": {}}
	h := newStorageHandler(db, store, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/:bucket/*path", func(c *gin.Context) {
		setTestSession(c, domain.Session{Role: "authenticated", UserID: "u1", IsAuthenticated: true})
		h.uploadObject(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/avatars/photo.jpg", strings.NewReader("hello world"))
	req.Header.Set("Content-Type", "image/jpeg")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotKey != "avatars/photo.jpg" {
		t.Errorf("store.Upload key = %q, want avatars/photo.jpg", gotKey)
	}
	if gotContentType != "image/jpeg" {
		t.Errorf("store.Upload contentType = %q", gotContentType)
	}
	if gotSize != int64(len("hello world")) {
		t.Errorf("store.Upload size = %d, want %d", gotSize, len("hello world"))
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["Key"] != "avatars/photo.jpg" {
		t.Errorf("response Key = %v", resp["Key"])
	}
}

func TestUploadObject_WritesMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var gotQuery string
	var gotArgs []any
	tx := &stubTx{execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
		gotQuery, gotArgs = q, args
		return 1, nil
	}}
	db := &stubDB{beginFn: func(ctx context.Context) (domain.Tx, error) { return tx, nil }}
	store := &stubObjectStore{}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {}})

	r := gin.New()
	r.POST("/storage/v1/object/:bucket/*path", func(c *gin.Context) {
		setTestSession(c, domain.Session{Role: "authenticated", UserID: "u1", IsAuthenticated: true})
		h.uploadObject(c)
	})
	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/avatars/photo.jpg", strings.NewReader("hello world"))
	req.Header.Set("Content-Type", "image/jpeg")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, gotQuery, "metadata")
	// find the metadata json arg and assert exact keys/values
	var found string
	for _, a := range gotArgs {
		if s, ok := a.(string); ok && strings.Contains(s, "mimetype") {
			found = s
		}
	}
	require.NotEmpty(t, found, "metadata json arg not passed")
	assert.Contains(t, found, `"mimetype":"image/jpeg"`)
	assert.Contains(t, found, `"size":11`)
	assert.Contains(t, found, `"cacheControl"`)
	assert.Contains(t, found, `"httpStatusCode":200`)
}

func TestUploadObject_DuplicateKeyConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tx := &stubTx{execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
		return 0, errors.New(`duplicate key value violates unique constraint "objects_pkey" (SQLSTATE 23505)`)
	}}
	db := &stubDB{beginFn: func(ctx context.Context) (domain.Tx, error) { return tx, nil }}
	h := newStorageHandler(db, &stubObjectStore{}, map[string]domain.Bucket{"avatars": {}})

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/:bucket/*path", h.uploadObject)

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/avatars/photo.jpg", strings.NewReader("data"))
	r.ServeHTTP(w, req)

	if w.Code != 409 {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "duplicate" {
		t.Errorf("expected error=duplicate, got %v", body["error"])
	}
}

func TestUploadObject_RLSDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tx := &stubTx{execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
		return 0, errors.New(`new row violates row-level security policy for table "objects"`)
	}}
	db := &stubDB{beginFn: func(ctx context.Context) (domain.Tx, error) { return tx, nil }}
	h := newStorageHandler(db, &stubObjectStore{}, map[string]domain.Bucket{"avatars": {}})

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/:bucket/*path", h.uploadObject)

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/avatars/photo.jpg", strings.NewReader("data"))
	r.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUploadObject_StoreUploadInternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &stubObjectStore{
		uploadFn: func(ctx context.Context, key string, r io.Reader, contentType string, size int64) error {
			return errors.New("connection reset by peer")
		},
	}
	tx := &stubTx{execFn: func(ctx context.Context, q string, args ...any) (int64, error) { return 1, nil }}
	db := &stubDB{beginFn: func(ctx context.Context) (domain.Tx, error) { return tx, nil }}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {}})

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/:bucket/*path", h.uploadObject)

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/avatars/photo.jpg", strings.NewReader("data"))
	r.ServeHTTP(w, req)

	if w.Code != 500 {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUploadObject_CommitFailureCleansUpStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var deletedKey string
	store := &stubObjectStore{
		deleteFn: func(ctx context.Context, key string) error { deletedKey = key; return nil },
	}
	tx := &stubTx{
		execFn:   func(ctx context.Context, q string, args ...any) (int64, error) { return 1, nil },
		commitFn: func(ctx context.Context) error { return errors.New("commit failed") },
	}
	db := &stubDB{beginFn: func(ctx context.Context) (domain.Tx, error) { return tx, nil }}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {}})

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/:bucket/*path", h.uploadObject)

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/avatars/photo.jpg", strings.NewReader("data"))
	r.ServeHTTP(w, req)

	if w.Code != 500 {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
	if deletedKey != "avatars/photo.jpg" {
		t.Errorf("expected orphaned object cleanup, deletedKey = %q", deletedKey)
	}
}

func TestUpdateObject_NotFoundNoRowsAffected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tx := &stubTx{execFn: func(ctx context.Context, q string, args ...any) (int64, error) { return 0, nil }}
	db := &stubDB{beginFn: func(ctx context.Context) (domain.Tx, error) { return tx, nil }}
	h := newStorageHandler(db, &stubObjectStore{}, map[string]domain.Bucket{"avatars": {}})

	w := httptest.NewRecorder()
	r := gin.New()
	r.PUT("/storage/v1/object/:bucket/*path", h.updateObject)

	req := httptest.NewRequest(http.MethodPut, "/storage/v1/object/avatars/missing.jpg", strings.NewReader("data"))
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateObject_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uploadCalled := false
	store := &stubObjectStore{
		uploadFn: func(ctx context.Context, key string, r io.Reader, contentType string, size int64) error {
			uploadCalled = true
			return nil
		},
	}
	tx := &stubTx{execFn: func(ctx context.Context, q string, args ...any) (int64, error) { return 1, nil }}
	db := &stubDB{beginFn: func(ctx context.Context) (domain.Tx, error) { return tx, nil }}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {}})

	w := httptest.NewRecorder()
	r := gin.New()
	r.PUT("/storage/v1/object/:bucket/*path", h.updateObject)

	req := httptest.NewRequest(http.MethodPut, "/storage/v1/object/avatars/photo.jpg", strings.NewReader("data"))
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !uploadCalled {
		t.Error("expected store.Upload to be called on successful metadata update")
	}
}

// --- Download (objectGetDispatch) tests ---

func TestObjectGetDispatch_Public_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
		return map[string]any{"id": "obj1"}, nil
	}}
	store := &stubObjectStore{
		downloadFn: func(ctx context.Context, key string) (io.ReadCloser, string, error) {
			if key != "avatars/photo.jpg" {
				t.Errorf("Download key = %q", key)
			}
			return io.NopCloser(strings.NewReader("image-bytes")), "image/jpeg", nil
		},
	}
	buckets := map[string]domain.Bucket{"avatars": {Public: true}}
	h := newStorageHandler(db, store, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/storage/v1/object/*all", h.objectGetDispatch)

	req := httptest.NewRequest(http.MethodGet, "/storage/v1/object/public/avatars/photo.jpg", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "image-bytes" {
		t.Errorf("body = %q", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestObjectGetDispatch_Public_BucketNotPublic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buckets := map[string]domain.Bucket{"avatars": {Public: false}}
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/storage/v1/object/*all", h.objectGetDispatch)

	req := httptest.NewRequest(http.MethodGet, "/storage/v1/object/public/avatars/photo.jpg", nil)
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "not_public" {
		t.Errorf("expected error=not_public, got %v", body["error"])
	}
}

func TestObjectGetDispatch_Public_BucketNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, nil)

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/storage/v1/object/*all", h.objectGetDispatch)

	req := httptest.NewRequest(http.MethodGet, "/storage/v1/object/public/missing/photo.jpg", nil)
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestObjectGetDispatch_Public_ObjectNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
		return nil, nil
	}}
	buckets := map[string]domain.Bucket{"avatars": {Public: true}}
	h := newStorageHandler(db, &stubObjectStore{}, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/storage/v1/object/*all", h.objectGetDispatch)

	req := httptest.NewRequest(http.MethodGet, "/storage/v1/object/public/avatars/missing.jpg", nil)
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestObjectGetDispatch_Public_MissingSegments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, nil)

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/storage/v1/object/*all", h.objectGetDispatch)

	req := httptest.NewRequest(http.MethodGet, "/storage/v1/object/public", nil)
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestObjectGetDispatch_Info_MissingSegments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, nil)

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/storage/v1/object/*all", h.objectGetDispatch)

	req := httptest.NewRequest(http.MethodGet, "/storage/v1/object/info", nil)
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestObjectGetDispatch_Default_MissingSegments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, nil)

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/storage/v1/object/*all", h.objectGetDispatch)

	req := httptest.NewRequest(http.MethodGet, "/storage/v1/object/onlybucket", nil)
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestObjectGetDispatch_Authenticated_MissingAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buckets := map[string]domain.Bucket{"avatars": {Public: false}}
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/storage/v1/object/*all", h.objectGetDispatch)

	req := httptest.NewRequest(http.MethodGet, "/storage/v1/object/authenticated/avatars/photo.jpg", nil)
	r.ServeHTTP(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestObjectGetDispatch_Authenticated_SecretKeySucceeds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("INSTANCEZ_SECRET_KEY", "test-secret-key")

	db := &stubDB{queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
		return map[string]any{"id": "obj1"}, nil
	}}
	store := &stubObjectStore{
		downloadFn: func(ctx context.Context, key string) (io.ReadCloser, string, error) {
			return io.NopCloser(strings.NewReader("private-bytes")), "application/pdf", nil
		},
	}
	buckets := map[string]domain.Bucket{"docs": {Public: false}}
	h := newStorageHandler(db, store, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/storage/v1/object/*all", h.objectGetDispatch)

	req := httptest.NewRequest(http.MethodGet, "/storage/v1/object/authenticated/docs/report.pdf", nil)
	req.Header.Set("apikey", "test-secret-key")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "private-bytes" {
		t.Errorf("body = %q", w.Body.String())
	}
}

// --- List / list-v2 tests ---

func TestListObjects_BucketNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, nil)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/list/:bucket", h.listObjects)

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/list/missing", strings.NewReader(`{}`))
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListObjects_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryFn: func(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
		return nil, nil
	}}
	buckets := map[string]domain.Bucket{"avatars": {}}
	h := newStorageHandler(db, &stubObjectStore{}, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/list/:bucket", h.listObjects)

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/list/avatars", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body []any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Fatalf("expected empty array, got %v", body)
	}
}

func TestListObjects_PrefixStrippedFromNames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotQuery string
	var gotArgs []any
	db := &stubDB{queryFn: func(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
		gotQuery = q
		gotArgs = args
		return []map[string]any{{"name": "folder/photo.jpg", "uploaded_at": "2024-01-01T00:00:00Z"}}, nil
	}}
	buckets := map[string]domain.Bucket{"avatars": {}}
	h := newStorageHandler(db, &stubObjectStore{}, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/list/:bucket", h.listObjects)

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/list/avatars", strings.NewReader(`{"prefix":"folder/"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(gotQuery, "LIKE") {
		t.Errorf("expected prefix filter in query, got %q", gotQuery)
	}
	if len(gotArgs) < 2 || gotArgs[1] != "folder/%" {
		t.Errorf("expected prefix arg 'folder/%%', got %v", gotArgs)
	}
	var body []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 || body[0]["name"] != "photo.jpg" {
		t.Fatalf("expected relative name 'photo.jpg', got %v", body)
	}
}

func TestListObjectsV2_Pagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryFn: func(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
		// Limit=2 requested; fetchLimit=3 rows returned to signal hasNext.
		return []map[string]any{
			{"name": "a.jpg"}, {"name": "b.jpg"}, {"name": "c.jpg"},
		}, nil
	}}
	buckets := map[string]domain.Bucket{"avatars": {}}
	h := newStorageHandler(db, &stubObjectStore{}, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/list-v2/:bucket", h.listObjectsV2)

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/list-v2/avatars", strings.NewReader(`{"limit":2}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["has_next"] != true {
		t.Errorf("expected has_next=true, got %v", resp["has_next"])
	}
	objects, _ := resp["objects"].([]any)
	if len(objects) != 2 {
		t.Fatalf("expected 2 objects (limit applied), got %d: %v", len(objects), objects)
	}
	if resp["next_cursor"] != "b.jpg" {
		t.Errorf("expected next_cursor='b.jpg' (last of the truncated page), got %v", resp["next_cursor"])
	}
}

func TestListObjectsV2_WithDelimiterGroupsFolders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryFn: func(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
		return []map[string]any{
			{"name": "folder/a.jpg"}, {"name": "folder/b.jpg"}, {"name": "top.jpg"},
		}, nil
	}}
	buckets := map[string]domain.Bucket{"avatars": {}}
	h := newStorageHandler(db, &stubObjectStore{}, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/list-v2/:bucket", h.listObjectsV2)

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/list-v2/avatars", strings.NewReader(`{"with_delimiter":true}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	folders, _ := resp["folders"].([]any)
	objects, _ := resp["objects"].([]any)
	if len(folders) != 1 {
		t.Fatalf("expected 1 deduped folder, got %d: %v", len(folders), folders)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 top-level object, got %d: %v", len(objects), objects)
	}
}

// --- objectExists (HEAD) tests ---

func TestObjectExists_Found(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
		return map[string]any{"id": "obj1"}, nil
	}}
	buckets := map[string]domain.Bucket{"avatars": {}}
	h := newStorageHandler(db, &stubObjectStore{}, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.HEAD("/storage/v1/object/:bucket/*path", h.objectExists)

	req := httptest.NewRequest(http.MethodHead, "/storage/v1/object/avatars/photo.jpg", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestObjectExists_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
		return nil, nil
	}}
	buckets := map[string]domain.Bucket{"avatars": {}}
	h := newStorageHandler(db, &stubObjectStore{}, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.HEAD("/storage/v1/object/:bucket/*path", h.objectExists)

	req := httptest.NewRequest(http.MethodHead, "/storage/v1/object/avatars/missing.jpg", nil)
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- removeObjects tests ---

func TestRemoveObjects_OnlyDeletesRowsRLSReturned(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var deleted []string
	store := &stubObjectStore{deleteFn: func(_ context.Context, k string) error { deleted = append(deleted, k); return nil }}
	var gotArgs []any
	db := &stubDB{
		// Old code probed with QueryRow SELECT: prove that path no longer authorizes deletes.
		queryRowFn: func(_ context.Context, _ string, args ...any) (map[string]any, error) {
			return map[string]any{"id": "x", "name": args[1]}, nil
		},
		queryFn: func(_ context.Context, q string, args ...any) ([]map[string]any, error) {
			require.Contains(t, q, "RETURNING name")
			gotArgs = args
			return []map[string]any{{"name": "photo.jpg"}}, nil
		},
	}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {}})
	r := gin.New()
	r.DELETE("/storage/v1/object/:bucket", h.removeObjects)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/storage/v1/object/avatars",
		strings.NewReader(`{"prefixes":["photo.jpg","/hidden.jpg","../escape","","ünï.png"]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, []string{"photo.jpg", "hidden.jpg", "ünï.png"}, gotArgs[1])
	assert.Equal(t, []string{"avatars/photo.jpg"}, deleted)
	var body []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body, 1)
	assert.Equal(t, "photo.jpg", body[0]["name"])
}

func TestRemoveObjects_EmptyOrAllInvalidSkipsDB(t *testing.T) {
	gin.SetMode(gin.TestMode)
	queried := false
	db := &stubDB{queryFn: func(context.Context, string, ...any) ([]map[string]any, error) { queried = true; return nil, nil }}
	h := newStorageHandler(db, &stubObjectStore{}, map[string]domain.Bucket{"avatars": {}})
	r := gin.New()
	r.DELETE("/storage/v1/object/:bucket", h.removeObjects)
	for _, body := range []string{`{"prefixes":[]}`, `{"prefixes":["..", ""]}`} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/storage/v1/object/avatars", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		assert.Equal(t, 200, w.Code)
		assert.JSONEq(t, `[]`, w.Body.String())
	}
	assert.False(t, queried)
}

func TestRemoveObjects_DBErrorSkipsStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deleted := 0
	store := &stubObjectStore{deleteFn: func(context.Context, string) error { deleted++; return nil }}
	db := &stubDB{queryFn: func(context.Context, string, ...any) ([]map[string]any, error) { return nil, errors.New("db down") }}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {}})
	r := gin.New()
	r.DELETE("/storage/v1/object/:bucket", h.removeObjects)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/storage/v1/object/avatars", strings.NewReader(`{"prefixes":["a"]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, 500, w.Code)
	assert.Zero(t, deleted)
}

func TestRemoveObjects_BadRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buckets := map[string]domain.Bucket{"avatars": {}}
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.DELETE("/storage/v1/object/:bucket", h.removeObjects)

	req := httptest.NewRequest(http.MethodDelete, "/storage/v1/object/avatars", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// --- Move / Copy tests ---

func TestMoveObject_SourceBucketNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, nil)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/move", h.moveObject)

	body := `{"bucketId":"missing","sourceKey":"old.jpg","destinationKey":"new.jpg"}`
	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCopyObject_DestinationBucketNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buckets := map[string]domain.Bucket{"avatars": {}}
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/copy", h.copyObject)

	body := `{"bucketId":"avatars","sourceKey":"old.jpg","destinationKey":"copy.jpg","destinationBucket":"missing"}`
	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/copy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

type opLog struct{ ops []string }

func (l *opLog) add(s string) { l.ops = append(l.ops, s) }

func moveCopyHarness(t *testing.T, execN int64, execErr, copyErr, commitErr error) (*StorageV1Handler, *opLog, *[]any) {
	t.Helper()
	log := &opLog{}
	var args []any
	tx := &stubTx{
		execFn: func(_ context.Context, _ string, a ...any) (int64, error) {
			log.add("exec")
			args = a
			return execN, execErr
		},
		commitFn:   func(context.Context) error { log.add("commit"); return commitErr },
		rollbackFn: func(context.Context) error { log.add("rollback"); return nil },
	}
	store := &stubObjectStore{
		copyFn:   func(_ context.Context, s, d string) error { log.add("copy:" + s + ">" + d); return copyErr },
		deleteFn: func(_ context.Context, k string) error { log.add("delete:" + k); return nil },
	}
	db := &stubDB{beginFn: func(context.Context) (domain.Tx, error) { return tx, nil }}
	return newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {}, "backups": {}}), log, &args
}

func runMoveCopyAs(h *StorageV1Handler, s domain.Session, route, body string) *httptest.ResponseRecorder {
	r := gin.New()
	auth := func(c *gin.Context) { setTestSession(c, s) }
	r.POST("/storage/v1/object/move", auth, h.moveObject)
	r.POST("/storage/v1/object/copy", auth, h.copyObject)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, route, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func runMoveCopy(h *StorageV1Handler, route, body string) *httptest.ResponseRecorder {
	return runMoveCopyAs(h, domain.Session{Role: "authenticated", UserID: "11111111-1111-1111-1111-111111111111", IsAuthenticated: true}, route, body)
}

const moveBody = `{"bucketId":"avatars","sourceKey":"old.jpg","destinationKey":"new.jpg"}`
const copyBody = `{"bucketId":"avatars","sourceKey":"old.jpg","destinationKey":"copy.jpg","destinationBucket":"backups"}`

func TestMoveObject_OrderDBThenCopyThenCommitThenDelete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, log, args := moveCopyHarness(t, 1, nil, nil, nil)
	w := runMoveCopy(h, "/storage/v1/object/move", moveBody)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, []string{"exec", "copy:avatars/old.jpg>avatars/new.jpg", "commit", "delete:avatars/old.jpg", "rollback"}, log.ops)
	assert.Equal(t, []any{"avatars", "new.jpg", "avatars", "old.jpg"}, *args)
}

func TestMoveObject_Denials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name string
		n    int64
		err  error
		want int
	}{
		{"hidden by RLS", 0, nil, 404},
		{"dest violates policy", 0, errors.New(`new row violates row-level security policy for table "objects"`), 403},
		{"dest exists", 0, errors.New(`duplicate key value violates unique constraint (SQLSTATE 23505)`), 409},
		{"db down", 0, errors.New("conn reset"), 500},
	}
	for _, tc := range cases {
		h, log, _ := moveCopyHarness(t, tc.n, tc.err, nil, nil)
		w := runMoveCopy(h, "/storage/v1/object/move", moveBody)
		assert.Equal(t, tc.want, w.Code, tc.name)
		assert.Equal(t, []string{"exec", "rollback"}, log.ops, "%s: store touched before authorization", tc.name)
	}
}

func TestMoveObject_CopyFailureRollsBack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, log, _ := moveCopyHarness(t, 1, nil, errors.New("s3 down"), nil)
	w := runMoveCopy(h, "/storage/v1/object/move", moveBody)
	assert.Equal(t, 500, w.Code)
	assert.Equal(t, []string{"exec", "copy:avatars/old.jpg>avatars/new.jpg", "rollback"}, log.ops)
}

func TestMoveObject_CommitFailureDropsCopyKeepsSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, log, _ := moveCopyHarness(t, 1, nil, nil, errors.New("commit failed"))
	w := runMoveCopy(h, "/storage/v1/object/move", moveBody)
	assert.Equal(t, 500, w.Code)
	assert.Contains(t, log.ops, "delete:avatars/new.jpg")
	assert.NotContains(t, log.ops, "delete:avatars/old.jpg")
}

func TestMoveCopy_BeginFailureNoStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, route := range []string{"/storage/v1/object/move", "/storage/v1/object/copy"} {
		var touched bool
		store := &stubObjectStore{
			copyFn:   func(context.Context, string, string) error { touched = true; return nil },
			deleteFn: func(context.Context, string) error { touched = true; return nil },
		}
		db := &stubDB{beginFn: func(context.Context) (domain.Tx, error) { return nil, errors.New("pool exhausted") }}
		h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {}, "backups": {}})
		w := runMoveCopy(h, route, moveBody)
		assert.Equal(t, 500, w.Code, route)
		assert.False(t, touched, route)
	}
}

func TestMoveCopy_SameSourceAndDestRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, route := range []string{"/storage/v1/object/move", "/storage/v1/object/copy"} {
		for _, body := range []string{
			`{"bucketId":"avatars","sourceKey":"a.jpg","destinationKey":"/a.jpg"}`,
			`{"bucketId":"avatars","sourceKey":"a.jpg","destinationKey":"a.jpg","destinationBucket":"avatars"}`,
			`{"bucketId":"avatars","sourceKey":"dir//a.jpg","destinationKey":"dir/./a.jpg"}`,
		} {
			h, log, _ := moveCopyHarness(t, 1, nil, nil, nil)
			w := runMoveCopy(h, route, body)
			assert.Equal(t, 400, w.Code, route+" "+body)
			assert.Empty(t, log.ops, route+" "+body)
		}
	}
}

func TestMoveCopy_SameKeyAcrossBucketsAllowed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	want := map[string][]string{
		"/storage/v1/object/move": {"exec", "copy:avatars/a.jpg>backups/a.jpg", "commit", "delete:avatars/a.jpg", "rollback"},
		"/storage/v1/object/copy": {"exec", "copy:avatars/a.jpg>backups/a.jpg", "commit", "rollback"},
	}
	for route, ops := range want {
		h, log, _ := moveCopyHarness(t, 1, nil, nil, nil)
		w := runMoveCopy(h, route, `{"bucketId":"avatars","sourceKey":"a.jpg","destinationKey":"a.jpg","destinationBucket":"backups"}`)
		assert.Equal(t, 200, w.Code, route)
		assert.Equal(t, ops, log.ops, route)
	}
}

func TestCopyObject_OrderAndOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, log, args := moveCopyHarness(t, 1, nil, nil, nil)
	w := runMoveCopy(h, "/storage/v1/object/copy", copyBody)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, []string{"exec", "copy:avatars/old.jpg>backups/copy.jpg", "commit", "rollback"}, log.ops)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", (*args)[4], "copy must be owned by the caller")
	assert.JSONEq(t, `{"Key":"backups/copy.jpg"}`, w.Body.String())
}

func TestCopyObject_AnonOwnerIsNull(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, args := moveCopyHarness(t, 1, nil, nil, nil)
	w := runMoveCopyAs(h, domain.Session{Role: "anon"}, "/storage/v1/object/copy", copyBody)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Nil(t, (*args)[4], "empty user id must bind as NULL, not ''")
}

func TestCopyObject_Denials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name string
		n    int64
		err  error
		want int
	}{
		{"source hidden by RLS", 0, nil, 404},
		{"dest insert denied", 0, errors.New(`new row violates row-level security policy for table "objects" (SQLSTATE 42501)`), 403},
		{"db down", 0, errors.New("conn reset"), 500},
	}
	for _, tc := range cases {
		h, log, _ := moveCopyHarness(t, tc.n, tc.err, nil, nil)
		w := runMoveCopy(h, "/storage/v1/object/copy", copyBody)
		assert.Equal(t, tc.want, w.Code, tc.name)
		assert.Equal(t, []string{"exec", "rollback"}, log.ops, "%s: store touched before authorization", tc.name)
	}
}

func TestCopyObject_CopyFailureNoCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, log, _ := moveCopyHarness(t, 1, nil, errors.New("s3 down"), nil)
	w := runMoveCopy(h, "/storage/v1/object/copy", copyBody)
	assert.Equal(t, 500, w.Code)
	assert.Equal(t, []string{"exec", "copy:avatars/old.jpg>backups/copy.jpg", "rollback"}, log.ops)
}

func TestCopyObject_CommitFailureIs500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, _ := moveCopyHarness(t, 1, nil, nil, errors.New("commit failed"))
	w := runMoveCopy(h, "/storage/v1/object/copy", copyBody)
	assert.Equal(t, 500, w.Code)
}

func TestNullIfEmpty(t *testing.T) {
	assert.Nil(t, nullIfEmpty(""))
	assert.Equal(t, "u", nullIfEmpty("u"))
	assert.Equal(t, " ", nullIfEmpty(" "))
}

// --- createSignedURLs (batch) tests ---

func TestCreateSignedURLs_EnforcesRLSPerPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var signed []string
	store := &stubObjectStore{
		signDownloadFn: func(_ context.Context, key string, _ time.Duration) (string, error) {
			signed = append(signed, key)
			if strings.Contains(key, "broken") {
				return "", errors.New("presign failed: secret detail")
			}
			return "https://example.com/" + key, nil
		},
	}
	var gotArgs []any
	db := &stubDB{queryFn: func(_ context.Context, q string, args ...any) ([]map[string]any, error) {
		require.Contains(t, q, "SELECT name FROM storage.objects")
		gotArgs = args
		// RLS hides "secret.jpg"; everything else in the ANY list is visible.
		return []map[string]any{{"name": "good.jpg"}, {"name": "ünï.png"}, {"name": "broken.jpg"}}, nil
	}}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {}})
	r := gin.New()
	r.POST("/storage/v1/object/sign/:bucket", h.createSignedURLs)

	body := `{"expiresIn":60,"paths":["good.jpg","secret.jpg","../x","ünï.png","broken.jpg"]}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/sign/avatars", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code, w.Body.String())
	require.Len(t, gotArgs, 2)
	assert.Equal(t, []string{"good.jpg", "secret.jpg", "ünï.png", "broken.jpg"}, gotArgs[1], "traversal key must not reach SQL")
	assert.Equal(t, []string{"avatars/good.jpg", "avatars/ünï.png", "avatars/broken.jpg"}, signed, "hidden path must never be signed")
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 5)
	assert.NotNil(t, resp[0]["signedURL"])
	assert.Nil(t, resp[0]["error"])
	for _, i := range []int{1, 2} {
		assert.Nil(t, resp[i]["signedURL"], resp[i])
		assert.Equal(t, "Either the object does not exist or you do not have access to it", resp[i]["error"])
	}
	assert.NotNil(t, resp[3]["signedURL"])
	assert.Nil(t, resp[4]["signedURL"])
	assert.NotContains(t, w.Body.String(), "secret detail", "backend error must not leak")
}

func TestCreateSignedURLs_BadInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	queried := false
	db := &stubDB{queryFn: func(context.Context, string, ...any) ([]map[string]any, error) { queried = true; return nil, nil }}
	h := newStorageHandler(db, &stubObjectStore{}, map[string]domain.Bucket{"avatars": {}})
	r := gin.New()
	r.POST("/storage/v1/object/sign/:bucket", h.createSignedURLs)
	tooMany, _ := json.Marshal(map[string]any{"paths": make([]string, maxSignPaths+1)})
	for _, body := range []string{`{"paths":[]}`, `{}`, `not json`, string(tooMany), `{"expiresIn":"abc","paths":["a"]}`} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/sign/avatars", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		assert.Equal(t, 400, w.Code, body)
	}
	assert.False(t, queried)
}

func TestCreateSignedURLs_DBErrorIs500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryFn: func(context.Context, string, ...any) ([]map[string]any, error) { return nil, errors.New("db down") }}
	h := newStorageHandler(db, &stubObjectStore{}, map[string]domain.Bucket{"avatars": {}})
	r := gin.New()
	r.POST("/storage/v1/object/sign/:bucket", h.createSignedURLs)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/sign/avatars", strings.NewReader(`{"paths":["a"]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, 500, w.Code)
}

func TestCreateSignedURLs_ExactCapAndDuplicates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	queries := 0
	db := &stubDB{queryFn: func(_ context.Context, _ string, args ...any) ([]map[string]any, error) {
		queries++
		keys := args[1].([]string)
		rows := make([]map[string]any, len(keys))
		for i, k := range keys {
			rows[i] = map[string]any{"name": k}
		}
		return rows, nil
	}}
	h := newStorageHandler(db, &stubObjectStore{}, map[string]domain.Bucket{"avatars": {}})
	r := gin.New()
	r.POST("/storage/v1/object/sign/:bucket", h.createSignedURLs)

	// Exactly maxSignPaths valid paths must pass (not >=).
	paths := make([]string, maxSignPaths)
	for i := range paths {
		paths[i] = fmt.Sprintf("f%d.jpg", i)
	}
	body, _ := json.Marshal(map[string]any{"paths": paths})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/sign/avatars", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())

	// Duplicate paths each get their own result, deduped into one SQL call.
	queries = 0
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/storage/v1/object/sign/avatars", strings.NewReader(`{"paths":["a","a","/a"]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 3)
	for _, item := range resp {
		assert.NotNil(t, item["signedURL"], item)
	}
	assert.Equal(t, 1, queries, "one SQL lookup regardless of duplicate paths")
}

func TestCreateSignedURLs_ExpiryClamped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var got time.Duration
	store := &stubObjectStore{signDownloadFn: func(_ context.Context, _ string, e time.Duration) (string, error) { got = e; return "u", nil }}
	db := &stubDB{queryFn: func(context.Context, string, ...any) ([]map[string]any, error) {
		return []map[string]any{{"name": "a"}}, nil
	}}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {}})
	r := gin.New()
	r.POST("/storage/v1/object/sign/:bucket", h.createSignedURLs)
	cases := map[string]time.Duration{
		`{"expiresIn":60,"paths":["a"]}`:       time.Minute,
		`{"paths":["a"]}`:                      time.Hour,
		`{"expiresIn":31536000,"paths":["a"]}`: 7 * 24 * time.Hour,
	}
	for body, want := range cases {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/sign/avatars", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, body)
		assert.Equal(t, want, got, body)
	}
}

func TestSignedExpiry(t *testing.T) {
	week := 7 * 24 * time.Hour
	cases := map[int]time.Duration{
		60: time.Minute, 0: time.Hour, -5: time.Hour, maxSignedURLExpiry: week,
		maxSignedURLExpiry + 1: week, 1 << 62: week,
	}
	for in, want := range cases {
		assert.Equal(t, want, signedExpiry(in), in)
	}
}

func TestCreateSignedURL_ExpiryClamped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var got time.Duration
	store := &stubObjectStore{signDownloadFn: func(_ context.Context, _ string, e time.Duration) (string, error) { got = e; return "u", nil }}
	db := &stubDB{queryRowFn: func(context.Context, string, ...any) (map[string]any, error) { return map[string]any{"id": "x"}, nil }}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {}})
	r := gin.New()
	r.POST("/storage/v1/object/sign/:bucket/*path", h.createSignedURL)
	cases := map[string]time.Duration{
		`{"expiresIn":60}`: time.Minute, `{}`: time.Hour, `{"expiresIn":-5}`: time.Hour,
		`{"expiresIn":31536000}`: 7 * 24 * time.Hour, `{"expiresIn":9223372036854775807}`: 7 * 24 * time.Hour,
		`{"expiresIn":"abc"}`: time.Hour,
	}
	for body, want := range cases {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/sign/avatars/a.txt", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, body)
		assert.Equal(t, want, got, body)
	}
}

func TestStorageErrShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	storageErr(c, 404, "not_found", `Bucket "x" not found`)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// statusCode MUST be the string "404" (storage-js contract), not a number.
	if body["statusCode"] != "404" {
		t.Errorf("statusCode = %#v, want string \"404\"", body["statusCode"])
	}
	if body["error"] != "not_found" {
		t.Errorf("error = %#v", body["error"])
	}
	if body["message"] != `Bucket "x" not found` {
		t.Errorf("message = %#v", body["message"])
	}
}

// --- Object key validation (C1) ---

func TestCleanPath(t *testing.T) {
	ok := map[string]string{
		"a.txt":          "a.txt",
		"/a.txt":         "a.txt",
		"//etc/passwd":   "etc/passwd",
		"dir/./file":     "dir/file",
		"dir//file":      "dir/file",
		"dir/":           "dir",
		"ünï/çødé 😀.png": "ünï/çødé 😀.png",
		"a..b/c...":      "a..b/c...",
	}
	for in, want := range ok {
		got, err := cleanPath(in)
		if err != nil || got != want {
			t.Errorf("cleanPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "/", ".", "..", "../x", "a/../../x", "a/..", "/../etc", "a\x00b"} {
		if got, err := cleanPath(in); err == nil {
			t.Errorf("cleanPath(%q) = %q, want error", in, got)
		}
	}

	longSeg := strings.Repeat("a", 5000)
	if got, err := cleanPath(longSeg); err != nil || got != longSeg {
		t.Errorf("cleanPath(long segment) = %q, %v; want unchanged", got, err)
	}
	deep := strings.Repeat("a/", 1000) + "file"
	if got, err := cleanPath(deep); err != nil || got != deep {
		t.Errorf("cleanPath(deep path) = %q, %v; want unchanged", got, err)
	}
}

func TestStorageRoutes_RejectTraversalKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("INSTANCEZ_SECRET_KEY", "test-secret-key")
	touched := false
	store := &stubObjectStore{
		uploadFn: func(context.Context, string, io.Reader, string, int64) error { touched = true; return nil },
		downloadFn: func(context.Context, string) (io.ReadCloser, string, error) {
			touched = true
			return io.NopCloser(strings.NewReader("")), "", nil
		},
		copyFn: func(context.Context, string, string) error { touched = true; return nil },
	}
	db := &stubDB{
		beginFn: func(context.Context) (domain.Tx, error) { touched = true; return &stubTx{}, nil },
		queryRowFn: func(context.Context, string, ...any) (map[string]any, error) {
			touched = true
			return map[string]any{"id": "x"}, nil
		},
	}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {Public: true}})
	h.jwtKeys = stubKeys(t)
	// Bound to the raw (uncleaned) wildcard param gin hands the handler, so the
	// case below tests the objectPath check itself, not token/path mismatch.
	forgedTraversalToken := h.signUploadToken("avatars", "/../secret", "")
	auth := func(c *gin.Context) {
		setTestSession(c, domain.Session{Role: "authenticated", UserID: "u1", IsAuthenticated: true})
	}
	r := gin.New()
	r.POST("/storage/v1/object/move", auth, h.moveObject)
	r.POST("/storage/v1/object/copy", auth, h.copyObject)
	r.POST("/storage/v1/object/sign/:bucket/*path", auth, h.createSignedURL)
	r.POST("/storage/v1/object/:bucket/*path", auth, h.uploadObject)
	r.HEAD("/storage/v1/object/:bucket/*path", auth, h.objectExists)
	r.POST("/storage/v1/object/upload/sign/:bucket/*path", auth, h.createSignedUploadURL)
	r.PUT("/storage/v1/object/upload/sign/:bucket/*path", h.uploadToSignedURL)
	r.GET("/storage/v1/object/*all", h.objectGetDispatch)

	cases := []struct {
		method, target, body string
		apikey               string
	}{
		{http.MethodPost, "/storage/v1/object/avatars/%2e%2e/%2e%2e/etc/passwd", "x", ""},
		{http.MethodPost, "/storage/v1/object/avatars/..%2f..%2fetc", "x", ""},
		{http.MethodGet, "/storage/v1/object/public/avatars/%2e%2e/secret", "", ""},
		{http.MethodPost, "/storage/v1/object/sign/avatars/a/../../b", "{}", ""},
		{http.MethodPost, "/storage/v1/object/move", `{"bucketId":"avatars","sourceKey":"a.txt","destinationKey":"../../x"}`, ""},
		{http.MethodPost, "/storage/v1/object/copy", `{"bucketId":"avatars","sourceKey":"../a","destinationKey":"b"}`, ""},
		{http.MethodPost, "/storage/v1/object/copy", `{"bucketId":"avatars","sourceKey":"","destinationKey":"b"}`, ""},
		{http.MethodHead, "/storage/v1/object/avatars/%2e%2e/secret", "", ""},
		{http.MethodGet, "/storage/v1/object/info/authenticated/avatars/%2e%2e/secret", "", "test-secret-key"},
		{http.MethodPost, "/storage/v1/object/upload/sign/avatars/%2e%2e/secret", "", ""},
		{http.MethodPut, "/storage/v1/object/upload/sign/avatars/%2e%2e/secret?token=" + forgedTraversalToken, "", ""},
	}
	for _, tc := range cases {
		touched = false
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		if tc.apikey != "" {
			req.Header.Set("apikey", tc.apikey)
		}
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s %s: got %d, want 400: %s", tc.method, tc.target, w.Code, w.Body.String())
		}
		if touched {
			t.Errorf("%s %s: reached DB or object store", tc.method, tc.target)
		}
	}
}

// --- C7: spooled uploads ---

func multipartBody(t *testing.T, filename, ct, content string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("cacheControl", "3600"))
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name=""; filename=%q`, filename))
	hdr.Set("Content-Type", ct)
	pw, err := mw.CreatePart(hdr)
	require.NoError(t, err)
	_, _ = pw.Write([]byte(content))
	require.NoError(t, mw.Close())
	return &buf, mw.FormDataContentType()
}

// isolatedTempDir points os.CreateTemp at a fresh dir so a test can check for leaked spool files.
func isolatedTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	return dir
}

func assertNoSpoolLeft(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "spool file leaked")
}

func TestUploadObject_TxOpensOnlyAfterBodyFinishes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := isolatedTempDir(t)
	var began, bodyClosed atomic.Bool
	tx := &stubTx{execFn: func(context.Context, string, ...any) (int64, error) { return 1, nil }}
	db := &stubDB{beginFn: func(context.Context) (domain.Tx, error) {
		assert.True(t, bodyClosed.Load(), "DB tx opened while the client body was still streaming")
		began.Store(true)
		return tx, nil
	}}
	var stored string
	var storedSize int64
	store := &stubObjectStore{uploadFn: func(_ context.Context, _ string, r io.Reader, _ string, size int64) error {
		_, ok := r.(*os.File)
		assert.True(t, ok, "store should receive the spooled file")
		b, _ := io.ReadAll(r)
		stored, storedSize = string(b), size
		return nil
	}}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {}})
	r := gin.New()
	r.POST("/storage/v1/object/:bucket/*path", h.uploadObject)

	pr, pw := io.Pipe()
	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/avatars/a.txt", pr)
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { r.ServeHTTP(w, req); close(done) }()

	// Each Write returns only once the handler has read it, so the handler is mid-body here.
	_, _ = pw.Write([]byte("slow "))
	_, _ = pw.Write([]byte("client "))
	assert.False(t, began.Load(), "tx began before the body finished")
	_, _ = pw.Write([]byte("body"))
	bodyClosed.Store(true)
	_ = pw.Close()
	<-done

	require.Equal(t, 200, w.Code, w.Body.String())
	assert.True(t, began.Load())
	assert.Equal(t, "slow client body", stored)
	assert.Equal(t, int64(len(stored)), storedSize)
	assertNoSpoolLeft(t, dir)
}

func TestUploadObject_ClientAbortNeverOpensTx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := isolatedTempDir(t)
	began := false
	db := &stubDB{beginFn: func(context.Context) (domain.Tx, error) { began = true; return &stubTx{}, nil }}
	h := newStorageHandler(db, &stubObjectStore{}, map[string]domain.Bucket{"avatars": {}})
	r := gin.New()
	r.POST("/storage/v1/object/:bucket/*path", h.uploadObject)

	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("partial"))
		_ = pw.CloseWithError(io.ErrUnexpectedEOF)
	}()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/storage/v1/object/avatars/a.txt", pr))

	assert.Equal(t, 500, w.Code)
	assert.False(t, began, "tx opened for an aborted body")
	assertNoSpoolLeft(t, dir)
}

func TestUploadObject_SpoolRemovedOnPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := isolatedTempDir(t)
	tx := &stubTx{execFn: func(context.Context, string, ...any) (int64, error) { return 1, nil }}
	db := &stubDB{beginFn: func(context.Context) (domain.Tx, error) { return tx, nil }}
	store := &stubObjectStore{uploadFn: func(context.Context, string, io.Reader, string, int64) error { panic("boom") }}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {}})
	r := gin.New()
	r.POST("/storage/v1/object/:bucket/*path", h.uploadObject)

	assert.Panics(t, func() {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/storage/v1/object/avatars/a.txt", strings.NewReader("x")))
	})
	assertNoSpoolLeft(t, dir)
}

func TestUploadObject_TooLargeRejectedBeforeTx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := isolatedTempDir(t)
	var began, streamed bool
	tx := &stubTx{execFn: func(context.Context, string, ...any) (int64, error) { return 1, nil }}
	db := &stubDB{beginFn: func(context.Context) (domain.Tx, error) { began = true; return tx, nil }}
	store := &stubObjectStore{uploadFn: func(context.Context, string, io.Reader, string, int64) error { streamed = true; return nil }}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {MaxSize: "1KB"}})
	r := gin.New()
	r.POST("/storage/v1/object/:bucket/*path", h.uploadObject)
	send := func(n int, multi bool) *httptest.ResponseRecorder {
		began, streamed = false, false
		var req *http.Request
		if multi {
			body, ct := multipartBody(t, "blob", "text/plain", strings.Repeat("x", n))
			req = httptest.NewRequest(http.MethodPost, "/storage/v1/object/avatars/a.bin", body)
			req.Header.Set("Content-Type", ct)
		} else {
			req = httptest.NewRequest(http.MethodPost, "/storage/v1/object/avatars/a.bin", strings.NewReader(strings.Repeat("x", n)))
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	for _, multi := range []bool{false, true} {
		for _, n := range []int{1025, 4096} {
			w := send(n, multi)
			assert.Equal(t, 413, w.Code, "%d bytes multipart=%v", n, multi)
			assert.Contains(t, w.Body.String(), "1KB")
			assert.False(t, began, "%d bytes: tx opened for an oversized body", n)
			assert.False(t, streamed, "%d bytes: oversized body reached the store", n)
		}
		w := send(1024, multi)
		assert.Equal(t, 200, w.Code, "exactly max_size must be accepted (multipart=%v)", multi)
		assert.True(t, began && streamed)
	}
	assertNoSpoolLeft(t, dir)
}

func TestUploadObject_TooLargeDefaultLimitNamesIt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newStorageHandler(&stubDB{}, &stubObjectStore{}, map[string]domain.Bucket{"avatars": {}})
	r := gin.New()
	r.POST("/storage/v1/object/:bucket/*path", h.uploadObject)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/storage/v1/object/avatars/a.bin", io.LimitReader(zeroes{}, 50<<20+1)))
	assert.Equal(t, 413, w.Code)
	assert.Contains(t, w.Body.String(), "50MB")
}

type zeroes struct{}

func (zeroes) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestUploadObject_RecordsRealSize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name, content string
		multipart     bool
	}{
		{"multipart", "hello world", true},
		{"raw", "hello world", false},
		{"raw empty", "", false},
		{"multipart empty", "", true},
		{"multipart unicode", "ünïcødé 😀", true},
	}
	for _, tc := range cases {
		var args []any
		stored := "<not called>"
		tx := &stubTx{execFn: func(_ context.Context, _ string, a ...any) (int64, error) { args = a; return 1, nil }}
		db := &stubDB{beginFn: func(context.Context) (domain.Tx, error) { return tx, nil }}
		store := &stubObjectStore{uploadFn: func(_ context.Context, _ string, r io.Reader, _ string, _ int64) error {
			b, _ := io.ReadAll(r)
			stored = string(b)
			return nil
		}}
		h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {}})
		r := gin.New()
		r.POST("/storage/v1/object/:bucket/*path", h.uploadObject)
		var req *http.Request
		if tc.multipart {
			body, ct := multipartBody(t, "blob", "text/plain", tc.content)
			req = httptest.NewRequest(http.MethodPost, "/storage/v1/object/avatars/f.txt", body)
			req.Header.Set("Content-Type", ct)
		} else {
			req = httptest.NewRequest(http.MethodPost, "/storage/v1/object/avatars/f.txt", strings.NewReader(tc.content))
			req.Header.Set("Content-Type", "text/plain")
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, tc.name+": "+w.Body.String())
		assert.Equal(t, tc.content, stored, tc.name)
		require.Len(t, args, 6, tc.name)
		assert.Equal(t, int64(len(tc.content)), args[2], tc.name)
		assert.Equal(t, "text/plain", args[3], tc.name)
		assert.Contains(t, args[4], fmt.Sprintf(`"size":%d`, len(tc.content)), tc.name)
	}
}

func TestUploadObject_ConflictSemantics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name, method, upsert string
		want, notWant        string
	}{
		{"insert", http.MethodPost, "", "INSERT", "ON CONFLICT"},
		{"insert x-upsert false", http.MethodPost, "false", "INSERT", "ON CONFLICT"},
		{"upsert", http.MethodPost, "true", "DO UPDATE", "UPDATE storage.objects"},
		{"update ignores x-upsert", http.MethodPut, "true", "UPDATE storage.objects", "INSERT"},
	}
	for _, tc := range cases {
		var q string
		tx := &stubTx{execFn: func(_ context.Context, query string, _ ...any) (int64, error) { q = query; return 1, nil }}
		db := &stubDB{beginFn: func(context.Context) (domain.Tx, error) { return tx, nil }}
		h := newStorageHandler(db, &stubObjectStore{}, map[string]domain.Bucket{"avatars": {}})
		r := gin.New()
		r.POST("/storage/v1/object/:bucket/*path", h.uploadObject)
		r.PUT("/storage/v1/object/:bucket/*path", h.updateObject)
		req := httptest.NewRequest(tc.method, "/storage/v1/object/avatars/f.txt", strings.NewReader("x"))
		if tc.upsert != "" {
			req.Header.Set("x-upsert", tc.upsert)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, tc.name)
		assert.Contains(t, q, tc.want, tc.name)
		assert.NotContains(t, q, tc.notWant, tc.name)
	}
}

func TestWriteObjectRow(t *testing.T) {
	row := objectRow{bucket: "b", name: "n", size: 3, mime: "text/plain", metadata: "{}", uploadedBy: nil}
	cases := []struct {
		name             string
		isUpdate, upsert bool
		rows             int64
		execErr, want    error
	}{
		{"update hits a row", true, false, 1, nil, nil},
		{"update hits nothing", true, false, 0, nil, errObjectNotFound},
		{"update error wins over zero rows", true, false, 0, errors.New("rls"), errors.New("rls")},
		{"insert zero rows is fine", false, false, 0, nil, nil},
		{"upsert zero rows is fine", false, true, 0, nil, nil},
	}
	for _, tc := range cases {
		var args []any
		db := &stubDB{execFn: func(_ context.Context, _ string, a ...any) (int64, error) { args = a; return tc.rows, tc.execErr }}
		err := writeObjectRow(context.Background(), db, row, tc.isUpdate, tc.upsert)
		if tc.want == nil {
			assert.NoError(t, err, tc.name)
		} else {
			assert.EqualError(t, err, tc.want.Error(), tc.name)
		}
		assert.Equal(t, []any{"b", "n", int64(3), "text/plain", "{}", nil}, args, tc.name)
	}
}

func TestUploadToSignedURL_Hardening(t *testing.T) {
	gin.SetMode(gin.TestMode)
	newH := func(b domain.Bucket, execErr error) (*StorageV1Handler, *string, *[]any) {
		stored := "<not called>"
		var args []any
		db := &stubDB{execFn: func(_ context.Context, _ string, a ...any) (int64, error) { args = a; return 1, execErr }}
		store := &stubObjectStore{uploadFn: func(_ context.Context, _ string, r io.Reader, _ string, _ int64) error {
			b, _ := io.ReadAll(r)
			stored = string(b)
			return nil
		}}
		h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": b})
		h.jwtKeys = stubKeys(t)
		return h, &stored, &args
	}
	do := func(h *StorageV1Handler, body io.Reader, ct string) *httptest.ResponseRecorder {
		token := h.signUploadToken("avatars", "f.txt", "")
		r := gin.New()
		r.PUT("/storage/v1/object/upload/sign/:bucket/*path", h.uploadToSignedURL)
		req := httptest.NewRequest(http.MethodPut, "/storage/v1/object/upload/sign/avatars/f.txt?token="+token, body)
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	t.Run("raw MIME outside allowlist", func(t *testing.T) {
		h, stored, args := newH(domain.Bucket{Types: []string{"image/*"}}, nil)
		w := do(h, strings.NewReader("<script>"), "text/html")
		assert.Equal(t, 422, w.Code, "MIME allowlist bypassed on signed upload")
		assert.Equal(t, "<not called>", *stored)
		assert.Nil(t, *args)
	})
	t.Run("multipart MIME outside allowlist", func(t *testing.T) {
		h, stored, _ := newH(domain.Bucket{Types: []string{"image/*"}}, nil)
		body, ct := multipartBody(t, "x.html", "text/html", "<script>")
		w := do(h, body, ct)
		assert.Equal(t, 422, w.Code)
		assert.Equal(t, "<not called>", *stored)
	})
	t.Run("multipart stores the file part and its size", func(t *testing.T) {
		h, stored, args := newH(domain.Bucket{Types: []string{"text/plain"}}, nil)
		body, ct := multipartBody(t, "blob", "text/plain", "blob body")
		w := do(h, body, ct)
		require.Equal(t, 200, w.Code, w.Body.String())
		assert.Equal(t, "blob body", *stored, "multipart framing stored as file content")
		require.Len(t, *args, 6)
		assert.Equal(t, int64(len("blob body")), (*args)[2])
		assert.Nil(t, (*args)[5], "anonymous token must record NULL uploaded_by")
	})
	t.Run("over max_size", func(t *testing.T) {
		h, stored, _ := newH(domain.Bucket{MaxSize: "1KB"}, nil)
		w := do(h, strings.NewReader(strings.Repeat("x", 1025)), "text/plain")
		assert.Equal(t, 413, w.Code)
		assert.Equal(t, "<not called>", *stored)
	})
	t.Run("failed metadata insert", func(t *testing.T) {
		h, _, _ := newH(domain.Bucket{}, errors.New("db down"))
		w := do(h, strings.NewReader("x"), "text/plain")
		assert.Equal(t, 500, w.Code, "failed metadata insert reported as success")
	})
}

// --- Download headers (C8) ---

func TestSetDownloadHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const pub, priv = "public, max-age=3600", "private, max-age=3600"
	cases := []struct {
		ct             string
		public         bool
		wantCT, wantCC string
		attach         bool
	}{
		{"image/png", true, "image/png", pub, false},
		{"image/png", false, "image/png", priv, false},
		{"text/plain", false, "text/plain", priv, false},
		{"text/html; charset=utf-8", false, "text/html; charset=utf-8", priv, true},
		{"TEXT/HTML", true, "TEXT/HTML", pub, true},
		{"image/svg+xml", true, "image/svg+xml", pub, true},
		{"application/xhtml+xml", true, "application/xhtml+xml", pub, true},
		{"text/xml", true, "text/xml", pub, true},
		{"application/javascript", true, "application/javascript", pub, true},
		{"application/rss+xml", true, "application/rss+xml", pub, true},
		{"application/mathml+xml", true, "application/mathml+xml", pub, true},
		{"html", false, "application/octet-stream", priv, false},
		{"", false, "application/octet-stream", priv, false},
		{"not a mime", false, "application/octet-stream", priv, false},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		setDownloadHeaders(c, tc.ct, tc.public)
		hd := w.Header()
		assert.Equal(t, tc.wantCT, hd.Get("Content-Type"), tc.ct)
		assert.Equal(t, tc.wantCC, hd.Get("Cache-Control"), tc.ct)
		assert.Equal(t, "nosniff", hd.Get("X-Content-Type-Options"), tc.ct)
		assert.Equal(t, tc.attach, strings.HasPrefix(hd.Get("Content-Disposition"), "attachment"), tc.ct)
	}
}

func TestServeDownload_PrivateHTMLIsAttachmentAndPrivate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryRowFn: func(context.Context, string, ...any) (map[string]any, error) { return map[string]any{"id": "x"}, nil }}
	store := &stubObjectStore{downloadFn: func(context.Context, string) (io.ReadCloser, string, error) {
		return io.NopCloser(strings.NewReader("<script>alert(1)</script>")), "text/html", nil
	}}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"docs": {Public: false}})
	r := gin.New()
	r.GET("/dl/:bucket/*path", func(c *gin.Context) { h.serveDownload(c, c.Param("bucket"), c.Param("path"), false) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dl/docs/x.html", nil))
	require.Equal(t, 200, w.Code)
	assert.Equal(t, "private, max-age=3600", w.Header().Get("Cache-Control"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.True(t, strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment"))
}
