package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
)

const storageUploadTokenExpiry = 2 * time.Hour

const (
	maxSignedURLExpiry = 7 * 24 * 3600 // S3 presign limit
	maxSignPaths       = 1000
	errNoObjectAccess  = "Either the object does not exist or you do not have access to it"
)

// signedExpiry defaults non-positive values to one hour and caps at seven days.
func signedExpiry(sec int) time.Duration {
	switch {
	case sec <= 0:
		sec = 3600
	case sec > maxSignedURLExpiry:
		sec = maxSignedURLExpiry
	}
	return time.Duration(sec) * time.Second
}

// StorageV1Handler serves supabase-js compatible /storage/v1/ endpoints.
type StorageV1Handler struct {
	cfg     *domain.Config
	db      domain.Database
	logger  *slog.Logger
	storage domain.ObjectStore
	jwtKeys *app.JWTKeyManager
}

func NewStorageV1Handler(deps ServerDeps) *StorageV1Handler {
	return &StorageV1Handler{
		cfg:     deps.Config,
		db:      deps.DB.Database,
		logger:  deps.Logger,
		storage: deps.Storage,
		jwtKeys: deps.JWTKeys,
	}
}

func (h *StorageV1Handler) Mount(root *gin.RouterGroup) {
	sg := root.Group("/storage/v1")

	// --- Bucket admin ---
	sg.GET("/bucket", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.listBuckets)
	sg.GET("/bucket/:id", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.getBucket)
	sg.POST("/bucket", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.createBucket)
	sg.PUT("/bucket/:id", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.updateBucket)
	sg.DELETE("/bucket/:id", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.deleteBucket)
	sg.POST("/bucket/:id/empty", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.emptyBucket)

	// --- File operations ---
	// Upload (POST) and update (PUT)
	sg.POST("/object/:bucket/*path", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.uploadObject)
	sg.PUT("/object/:bucket/*path", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.updateObject)

	// GET /object/* catch-all — dispatches to public download, authenticated
	// download, or object info based on path prefix. supabase-js sends:
	//   GET /object/<bucket>/<path>          — authenticated download
	//   GET /object/public/<bucket>/<path>   — public download
	//   GET /object/authenticated/<bucket>/<path> — authenticated download (alt)
	//   GET /object/info/[authenticated/]<bucket>/<path> — object info
	// Gin can't register overlapping param routes, so one handler parses them.
	// apikey is checked inside objectGetDispatch itself — public downloads
	// must stay unauthenticated (real Supabase public bucket URLs need zero
	// headers), everything else on this path requires it.
	sg.GET("/object/*all", h.objectGetDispatch)

	// List
	sg.POST("/object/list/:bucket", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.listObjects)
	sg.POST("/object/list-v2/:bucket", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.listObjectsV2)

	// Exists (HEAD)
	sg.HEAD("/object/:bucket/*path", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, false), h.objectExists)

	// Remove (DELETE with paths in body)
	sg.DELETE("/object/:bucket", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.removeObjects)

	// Move & Copy
	sg.POST("/object/move", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.moveObject)
	sg.POST("/object/copy", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.copyObject)

	// Signed URLs
	sg.POST("/object/sign/:bucket/*path", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.createSignedURL)
	sg.POST("/object/sign/:bucket", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.createSignedURLs)

	// Signed upload
	sg.POST("/object/upload/sign/:bucket/*path", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.createSignedUploadURL)
	// Authorized purely by the HMAC-signed token in the query string,
	// matching real Supabase's signed-upload redemption — no apikey.
	sg.PUT("/object/upload/sign/:bucket/*path", h.uploadToSignedURL)
}

// --- Bucket admin handlers ---

func (h *StorageV1Handler) listBuckets(c *gin.Context) {
	var buckets []gin.H
	for name, b := range h.cfg.Storage {
		buckets = append(buckets, gin.H{
			"id":         name,
			"name":       name,
			"public":     b.Public,
			"created_at": time.Now().Format(time.RFC3339),
			"updated_at": time.Now().Format(time.RFC3339),
		})
	}
	if buckets == nil {
		buckets = []gin.H{}
	}
	c.JSON(200, buckets)
}

func (h *StorageV1Handler) getBucket(c *gin.Context) {
	id := c.Param("id")
	b, ok := h.cfg.Storage[id]
	if !ok {
		storageErr(c, 404, "not_found", fmt.Sprintf("Bucket %q not found", id))
		return
	}
	c.JSON(200, gin.H{
		"id":         id,
		"name":       id,
		"public":     b.Public,
		"created_at": time.Now().Format(time.RFC3339),
		"updated_at": time.Now().Format(time.RFC3339),
	})
}

func (h *StorageV1Handler) createBucket(c *gin.Context) {
	// Buckets are YAML-defined; runtime creation is not supported.
	storageErr(c, 400, "not_supported", "Buckets are defined in instancez.yaml. Runtime creation is not supported.")
}

func (h *StorageV1Handler) updateBucket(c *gin.Context) {
	storageErr(c, 400, "not_supported", "Buckets are defined in instancez.yaml. Runtime modification is not supported.")
}

func (h *StorageV1Handler) deleteBucket(c *gin.Context) {
	storageErr(c, 400, "not_supported", "Buckets are defined in instancez.yaml. Runtime deletion is not supported.")
}

func (h *StorageV1Handler) emptyBucket(c *gin.Context) {
	id := c.Param("id")
	if _, ok := h.cfg.Storage[id]; !ok {
		storageErr(c, 404, "not_found", fmt.Sprintf("Bucket %q not found", id))
		return
	}

	// ponytail: one unbatched DELETE; page it by ctid/LIMIT if buckets reach ~100k objects.
	rows, err := h.db.Query(h.rlsCtx(c), "DELETE FROM storage.objects WHERE bucket_id = $1 RETURNING name", id)
	if err != nil {
		h.logger.Error("empty bucket", "error", err)
		storageErr(c, 500, "internal", "Failed to empty bucket")
		return
	}
	deleteBytes(c.Request.Context(), h.storage, h.logger, id, rows)
	c.JSON(200, gin.H{"message": "Successfully emptied"})
}

// deleteBytes removes object bytes for rows whose metadata delete already committed.
func deleteBytes(ctx context.Context, store domain.ObjectStore, logger *slog.Logger, bucket string, rows []map[string]any) {
	for _, row := range rows {
		if err := store.Delete(ctx, bucket+"/"+asString(row["name"])); err != nil {
			logger.Warn("delete object bytes", "bucket", bucket, "error", err)
		}
	}
}

// --- File operation handlers ---

func (h *StorageV1Handler) getBucketConfig(name string) (domain.Bucket, bool) {
	b, ok := h.cfg.Storage[name]
	return b, ok
}

// rlsCtx returns a request context bound to the caller's effective Postgres
// role so that RLS policies on storage.objects are enforced for the query that
// runs under it. Without this, storage queries fall through to the system
// service_role default (BYPASSRLS) and any caller could read or modify any
// object. An unauthenticated request resolves to the `anon` role (only public
// buckets are visible); an admin-key request keeps service_role.
//
// This is the authorization boundary for object access — the metadata row a
// query can see/insert/update/delete is exactly what the bucket's policies
// allow, and the actual S3 bytes are only reachable once the metadata row is.
func (h *StorageV1Handler) rlsCtx(c *gin.Context) context.Context { return rlsContext(h.db, c) }

// rlsContext binds the request context to the caller's role so storage.objects RLS applies.
func rlsContext(db domain.Database, c *gin.Context) context.Context {
	ctx, err := db.WithRLS(c.Request.Context(), getSession(c))
	if err != nil {
		// WithRLS only stashes the session on the context; it does not perform
		// I/O and never errors in practice. Fall back to the raw context.
		return c.Request.Context()
	}
	return ctx
}

var errInvalidKey = errors.New("invalid object key")

// cleanPath normalizes an object key and rejects empty, NUL and ".." keys.
func cleanPath(p string) (string, error) {
	p = strings.TrimLeft(p, "/")
	if p == "" || strings.ContainsRune(p, 0) {
		return "", errInvalidKey
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", errInvalidKey
		}
	}
	if p = path.Clean(p); p == "." {
		return "", errInvalidKey
	}
	return p, nil
}

// objectPath cleans raw and writes a 400 when it is not a valid key.
func objectPath(c *gin.Context, raw string) (string, bool) {
	p, err := cleanPath(raw)
	if err != nil {
		storageErr(c, 400, "invalid_key", "Invalid key")
		return "", false
	}
	return p, true
}

// validKeys cleans ps and drops the paths that fail cleanPath.
func validKeys(ps []string) []string {
	keys := make([]string, 0, len(ps))
	for _, p := range ps {
		if k, err := cleanPath(p); err == nil {
			keys = append(keys, k)
		}
	}
	return keys
}

// objectMetadataJSON builds the exact Supabase storage ObjectMetadata blob
// persisted to storage.objects.metadata on upload. eTag is written empty: the
// row is persisted before the bytes reach the object store (RLS-atomic
// ordering), so the backend ETag isn't known yet. Migration preserves the real
// eTag from the source.
// ponytail: empty eTag at upload; upgrade to a post-commit Head()+jsonb_set if
// clients need live eTags.
func objectMetadataJSON(size int64, contentType, cacheControl string) string {
	if cacheControl == "" {
		cacheControl = "max-age=3600" // Supabase default
	}
	b, _ := json.Marshal(map[string]any{
		"size":           size,
		"contentLength":  size,
		"mimetype":       contentType,
		"cacheControl":   cacheControl,
		"lastModified":   time.Now().UTC().Format(time.RFC3339),
		"eTag":           "",
		"httpStatusCode": 200,
	})
	return string(b)
}

func (h *StorageV1Handler) uploadObject(c *gin.Context) {
	h.doUpload(c, false)
}

func (h *StorageV1Handler) updateObject(c *gin.Context) {
	h.doUpload(c, true)
}

var errObjectNotFound = errors.New("object not found")

type execer interface {
	Exec(ctx context.Context, query string, args ...any) (int64, error)
}

type objectRow struct {
	bucket, name, mime, metadata string
	size                         int64
	uploadedBy                   any
}

// writeObjectRow inserts, upserts or updates one storage.objects row under ctx's role.
func writeObjectRow(ctx context.Context, db execer, r objectRow, isUpdate, upsert bool) error {
	q := "INSERT INTO storage.objects (bucket_id, name, size, mime, metadata, uploaded_by) VALUES ($1, $2, $3, $4, $5::jsonb, $6)"
	switch {
	case isUpdate:
		q = "UPDATE storage.objects SET size = $3, mime = $4, metadata = $5::jsonb, uploaded_at = NOW(), uploaded_by = $6 WHERE bucket_id = $1 AND name = $2"
	case upsert:
		q += " ON CONFLICT (bucket_id, name) DO UPDATE SET size = EXCLUDED.size, mime = EXCLUDED.mime, metadata = EXCLUDED.metadata, uploaded_by = EXCLUDED.uploaded_by, uploaded_at = NOW()"
	}
	n, err := db.Exec(ctx, q, r.bucket, r.name, r.size, r.mime, r.metadata, r.uploadedBy)
	if err == nil && isUpdate && n == 0 {
		return errObjectNotFound
	}
	return err
}

const defaultMaxUpload = 50 << 20

func bucketMaxBytes(b domain.Bucket) int64 {
	if n := parseSizeBytes(b.MaxSize); n > 0 {
		return n
	}
	return defaultMaxUpload
}

func defaultMIME(ct string) string {
	if ct == "" {
		return "application/octet-stream"
	}
	return ct
}

// uploadBody returns the file stream and content type from a raw or multipart upload.
func uploadBody(c *gin.Context) (io.Reader, string, error) {
	if !strings.HasPrefix(c.ContentType(), "multipart/form-data") {
		return c.Request.Body, defaultMIME(c.ContentType()), nil
	}
	mr, err := c.Request.MultipartReader()
	if err != nil {
		return nil, "", errors.New("failed to parse multipart form")
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			return nil, "", errors.New("no file found in multipart upload")
		}
		if part.FileName() != "" {
			return part, defaultMIME(part.Header.Get("Content-Type")), nil
		}
		_ = part.Close()
	}
}

// spool copies r to a temp file so no DB connection waits on a slow client.
func spool(r io.Reader) (*os.File, int64, error) {
	f, err := os.CreateTemp("", "inz-upload-*")
	if err != nil {
		return nil, 0, err
	}
	n, err := io.Copy(f, r)
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		closeSpool(f)
		return nil, 0, err
	}
	return f, n, nil
}

func closeSpool(f *os.File) {
	_ = f.Close()
	_ = os.Remove(f.Name())
}

// readUpload spools a validated body; on !ok the error response is already
// written. authorize, when set, runs after the MIME check but before the
// body is spooled to disk (with the now-known content type), so a denied or
// anonymous caller never gets bytes onto TMPDIR.
func (h *StorageV1Handler) readUpload(c *gin.Context, bucket domain.Bucket, authorize func(contentType string) error) (f *os.File, size int64, contentType string, ok bool) {
	body, contentType, err := uploadBody(c)
	if err != nil {
		storageErr(c, 400, "bad_request", err.Error())
		return nil, 0, "", false
	}
	if len(bucket.Types) > 0 && !matchesMIME(contentType, bucket.Types) {
		storageErr(c, 422, "invalid_mime_type", fmt.Sprintf("Content type %q not allowed", contentType))
		return nil, 0, "", false
	}
	if authorize != nil {
		if err := authorize(contentType); err != nil {
			h.uploadWriteError(c, err)
			return nil, 0, "", false
		}
	}
	f, size, err = spool(http.MaxBytesReader(c.Writer, io.NopCloser(body), bucketMaxBytes(bucket)))
	if err != nil {
		h.spoolFailed(c, err, bucket.MaxSize)
		return nil, 0, "", false
	}
	return f, size, contentType, true
}

func (h *StorageV1Handler) spoolFailed(c *gin.Context, err error, maxSize string) {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		if maxSize == "" {
			maxSize = fmt.Sprintf("%dMB", defaultMaxUpload>>20)
		}
		storageErr(c, 413, "payload_too_large", fmt.Sprintf("File exceeds maximum size of %s", maxSize))
		return
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.Canceled) {
		h.logger.Warn("spool upload aborted by client", "error", err)
		storageErr(c, 400, "bad_request", "Upload aborted before completion")
		return
	}
	h.logger.Error("spool upload", "error", err)
	storageErr(c, 500, "internal", "Upload failed")
}

func (h *StorageV1Handler) doUpload(c *gin.Context, isUpdate bool) {
	bucketName := c.Param("bucket")
	objPath, ok := objectPath(c, c.Param("path"))
	if !ok {
		return
	}
	bucket, ok := h.getBucketConfig(bucketName)
	if !ok {
		storageErr(c, 404, "not_found", "Bucket not found")
		return
	}
	uploadedBy := nullIfEmpty(getSession(c).UserID)
	upsert := !isUpdate && c.GetHeader("x-upsert") == "true"
	file, size, contentType, ok := h.readUpload(c, bucket, func(ct string) error {
		probeRow := objectRow{bucket: bucketName, name: objPath, mime: ct, uploadedBy: uploadedBy,
			metadata: objectMetadataJSON(0, ct, c.GetHeader("Cache-Control"))}
		return h.probeUploadPermission(c, probeRow, isUpdate, upsert)
	})
	if !ok {
		return
	}
	defer closeSpool(file)

	row := objectRow{bucket: bucketName, name: objPath, size: size, mime: contentType,
		uploadedBy: uploadedBy,
		metadata:   objectMetadataJSON(size, contentType, c.GetHeader("Cache-Control"))}

	// The tx write below is the real gate; the pre-spool probe above only avoids spooling for a denied caller.
	ctx := h.rlsCtx(c)
	tx, err := h.db.Begin(ctx)
	if err != nil {
		storageErr(c, 500, "internal", "Upload failed")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := writeObjectRow(ctx, tx, row, isUpdate, upsert); err != nil {
		h.uploadWriteError(c, err)
		return
	}

	key := bucketName + "/" + objPath
	if err := h.storage.Upload(c.Request.Context(), key, file, contentType, size); err != nil {
		h.logger.Error("upload error", "error", err)
		storageErr(c, 500, "internal", "Upload failed")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		_ = h.storage.Delete(c.Request.Context(), key)
		storageErr(c, 500, "internal", "Upload failed")
		return
	}
	c.JSON(200, gin.H{"Key": key, "Id": objPath})
}

// nullIfEmpty binds "" as SQL NULL.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// storageErr writes a storage-js compatible error body: {statusCode, error, message}.
// statusCode is the HTTP status rendered as a string, matching @supabase/storage-js.
func storageErr(c *gin.Context, status int, errSlug, message string) {
	c.JSON(status, gin.H{
		"statusCode": strconv.Itoa(status),
		"error":      errSlug,
		"message":    message,
	})
}

// uploadWriteError maps a failed metadata write to the right client response:
// no row to update → 404, duplicate key → 409, an RLS/permission denial → 403, anything else → 500.
func (h *StorageV1Handler) uploadWriteError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errObjectNotFound):
		storageErr(c, 404, "not_found", "Object not found")
	case isDuplicate(err):
		storageErr(c, 409, "duplicate", "The resource already exists")
	case isPermissionDenied(err):
		storageErr(c, 403, "forbidden", "Not authorized to write this object")
	default:
		h.logger.Error("record object", "error", err)
		storageErr(c, 500, "internal", "Failed to record object")
	}
}

func isDuplicate(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "23505")
}

func isPermissionDenied(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "row-level security") || strings.Contains(msg, "42501") || strings.Contains(msg, "permission denied")
}

func (h *StorageV1Handler) objectGetDispatch(c *gin.Context) {
	all := strings.TrimPrefix(c.Param("all"), "/")
	segments := strings.SplitN(all, "/", 3)

	switch segments[0] {
	case "public":
		if len(segments) < 3 {
			storageErr(c, 400, "bad_request", "Missing bucket or path")
			return
		}
		h.serveDownload(c, segments[1], segments[2], true)
	case "authenticated":
		if len(segments) < 3 {
			storageErr(c, 400, "bad_request", "Missing bucket or path")
			return
		}
		apiKeyGuard(h.jwtKeys)(c)
		if c.IsAborted() {
			return
		}
		jwtAuth(h.jwtKeys, true)(c)
		if c.IsAborted() {
			return
		}
		h.serveDownload(c, segments[1], segments[2], false)
	case "info":
		if len(segments) < 2 {
			storageErr(c, 400, "bad_request", "Missing path")
			return
		}
		apiKeyGuard(h.jwtKeys)(c)
		if c.IsAborted() {
			return
		}
		jwtAuth(h.jwtKeys, true)(c)
		if c.IsAborted() {
			return
		}
		rest := strings.TrimPrefix(strings.TrimPrefix(all, "info/"), "authenticated/")
		bucket, objPath, ok := strings.Cut(rest, "/")
		if !ok {
			storageErr(c, 400, "bad_request", "Missing bucket or path")
			return
		}
		c.Set("_bucket", bucket)
		c.Set("_path", objPath)
		h.objectInfo(c)
	default:
		if len(segments) < 2 {
			storageErr(c, 400, "bad_request", "Missing path")
			return
		}
		apiKeyGuard(h.jwtKeys)(c)
		if c.IsAborted() {
			return
		}
		jwtAuth(h.jwtKeys, true)(c)
		if c.IsAborted() {
			return
		}
		h.serveDownload(c, segments[0], strings.Join(segments[1:], "/"), false)
	}
}

func (h *StorageV1Handler) serveDownload(c *gin.Context, bucketName, objPath string, publicOnly bool) {
	var ok bool
	if objPath, ok = objectPath(c, objPath); !ok {
		return
	}
	tp, err := parseTransformParams(c)
	if err != nil {
		storageErr(c, 400, "invalid_transform", err.Error())
		return
	}

	bucket, ok := h.getBucketConfig(bucketName)
	if !ok {
		storageErr(c, 404, "not_found", "Bucket not found")
		return
	}
	if publicOnly && !bucket.Public {
		storageErr(c, 400, "not_public", "Bucket is not public")
		return
	}

	ctx := h.rlsCtx(c)
	row, err := h.db.QueryRow(ctx, "SELECT id FROM storage.objects WHERE bucket_id = $1 AND name = $2", bucketName, objPath)
	if err != nil || row == nil {
		storageErr(c, 404, "not_found", "Object not found")
		return
	}

	key := bucketName + "/" + objPath
	body, contentType, err := h.storage.Download(ctx, key)
	if err != nil {
		h.logger.Error("download error", "error", err)
		storageErr(c, 500, "internal", "Download failed")
		return
	}
	defer func() { _ = body.Close() }()

	if tp != nil && strings.HasPrefix(contentType, "image/") {
		transformed, newCT, err := applyTransform(body, contentType, tp)
		if errors.Is(err, errImageTooLarge) {
			storageErr(c, 413, "image_too_large", err.Error())
			return
		}
		if err != nil {
			storageErr(c, 400, "invalid_transform", err.Error())
			return
		}
		body, contentType = transformed, newCT
	}

	setDownloadHeaders(c, contentType, publicOnly)
	c.Status(200)
	_, _ = io.Copy(c.Writer, body)
}

// activeContentTypes can run script when rendered inline on our origin.
var activeContentTypes = map[string]bool{
	"text/html": true, "text/xsl": true,
	"text/javascript": true, "application/javascript": true, "application/x-javascript": true,
}

// isActiveContent covers HTML/JS, every XML family type (svg, xhtml, rss,
// atom, mathml), and multipart/* (e.g. x-mixed-replace can push new content
// into the same response, so it's active like the rest).
func isActiveContent(mt string) bool {
	return activeContentTypes[mt] || strings.HasSuffix(mt, "+xml") || strings.HasSuffix(mt, "/xml") || strings.HasPrefix(mt, "multipart/")
}

func setDownloadHeaders(c *gin.Context, contentType string, public bool) {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.Contains(mt, "/") {
		contentType, mt = "application/octet-stream", ""
	}
	c.Header("Content-Type", contentType)
	c.Header("X-Content-Type-Options", "nosniff")
	if isActiveContent(mt) {
		c.Header("Content-Disposition", "attachment")
	}
	if public {
		c.Header("Cache-Control", "public, max-age=3600")
	} else {
		c.Header("Cache-Control", "private, max-age=3600")
	}
}

func (h *StorageV1Handler) listObjects(c *gin.Context) {
	bucketName := c.Param("bucket")
	if _, ok := h.getBucketConfig(bucketName); !ok {
		storageErr(c, 404, "not_found", "Bucket not found")
		return
	}

	var req struct {
		Prefix string `json:"prefix"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
		Search string `json:"search"`
	}
	_ = c.ShouldBindJSON(&req)

	prefix := strings.TrimPrefix(req.Prefix, "/")
	if req.Limit <= 0 {
		req.Limit = 100
	}

	ctx := h.rlsCtx(c)

	query := "SELECT name, size, mime, uploaded_at, metadata FROM storage.objects WHERE bucket_id = $1"
	args := []any{bucketName}
	argIdx := 2

	if prefix != "" {
		query += fmt.Sprintf(" AND name LIKE $%d", argIdx)
		args = append(args, prefix+"%")
		argIdx++
	}
	if req.Search != "" {
		query += fmt.Sprintf(" AND name LIKE $%d", argIdx)
		args = append(args, "%"+req.Search+"%")
		argIdx++
	}
	query += " ORDER BY name"
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
	args = append(args, req.Limit, req.Offset)

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.logger.Error("list objects", "error", err)
		storageErr(c, 500, "internal", "Failed to list")
		return
	}

	var items []gin.H
	for _, row := range rows {
		name, _ := row["name"].(string)
		// Strip prefix to return relative names (supabase convention)
		relName := strings.TrimPrefix(name, prefix)
		items = append(items, gin.H{
			"name":       relName,
			"id":         name,
			"created_at": asString(row["uploaded_at"]),
			"updated_at": asString(row["uploaded_at"]),
			"metadata":   row["metadata"],
		})
	}
	if items == nil {
		items = []gin.H{}
	}
	c.JSON(200, items)
}

func (h *StorageV1Handler) listObjectsV2(c *gin.Context) {
	bucketName := c.Param("bucket")
	if _, ok := h.getBucketConfig(bucketName); !ok {
		storageErr(c, 404, "not_found", "Bucket not found")
		return
	}

	var req struct {
		Prefix        string `json:"prefix"`
		Limit         int    `json:"limit"`
		Cursor        string `json:"cursor"`
		WithDelimiter bool   `json:"with_delimiter"`
		SortBy        struct {
			Column string `json:"column"`
			Order  string `json:"order"`
		} `json:"sortBy"`
	}
	_ = c.ShouldBindJSON(&req)

	prefix := strings.TrimPrefix(req.Prefix, "/")
	if req.Limit <= 0 {
		req.Limit = 100
	}

	ctx := h.rlsCtx(c)

	// Fetch one extra row to determine hasNext.
	fetchLimit := req.Limit + 1

	query := "SELECT name, size, mime, uploaded_at, metadata FROM storage.objects WHERE bucket_id = $1"
	args := []any{bucketName}
	argIdx := 2

	if prefix != "" {
		query += fmt.Sprintf(" AND name LIKE $%d", argIdx)
		args = append(args, prefix+"%")
		argIdx++
	}
	if req.Cursor != "" {
		query += fmt.Sprintf(" AND name > $%d", argIdx)
		args = append(args, req.Cursor)
		argIdx++
	}

	sortCol := "name"
	sortOrder := "ASC"
	if req.SortBy.Column == "updated_at" || req.SortBy.Column == "created_at" {
		sortCol = "uploaded_at"
	}
	if strings.EqualFold(req.SortBy.Order, "desc") {
		sortOrder = "DESC"
	}
	query += fmt.Sprintf(" ORDER BY %s %s", sortCol, sortOrder)
	query += fmt.Sprintf(" LIMIT $%d", argIdx)
	args = append(args, fetchLimit)

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		h.logger.Error("list objects v2", "error", err)
		storageErr(c, 500, "internal", "Failed to list")
		return
	}

	hasNext := len(rows) > req.Limit
	if hasNext {
		rows = rows[:req.Limit]
	}

	var folders []gin.H
	var objects []gin.H
	seenFolders := map[string]bool{}

	for _, row := range rows {
		name, _ := row["name"].(string)
		relName := strings.TrimPrefix(name, prefix)

		if req.WithDelimiter {
			if idx := strings.Index(relName, "/"); idx >= 0 {
				folderName := relName[:idx+1]
				if !seenFolders[folderName] {
					seenFolders[folderName] = true
					folders = append(folders, gin.H{"name": folderName, "key": prefix + folderName})
				}
				continue
			}
		}

		objects = append(objects, gin.H{
			"name":       relName,
			"id":         name,
			"created_at": asString(row["uploaded_at"]),
			"updated_at": asString(row["uploaded_at"]),
			"metadata":   row["metadata"],
		})
	}

	if folders == nil {
		folders = []gin.H{}
	}
	if objects == nil {
		objects = []gin.H{}
	}

	result := gin.H{
		"has_next": hasNext,
		"folders":  folders,
		"objects":  objects,
	}
	if hasNext && len(rows) > 0 {
		lastRow := rows[len(rows)-1]
		result["next_cursor"], _ = lastRow["name"].(string)
	}
	c.JSON(200, result)
}

func (h *StorageV1Handler) objectInfo(c *gin.Context) {
	bucketName := c.GetString("_bucket")
	objPath, ok := objectPath(c, c.GetString("_path"))
	if !ok {
		return
	}

	if _, ok := h.getBucketConfig(bucketName); !ok {
		storageErr(c, 404, "not_found", "Bucket not found")
		return
	}

	ctx := h.rlsCtx(c)
	row, err := h.db.QueryRow(ctx,
		"SELECT id, name, size, mime, uploaded_at, uploaded_by, metadata FROM storage.objects WHERE bucket_id = $1 AND name = $2",
		bucketName, objPath)
	if err != nil || row == nil {
		storageErr(c, 404, "not_found", "Object not found")
		return
	}

	c.JSON(200, gin.H{
		"id":           asString(row["id"]),
		"name":         asString(row["name"]),
		"size":         row["size"],
		"content_type": asString(row["mime"]),
		"created_at":   asString(row["uploaded_at"]),
		"updated_at":   asString(row["uploaded_at"]),
		"metadata":     row["metadata"],
	})
}

func (h *StorageV1Handler) objectExists(c *gin.Context) {
	bucketName := c.Param("bucket")
	objPath, ok := objectPath(c, c.Param("path"))
	if !ok {
		return
	}

	if _, ok := h.getBucketConfig(bucketName); !ok {
		c.Status(404)
		return
	}

	ctx := h.rlsCtx(c)
	row, err := h.db.QueryRow(ctx, "SELECT id FROM storage.objects WHERE bucket_id = $1 AND name = $2", bucketName, objPath)
	if err != nil || row == nil {
		c.Status(404)
		return
	}
	c.Status(200)
}

func (h *StorageV1Handler) removeObjects(c *gin.Context) {
	bucketName := c.Param("bucket")
	if _, ok := h.getBucketConfig(bucketName); !ok {
		storageErr(c, 404, "not_found", "Bucket not found")
		return
	}

	var req struct {
		Prefixes []string `json:"prefixes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		storageErr(c, 400, "bad_request", "Expected {prefixes: [...]}")
		return
	}

	keys := validKeys(req.Prefixes)
	deleted := []gin.H{}
	if len(keys) == 0 {
		c.JSON(200, deleted)
		return
	}
	rows, err := h.db.Query(h.rlsCtx(c),
		"DELETE FROM storage.objects WHERE bucket_id = $1 AND name = ANY($2::text[]) RETURNING name", bucketName, keys)
	if err != nil {
		h.logger.Error("remove objects", "error", err)
		storageErr(c, 500, "internal", "Failed to remove objects")
		return
	}
	deleteBytes(c.Request.Context(), h.storage, h.logger, bucketName, rows)
	for _, row := range rows {
		deleted = append(deleted, gin.H{"name": asString(row["name"]), "bucket_id": bucketName})
	}
	c.JSON(200, deleted)
}

func (h *StorageV1Handler) moveObject(c *gin.Context) {
	var req struct {
		BucketID          string `json:"bucketId"`
		SourceKey         string `json:"sourceKey"`
		DestinationKey    string `json:"destinationKey"`
		DestinationBucket string `json:"destinationBucket"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		storageErr(c, 400, "bad_request", "Invalid request")
		return
	}

	srcBucket := req.BucketID
	dstBucket := req.DestinationBucket
	if dstBucket == "" {
		dstBucket = srcBucket
	}

	if _, ok := h.getBucketConfig(srcBucket); !ok {
		storageErr(c, 404, "not_found", "Source bucket not found")
		return
	}
	if _, ok := h.getBucketConfig(dstBucket); !ok {
		storageErr(c, 404, "not_found", "Destination bucket not found")
		return
	}

	src, errS := cleanPath(req.SourceKey)
	dst, errD := cleanPath(req.DestinationKey)
	if errS != nil || errD != nil {
		storageErr(c, 400, "invalid_key", "Invalid key")
		return
	}

	if srcBucket == dstBucket && src == dst {
		storageErr(c, 400, "invalid_key", "Source and destination are the same")
		return
	}
	ctx := h.rlsCtx(c)
	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.logger.Error("move begin", "error", err)
		storageErr(c, 500, "internal", "Move failed")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	n, err := tx.Exec(ctx,
		"UPDATE storage.objects SET bucket_id = $1, name = $2 WHERE bucket_id = $3 AND name = $4",
		dstBucket, dst, srcBucket, src)
	if err != nil {
		h.uploadWriteError(c, err)
		return
	}
	if n == 0 {
		storageErr(c, 404, "not_found", "Object not found")
		return
	}
	srcKey, dstKey := srcBucket+"/"+src, dstBucket+"/"+dst
	if err := h.storage.Copy(c.Request.Context(), srcKey, dstKey); err != nil {
		h.logger.Error("move copy", "error", err)
		storageErr(c, 500, "internal", "Move failed")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		// A failed commit is ambiguous (it may have applied with the reply
		// lost), so don't touch storage here: the orphaned copy is unreachable
		// either way since the metadata row never committed.
		h.logger.Error("move commit", "error", err)
		storageErr(c, 500, "internal", "Move failed")
		return
	}
	if err := h.storage.Delete(c.Request.Context(), srcKey); err != nil {
		h.logger.Warn("move delete source", "key", srcKey, "error", err)
	}
	c.JSON(200, gin.H{"message": "Successfully moved"})
}

func (h *StorageV1Handler) copyObject(c *gin.Context) {
	var req struct {
		BucketID          string `json:"bucketId"`
		SourceKey         string `json:"sourceKey"`
		DestinationKey    string `json:"destinationKey"`
		DestinationBucket string `json:"destinationBucket"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		storageErr(c, 400, "bad_request", "Invalid request")
		return
	}

	srcBucket := req.BucketID
	dstBucket := req.DestinationBucket
	if dstBucket == "" {
		dstBucket = srcBucket
	}

	if _, ok := h.getBucketConfig(srcBucket); !ok {
		storageErr(c, 404, "not_found", "Source bucket not found")
		return
	}
	if _, ok := h.getBucketConfig(dstBucket); !ok {
		storageErr(c, 404, "not_found", "Destination bucket not found")
		return
	}

	src, errS := cleanPath(req.SourceKey)
	dst, errD := cleanPath(req.DestinationKey)
	if errS != nil || errD != nil {
		storageErr(c, 400, "invalid_key", "Invalid key")
		return
	}

	if srcBucket == dstBucket && src == dst {
		storageErr(c, 400, "invalid_key", "Source and destination are the same")
		return
	}
	ctx := h.rlsCtx(c)
	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.logger.Error("copy begin", "error", err)
		storageErr(c, 500, "internal", "Copy failed")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	n, err := tx.Exec(ctx,
		`INSERT INTO storage.objects (bucket_id, name, size, mime, uploaded_by, metadata)
		 SELECT $1, $2, size, mime, $5::uuid, metadata FROM storage.objects WHERE bucket_id = $3 AND name = $4
		 ON CONFLICT (bucket_id, name) DO UPDATE SET size = EXCLUDED.size, mime = EXCLUDED.mime,
		   metadata = EXCLUDED.metadata, uploaded_by = EXCLUDED.uploaded_by, uploaded_at = NOW()`,
		dstBucket, dst, srcBucket, src, nullIfEmpty(getSession(c).UserID))
	if err != nil {
		h.uploadWriteError(c, err)
		return
	}
	if n == 0 {
		storageErr(c, 404, "not_found", "Object not found")
		return
	}
	dstKey := dstBucket + "/" + dst
	if err := h.storage.Copy(c.Request.Context(), srcBucket+"/"+src, dstKey); err != nil {
		h.logger.Error("copy", "error", err)
		storageErr(c, 500, "internal", "Copy failed")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.logger.Error("copy commit", "error", err)
		storageErr(c, 500, "internal", "Copy failed")
		return
	}
	c.JSON(200, gin.H{"Key": dstKey})
}

// --- Signed URL handlers ---

func (h *StorageV1Handler) createSignedURL(c *gin.Context) {
	bucketName := c.Param("bucket")
	objPath, ok := objectPath(c, c.Param("path"))
	if !ok {
		return
	}

	if _, ok := h.getBucketConfig(bucketName); !ok {
		storageErr(c, 404, "not_found", "Bucket not found")
		return
	}

	var req struct {
		ExpiresIn int `json:"expiresIn"`
	}
	_ = c.ShouldBindJSON(&req)

	ctx := h.rlsCtx(c)
	row, err := h.db.QueryRow(ctx, "SELECT id FROM storage.objects WHERE bucket_id = $1 AND name = $2", bucketName, objPath)
	if err != nil || row == nil {
		storageErr(c, 404, "not_found", "Object not found")
		return
	}

	url, err := h.storage.SignDownload(ctx, bucketName+"/"+objPath, signedExpiry(req.ExpiresIn))
	if err != nil {
		h.logger.Error("sign download", "error", err)
		storageErr(c, 500, "internal", "Failed to create signed URL")
		return
	}

	c.JSON(200, gin.H{"signedURL": url})
}

func (h *StorageV1Handler) createSignedURLs(c *gin.Context) {
	bucketName := c.Param("bucket")
	if _, ok := h.getBucketConfig(bucketName); !ok {
		storageErr(c, 404, "not_found", "Bucket not found")
		return
	}

	var req struct {
		ExpiresIn int      `json:"expiresIn"`
		Paths     []string `json:"paths"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Paths) == 0 || len(req.Paths) > maxSignPaths {
		storageErr(c, 400, "bad_request", fmt.Sprintf("Expected 1-%d paths", maxSignPaths))
		return
	}

	ctx := h.rlsCtx(c)
	keys := validKeys(req.Paths)
	visible := map[string]bool{}
	if len(keys) > 0 {
		rows, err := h.db.Query(ctx, "SELECT name FROM storage.objects WHERE bucket_id = $1 AND name = ANY($2::text[])", bucketName, keys)
		if err != nil {
			h.logger.Error("sign urls lookup", "error", err)
			storageErr(c, 500, "internal", "Failed to create signed URLs")
			return
		}
		for _, row := range rows {
			visible[asString(row["name"])] = true
		}
	}

	expiry := signedExpiry(req.ExpiresIn)
	results := make([]gin.H, 0, len(req.Paths))
	for _, p := range req.Paths {
		k, err := cleanPath(p)
		if err != nil || !visible[k] {
			results = append(results, gin.H{"path": p, "signedURL": nil, "error": errNoObjectAccess})
			continue
		}
		url, err := h.storage.SignDownload(ctx, bucketName+"/"+k, expiry)
		if err != nil {
			h.logger.Error("sign download", "error", err)
			results = append(results, gin.H{"path": p, "signedURL": nil, "error": "Failed to create signed URL"})
			continue
		}
		results = append(results, gin.H{"path": p, "signedURL": url, "error": nil})
	}
	c.JSON(200, results)
}

func (h *StorageV1Handler) createSignedUploadURL(c *gin.Context) {
	bucketName := c.Param("bucket")
	objPath, ok := objectPath(c, c.Param("path"))
	if !ok {
		return
	}

	if _, ok := h.getBucketConfig(bucketName); !ok {
		storageErr(c, 404, "not_found", "Bucket not found")
		return
	}

	// Authorize before minting. A signed upload URL is a bearer capability whose
	// redemption (uploadToSignedURL) runs as service_role, so this is the only
	// point at which the caller's INSERT policy can be enforced. Probe the same
	// metadata write the redemption will perform, under the caller's role, in a
	// transaction that is always rolled back; if RLS denies it, no token is
	// handed out. This mirrors the download path (createSignedURL probes a
	// SELECT) and Supabase's signUploadObjectUrl, which runs canUpload first.
	session := getSession(c)
	uploadedBy := nullIfEmpty(session.UserID)
	probeRow := objectRow{bucket: bucketName, name: objPath, uploadedBy: uploadedBy, metadata: objectMetadataJSON(0, "", "")}
	if err := h.probeUploadPermission(c, probeRow, false, c.GetHeader("x-upsert") == "true"); err != nil {
		h.uploadWriteError(c, err)
		return
	}

	token := h.signUploadToken(bucketName, objPath, session.UserID)
	if token == "" {
		storageErr(c, 500, "internal", "Failed to create signed upload URL")
		return
	}

	c.JSON(200, gin.H{
		"url": (&url.URL{
			Path:     "/object/upload/sign/" + bucketName + "/" + objPath,
			RawQuery: url.Values{"token": {token}}.Encode(),
		}).String(),
		"token": token,
		"path":  objPath,
	})
}

// probeUploadPermission reports whether the bucket's RLS policies permit the
// caller to write this object, without persisting anything. It runs the same
// write writeObjectRow would perform for isUpdate/upsert, under the caller's
// Postgres role, inside a transaction that is always rolled back, and returns
// the raw database error so callers can map an RLS denial to 403, a
// duplicate to 409, a missing row to 404, etc. via uploadWriteError.
func (h *StorageV1Handler) probeUploadPermission(c *gin.Context, row objectRow, isUpdate, upsert bool) error {
	ctx := h.rlsCtx(c)
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return writeObjectRow(ctx, tx, row, isUpdate, upsert)
}

func (h *StorageV1Handler) uploadToSignedURL(c *gin.Context) {
	bucketName := c.Param("bucket")
	objPath, ok := objectPath(c, c.Param("path"))
	if !ok {
		return
	}

	bucket, ok := h.getBucketConfig(bucketName)
	if !ok {
		storageErr(c, 404, "not_found", "Bucket not found")
		return
	}

	token := c.Query("token")
	owner, ok := h.verifyUploadToken(token, bucketName, objPath)
	if !ok {
		storageErr(c, 400, "invalid_token", "Invalid or expired upload token")
		return
	}

	file, size, contentType, ok := h.readUpload(c, bucket, nil)
	if !ok {
		return
	}
	defer closeSpool(file)

	key := bucketName + "/" + objPath
	if err := h.storage.Upload(c.Request.Context(), key, file, contentType, size); err != nil {
		h.logger.Error("signed upload error", "error", err)
		storageErr(c, 500, "internal", "Upload failed")
		return
	}

	// The HMAC upload token is the authorization for this route (there is no
	// jwtAuth on it), so the metadata write runs as service_role rather than
	// the anonymous caller, equivalent to an S3 presigned PUT — attached
	// explicitly since the request pool's default is anon. The owner bound
	// into the token at mint time is written to uploaded_by so owner-scoped RLS
	// policies match the row the same way they matched the mint-time probe.
	ctx, err := h.db.WithRLS(c.Request.Context(), domain.Session{Role: "service_role", IsAuthenticated: true})
	if err != nil {
		ctx = c.Request.Context()
	}
	row := objectRow{bucket: bucketName, name: objPath, size: size, mime: contentType, uploadedBy: nullIfEmpty(owner),
		metadata: objectMetadataJSON(size, contentType, c.GetHeader("Cache-Control"))}
	if err := writeObjectRow(ctx, h.db, row, false, true); err != nil {
		h.logger.Error("signed upload record", "error", err)
		storageErr(c, 500, "internal", "Failed to record object")
		return
	}

	c.JSON(200, gin.H{
		"Key":      bucketName + "/" + objPath,
		"path":     objPath,
		"fullPath": bucketName + "/" + objPath,
	})
}

// signUploadToken creates an HMAC token for signed uploads. The owner (the
// minting user's id, or "" for anonymous) is bound into the token so the
// redemption can persist it as uploaded_by, keeping the stored row's ownership
// consistent with the INSERT policy that authorized the mint. Returns "" when
// no signing secret is available; callers treat an empty token as a failure so
// we never emit a token an attacker could trivially forge.
func (h *StorageV1Handler) signUploadToken(bucket, objPath, owner string) string {
	active, err := h.jwtKeys.Active(context.Background())
	if err != nil {
		return ""
	}
	secret := active.SymmetricSecret()
	if len(secret) == 0 {
		h.logger.Error("storage upload token: active JWT key has no usable secret")
		return ""
	}
	expiry := time.Now().Add(storageUploadTokenExpiry).Unix()
	payload := fmt.Sprintf("%s/%s:%d:%s", bucket, objPath, expiry, owner)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	// owner is a UUID or "", neither of which contains a '.', so it is safe to
	// carry as its own dot-delimited segment.
	return fmt.Sprintf("%d.%s.%s", expiry, owner, sig)
}

// verifyUploadToken checks the token's signature, expiry, and path binding and
// returns the owner bound into it. ok is false on any mismatch.
func (h *StorageV1Handler) verifyUploadToken(token, bucket, objPath string) (owner string, ok bool) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return "", false
	}
	owner = parts[1]

	var expiry int64
	if _, err := fmt.Sscanf(parts[0], "%d", &expiry); err != nil {
		return "", false
	}
	if time.Now().Unix() > expiry {
		return "", false
	}

	active, err := h.jwtKeys.Active(context.Background())
	if err != nil {
		return "", false
	}
	secret := active.SymmetricSecret()
	if len(secret) == 0 {
		// Fail closed: with no secret we cannot verify, so reject rather
		// than HMAC with an empty (forgeable) key.
		return "", false
	}

	payload := fmt.Sprintf("%s/%s:%d:%s", bucket, objPath, expiry, owner)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[2]), []byte(expected)) {
		return "", false
	}
	return owner, true
}
