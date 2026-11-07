package http

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
)

// StorageHandler serves serverless-friendly storage endpoints that return
// presigned URLs for direct-to-provider uploads and downloads.
type StorageHandler struct {
	cfg     *domain.Config
	db      domain.Database
	logger  *slog.Logger
	storage domain.ObjectStore
	jwtKeys *app.JWTKeyManager
}

func NewStorageHandler(deps ServerDeps) *StorageHandler {
	return &StorageHandler{
		cfg:     deps.Config,
		db:      deps.DB.Database,
		logger:  deps.Logger,
		storage: deps.Storage,
		jwtKeys: deps.JWTKeys,
	}
}

func (h *StorageHandler) Mount(root *gin.RouterGroup) {
	for bucketName, bucket := range h.cfg.Storage {
		name := bucketName
		b := bucket

		group := root.Group("/storage/" + name)
		group.POST("/sign", jwtAuth(h.jwtKeys, true), h.handleSignUpload(name, b))
		group.GET("/:id", jwtAuth(h.jwtKeys, !b.Public), h.handleSignDownload(name, b))
		group.DELETE("/:id", jwtAuth(h.jwtKeys, true), h.handleDelete(name, b))
	}
}

// maxKeyBytes is the S3 object key limit.
const maxKeyBytes = 1024

func (h *StorageHandler) handleSignUpload(bucketName string, bucket domain.Bucket) gin.HandlerFunc {
	return func(c *gin.Context) {
		session := getSession(c)
		var req struct {
			ContentType string  `json:"content_type" binding:"required"`
			Size        int64   `json:"size"`
			Path        *string `json:"path"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			problemJSON(c, 400, "bad_request", "Missing content_type")
			return
		}

		if len(bucket.Types) > 0 && !matchesMIME(req.ContentType, bucket.Types) {
			problemJSON(c, 422, "validation",
				fmt.Sprintf("Content type %q not allowed. Allowed: %v", req.ContentType, bucket.Types))
			return
		}

		// A presigned PUT cannot cap length, so max_size needs a declared size to sign.
		maxBytes := parseSizeBytes(bucket.MaxSize)
		if req.Size < 0 {
			problemJSON(c, 400, "bad_request", "size must not be negative")
			return
		}
		if maxBytes > 0 && req.Size == 0 {
			problemJSON(c, 400, "bad_request", "size is required for buckets with max_size")
			return
		}
		if maxBytes > 0 && req.Size > maxBytes {
			problemJSON(c, 422, "validation",
				fmt.Sprintf("File size %d bytes exceeds maximum %s", req.Size, bucket.MaxSize))
			return
		}
		if !anonMayReach(c, h.cfg, bucketName) {
			problemJSON(c, 403, "forbidden", "Not authorized to upload to this bucket")
			return
		}

		key := generateRandomToken()
		if req.Path != nil {
			clean, err := cleanPath(*req.Path)
			if err != nil || clean != *req.Path || len(bucketName)+1+len(clean) > maxKeyBytes {
				problemJSON(c, 400, "bad_request", "Invalid path")
				return
			}
			key = clean
		}

		url, err := h.storage.SignUpload(c.Request.Context(), bucketName+"/"+key, req.ContentType, req.Size, 15*time.Minute)
		if err != nil {
			h.logger.Error("sign upload error", "error", err)
			problemJSON(c, 500, "internal", "Failed to generate upload URL")
			return
		}

		row := objectRow{bucket: bucketName, name: key, size: req.Size, mime: req.ContentType,
			uploadedBy: nullIfEmpty(session.UserID), metadata: objectMetadataJSON(req.Size, req.ContentType, "")}
		upsert := c.GetHeader("x-upsert") == "true"
		if err := writeObjectRow(rlsContext(h.db, c), h.db, row, false, upsert); err != nil {
			switch {
			case isDuplicateKeyErr(err):
				problemJSON(c, 409, "conflict", "An object already exists at this path")
			case isPermissionDenied(err):
				problemJSON(c, 403, "forbidden", "Not authorized to upload to this bucket")
			default:
				h.logger.Error("record object", "error", err)
				problemJSON(c, 500, "internal", "Failed to record object")
			}
			return
		}

		c.JSON(200, gin.H{"id": key, "path": key, "upload_url": url})
	}
}

func (h *StorageHandler) handleSignDownload(bucketName string, bucket domain.Bucket) gin.HandlerFunc {
	return func(c *gin.Context) {
		name, err := cleanPath(c.Param("id"))
		if err != nil {
			problemJSON(c, 400, "bad_request", "Invalid key")
			return
		}
		if !bucket.Public && !anonMayReach(c, h.cfg, bucketName) {
			problemJSON(c, 404, "not_found", "Object not found")
			return
		}

		ctx := rlsContext(h.db, c)
		if bucket.Public {
			if ctx, err = serviceContext(h.db, c.Request.Context()); err != nil {
				problemJSON(c, 500, "internal", "Failed to generate download URL")
				return
			}
		}
		row, err := h.db.QueryRow(ctx,
			"SELECT mime FROM storage.objects WHERE name = $1 AND bucket_id = $2", name, bucketName)
		if err != nil || row == nil {
			problemJSON(c, 404, "not_found", "Object not found")
			return
		}

		opts := downloadOptions(asString(row["mime"]), bucket.Public, "", false)
		url, err := h.storage.SignDownload(ctx, bucketName+"/"+name, 15*time.Minute, opts)
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to generate download URL")
			return
		}

		c.JSON(200, gin.H{
			"url": url,
		})
	}
}

func (h *StorageHandler) handleDelete(bucketName string, bucket domain.Bucket) gin.HandlerFunc {
	return func(c *gin.Context) {
		name, err := cleanPath(c.Param("id"))
		if err != nil {
			problemJSON(c, 400, "bad_request", "Invalid key")
			return
		}
		if !anonMayReach(c, h.cfg, bucketName) {
			problemJSON(c, 404, "not_found", "Object not found")
			return
		}

		rows, err := h.db.Query(rlsContext(h.db, c),
			"DELETE FROM storage.objects WHERE name = $1 AND bucket_id = $2 RETURNING name", name, bucketName)
		if err != nil {
			h.logger.Error("storage delete error", "error", err)
			problemJSON(c, 500, "internal", "Failed to delete object")
			return
		}
		if len(rows) == 0 {
			problemJSON(c, 404, "not_found", "Object not found")
			return
		}

		deleteBytes(c.Request.Context(), h.storage, h.logger, bucketName, rows)
		c.Status(204)
	}
}

// matchesMIME checks if a content type matches any of the allowed patterns (e.g., "image/*").
func matchesMIME(contentType string, allowed []string) bool {
	for _, pattern := range allowed {
		if pattern == contentType {
			return true
		}
		if idx := len(pattern) - 1; idx > 0 && pattern[idx] == '*' {
			prefix := pattern[:idx]
			if len(contentType) >= len(prefix) && contentType[:len(prefix)] == prefix {
				return true
			}
		}
	}
	return false
}
