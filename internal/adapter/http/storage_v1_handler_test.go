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

func TestUploadObject_StoreUploadTooLarge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &stubObjectStore{
		uploadFn: func(ctx context.Context, key string, r io.Reader, contentType string, size int64) error {
			return errors.New("http: request body too large")
		},
	}
	tx := &stubTx{execFn: func(ctx context.Context, q string, args ...any) (int64, error) { return 1, nil }}
	db := &stubDB{beginFn: func(ctx context.Context) (domain.Tx, error) { return tx, nil }}
	h := newStorageHandler(db, store, map[string]domain.Bucket{"avatars": {MaxSize: "1kb"}})

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/:bucket/*path", h.uploadObject)

	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/avatars/photo.jpg", strings.NewReader("data"))
	r.ServeHTTP(w, req)

	if w.Code != 413 {
		t.Fatalf("expected 413, got %d: %s", w.Code, w.Body.String())
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

func TestRemoveObjects_DeletesMatchingPrefixes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var deletedKeys []string
	store := &stubObjectStore{
		deleteFn: func(ctx context.Context, key string) error {
			deletedKeys = append(deletedKeys, key)
			return nil
		},
	}
	db := &stubDB{queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
		name, _ := args[1].(string)
		if name == "missing.jpg" {
			return nil, nil
		}
		return map[string]any{"id": "x", "name": name, "bucket_id": "avatars"}, nil
	}}
	buckets := map[string]domain.Bucket{"avatars": {}}
	h := newStorageHandler(db, store, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.DELETE("/storage/v1/object/:bucket", h.removeObjects)

	req := httptest.NewRequest(http.MethodDelete, "/storage/v1/object/avatars",
		strings.NewReader(`{"prefixes":["photo.jpg","missing.jpg"]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(deletedKeys) != 1 || deletedKeys[0] != "avatars/photo.jpg" {
		t.Errorf("expected only the found object deleted, got %v", deletedKeys)
	}
	var body []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 {
		t.Fatalf("expected 1 deleted entry in response, got %d: %v", len(body), body)
	}
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

func TestMoveObject_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotSrc, gotDst string
	store := &stubObjectStore{
		copyFn: func(ctx context.Context, srcKey, dstKey string) error {
			gotSrc, gotDst = srcKey, dstKey
			return nil
		},
	}
	var deletedKey string
	store.deleteFn = func(ctx context.Context, key string) error { deletedKey = key; return nil }
	buckets := map[string]domain.Bucket{"avatars": {}}
	h := newStorageHandler(&stubDB{}, store, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/move", h.moveObject)

	body := `{"bucketId":"avatars","sourceKey":"old.jpg","destinationKey":"new.jpg"}`
	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotSrc != "avatars/old.jpg" || gotDst != "avatars/new.jpg" {
		t.Errorf("Copy(src, dst) = (%q, %q)", gotSrc, gotDst)
	}
	if deletedKey != "avatars/old.jpg" {
		t.Errorf("expected source deleted after move, got %q", deletedKey)
	}
}

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

func TestMoveObject_CopyFailurePropagates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &stubObjectStore{
		copyFn: func(ctx context.Context, srcKey, dstKey string) error {
			return errors.New("store unavailable")
		},
	}
	buckets := map[string]domain.Bucket{"avatars": {}}
	h := newStorageHandler(&stubDB{}, store, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/move", h.moveObject)

	body := `{"bucketId":"avatars","sourceKey":"old.jpg","destinationKey":"new.jpg"}`
	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/move", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 500 {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCopyObject_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotSrc, gotDst string
	store := &stubObjectStore{
		copyFn: func(ctx context.Context, srcKey, dstKey string) error {
			gotSrc, gotDst = srcKey, dstKey
			return nil
		},
	}
	buckets := map[string]domain.Bucket{"avatars": {}, "backups": {}}
	h := newStorageHandler(&stubDB{}, store, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/copy", h.copyObject)

	body := `{"bucketId":"avatars","sourceKey":"old.jpg","destinationKey":"copy.jpg","destinationBucket":"backups"}`
	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/copy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotSrc != "avatars/old.jpg" || gotDst != "backups/copy.jpg" {
		t.Errorf("Copy(src, dst) = (%q, %q)", gotSrc, gotDst)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["Key"] != "backups/copy.jpg" {
		t.Errorf("response Key = %v", resp["Key"])
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

// --- createSignedURLs (batch) tests ---

func TestCreateSignedURLs_MixedResults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &stubObjectStore{
		signDownloadFn: func(ctx context.Context, key string, expiry time.Duration) (string, error) {
			if strings.Contains(key, "bad") {
				return "", errors.New("sign failed")
			}
			return "https://example.com/" + key, nil
		},
	}
	buckets := map[string]domain.Bucket{"avatars": {}}
	h := newStorageHandler(&stubDB{}, store, buckets)

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/storage/v1/object/sign/:bucket", h.createSignedURLs)

	body := `{"expiresIn":60,"paths":["good.jpg","bad.jpg"]}`
	req := httptest.NewRequest(http.MethodPost, "/storage/v1/object/sign/avatars", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(resp), resp)
	}
	if resp[0]["signedURL"] == nil {
		t.Errorf("expected signedURL for good.jpg, got %v", resp[0])
	}
	if resp[1]["error"] == nil {
		t.Errorf("expected error for bad.jpg, got %v", resp[1])
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
