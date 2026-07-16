package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/adapter/funcs"
	"github.com/instancez/instancez/internal/domain"
)

// bearerToken extracts the raw token from an "Authorization: Bearer <token>"
// header value, returning "" when the prefix is absent.
func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimPrefix(header, prefix)
}

// FunctionsHandler serves code functions at /functions/v1/:name.
type FunctionsHandler struct{ rt domain.FunctionRuntime }

// NewFunctionsHandler creates a FunctionsHandler backed by rt. rt may be nil,
// in which case all requests return 501 Not Implemented.
func NewFunctionsHandler(rt domain.FunctionRuntime) *FunctionsHandler {
	return &FunctionsHandler{rt: rt}
}

// Mount mounts the function routes on g. Both the bare function path and any
// subpath under it route to the handler, which receives the full request path
// (req.path) and can route internally — matching Supabase Edge Functions, where
// /functions/v1/<name>/<subpath> reaches <name>.
func (h *FunctionsHandler) Mount(g *gin.RouterGroup) {
	g.Any("/:name", h.invoke)
	g.Any("/:name/*rest", h.invoke)
}

func (h *FunctionsHandler) invoke(c *gin.Context) {
	if h.rt == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"message": "functions runtime not available"})
		return
	}
	name := c.Param("name")
	if !h.rt.Has(name) {
		c.JSON(http.StatusNotFound, gin.H{"message": "function not found"})
		return
	}
	if h.rt.AuthRequired(name) && claimsFromGinContext(c) == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "authentication required"})
		return
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		problemJSON(c, 400, "bad_request", "Cannot read request body")
		return
	}
	// Reuse the request ID set by requestIDMiddleware (echoed as X-Request-Id).
	// If the middleware hasn't run on this route group, fall back to a fresh ID.
	reqID := domain.RequestIDFromContext(c.Request.Context())
	if reqID == "" {
		var b [16]byte
		if _, err := rand.Read(b[:]); err == nil {
			reqID = hex.EncodeToString(b[:])
		}
	}

	resp, err := h.rt.Invoke(c.Request.Context(), domain.FunctionRequest{
		Name:        name,
		Method:      c.Request.Method,
		Path:        c.Request.URL.Path,
		Query:       c.Request.URL.Query(),
		RawQuery:    c.Request.URL.RawQuery,
		Headers:     c.Request.Header,
		Body:        body,
		Claims:      claimsFromGinContext(c),
		CallerToken: bearerToken(c.GetHeader("Authorization")),
		RequestID:   reqID,
	})
	if err != nil {
		switch {
		case errors.Is(err, funcs.ErrTimeout) || errors.Is(err, context.DeadlineExceeded):
			c.JSON(http.StatusGatewayTimeout, gin.H{"message": "function invocation timed out"})
		case errors.Is(err, funcs.ErrSaturated):
			c.JSON(http.StatusServiceUnavailable, gin.H{"message": "functions runtime saturated"})
		default: // funcs.ErrWorkerFailed and any other invoke error
			// Do NOT echo err to the client: it can leak internal details such
			// as the worker's unix socket path (dial unix /tmp/inz-fn-*.sock).
			// Log the real error server-side and return a generic body.
			slog.Default().Error("functions: invoke failed", "fn", name, "request_id", reqID, "err", err)
			c.JSON(http.StatusBadGateway, gin.H{"message": "bad gateway"})
		}
		return
	}
	if resp.Status < 100 || resp.Status > 599 {
		c.JSON(http.StatusBadGateway, gin.H{"message": "function returned invalid HTTP status"})
		return
	}
	for k, vs := range resp.Headers {
		for _, v := range vs {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Writer.WriteHeader(resp.Status)
	_, _ = c.Writer.Write(resp.Body)
}

// claimsFromGinContext extracts the session stored by jwtAuth middleware and
// converts it to the map[string]any shape expected by FunctionRequest.Claims.
// Returns nil when the session is anonymous (Role == "anon") or absent.
func claimsFromGinContext(c *gin.Context) map[string]any {
	session := getSession(c)
	if !session.IsAuthenticated {
		return nil
	}
	claims := map[string]any{
		"sub":  session.UserID,
		"role": session.Role,
	}
	if session.Email != "" {
		claims["email"] = session.Email
	}
	if session.JWT != "" {
		claims["jwt"] = session.JWT
	}
	return claims
}
