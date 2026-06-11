package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/domain"
)

// --- stubObjectStore ---

type stubObjectStore struct {
	signDownloadFn func(ctx context.Context, key string, expiry time.Duration) (string, error)
	deleteFn       func(ctx context.Context, key string) error
}

func (s *stubObjectStore) SignUpload(ctx context.Context, key, contentType string, expiry time.Duration) (string, error) {
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
	return nil
}
func (s *stubObjectStore) Download(ctx context.Context, key string) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader("")), "application/octet-stream", nil
}
func (s *stubObjectStore) Copy(ctx context.Context, srcKey, dstKey string) error { return nil }
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

func TestEmptyBucket_DeletesObjects(t *testing.T) {
	gin.SetMode(gin.TestMode)

	deleted := []string{}
	store := &stubObjectStore{
		deleteFn: func(ctx context.Context, key string) error {
			deleted = append(deleted, key)
			return nil
		},
	}

	db := &stubDB{
		queryFn: func(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
			// Return one object named "photo.jpg"
			return []map[string]any{{"name": "photo.jpg"}}, nil
		},
	}

	buckets := map[string]domain.Bucket{
		"avatars": {Public: false},
	}
	h := newStorageHandler(db, store, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/bucket/:id/empty", func(c *gin.Context) {
		setTestSession(c, domain.Session{Role: "service_role"})
		h.emptyBucket(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/bucket/avatars/empty", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(deleted) != 1 || deleted[0] != "avatars/photo.jpg" {
		t.Errorf("expected Delete called with avatars/photo.jpg, got %v", deleted)
	}
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
