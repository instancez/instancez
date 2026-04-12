package http

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/saedx1/ultrabase/internal/app"
	"github.com/saedx1/ultrabase/internal/domain"
)

// StorageHandler serves storage endpoints (signed URLs).
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
		db:      deps.DB,
		logger:  deps.Logger,
		storage: deps.Storage,
		jwtKeys: deps.JWTKeys,
	}
}

func (h *StorageHandler) Mount(api *gin.RouterGroup) {
	for bucketName, bucket := range h.cfg.Storage {
		name := bucketName
		b := bucket

		group := api.Group("/storage/" + name)

		// Sign upload — requires auth
		group.POST("/sign", jwtAuth(h.jwtKeys, true), h.handleSignUpload(name, b))

		// Sign download — public buckets don't need auth
		if b.Public {
			group.GET("/:id", h.handleSignDownload(name, b))
		} else {
			group.GET("/:id", jwtAuth(h.jwtKeys, true), h.handleSignDownload(name, b))
		}

		// Delete — requires auth
		group.DELETE("/:id", jwtAuth(h.jwtKeys, true), h.handleDelete(name, b))
	}
}

func (h *StorageHandler) handleSignUpload(bucketName string, bucket domain.Bucket) gin.HandlerFunc {
	return func(c *gin.Context) {
		session := getSession(c)
		var req struct {
			ContentType string `json:"content_type" binding:"required"`
			Size        int64  `json:"size"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			problemJSON(c, 400, "bad_request", "Missing content_type")
			return
		}

		// Validate MIME type against bucket types
		if len(bucket.Types) > 0 && !matchesMIME(req.ContentType, bucket.Types) {
			problemJSON(c, 422, "validation",
				fmt.Sprintf("Content type %q not allowed. Allowed: %v", req.ContentType, bucket.Types))
			return
		}

		// Validate file size against bucket max_size
		if bucket.MaxSize != "" && req.Size > 0 {
			maxBytes := parseSizeBytes(bucket.MaxSize)
			if maxBytes > 0 && req.Size > maxBytes {
				problemJSON(c, 422, "validation",
					fmt.Sprintf("File size %d bytes exceeds maximum %s", req.Size, bucket.MaxSize))
				return
			}
		}

		// Generate object key (UUID)
		key := generateRandomToken()

		// Sign upload URL
		url, err := h.storage.SignUpload(c.Request.Context(), bucketName+"/"+key, req.ContentType, 15*time.Minute)
		if err != nil {
			h.logger.Error("sign upload error", "error", err)
			problemJSON(c, 500, "internal", "Failed to generate upload URL")
			return
		}

		// Record in _objects
		ctx := c.Request.Context()
		var uploadedBy any
		if session.UserID != "" {
			uploadedBy = session.UserID
		}
		_, err = h.db.Exec(ctx,
			"INSERT INTO _objects (id, bucket_id, size, mime, uploaded_by) VALUES ($1, $2, 0, $3, $4)",
			key, bucketName, req.ContentType, uploadedBy)
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to record object")
			return
		}

		c.JSON(200, gin.H{
			"id":         key,
			"upload_url": url,
		})
	}
}

func (h *StorageHandler) handleSignDownload(bucketName string, bucket domain.Bucket) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")

		// Verify object exists
		ctx := c.Request.Context()
		row, err := h.db.QueryRow(ctx,
			"SELECT id, mime FROM _objects WHERE id = $1 AND bucket_id = $2", id, bucketName)
		if err != nil || row == nil {
			problemJSON(c, 404, "not_found", "Object not found")
			return
		}

		url, err := h.storage.SignDownload(ctx, bucketName+"/"+id, 15*time.Minute)
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
		id := c.Param("id")
		ctx := c.Request.Context()

		// Delete from provider
		if err := h.storage.Delete(ctx, bucketName+"/"+id); err != nil {
			h.logger.Error("storage delete error", "error", err)
			problemJSON(c, 500, "internal", "Failed to delete object")
			return
		}

		// Delete from _objects
		h.db.Exec(ctx, "DELETE FROM _objects WHERE id = $1 AND bucket_id = $2", id, bucketName)

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
