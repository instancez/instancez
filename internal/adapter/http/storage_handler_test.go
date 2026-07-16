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
		signUploadFn: func(ctx context.Context, key, contentType string, expiry time.Duration) (string, error) {
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
		signDownloadFn: func(ctx context.Context, key string, expiry time.Duration) (string, error) {
			return "https://example.com/download?sig=xyz", nil
		},
	}
	h := newLegacyStorageHandler(db, store)

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/storage/avatars/:id", h.handleSignDownload("avatars", domain.Bucket{}))

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
	r.GET("/storage/avatars/:id", h.handleSignDownload("avatars", domain.Bucket{}))

	req := httptest.NewRequest(http.MethodGet, "/storage/avatars/missing.jpg", nil)
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleDelete_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var deletedKey string
	store := &stubObjectStore{
		deleteFn: func(ctx context.Context, key string) error { deletedKey = key; return nil },
	}
	h := newLegacyStorageHandler(&stubDB{}, store)

	w := httptest.NewRecorder()
	r := gin.New()
	r.DELETE("/storage/avatars/:id", h.handleDelete("avatars", domain.Bucket{}))

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
	h := newLegacyStorageHandler(&stubDB{}, store)

	w := httptest.NewRecorder()
	r := gin.New()
	r.DELETE("/storage/avatars/:id", h.handleDelete("avatars", domain.Bucket{}))

	req := httptest.NewRequest(http.MethodDelete, "/storage/avatars/photo.jpg", nil)
	r.ServeHTTP(w, req)

	if w.Code != 500 {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}
