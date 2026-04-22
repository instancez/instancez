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
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/saedx1/ultrabase/internal/app"
	"github.com/saedx1/ultrabase/internal/domain"
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
		db:      deps.DB,
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

	// Download — authenticated path
	sg.GET("/object/authenticated/:bucket/*path", jwtAuth(h.jwtKeys, true), h.downloadObject)
	// Download — public path
	sg.GET("/object/public/:bucket/*path", h.downloadPublicObject)

	// List
	sg.POST("/object/list/:bucket", jwtAuth(h.jwtKeys, true), h.listObjects)

	// Info
	sg.GET("/object/info/authenticated/:bucket/*path", jwtAuth(h.jwtKeys, true), h.objectInfo)

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
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": fmt.Sprintf("Bucket %q not found", id)})
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
	c.JSON(400, gin.H{"statusCode": "400", "error": "not_supported", "message": "Buckets are defined in ultrabase.yaml. Runtime creation is not supported."})
}

func (h *StorageV1Handler) updateBucket(c *gin.Context) {
	c.JSON(400, gin.H{"statusCode": "400", "error": "not_supported", "message": "Buckets are defined in ultrabase.yaml. Runtime modification is not supported."})
}

func (h *StorageV1Handler) deleteBucket(c *gin.Context) {
	c.JSON(400, gin.H{"statusCode": "400", "error": "not_supported", "message": "Buckets are defined in ultrabase.yaml. Runtime deletion is not supported."})
}

func (h *StorageV1Handler) emptyBucket(c *gin.Context) {
	id := c.Param("id")
	if _, ok := h.cfg.Storage[id]; !ok {
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": fmt.Sprintf("Bucket %q not found", id)})
		return
	}

	ctx := c.Request.Context()
	rows, err := h.db.Query(ctx, "SELECT name FROM _objects WHERE bucket_id = $1", id)
	if err != nil {
		h.logger.Error("empty bucket query", "error", err)
		c.JSON(500, gin.H{"statusCode": "500", "error": "internal", "message": "Failed to list objects"})
		return
	}
	for _, row := range rows {
		name, _ := row["name"].(string)
		_ = h.storage.Delete(ctx, id+"/"+name)
	}
	h.db.Exec(ctx, "DELETE FROM _objects WHERE bucket_id = $1", id)
	c.JSON(200, gin.H{"message": "Successfully emptied"})
}

// --- File operation handlers ---

func (h *StorageV1Handler) getBucketConfig(name string) (domain.Bucket, bool) {
	b, ok := h.cfg.Storage[name]
	return b, ok
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
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Bucket not found"})
		return
	}

	session := getSession(c)

	contentType := c.ContentType()
	if ct := c.GetHeader("Content-Type"); ct != "" {
		contentType = strings.Split(ct, ";")[0]
		contentType = strings.TrimSpace(contentType)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Validate MIME
	if len(bucket.Types) > 0 && !matchesMIME(contentType, bucket.Types) {
		c.JSON(422, gin.H{"statusCode": "422", "error": "invalid_mime_type", "message": fmt.Sprintf("Content type %q not allowed", contentType)})
		return
	}

	// Enforce bucket size limit via a limited reader
	var maxBytes int64 = 50 * 1024 * 1024 // 50MB default cap for proxied uploads
	if bucket.MaxSize != "" {
		if mb := parseSizeBytes(bucket.MaxSize); mb > 0 {
			maxBytes = mb
		}
	}
	limitedBody := http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)

	key := bucketName + "/" + objPath
	if err := h.storage.Upload(c.Request.Context(), key, limitedBody, contentType, c.Request.ContentLength); err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			c.JSON(413, gin.H{"statusCode": "413", "error": "payload_too_large", "message": fmt.Sprintf("File exceeds maximum size of %s", bucket.MaxSize)})
			return
		}
		h.logger.Error("upload error", "error", err)
		c.JSON(500, gin.H{"statusCode": "500", "error": "internal", "message": "Upload failed"})
		return
	}

	ctx := c.Request.Context()
	var uploadedBy any
	if session.UserID != "" {
		uploadedBy = session.UserID
	}

	size := c.Request.ContentLength
	if size < 0 {
		size = 0
	}

	if isUpdate {
		h.db.Exec(ctx,
			"UPDATE _objects SET size = $1, mime = $2, uploaded_at = NOW(), uploaded_by = $3 WHERE bucket_id = $4 AND name = $5",
			size, contentType, uploadedBy, bucketName, objPath)
	} else {
		// Upsert: if the path already exists, update it (matches Supabase behavior with upsert header)
		upsert := c.GetHeader("x-upsert") == "true"
		if upsert {
			h.db.Exec(ctx,
				`INSERT INTO _objects (bucket_id, name, size, mime, uploaded_by)
				 VALUES ($1, $2, $3, $4, $5)
				 ON CONFLICT (bucket_id, name)
				 DO UPDATE SET size = EXCLUDED.size, mime = EXCLUDED.mime, uploaded_by = EXCLUDED.uploaded_by, uploaded_at = NOW()`,
				bucketName, objPath, size, contentType, uploadedBy)
		} else {
			_, err := h.db.Exec(ctx,
				"INSERT INTO _objects (bucket_id, name, size, mime, uploaded_by) VALUES ($1, $2, $3, $4, $5)",
				bucketName, objPath, size, contentType, uploadedBy)
			if err != nil {
				if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "23505") {
					c.JSON(409, gin.H{"statusCode": "409", "error": "duplicate", "message": "The resource already exists"})
					return
				}
				h.logger.Error("insert object", "error", err)
				c.JSON(500, gin.H{"statusCode": "500", "error": "internal", "message": "Failed to record object"})
				return
			}
		}
	}

	c.JSON(200, gin.H{
		"Key":     bucketName + "/" + objPath,
		"Id":      objPath,
	})
}

func (h *StorageV1Handler) downloadObject(c *gin.Context) {
	bucketName := c.Param("bucket")
	objPath := h.cleanPath(c.Param("path"))

	if _, ok := h.getBucketConfig(bucketName); !ok {
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Bucket not found"})
		return
	}

	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx, "SELECT id FROM _objects WHERE bucket_id = $1 AND name = $2", bucketName, objPath)
	if err != nil || row == nil {
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Object not found"})
		return
	}

	key := bucketName + "/" + objPath
	body, contentType, err := h.storage.Download(ctx, key)
	if err != nil {
		h.logger.Error("download error", "error", err)
		c.JSON(500, gin.H{"statusCode": "500", "error": "internal", "message": "Download failed"})
		return
	}
	defer body.Close()

	if contentType != "" {
		c.Header("Content-Type", contentType)
	}
	c.Header("Cache-Control", "public, max-age=3600")
	c.Status(200)
	io.Copy(c.Writer, body)
}

func (h *StorageV1Handler) downloadPublicObject(c *gin.Context) {
	bucketName := c.Param("bucket")
	objPath := h.cleanPath(c.Param("path"))

	bucket, ok := h.getBucketConfig(bucketName)
	if !ok {
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Bucket not found"})
		return
	}
	if !bucket.Public {
		c.JSON(400, gin.H{"statusCode": "400", "error": "not_public", "message": "Bucket is not public"})
		return
	}

	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx, "SELECT id FROM _objects WHERE bucket_id = $1 AND name = $2", bucketName, objPath)
	if err != nil || row == nil {
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Object not found"})
		return
	}

	key := bucketName + "/" + objPath
	body, contentType, err := h.storage.Download(ctx, key)
	if err != nil {
		h.logger.Error("download error", "error", err)
		c.JSON(500, gin.H{"statusCode": "500", "error": "internal", "message": "Download failed"})
		return
	}
	defer body.Close()

	if contentType != "" {
		c.Header("Content-Type", contentType)
	}
	c.Header("Cache-Control", "public, max-age=3600")
	c.Status(200)
	io.Copy(c.Writer, body)
}

func (h *StorageV1Handler) listObjects(c *gin.Context) {
	bucketName := c.Param("bucket")
	if _, ok := h.getBucketConfig(bucketName); !ok {
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Bucket not found"})
		return
	}

	var req struct {
		Prefix string `json:"prefix"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
		Search string `json:"search"`
	}
	c.ShouldBindJSON(&req)

	prefix := strings.TrimPrefix(req.Prefix, "/")
	if req.Limit <= 0 {
		req.Limit = 100
	}

	ctx := c.Request.Context()

	query := "SELECT name, size, mime, uploaded_at, metadata FROM _objects WHERE bucket_id = $1"
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
		c.JSON(500, gin.H{"statusCode": "500", "error": "internal", "message": "Failed to list"})
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

func (h *StorageV1Handler) objectInfo(c *gin.Context) {
	bucketName := c.Param("bucket")
	objPath := h.cleanPath(c.Param("path"))

	if _, ok := h.getBucketConfig(bucketName); !ok {
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Bucket not found"})
		return
	}

	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx,
		"SELECT id, name, size, mime, uploaded_at, uploaded_by, metadata FROM _objects WHERE bucket_id = $1 AND name = $2",
		bucketName, objPath)
	if err != nil || row == nil {
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Object not found"})
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

	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx, "SELECT id FROM _objects WHERE bucket_id = $1 AND name = $2", bucketName, objPath)
	if err != nil || row == nil {
		c.Status(404)
		return
	}
	c.Status(200)
}

func (h *StorageV1Handler) removeObjects(c *gin.Context) {
	bucketName := c.Param("bucket")
	if _, ok := h.getBucketConfig(bucketName); !ok {
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Bucket not found"})
		return
	}

	var req struct {
		Prefixes []string `json:"prefixes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"statusCode": "400", "error": "bad_request", "message": "Expected {prefixes: [...]}"})
		return
	}

	ctx := c.Request.Context()
	var deleted []gin.H
	for _, p := range req.Prefixes {
		p = strings.TrimPrefix(p, "/")
		row, err := h.db.QueryRow(ctx, "SELECT id, name, bucket_id FROM _objects WHERE bucket_id = $1 AND name = $2", bucketName, p)
		if err != nil || row == nil {
			continue
		}
		_ = h.storage.Delete(ctx, bucketName+"/"+p)
		h.db.Exec(ctx, "DELETE FROM _objects WHERE bucket_id = $1 AND name = $2", bucketName, p)
		deleted = append(deleted, gin.H{"name": p, "bucket_id": bucketName})
	}
	if deleted == nil {
		deleted = []gin.H{}
	}
	c.JSON(200, deleted)
}

func (h *StorageV1Handler) moveObject(c *gin.Context) {
	var req struct {
		BucketID        string `json:"bucketId"`
		SourceKey       string `json:"sourceKey"`
		DestinationKey  string `json:"destinationKey"`
		DestinationBucket string `json:"destinationBucket"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"statusCode": "400", "error": "bad_request", "message": "Invalid request"})
		return
	}

	srcBucket := req.BucketID
	dstBucket := req.DestinationBucket
	if dstBucket == "" {
		dstBucket = srcBucket
	}

	if _, ok := h.getBucketConfig(srcBucket); !ok {
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Source bucket not found"})
		return
	}
	if _, ok := h.getBucketConfig(dstBucket); !ok {
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Destination bucket not found"})
		return
	}

	ctx := c.Request.Context()
	srcKey := srcBucket + "/" + req.SourceKey
	dstKey := dstBucket + "/" + req.DestinationKey

	if err := h.storage.Copy(ctx, srcKey, dstKey); err != nil {
		h.logger.Error("move copy", "error", err)
		c.JSON(500, gin.H{"statusCode": "500", "error": "internal", "message": "Move failed"})
		return
	}
	if err := h.storage.Delete(ctx, srcKey); err != nil {
		h.logger.Error("move delete", "error", err)
	}

	// Update DB
	h.db.Exec(ctx,
		"UPDATE _objects SET bucket_id = $1, name = $2 WHERE bucket_id = $3 AND name = $4",
		dstBucket, req.DestinationKey, srcBucket, req.SourceKey)

	c.JSON(200, gin.H{"message": "Successfully moved"})
}

func (h *StorageV1Handler) copyObject(c *gin.Context) {
	var req struct {
		BucketID        string `json:"bucketId"`
		SourceKey       string `json:"sourceKey"`
		DestinationKey  string `json:"destinationKey"`
		DestinationBucket string `json:"destinationBucket"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"statusCode": "400", "error": "bad_request", "message": "Invalid request"})
		return
	}

	srcBucket := req.BucketID
	dstBucket := req.DestinationBucket
	if dstBucket == "" {
		dstBucket = srcBucket
	}

	if _, ok := h.getBucketConfig(srcBucket); !ok {
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Source bucket not found"})
		return
	}
	if _, ok := h.getBucketConfig(dstBucket); !ok {
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Destination bucket not found"})
		return
	}

	ctx := c.Request.Context()
	srcKey := srcBucket + "/" + req.SourceKey
	dstKey := dstBucket + "/" + req.DestinationKey

	if err := h.storage.Copy(ctx, srcKey, dstKey); err != nil {
		h.logger.Error("copy", "error", err)
		c.JSON(500, gin.H{"statusCode": "500", "error": "internal", "message": "Copy failed"})
		return
	}

	// Copy DB row
	h.db.Exec(ctx,
		`INSERT INTO _objects (bucket_id, name, size, mime, uploaded_by, metadata)
		 SELECT $1, $2, size, mime, uploaded_by, metadata FROM _objects WHERE bucket_id = $3 AND name = $4
		 ON CONFLICT (bucket_id, name) DO UPDATE SET size = EXCLUDED.size, mime = EXCLUDED.mime, uploaded_at = NOW()`,
		dstBucket, req.DestinationKey, srcBucket, req.SourceKey)

	c.JSON(200, gin.H{"Key": dstBucket + "/" + req.DestinationKey})
}

// --- Signed URL handlers ---

func (h *StorageV1Handler) createSignedURL(c *gin.Context) {
	bucketName := c.Param("bucket")
	objPath := h.cleanPath(c.Param("path"))

	if _, ok := h.getBucketConfig(bucketName); !ok {
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Bucket not found"})
		return
	}

	var req struct {
		ExpiresIn int `json:"expiresIn"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ExpiresIn <= 0 {
		req.ExpiresIn = 3600
	}

	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx, "SELECT id FROM _objects WHERE bucket_id = $1 AND name = $2", bucketName, objPath)
	if err != nil || row == nil {
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Object not found"})
		return
	}

	url, err := h.storage.SignDownload(ctx, bucketName+"/"+objPath, time.Duration(req.ExpiresIn)*time.Second)
	if err != nil {
		h.logger.Error("sign download", "error", err)
		c.JSON(500, gin.H{"statusCode": "500", "error": "internal", "message": "Failed to create signed URL"})
		return
	}

	c.JSON(200, gin.H{"signedURL": url})
}

func (h *StorageV1Handler) createSignedURLs(c *gin.Context) {
	bucketName := c.Param("bucket")
	if _, ok := h.getBucketConfig(bucketName); !ok {
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Bucket not found"})
		return
	}

	var req struct {
		ExpiresIn int      `json:"expiresIn"`
		Paths     []string `json:"paths"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"statusCode": "400", "error": "bad_request", "message": "Invalid request"})
		return
	}
	if req.ExpiresIn <= 0 {
		req.ExpiresIn = 3600
	}

	ctx := c.Request.Context()
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
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Bucket not found"})
		return
	}

	token := h.signUploadToken(bucketName, objPath)

	c.JSON(200, gin.H{
		"url":   fmt.Sprintf("/storage/v1/object/upload/sign/%s/%s", bucketName, objPath),
		"token": token,
		"path":  objPath,
	})
}

func (h *StorageV1Handler) uploadToSignedURL(c *gin.Context) {
	bucketName := c.Param("bucket")
	objPath := h.cleanPath(c.Param("path"))

	bucket, ok := h.getBucketConfig(bucketName)
	if !ok {
		c.JSON(404, gin.H{"statusCode": "404", "error": "not_found", "message": "Bucket not found"})
		return
	}

	token := c.Query("token")
	if !h.verifyUploadToken(token, bucketName, objPath) {
		c.JSON(400, gin.H{"statusCode": "400", "error": "invalid_token", "message": "Invalid or expired upload token"})
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
			c.JSON(413, gin.H{"statusCode": "413", "error": "payload_too_large", "message": "File too large"})
			return
		}
		h.logger.Error("signed upload error", "error", err)
		c.JSON(500, gin.H{"statusCode": "500", "error": "internal", "message": "Upload failed"})
		return
	}

	ctx := c.Request.Context()
	size := c.Request.ContentLength
	if size < 0 {
		size = 0
	}
	h.db.Exec(ctx,
		`INSERT INTO _objects (bucket_id, name, size, mime)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (bucket_id, name)
		 DO UPDATE SET size = EXCLUDED.size, mime = EXCLUDED.mime, uploaded_at = NOW()`,
		bucketName, objPath, size, contentType)

	c.JSON(200, gin.H{
		"Key":  bucketName + "/" + objPath,
		"path": objPath,
		"fullPath": bucketName + "/" + objPath,
	})
}

// signUploadToken creates an HMAC token for signed uploads.
func (h *StorageV1Handler) signUploadToken(bucket, objPath string) string {
	active, err := h.jwtKeys.Active(context.Background())
	if err != nil {
		return ""
	}
	expiry := time.Now().Add(storageUploadTokenExpiry).Unix()
	payload := fmt.Sprintf("%s/%s:%d", bucket, objPath, expiry)
	mac := hmac.New(sha256.New, active.Secret)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%d.%s", expiry, sig)
}

func (h *StorageV1Handler) verifyUploadToken(token, bucket, objPath string) bool {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return false
	}

	var expiry int64
	if _, err := fmt.Sscanf(parts[0], "%d", &expiry); err != nil {
		return false
	}
	if time.Now().Unix() > expiry {
		return false
	}

	active, err := h.jwtKeys.Active(context.Background())
	if err != nil {
		return false
	}

	payload := fmt.Sprintf("%s/%s:%d", bucket, objPath, expiry)
	mac := hmac.New(sha256.New, active.Secret)
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(parts[1]), []byte(expected))
}

