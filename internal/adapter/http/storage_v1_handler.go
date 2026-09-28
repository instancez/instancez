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
	"syscall"
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
	key, opt := apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, false)

	// --- Bucket admin ---
	sg.GET("/bucket", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.listBuckets)
	sg.GET("/bucket/:id", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.getBucket)
	sg.POST("/bucket", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.createBucket)
	sg.PUT("/bucket/:id", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.updateBucket)
	sg.DELETE("/bucket/:id", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.deleteBucket)
	sg.POST("/bucket/:id/empty", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true), h.emptyBucket)

	// --- File operations ---
	// Upload (POST) and update (PUT)
	sg.POST("/object/:bucket/*path", key, opt, h.anonGate(denyForbidden), h.uploadObject)
	sg.PUT("/object/:bucket/*path", key, opt, h.anonGate(denyForbidden), h.updateObject)

	// One catch-all, since gin cannot overlap param routes; objectGetDispatch checks apikey except on public downloads.
	sg.GET("/object/*all", h.objectGetDispatch)

	// List
	sg.POST("/object/list/:bucket", key, opt, h.anonGate(denyEmptyList), h.listObjects)
	sg.POST("/object/list-v2/:bucket", key, opt, h.anonGate(denyEmptyListV2), h.listObjectsV2)

	// Exists (HEAD)
	sg.HEAD("/object/:bucket/*path", key, opt, h.anonGate(func(c *gin.Context) { c.Status(404) }), h.objectExists)

	// Remove (DELETE with paths in body)
	sg.DELETE("/object/:bucket", key, opt, h.removeObjects)

	// Move & Copy
	sg.POST("/object/move", key, opt, h.moveObject)
	sg.POST("/object/copy", key, opt, h.copyObject)

	// Signed URLs
	sg.POST("/object/sign/:bucket/*path", key, opt, h.anonGate(denyNotFound), h.createSignedURL)
	sg.POST("/object/sign/:bucket", key, opt, h.createSignedURLs)

	// Signed upload
	sg.POST("/object/upload/sign/:bucket/*path", key, opt, h.anonGate(denyForbidden), h.createSignedUploadURL)
	// Authorized purely by the HMAC-signed token in the query string,
	// matching real Supabase's signed-upload redemption — no apikey.
	sg.PUT("/object/upload/sign/:bucket/*path", h.uploadToSignedURL)
}

func (h *StorageV1Handler) anonAllowed(c *gin.Context, bucket string) bool {
	return anonMayReach(c, h.cfg, bucket)
}

// anonMayReach lets anon reach only buckets whose own rls policies decide, since nothing else would authorize it in Supabase.
func anonMayReach(c *gin.Context, cfg *domain.Config, bucket string) bool {
	if r := getSession(c).Role; r == domain.JWTRoleAuthenticated || r == domain.JWTRoleService {
		return true
	}
	b, ok := cfg.Storage[bucket]
	return ok && len(b.RLS) > 0
}

func (h *StorageV1Handler) anonGate(deny gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if raw, hasPath := c.Params.Get("path"); hasPath {
			if _, ok := objectPath(c, raw); !ok {
				c.Abort()
				return
			}
		}
		if !h.anonAllowed(c, c.Param("bucket")) {
			deny(c)
			c.Abort()
		}
	}
}

// callerMayReach runs apikey, optional JWT and the anon gate inline; false means a response was written.
func (h *StorageV1Handler) callerMayReach(c *gin.Context, bucket string, deny gin.HandlerFunc) bool {
	if apiKeyGuard(h.jwtKeys)(c); c.IsAborted() {
		return false
	}
	if jwtAuth(h.jwtKeys, false)(c); c.IsAborted() {
		return false
	}
	if !h.anonAllowed(c, bucket) {
		deny(c)
		return false
	}
	return true
}

func denyNotFound(c *gin.Context) { storageErr(c, 404, "not_found", "Object not found") }
func denyForbidden(c *gin.Context) {
	storageErr(c, 403, "forbidden", "Not authorized to write this object")
}
func denyEmptyList(c *gin.Context) { c.JSON(200, []gin.H{}) }
func denyEmptyListV2(c *gin.Context) {
	c.JSON(200, gin.H{"has_next": false, "folders": []gin.H{}, "objects": []gin.H{}})
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
// object. An unauthenticated request resolves to the `anon` role; an admin-key
// request keeps service_role.
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

// serviceContext bypasses RLS, so callers must gate on the bucket and scope the query to one object.
func serviceContext(db domain.Database, ctx context.Context) (context.Context, error) {
	return db.WithRLS(ctx, domain.Session{Role: "service_role", IsAuthenticated: true})
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

// readUpload spools the body; on !ok the error response is already written.
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
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.Canceled) || errors.Is(err, syscall.ECONNRESET) {
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
		// An ambiguous commit may have applied, so leave storage alone (no compensating delete).
		h.logger.Error("upload commit failed", "bucket", bucketName, "key", objPath, "error", err)
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

// uploadWriteError maps a failed metadata write to 404, 409, 403 or 500.
func (h *StorageV1Handler) uploadWriteError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errObjectNotFound):
		storageErr(c, 404, "not_found", "Object not found")
	case isDuplicateKeyErr(err):
		storageErr(c, 409, "duplicate", "The resource already exists")
	case isPermissionDenied(err):
		storageErr(c, 403, "forbidden", "Not authorized to write this object")
	default:
		h.logger.Error("record object", "error", err)
		storageErr(c, 500, "internal", "Failed to record object")
	}
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
	case "sign":
		// No apikey: the token is the grant, as in Supabase.
		if len(segments) < 3 {
			storageErr(c, 400, "bad_request", "Missing bucket or path")
			return
		}
		h.redeemSignedURL(c, segments[1], segments[2])
	case "authenticated":
		if len(segments) < 3 {
			storageErr(c, 400, "bad_request", "Missing bucket or path")
			return
		}
		if !h.callerMayReach(c, segments[1], denyNotFound) {
			return
		}
		h.serveDownload(c, segments[1], segments[2], false)
	case "info":
		if len(segments) < 2 {
			storageErr(c, 400, "bad_request", "Missing path")
			return
		}
		rest := strings.TrimPrefix(strings.TrimPrefix(all, "info/"), "authenticated/")
		bucket, objPath, ok := strings.Cut(rest, "/")
		if !ok {
			storageErr(c, 400, "bad_request", "Missing bucket or path")
			return
		}
		if !h.callerMayReach(c, bucket, denyNotFound) {
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
		if !h.callerMayReach(c, segments[0], denyNotFound) {
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
	if !ok || (publicOnly && !bucket.Public) {
		storageErr(c, 404, "not_found", "Bucket not found")
		return
	}

	ctx := h.rlsCtx(c)
	if publicOnly {
		if ctx, err = serviceContext(h.db, c.Request.Context()); err != nil {
			storageErr(c, 500, "internal", "Download failed")
			return
		}
	}
	row, err := h.db.QueryRow(ctx, "SELECT id FROM storage.objects WHERE bucket_id = $1 AND name = $2", bucketName, objPath)
	if err != nil || row == nil {
		storageErr(c, 404, "not_found", "Object not found")
		return
	}

	key := bucketName + "/" + objPath
	body, contentType, err := h.storage.Download(ctx, key)
	if err != nil {
		h.downloadErr(c, err)
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

	download, hasDownload := c.GetQuery("download")
	writeDownloadHeaders(c, downloadOptions(contentType, publicOnly, download, hasDownload))
	c.Status(200)
	_, _ = io.Copy(c.Writer, body)
}

// activeContentTypes can run script when rendered inline on our origin.
var activeContentTypes = map[string]bool{
	"text/html": true, "text/xsl": true,
	"text/javascript": true, "application/javascript": true, "application/x-javascript": true,
}

// isActiveContent covers HTML/JS, every XML family type, and multipart/* (e.g. x-mixed-replace).
func isActiveContent(mt string) bool {
	return activeContentTypes[mt] || strings.HasSuffix(mt, "+xml") || strings.HasSuffix(mt, "/xml") || strings.HasPrefix(mt, "multipart/")
}

func contentDisposition(name string) string {
	if v := mime.FormatMediaType("attachment", map[string]string{"filename": name}); name != "" && v != "" {
		return v
	}
	return "attachment"
}

func downloadOptions(contentType string, public bool, download string, hasDownload bool) domain.DownloadOptions {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.Contains(mt, "/") {
		contentType, mt = "application/octet-stream", ""
	}
	o := domain.DownloadOptions{ContentType: contentType, CacheControl: "private, max-age=3600"}
	if public {
		o.CacheControl = "public, max-age=3600"
	}
	switch {
	case hasDownload:
		o.ContentDisposition = contentDisposition(download)
	case isActiveContent(mt):
		o.ContentDisposition = "attachment"
	}
	return o
}

func writeDownloadHeaders(c *gin.Context, o domain.DownloadOptions) {
	c.Header("Content-Type", o.ContentType)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", o.CacheControl)
	if o.ContentDisposition != "" {
		c.Header("Content-Disposition", o.ContentDisposition)
	}
}

// downloadErr maps a registered object whose bytes are gone to 404.
func (h *StorageV1Handler) downloadErr(c *gin.Context, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		storageErr(c, 404, "not_found", "Object not found")
		return
	}
	h.logger.Error("download error", "error", err)
	storageErr(c, 500, "internal", "Download failed")
}

// downloadMAC signs with a key derived for downloads, so upload and download tokens can't be swapped.
func (h *StorageV1Handler) downloadMAC(ctx context.Context, bucket, objPath string, exp int64) []byte {
	active, err := h.jwtKeys.Active(ctx)
	if err != nil || len(active.SymmetricSecret()) == 0 {
		return nil
	}
	derived := hmac.New(sha256.New, active.SymmetricSecret())
	derived.Write([]byte("storage-download"))
	m := hmac.New(sha256.New, derived.Sum(nil))
	_, _ = fmt.Fprintf(m, "%s\x00%s\x00%d", bucket, objPath, exp)
	return m.Sum(nil)
}

// signDownloadToken returns "<unix exp>.<hex hmac>", or "" when no signing key is available.
func (h *StorageV1Handler) signDownloadToken(ctx context.Context, bucket, objPath string, expiry time.Duration) string {
	exp := time.Now().Add(expiry).Unix()
	sig := h.downloadMAC(ctx, bucket, objPath, exp)
	if sig == nil {
		return ""
	}
	return strconv.FormatInt(exp, 10) + "." + hex.EncodeToString(sig)
}

func (h *StorageV1Handler) verifyDownloadToken(ctx context.Context, token, bucket, objPath string) (time.Time, bool) {
	expStr, sigHex, _ := strings.Cut(token, ".")
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || strconv.FormatInt(exp, 10) != expStr || time.Now().Unix() > exp {
		return time.Time{}, false
	}
	sig, err := hex.DecodeString(sigHex)
	want := h.downloadMAC(ctx, bucket, objPath, exp)
	if err != nil || want == nil || !hmac.Equal(sig, want) {
		return time.Time{}, false
	}
	return time.Unix(exp, 0), true
}

// redeemSignedURL serves a createSignedUrl(s) URL: S3 gets a 302 to a short presign, the local provider streams.
func (h *StorageV1Handler) redeemSignedURL(c *gin.Context, bucketName, rawPath string) {
	objPath, ok := objectPath(c, rawPath)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	exp, ok := h.verifyDownloadToken(ctx, c.Query("token"), bucketName, objPath)
	if !ok {
		storageErr(c, 400, "invalid_token", "Invalid or expired signed URL")
		return
	}
	bucket, ok := h.getBucketConfig(bucketName)
	if !ok {
		storageErr(c, 404, "not_found", "Bucket not found")
		return
	}
	ctx, err := serviceContext(h.db, ctx)
	if err != nil {
		storageErr(c, 500, "internal", "Download failed")
		return
	}
	row, err := h.db.QueryRow(ctx, "SELECT mime FROM storage.objects WHERE bucket_id = $1 AND name = $2", bucketName, objPath)
	if err != nil || row == nil {
		storageErr(c, 404, "not_found", "Object not found")
		return
	}
	download, hasDownload := c.GetQuery("download")
	opts := downloadOptions(asString(row["mime"]), bucket.Public, download, hasDownload)
	key := bucketName + "/" + objPath
	u, err := h.storage.SignDownload(ctx, key, max(time.Second, min(time.Minute, time.Until(exp))), opts)
	if err != nil {
		h.logger.Error("redeem signed url", "error", err)
		storageErr(c, 500, "internal", "Download failed")
		return
	}
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Cache-Control", "no-store")
		c.Redirect(http.StatusFound, u)
		return
	}
	body, _, err := h.storage.Download(ctx, key)
	if err != nil {
		h.downloadErr(c, err)
		return
	}
	defer func() { _ = body.Close() }()
	writeDownloadHeaders(c, opts)
	c.Status(200)
	_, _ = io.Copy(c.Writer, body)
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
	if len(keys) == 0 || !h.anonAllowed(c, bucketName) {
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
	if !h.anonAllowed(c, srcBucket) || !h.anonAllowed(c, dstBucket) {
		denyNotFound(c)
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
		// An ambiguous commit may have applied, so leave storage alone (no compensating delete).
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
	if !h.anonAllowed(c, srcBucket) || !h.anonAllowed(c, dstBucket) {
		denyNotFound(c)
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

	tok := h.signDownloadToken(c.Request.Context(), bucketName, objPath, signedExpiry(req.ExpiresIn))
	if tok == "" {
		storageErr(c, 500, "internal", "Failed to create signed URL")
		return
	}
	c.JSON(200, gin.H{"signedURL": signedDownloadURL(bucketName, objPath, tok)})
}

// signedDownloadURL is relative and unescaped, like Supabase, since storage-js encodeURIs it.
func signedDownloadURL(bucket, objPath, token string) string {
	return "/object/sign/" + bucket + "/" + objPath + "?token=" + token
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
	if len(keys) > 0 && h.anonAllowed(c, bucketName) {
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
		tok := h.signDownloadToken(c.Request.Context(), bucketName, k, expiry)
		if tok == "" {
			results = append(results, gin.H{"path": p, "signedURL": nil, "error": "Failed to create signed URL"})
			continue
		}
		results = append(results, gin.H{"path": p, "signedURL": signedDownloadURL(bucketName, k, tok), "error": nil})
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

// probeUploadPermission runs the object write under the caller's role in a rolled-back tx and returns the raw DB error.
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
