package http

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
)

const (
	contextKeySession   = "instancez_session"
	contextKeyUserID    = "instancez_user_id"
	contextKeyRequestID = "instancez_request_id"
)

// requestIDSafe restricts incoming X-Request-Id values to characters that
// are always safe to quote into a SQL string literal and to log. Clients
// that send anything fancier get a fresh generated ID instead — their
// header is echoed nowhere.
var requestIDSafe = regexp.MustCompile(`^[A-Za-z0-9_.:\-]{1,128}$`)

// requestIDMiddleware reads X-Request-Id from the request (case-insensitive
// per RFC 7230), validates it, or generates a fresh 128-bit hex ID. The ID
// is:
//
//  1. Stored in gin context so handlers and loggers can access it
//  2. Echoed back on the response (X-Request-Id) so clients can correlate
//  3. Attached to the request context via domain.ContextWithRequestID so
//     the postgres adapter can publish it to SQL as a per-transaction GUC
//     for RLS policies that want to log or gate by request ID.
func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.GetHeader("X-Request-Id"))
		if id == "" || !requestIDSafe.MatchString(id) {
			id = generateRequestID()
		}
		c.Set(contextKeyRequestID, id)
		c.Header("X-Request-Id", id)
		c.Request = c.Request.WithContext(domain.ContextWithRequestID(c.Request.Context(), id))
		c.Next()
	}
}

// generateRequestID returns a 16-byte random hex string. crypto/rand is
// used so two concurrent requests cannot collide under load, and so the
// ID isn't guessable (clients that want to trace specific flows must set
// their own header).
func generateRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// requestLogger logs each request: fixed-width aligned columns in dev, the full
// structured record in prod (for the JSON stream).
func requestLogger(logger *slog.Logger, devMode bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		dur := time.Since(start).Round(time.Microsecond)
		if devMode {
			// One fmt.Fprintf = one Write of a sub-4KB line, so concurrent
			// requests can't interleave mid-line. Columns: status, duration
			// (right-aligned), method, then the path trails free.
			_, _ = fmt.Fprintf(os.Stdout, "  %3d  %s  %-7s %s\n",
				c.Writer.Status(), padLeft(dur.String(), 8), c.Request.Method, c.Request.URL.Path)
			return
		}
		logger.Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration", dur,
			"user_id", c.GetString(contextKeyUserID),
			"request_id", c.GetString(contextKeyRequestID),
		)
	}
}

// padLeft right-aligns s to w display columns by rune count, so multibyte units
// like "µs" still line up with plain-ASCII durations like "15ms".
func padLeft(s string, w int) string {
	if n := utf8.RuneCountInString(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
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
			if o == origin || o == "*" {
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

		c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")

		// Includes the headers supabase-js attaches on every request (apikey,
		// x-client-info) plus the PostgREST schema selectors (content-profile,
		// accept-profile) and the existing set. Not app-configurable — no app
		// has a reason to add custom headers to a supabase-js-shaped API.
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, Prefer, Accept, apikey, x-client-info, Range, Range-Unit, Content-Profile, Accept-Profile, X-Requested-With")
		c.Header("Access-Control-Expose-Headers", "Content-Range, Content-Profile, Location")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// bodySizeLimit limits request body size. Storage upload paths are excluded
// because the storage handler applies per-bucket size limits itself.
func bodySizeLimit(maxSize string) gin.HandlerFunc {
	limit := parseSizeBytes(maxSize)
	if limit == 0 {
		limit = 1 << 20 // 1MB default
	}
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/storage/v1/object/") {
			c.Next()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

// verifySignedJWT parses tokenStr and resolves its signing key from keys by
// the kid header claim, RSA or HMAC depending on the token's alg. Shared by
// jwtAuth (Authorization header) and apiKeyGuard (apikey header) — both
// verify the same kind of token, just read from different headers.
func verifySignedJWT(ctx context.Context, keys *app.JWTKeyManager, tokenStr string) (*jwt.Token, error) {
	return jwt.Parse(tokenStr, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		key, err := keys.Get(ctx, kid)
		if err != nil {
			return nil, err
		}
		switch token.Method.(type) {
		case *jwt.SigningMethodRSA:
			if key.PublicKey == nil {
				return nil, fmt.Errorf("no RSA public key for kid %s", kid)
			}
			return key.PublicKey, nil
		case *jwt.SigningMethodHMAC:
			if len(key.Secret) == 0 {
				return nil, fmt.Errorf("no HMAC secret for kid %s", kid)
			}
			return key.Secret, nil
		default:
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
	},
		jwt.WithExpirationRequired(),
		// 30s clock-skew allowance, matching PostgREST/Supabase exp validation.
		jwt.WithLeeway(30*time.Second))
}

// API key tiers. The publishable key (inz_publishable_…) is the client-safe
// value that gates every request and maps to the anon role; the secret key
// (inz_secret_…) is the server-side key that maps to service_role and unlocks
// the admin surface. Both are opaque env-passed strings, not JWTs, matched by
// constant-time compare.
const (
	tierNone        = ""
	tierPublishable = "publishable"
	tierSecret      = "secret"
)

// keyTier classifies a presented key against the configured publishable and
// secret keys, returning tierNone for an empty or unrecognized value. The env
// is read per call to match the rest of the middleware's key handling and to
// pick up a rotated value without a restart.
func keyTier(key string) string {
	if key == "" {
		return tierNone
	}
	if sk := os.Getenv("INSTANCEZ_SECRET_KEY"); sk != "" && constantTimeEqual(key, sk) {
		return tierSecret
	}
	if pk := os.Getenv("INSTANCEZ_PUBLISHABLE_KEY"); pk != "" && constantTimeEqual(key, pk) {
		return tierPublishable
	}
	return tierNone
}

// jwtAuth resolves the request's Postgres role. The secret key in the apikey
// header maps straight to service_role. Otherwise the role comes from the
// Authorization: Bearer user token, with the signing key resolved per-request
// from the JWTKeyManager via the kid header claim.
//
// User-token claims follow GoTrue: sub is a string UUID, role is one of
// "anon" | "authenticated", aud is "authenticated". The raw encoded token is
// stashed on the session so the auth.jwt() SQL helper can read it.
func jwtAuth(keys *app.JWTKeyManager, required bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		// The secret key is the server-side bypass credential: it maps to
		// service_role and the admin surface with no bearer token involved.
		if keyTier(c.GetHeader("apikey")) == tierSecret {
			c.Set(contextKeySession, domain.Session{
				Role:            "service_role",
				IsAuthenticated: true,
			})
			c.Set("is_admin", true)
			c.Next()
			return
		}

		header := c.GetHeader("Authorization")
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

		// supabase-js echoes the publishable key into the bearer slot on
		// unauthenticated requests. It carries no user identity, so it resolves
		// to anon rather than going through JWT verification.
		if keyTier(tokenStr) == tierPublishable {
			if required {
				problemJSON(c, 401, "unauthorized", "Missing or invalid Authorization header")
				c.Abort()
				return
			}
			c.Set(contextKeySession, domain.Session{Role: "anon", IsAuthenticated: false})
			c.Next()
			return
		}

		token, err := verifySignedJWT(c.Request.Context(), keys, tokenStr)

		// A token that fails verification is a 401, even on anon-allowed
		// routes. The anon role is for requests with no user credentials at all
		// (the publishable-key branch above); a present-but-bad user token is an
		// error, not a silent downgrade that returns anon rows with a 200.
		if err != nil || !token.Valid {
			problemJSON(c, 401, "unauthorized", "Invalid or expired token")
			c.Abort()
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
			// No role claim resolves to anon, the way PostgREST treats a
			// token that doesn't name a role. Defaulting up to authenticated
			// would hand out more access than the token asked for.
			role = "anon"
		}
		// A verified bearer token may only assume anon or authenticated.
		// service_role is never carried on the wire — it comes solely from the
		// secret key (keyTier → tierSecret, handled above). Rejecting any other
		// role means a signature-valid token cannot escalate to a BYPASSRLS role.
		if role != "anon" && role != "authenticated" {
			problemJSON(c, 401, "unauthorized", "Invalid token: unsupported role")
			c.Abort()
			return
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

// apiKeyGuard enforces the apikey header supabase-js attaches to every request:
// it must be the publishable or secret key. keys == nil means a unit test built
// the handler without key support; skip the check rather than fail every such
// test.
func apiKeyGuard(keys *app.JWTKeyManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if keys == nil {
			c.Next()
			return
		}
		key := c.GetHeader("apikey")
		if keyTier(key) != tierNone {
			c.Next()
			return
		}
		if key == "" {
			pgJSON(c, 401, "no_api_key", "No API key found in request", "", "")
		} else {
			pgJSON(c, 401, "invalid_api_key", "Invalid API key", "", "")
		}
		c.Abort()
	}
}

// adminKeyAuth protects admin endpoints. The secret key is the admin
// credential: supabase-js sends it in the apikey header (createClient with the
// secret key), while hand-rolled callers and the metrics scraper may send it as
// a bearer token, so either slot is accepted.
func adminKeyAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if os.Getenv("INSTANCEZ_SECRET_KEY") == "" {
			c.JSON(404, gin.H{"error": "not found"})
			c.Abort()
			return
		}
		if keyTier(c.GetHeader("apikey")) == tierSecret {
			c.Next()
			return
		}
		if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") &&
			keyTier(strings.TrimPrefix(h, "Bearer ")) == tierSecret {
			c.Next()
			return
		}
		problemJSON(c, 401, "unauthorized", "Invalid secret key")
		c.Abort()
	}
}

// serviceRoleSession attaches an explicit service_role DB session to the
// request context. The request pool's fallback (no session on context)
// defaults to anon, so system endpoints that operate on reserved-schema
// tables (auth.*, storage.objects) — where RLS doesn't apply and access is
// gated by application logic instead — must opt into service_role
// explicitly rather than relying on an implicit elevated default.
func serviceRoleSession(db domain.Database) gin.HandlerFunc {
	return func(c *gin.Context) {
		if db == nil {
			// Unit tests construct handlers directly with a stub service and no
			// real DB; nothing to attach a session to.
			c.Next()
			return
		}
		ctx, err := db.WithRLS(c.Request.Context(), domain.Session{Role: "service_role", IsAuthenticated: true})
		if err != nil {
			problemJSON(c, 500, "internal_error", "failed to establish database session")
			c.Abort()
			return
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// constantTimeEqual reports whether a and b are equal without leaking their
// relationship through comparison timing. Used for bearer/admin-key checks.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
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

// errTypeToCode maps instancez's error-type slugs onto PostgREST-compatible
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
	// Policy errors with no SQLSTATE / PGRST equivalent: the slug doubles
	// as the stable client-facing `code` so callers can branch on it.
	"signup_disabled": "signup_disabled",
}

// profileHeaderGuard enforces that Accept-Profile (for reads) and
// Content-Profile (for writes) request a configured schema. An absent
// header is always accepted.
func profileHeaderGuard(schemas ...string) gin.HandlerFunc {
	allowed := map[string]bool{"public": true}
	for _, s := range schemas {
		allowed[s] = true
	}
	schemaList := make([]string, 0, len(allowed))
	for s := range allowed {
		schemaList = append(schemaList, s)
	}
	sort.Strings(schemaList)
	msg := "The schema must be one of the following: " + strings.Join(schemaList, ", ")

	return func(c *gin.Context) {
		var header string
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead:
			header = c.GetHeader("Accept-Profile")
		default:
			header = c.GetHeader("Content-Profile")
		}
		if header == "" || allowed[header] {
			if header != "" {
				c.Set("_schema", header)
			}
			c.Next()
			return
		}
		pgJSON(c, http.StatusNotAcceptable, "PGRST106",
			msg,
			fmt.Sprintf("Requested schema %q is not exposed", header),
			"")
		c.Abort()
	}
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
