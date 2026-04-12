package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/saedx1/ultrabase/internal/app"
	"github.com/saedx1/ultrabase/internal/domain"
)

const (
	contextKeySession = "ultrabase_session"
	contextKeyUserID  = "ultrabase_user_id"
)

// requestLogger logs each request (pretty in dev, JSON in prod).
func requestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration", time.Since(start).Round(time.Microsecond),
			"user_id", c.GetString(contextKeyUserID),
		)
	}
}

// corsMiddleware handles CORS headers.
func corsMiddleware(cfg domain.CORS, devMode bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		origins := cfg.Origins
		if devMode && len(origins) == 0 {
			origins = []string{"*"}
		}

		origin := c.GetHeader("Origin")
		allowed := false
		for _, o := range origins {
			if o == "*" || o == origin {
				allowed = true
				break
			}
		}
		if allowed && origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		} else if len(origins) > 0 {
			c.Header("Access-Control-Allow-Origin", origins[0])
			c.Header("Vary", "Origin")
		}

		methods := cfg.Methods
		if len(methods) == 0 {
			methods = []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"}
		}
		c.Header("Access-Control-Allow-Methods", strings.Join(methods, ", "))

		headers := cfg.Headers
		if len(headers) == 0 {
			// Includes the headers supabase-js attaches on every request
			// (apikey, x-client-info) plus the PostgREST schema selectors
			// (content-profile, accept-profile) and the existing set.
			headers = []string{
				"Authorization", "Content-Type", "Prefer", "Accept",
				"apikey", "x-client-info",
				"Range", "Range-Unit",
				"Content-Profile", "Accept-Profile",
				"X-Requested-With",
			}
		}
		c.Header("Access-Control-Allow-Headers", strings.Join(headers, ", "))
		c.Header("Access-Control-Expose-Headers", "Content-Range, Content-Profile, Location")

		if cfg.Credentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}

		if cfg.MaxAge > 0 {
			c.Header("Access-Control-Max-Age", strconv.Itoa(cfg.MaxAge))
		}

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// bodySizeLimit limits request body size.
func bodySizeLimit(maxSize string) gin.HandlerFunc {
	limit := parseSizeBytes(maxSize)
	if limit == 0 {
		limit = 1 << 20 // 1MB default
	}
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

// jwtAuth validates the Authorization: Bearer <token> header. The signing key
// is resolved per-request from the JWTKeyManager using the kid header claim.
//
// Claims shape follows GoTrue: sub is a string UUID, role is one of
// "anon" | "authenticated" | "service_role", aud is "authenticated". The raw
// encoded token is stashed on the session so the auth.jwt() SQL helper can
// read it.
func jwtAuth(keys *app.JWTKeyManager, required bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")

		// Check admin key first — treated as service_role, the GoTrue name
		// for a server-side bypass key.
		adminKey := os.Getenv("ULTRABASE_ADMIN_KEY")
		if adminKey != "" && header == "Bearer "+adminKey {
			c.Set(contextKeySession, domain.Session{
				Role:            "service_role",
				IsAuthenticated: true,
			})
			c.Set("is_admin", true)
			c.Next()
			return
		}

		if !strings.HasPrefix(header, "Bearer ") {
			if required {
				problemJSON(c, 401, "unauthorized", "Missing or invalid Authorization header")
				c.Abort()
				return
			}
			// Anonymous access allowed — pin role to 'anon' so RLS helpers
			// return a deterministic value.
			c.Set(contextKeySession, domain.Session{
				Role:            "anon",
				IsAuthenticated: false,
			})
			c.Next()
			return
		}

		tokenStr := strings.TrimPrefix(header, "Bearer ")
		token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			kid, _ := token.Header["kid"].(string)
			key, err := keys.Get(c.Request.Context(), kid)
			if err != nil {
				return nil, err
			}
			return key.Secret, nil
		}, jwt.WithExpirationRequired())

		if err != nil || !token.Valid {
			if required {
				problemJSON(c, 401, "unauthorized", "Invalid or expired token")
				c.Abort()
				return
			}
			c.Set(contextKeySession, domain.Session{Role: "anon", IsAuthenticated: false})
			c.Next()
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			problemJSON(c, 401, "unauthorized", "Invalid token claims")
			c.Abort()
			return
		}

		// GoTrue contract: sub is a UUID string. We no longer accept number
		// sub values — legacy tokens signed before the UUID migration won't
		// validate, which is intentional (users re-login).
		userID, _ := claims["sub"].(string)
		if userID == "" {
			if required {
				problemJSON(c, 401, "unauthorized", "Invalid token: missing sub claim")
				c.Abort()
				return
			}
			c.Set(contextKeySession, domain.Session{Role: "anon", IsAuthenticated: false})
			c.Next()
			return
		}

		role, _ := claims["role"].(string)
		if role == "" {
			role = "authenticated"
		}
		email, _ := claims["email"].(string)

		session := domain.Session{
			UserID:          userID,
			Role:            role,
			Email:           email,
			JWT:             tokenStr,
			IsAuthenticated: role != "anon",
		}
		c.Set(contextKeySession, session)
		c.Set(contextKeyUserID, userID)
		c.Next()
	}
}

// adminKeyAuth protects admin endpoints.
func adminKeyAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		adminKey := os.Getenv("ULTRABASE_ADMIN_KEY")
		if adminKey == "" {
			c.JSON(404, gin.H{"error": "not found"})
			c.Abort()
			return
		}

		header := c.GetHeader("Authorization")
		if header != "Bearer "+adminKey {
			problemJSON(c, 401, "unauthorized", "Invalid admin key")
			c.Abort()
			return
		}

		c.Next()
	}
}

// getSession retrieves the session from context.
func getSession(c *gin.Context) domain.Session {
	if s, ok := c.Get(contextKeySession); ok {
		return s.(domain.Session)
	}
	return domain.Session{}
}

// isAdmin checks if the request is authenticated with the admin key.
func isAdmin(c *gin.Context) bool {
	v, _ := c.Get("is_admin")
	return v == true
}

// errTypeToCode maps ultrabase's error-type slugs onto PostgREST-compatible
// error codes. Postgres SQLSTATEs are used where they fit; PGRST* codes are
// reserved for API-level errors that have no underlying SQLSTATE.
var errTypeToCode = map[string]string{
	"bad_request":  "PGRST100",
	"unauthorized": "PGRST301",
	"forbidden":    "42501",
	"not_found":    "PGRST116",
	"conflict":     "23505",
	"validation":   "23514",
	"internal":     "XX000",
}

// pgJSON writes a PostgREST-compatible error body: {code, message, details, hint}.
// All four fields are always present so clients can rely on the shape.
func pgJSON(c *gin.Context, status int, code, message, details, hint string) {
	c.JSON(status, gin.H{
		"code":    code,
		"message": message,
		"details": details,
		"hint":    hint,
	})
}

// problemJSON writes a PostgREST-compatible error response. The errType slug
// is mapped to a code via errTypeToCode; detail becomes the message field.
// Name is kept for historical reasons — the output is no longer RFC 7807.
func problemJSON(c *gin.Context, status int, errType, detail string) {
	code, ok := errTypeToCode[errType]
	if !ok {
		code = "PGRST000"
	}
	pgJSON(c, status, code, detail, "", "")
}

// problemJSONWithParams writes a validation error that carries per-field
// diagnostics. The params list is rendered as a JSON string in `details` so
// the top-level shape stays PostgREST-compatible.
func problemJSONWithParams(c *gin.Context, status int, errType, detail string, params []gin.H) {
	code, ok := errTypeToCode[errType]
	if !ok {
		code = "PGRST000"
	}
	details := ""
	if len(params) > 0 {
		if b, err := json.Marshal(params); err == nil {
			details = string(b)
		}
	}
	pgJSON(c, status, code, detail, details, "")
}

// parseSizeBytes parses "1MB", "500KB", etc. into bytes.
func parseSizeBytes(s string) int64 {
	s = strings.TrimSpace(strings.ToUpper(s))
	multipliers := map[string]int64{
		"KB": 1024,
		"MB": 1024 * 1024,
		"GB": 1024 * 1024 * 1024,
		"TB": 1024 * 1024 * 1024 * 1024,
	}
	for suffix, mult := range multipliers {
		if strings.HasSuffix(s, suffix) {
			numStr := strings.TrimSuffix(s, suffix)
			if n, err := strconv.ParseFloat(strings.TrimSpace(numStr), 64); err == nil {
				return int64(n * float64(mult))
			}
		}
	}
	// Try plain number as bytes
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	return 0
}

// computeHMACSignature computes HMAC-SHA256 for webhook signing.
func computeHMACSignature(secret, timestamp, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "." + body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
