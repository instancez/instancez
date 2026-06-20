package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
)

const storageUploadTokenExpiry = 2 * time.Hour

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
	sg.GET("/bucket", jwtAuth(h.jwtKeys, true), h.listBuckets)
	sg.GET("/bucket/:id", jwtAuth(h.jwtKeys, true), h.getBucket)
	sg.POST("/bucket", jwtAuth(h.jwtKeys, true), h.createBucket)
	sg.PUT("/bucket/:id", jwtAuth(h.jwtKeys, true), h.updateBucket)
	sg.DELETE("/bucket/:id", jwtAuth(h.jwtKeys, true), h.deleteBucket)
	sg.POST("/bucket/:id/empty", jwtAuth(h.jwtKeys, true), h.emptyBucket)

	// --- File operations ---
	// Upload (POST) and update (PUT)
	sg.POST("/object/:bucket/*path", jwtAuth(h.jwtKeys, true), h.uploadObject)
	sg.PUT("/object/:bucket/*path", jwtAuth(h.jwtKeys, true), h.updateObject)

	// GET /object/* catch-all — dispatches to public download, authenticated
	// download, or object info based on path prefix. supabase-js sends:
	//   GET /object/<bucket>/<path>          — authenticated download
	//   GET /object/public/<bucket>/<path>   — public download
	//   GET /object/authenticated/<bucket>/<path> — authenticated download (alt)
	//   GET /object/info/authenticated/<bucket>/<path> — object info
	// Gin can't register overlapping param routes, so one handler parses them.
	sg.GET("/object/*all", h.objectGetDispatch)

	// List
	sg.POST("/object/list/:bucket", jwtAuth(h.jwtKeys, true), h.listObjects)
	sg.POST("/object/list-v2/:bucket", jwtAuth(h.jwtKeys, true), h.listObjectsV2)

	// Exists (HEAD)
	sg.HEAD("/object/:bucket/*path", jwtAuth(h.jwtKeys, false), h.objectExists)

	// Remove (DELETE with paths in body)
	sg.DELETE("/object/:bucket", jwtAuth(h.jwtKeys, true), h.removeObjects)

	// Move & Copy
	sg.POST("/object/move", jwtAuth(h.jwtKeys, true), h.moveObject)
	sg.POST("/object/copy", jwtAuth(h.jwtKeys, true), h.copyObject)

	// Signed URLs
	sg.POST("/object/sign/:bucket/*path", jwtAuth(h.jwtKeys, true), h.createSignedURL)
	sg.POST("/object/sign/:bucket", jwtAuth(h.jwtKeys, true), h.createSignedURLs)

	// Signed upload
	sg.POST("/object/upload/sign/:bucket/*path", jwtAuth(h.jwtKeys, true), h.createSignedUploadURL)
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

	ctx := h.rlsCtx(c)
	rows, err := h.db.Query(ctx, "SELECT name FROM storage.objects WHERE bucket_id = $1", id)
	if err != nil {
		h.logger.Error("empty bucket query", "error", err)
		storageErr(c, 500, "internal", "Failed to list objects")
		return
	}
	for _, row := range rows {
		name, _ := row["name"].(string)
		_ = h.storage.Delete(ctx, id+"/"+name)
	}
	_, _ = h.db.Exec(ctx, "DELETE FROM storage.objects WHERE bucket_id = $1", id)
	c.JSON(200, gin.H{"message": "Successfully emptied"})
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
func (h *StorageV1Handler) rlsCtx(c *gin.Context) context.Context {
	session := getSession(c)
	ctx, err := h.db.WithRLS(c.Request.Context(), session)
	if err != nil {
		// WithRLS only stashes the session on the context; it does not perform
		// I/O and never errors in practice. Fall back to the raw context.
		return c.Request.Context()
	}
	return ctx
}

func (h *StorageV1Handler) cleanPath(p string) string {
	p = strings.TrimPrefix(p, "/")
	return path.Clean(p)
}

func (h *StorageV1Handler) uploadObject(c *gin.Context) {
	h.doUpload(c, false)
}

func (h *StorageV1Handler) updateObject(c *gin.Context) {
	h.doUpload(c, true)
}

func (h *StorageV1Handler) doUpload(c *gin.Context, isUpdate bool) {
	bucketName := c.Param("bucket")
	objPath := h.cleanPath(c.Param("path"))

	bucket, ok := h.getBucketConfig(bucketName)
	if !ok {
		storageErr(c, 404, "not_found", "Bucket not found")
		return
	}

	session := getSession(c)

	// Enforce bucket size limit
	var maxBytes int64 = 50 * 1024 * 1024
	if bucket.MaxSize != "" {
		if mb := parseSizeBytes(bucket.MaxSize); mb > 0 {
			maxBytes = mb
		}
	}

	// supabase-js sends uploads as multipart/form-data with the file in a
	// form field. Extract the actual file and its content type from the part.
	var body io.Reader
	var contentType string
	var size int64

	if strings.HasPrefix(c.ContentType(), "multipart/form-data") {
		mr, err := c.Request.MultipartReader()
		if err != nil {
			storageErr(c, 400, "bad_request", "Failed to parse multipart form")
			return
		}
		var found bool
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			if part.FileName() == "" {
				_ = part.Close()
				continue
			}
			body = part
			contentType = part.Header.Get("Content-Type")
			size = -1
			found = true
			defer func() { _ = part.Close() }()
			break
		}
		if !found {
			storageErr(c, 400, "bad_request", "No file found in multipart upload")
			return
		}
	} else {
		body = c.Request.Body
		contentType = c.ContentType()
		if ct := c.GetHeader("Content-Type"); ct != "" {
			contentType = strings.Split(ct, ";")[0]
			contentType = strings.TrimSpace(contentType)
		}
		size = c.Request.ContentLength
	}

	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Validate MIME
	if len(bucket.Types) > 0 && !matchesMIME(contentType, bucket.Types) {
		storageErr(c, 422, "invalid_mime_type", fmt.Sprintf("Content type %q not allowed", contentType))
		return
	}

	limitedBody := http.MaxBytesReader(c.Writer, io.NopCloser(body), maxBytes)

	var uploadedBy any
	if session.UserID != "" {
		uploadedBy = session.UserID
	}
	if size < 0 {
		size = 0
	}

	// Write the metadata row FIRST, inside a transaction bound to the caller's
	// role, so that RLS authorizes the write before any bytes reach the object
	// store. If the policy denies the write we roll back and never touch S3;
	// the actual upload only happens once the metadata insert/update succeeds.
	ctx := h.rlsCtx(c)
	tx, err := h.db.Begin(ctx)
	if err != nil {
		storageErr(c, 500, "internal", "Upload failed")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if isUpdate {
		n, err := tx.Exec(ctx,
			"UPDATE storage.objects SET size = $1, mime = $2, uploaded_at = NOW(), uploaded_by = $3 WHERE bucket_id = $4 AND name = $5",
			size, contentType, uploadedBy, bucketName, objPath)
		if err != nil {
			h.uploadWriteError(c, err)
			return
		}
		if n == 0 {
			// No row the caller is permitted to update (RLS-filtered or absent).
			storageErr(c, 404, "not_found", "Object not found")
			return
		}
	} else {
		upsert := c.GetHeader("x-upsert") == "true"
		if upsert {
			if _, err := tx.Exec(ctx,
				`INSERT INTO storage.objects (bucket_id, name, size, mime, uploaded_by)
				 VALUES ($1, $2, $3, $4, $5)
				 ON CONFLICT (bucket_id, name)
				 DO UPDATE SET size = EXCLUDED.size, mime = EXCLUDED.mime, uploaded_by = EXCLUDED.uploaded_by, uploaded_at = NOW()`,
				bucketName, objPath, size, contentType, uploadedBy); err != nil {
				h.uploadWriteError(c, err)
				return
			}
		} else {
			if _, err := tx.Exec(ctx,
				"INSERT INTO storage.objects (bucket_id, name, size, mime, uploaded_by) VALUES ($1, $2, $3, $4, $5)",
				bucketName, objPath, size, contentType, uploadedBy); err != nil {
				h.uploadWriteError(c, err)
				return
			}
		}
	}

	// Metadata write authorized — now stream the bytes to the object store.
	key := bucketName + "/" + objPath
	if err := h.storage.Upload(c.Request.Context(), key, limitedBody, contentType, size); err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			storageErr(c, 413, "payload_too_large", fmt.Sprintf("File exceeds maximum size of %s", bucket.MaxSize))
			return
		}
		h.logger.Error("upload error", "error", err)
		storageErr(c, 500, "internal", "Upload failed")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		// Best-effort cleanup of the now-orphaned object; the metadata never
		// committed so it would be invisible anyway.
		_ = h.storage.Delete(c.Request.Context(), key)
		storageErr(c, 500, "internal", "Upload failed")
		return
	}

	c.JSON(200, gin.H{
		"Key": bucketName + "/" + objPath,
		"Id":  objPath,
	})
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
// duplicate key → 409, an RLS/permission denial → 403, anything else → 500.
func (h *StorageV1Handler) uploadWriteError(c *gin.Context, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "duplicate key") || strings.Contains(msg, "23505"):
		storageErr(c, 409, "duplicate", "The resource already exists")
	case strings.Contains(msg, "row-level security") || strings.Contains(msg, "42501") || strings.Contains(msg, "permission denied"):
		storageErr(c, 403, "forbidden", "Not authorized to write this object")
	default:
		h.logger.Error("record object", "error", err)
		storageErr(c, 500, "internal", "Failed to record object")
	}
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
		jwtAuth(h.jwtKeys, true)(c)
		if c.IsAborted() {
			return
		}
		rest := strings.SplitN(strings.TrimPrefix(all, "info/"), "/", 3)
		if len(rest) >= 2 && rest[0] == "authenticated" {
			c.Set("_bucket", rest[1])
			objPath := ""
			if len(rest) == 3 {
				objPath = rest[2]
			}
			c.Set("_path", objPath)
			h.objectInfo(c)
			return
		}
		storageErr(c, 404, "not_found", "Not found")
	default:
		if len(segments) < 2 {
			storageErr(c, 400, "bad_request", "Missing path")
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
	objPath = h.cleanPath(objPath)

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

	// Image transforms
	if tp := parseTransformParams(c); tp != nil && strings.HasPrefix(contentType, "image/") {
		transformed, newCT, err := applyTransform(body, contentType, tp)
		if err == nil {
			body = transformed
			contentType = newCT
		}
	}

	if contentType != "" {
		c.Header("Content-Type", contentType)
	}
	c.Header("Cache-Control", "public, max-age=3600")
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
	objPath := h.cleanPath(c.GetString("_path"))

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
	objPath := h.cleanPath(c.Param("path"))

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

	ctx := h.rlsCtx(c)
	var deleted []gin.H
	for _, p := range req.Prefixes {
		p = strings.TrimPrefix(p, "/")
		row, err := h.db.QueryRow(ctx, "SELECT id, name, bucket_id FROM storage.objects WHERE bucket_id = $1 AND name = $2", bucketName, p)
		if err != nil || row == nil {
			continue
		}
		_ = h.storage.Delete(ctx, bucketName+"/"+p)
		_, _ = h.db.Exec(ctx, "DELETE FROM storage.objects WHERE bucket_id = $1 AND name = $2", bucketName, p)
		deleted = append(deleted, gin.H{"name": p, "bucket_id": bucketName})
	}
	if deleted == nil {
		deleted = []gin.H{}
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

	ctx := h.rlsCtx(c)
	srcKey := srcBucket + "/" + req.SourceKey
	dstKey := dstBucket + "/" + req.DestinationKey

	if err := h.storage.Copy(ctx, srcKey, dstKey); err != nil {
		h.logger.Error("move copy", "error", err)
		storageErr(c, 500, "internal", "Move failed")
		return
	}
	if err := h.storage.Delete(ctx, srcKey); err != nil {
		h.logger.Error("move delete", "error", err)
	}

	// Update DB
	_, _ = h.db.Exec(ctx,
		"UPDATE storage.objects SET bucket_id = $1, name = $2 WHERE bucket_id = $3 AND name = $4",
		dstBucket, req.DestinationKey, srcBucket, req.SourceKey)

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

	ctx := h.rlsCtx(c)
	srcKey := srcBucket + "/" + req.SourceKey
	dstKey := dstBucket + "/" + req.DestinationKey

	if err := h.storage.Copy(ctx, srcKey, dstKey); err != nil {
		h.logger.Error("copy", "error", err)
		storageErr(c, 500, "internal", "Copy failed")
		return
	}

	// Copy DB row
	_, _ = h.db.Exec(ctx,
		`INSERT INTO storage.objects (bucket_id, name, size, mime, uploaded_by, metadata)
		 SELECT $1, $2, size, mime, uploaded_by, metadata FROM storage.objects WHERE bucket_id = $3 AND name = $4
		 ON CONFLICT (bucket_id, name) DO UPDATE SET size = EXCLUDED.size, mime = EXCLUDED.mime, uploaded_at = NOW()`,
		dstBucket, req.DestinationKey, srcBucket, req.SourceKey)

	c.JSON(200, gin.H{"Key": dstBucket + "/" + req.DestinationKey})
}

// --- Signed URL handlers ---

func (h *StorageV1Handler) createSignedURL(c *gin.Context) {
	bucketName := c.Param("bucket")
	objPath := h.cleanPath(c.Param("path"))

	if _, ok := h.getBucketConfig(bucketName); !ok {
		storageErr(c, 404, "not_found", "Bucket not found")
		return
	}

	var req struct {
		ExpiresIn int `json:"expiresIn"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ExpiresIn <= 0 {
		req.ExpiresIn = 3600
	}

	ctx := h.rlsCtx(c)
	row, err := h.db.QueryRow(ctx, "SELECT id FROM storage.objects WHERE bucket_id = $1 AND name = $2", bucketName, objPath)
	if err != nil || row == nil {
		storageErr(c, 404, "not_found", "Object not found")
		return
	}

	url, err := h.storage.SignDownload(ctx, bucketName+"/"+objPath, time.Duration(req.ExpiresIn)*time.Second)
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
	if err := c.ShouldBindJSON(&req); err != nil {
		storageErr(c, 400, "bad_request", "Invalid request")
		return
	}
	if req.ExpiresIn <= 0 {
		req.ExpiresIn = 3600
	}

	ctx := h.rlsCtx(c)
	var results []gin.H
	for _, p := range req.Paths {
		p = strings.TrimPrefix(p, "/")
		url, err := h.storage.SignDownload(ctx, bucketName+"/"+p, time.Duration(req.ExpiresIn)*time.Second)
		if err != nil {
			results = append(results, gin.H{"path": p, "error": err.Error()})
			continue
		}
		results = append(results, gin.H{"path": p, "signedURL": url})
	}
	c.JSON(200, results)
}

func (h *StorageV1Handler) createSignedUploadURL(c *gin.Context) {
	bucketName := c.Param("bucket")
	objPath := h.cleanPath(c.Param("path"))

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
	var uploadedBy any
	if session.UserID != "" {
		uploadedBy = session.UserID
	}
	if err := h.probeUploadPermission(c, bucketName, objPath, uploadedBy); err != nil {
		h.uploadWriteError(c, err)
		return
	}

	token := h.signUploadToken(bucketName, objPath, session.UserID)
	if token == "" {
		storageErr(c, 500, "internal", "Failed to create signed upload URL")
		return
	}

	c.JSON(200, gin.H{
		"url":   fmt.Sprintf("/storage/v1/object/upload/sign/%s/%s", bucketName, objPath),
		"token": token,
		"path":  objPath,
	})
}

// probeUploadPermission reports whether the bucket's RLS policies permit the
// caller to write this object, without persisting anything. It runs the same
// INSERT (or upsert, honouring x-upsert) the signed-upload redemption performs,
// under the caller's Postgres role, inside a transaction that is always rolled
// back, and returns the raw database error so callers can map an RLS denial to
// 403, a duplicate to 409, etc. via uploadWriteError.
func (h *StorageV1Handler) probeUploadPermission(c *gin.Context, bucketName, objPath string, uploadedBy any) error {
	ctx := h.rlsCtx(c)
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if c.GetHeader("x-upsert") == "true" {
		_, err = tx.Exec(ctx,
			`INSERT INTO storage.objects (bucket_id, name, size, mime, uploaded_by)
			 VALUES ($1, $2, 0, '', $3)
			 ON CONFLICT (bucket_id, name)
			 DO UPDATE SET uploaded_at = NOW()`,
			bucketName, objPath, uploadedBy)
	} else {
		_, err = tx.Exec(ctx,
			"INSERT INTO storage.objects (bucket_id, name, size, mime, uploaded_by) VALUES ($1, $2, 0, '', $3)",
			bucketName, objPath, uploadedBy)
	}
	return err
}

func (h *StorageV1Handler) uploadToSignedURL(c *gin.Context) {
	bucketName := c.Param("bucket")
	objPath := h.cleanPath(c.Param("path"))

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

	contentType := c.ContentType()
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	var maxBytes int64 = 50 * 1024 * 1024
	if bucket.MaxSize != "" {
		if mb := parseSizeBytes(bucket.MaxSize); mb > 0 {
			maxBytes = mb
		}
	}
	limitedBody := http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)

	key := bucketName + "/" + objPath
	if err := h.storage.Upload(c.Request.Context(), key, limitedBody, contentType, c.Request.ContentLength); err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			storageErr(c, 413, "payload_too_large", "File too large")
			return
		}
		h.logger.Error("signed upload error", "error", err)
		storageErr(c, 500, "internal", "Upload failed")
		return
	}

	// The HMAC upload token is the authorization for this route (there is no
	// jwtAuth on it), so the metadata write runs as service_role rather than
	// the anonymous caller, equivalent to an S3 presigned PUT. The owner bound
	// into the token at mint time is written to uploaded_by so owner-scoped RLS
	// policies match the row the same way they matched the mint-time probe.
	ctx := c.Request.Context()
	size := c.Request.ContentLength
	if size < 0 {
		size = 0
	}
	var uploadedBy any
	if owner != "" {
		uploadedBy = owner
	}
	_, _ = h.db.Exec(ctx,
		`INSERT INTO storage.objects (bucket_id, name, size, mime, uploaded_by)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (bucket_id, name)
		 DO UPDATE SET size = EXCLUDED.size, mime = EXCLUDED.mime, uploaded_by = EXCLUDED.uploaded_by, uploaded_at = NOW()`,
		bucketName, objPath, size, contentType, uploadedBy)

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
