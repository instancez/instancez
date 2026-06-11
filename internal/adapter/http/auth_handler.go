package http

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

type ctxKey int

const (
	ctxKeyIP ctxKey = iota
	ctxKeyUA
)

func ctxWithRequestMeta(ctx context.Context, c *gin.Context) context.Context {
	ctx = context.WithValue(ctx, ctxKeyIP, c.ClientIP())
	ctx = context.WithValue(ctx, ctxKeyUA, c.GetHeader("User-Agent"))
	return ctx
}

// AuthHandler serves GoTrue-compatible authentication endpoints under
// /auth/v1/*. Response shapes mirror Supabase's gotrue-js contract so that
// @supabase/supabase-js can drive instancez unmodified.
type AuthHandler struct {
	cfg     *domain.Config
	db      domain.Database
	logger  *slog.Logger
	email   domain.EmailSender
	jwtKeys *app.JWTKeyManager
}

func NewAuthHandler(deps ServerDeps) *AuthHandler {
	return &AuthHandler{
		cfg:     deps.Config,
		db:      deps.DB.Database,
		logger:  deps.Logger,
		email:   deps.Email,
		jwtKeys: deps.JWTKeys,
	}
}

// Mount registers the /auth/v1/* routes on the root router group.
func (h *AuthHandler) Mount(root *gin.RouterGroup) {
	auth := root.Group("/auth/v1")
	authRL := rateLimitMiddleware(10) // 10 req/s per IP on sensitive endpoints
	auth.POST("/signup", authRL, h.handleSignupDispatch)
	auth.POST("/token", authRL, h.handleToken)
	auth.GET("/user", jwtAuth(h.jwtKeys, true), h.handleGetUser)
	auth.PUT("/user", jwtAuth(h.jwtKeys, true), h.handleUpdateUser)
	auth.POST("/logout", jwtAuth(h.jwtKeys, true), h.handleLogout)
	auth.GET("/settings", h.handleSettings)

	if h.cfg.Auth.Email != nil {
		auth.POST("/recover", authRL, h.handleRecover)
		auth.POST("/verify", authRL, h.handleVerify)
		auth.GET("/verify", h.handleVerifyGET)
		auth.POST("/otp", authRL, h.handleOTP)
		auth.POST("/resend", authRL, h.handleResend)
		auth.POST("/reauthenticate", jwtAuth(h.jwtKeys, true), h.handleReauthenticate)
	}
	auth.POST("/token/verify", h.handleTokenVerify)
	auth.GET("/.well-known/jwks.json", h.handleJWKS)

	// Identity management
	auth.GET("/user/identities", jwtAuth(h.jwtKeys, true), h.handleListIdentities)
	auth.POST("/user/identities/authorize", jwtAuth(h.jwtKeys, true), h.handleLinkIdentity)
	auth.DELETE("/user/identities/:id", jwtAuth(h.jwtKeys, true), h.handleUnlinkIdentity)

	admin := auth.Group("/admin", adminKeyAuth())
	admin.POST("/generate_link", h.handleGenerateLink)
	admin.POST("/users", h.handleAdminCreateUser)
	admin.GET("/users", h.handleAdminListUsers)
	admin.GET("/users/:uid", h.handleAdminGetUser)
	admin.PUT("/users/:uid", h.handleAdminUpdateUser)
	admin.DELETE("/users/:uid", h.handleAdminDeleteUser)
	admin.POST("/users/:uid/signout", h.handleAdminSignOut)
	admin.DELETE("/users/:uid/factors/:factor_id", h.handleAdminDeleteFactor)

	auth.POST("/invite", adminKeyAuth(), h.handleAdminInvite)

	h.MountMFA(auth)

	// OAuth (Supabase calls this /authorize)
	auth.GET("/authorize", h.handleAuthorize)
	if h.cfg.Auth.Google != nil {
		auth.GET("/callback/google", h.handleOAuthCallback("google"))
	}
	if h.cfg.Auth.GitHub != nil {
		auth.GET("/callback/github", h.handleOAuthCallback("github"))
	}
}

// ---------- signup ----------

// handleSignupDispatch peeks the raw request body before validation so we
// can route empty / missing-email bodies to the anonymous sign-in path.
// The existing handleSignup uses `binding:"required,email"`, which would
// otherwise reject anonymous requests with a 400 before any branching
// logic runs.
//
// This is also where the allow_signup / allow_anonymous yaml flags are
// enforced. Gating happens here (not at mount time) so the route stays in
// the OpenAPI surface and clients get a typed `signup_disabled` error code
// instead of a 404. The admin-keyed escape hatches (handleAdminCreateUser,
// handleAdminInvite) sit on a different middleware chain and intentionally
// ignore these flags — see the comments on those handlers.
func (h *AuthHandler) handleSignupDispatch(c *gin.Context) {
	// allow_signup=false rejects every variant (credentialed and anonymous)
	// before any body parse — anonymous users are still new user rows, so
	// disabling registration must disable both branches. Bailing early
	// also avoids wasting a body read + JSON unmarshal on requests we'll
	// reject anyway.
	if !h.cfg.Auth.SignupAllowed() {
		problemJSON(c, 403, "signup_disabled", "Public signup is disabled")
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		problemJSON(c, 400, "bad_request", "Invalid signup request")
		return
	}
	c.Request.Body = io.NopCloser(strings.NewReader(string(body)))

	var probe map[string]any
	_ = json.Unmarshal(body, &probe)
	email, _ := probe["email"].(string)
	isAnonymous := strings.TrimSpace(email) == ""

	if isAnonymous && !h.cfg.Auth.AnonymousAllowed() {
		problemJSON(c, 403, "signup_disabled", "Anonymous sign-in is not allowed")
		return
	}

	if isAnonymous {
		h.handleSignupAnonymous(c, probe)
		return
	}
	h.handleSignup(c)
}

// handleSignupAnonymous creates a placeholder user with no email/password
// and a JWT carrying is_anonymous=true. supabase-js exposes this via
// auth.signInAnonymously().
func (h *AuthHandler) handleSignupAnonymous(c *gin.Context, probe map[string]any) {
	userData, _ := probe["data"].(map[string]any)
	userMetaJSON := marshalJSONBDefault(userData)
	appMetaJSON, _ := json.Marshal(map[string]any{
		"provider":     "anonymous",
		"providers":    []string{"anonymous"},
		"is_anonymous": true,
	})

	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx,
		fmt.Sprintf("INSERT INTO auth.users (is_anonymous, raw_app_meta_data, raw_user_meta_data) VALUES (true, $1::jsonb, $2::jsonb) RETURNING %s, is_anonymous", userSelectCols),
		string(appMetaJSON), string(userMetaJSON))
	if err != nil || row == nil {
		h.logger.Error("anonymous signup error", "error", err)
		problemJSON(c, 500, "internal", "Failed to create anonymous user")
		return
	}

	userID := asString(row["id"])
	ctx = ctxWithRequestMeta(ctx, c)
	session, err := h.buildSession(ctx, userID, row)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to generate token")
		return
	}
	c.JSON(200, session)
}

func (h *AuthHandler) handleSignup(c *gin.Context) {
	var req struct {
		Email    string         `json:"email" binding:"required,email"`
		Password string         `json:"password" binding:"required,min=8"`
		Data     map[string]any `json:"data"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Invalid signup request: "+err.Error())
		return
	}

	hash, ok := hashPassword(c, req.Password)
	if !ok {
		return
	}

	userMetaJSON := marshalJSONBDefault(req.Data)

	// Signup always sets raw_user_meta_data (JSONB). Signup data is stored
	// in raw_user_meta_data rather than promoted to top-level columns.
	cols := []string{"email", "password_hash", "raw_user_meta_data"}
	placeholders := []string{"$1", "$2", "$3::jsonb"}
	vals := []any{req.Email, string(hash), string(userMetaJSON)}

	query := fmt.Sprintf(
		"INSERT INTO auth.users (%s) VALUES (%s) RETURNING %s",
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "), userSelectCols)

	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx, query, vals...)
	if err != nil {
		if isDuplicateKeyErr(err) {
			problemJSON(c, 409, "conflict", "Email already registered")
			return
		}
		h.logger.Error("signup error", "error", err)
		problemJSON(c, 500, "internal", "Failed to create user")
		return
	}

	userID := asString(row["id"])

	// Send verification email if configured
	if h.cfg.Auth.Email != nil && h.cfg.Auth.Email.VerifyEmail && h.email != nil {
		go h.sendVerificationEmail(userID, req.Email)
	}

	ctx = ctxWithRequestMeta(ctx, c)
	session, err := h.buildSession(ctx, userID, row)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to generate token")
		return
	}
	c.JSON(200, session)
}

// ---------- /token (grant_type dispatch) ----------

func (h *AuthHandler) handleToken(c *gin.Context) {
	// grant_type may arrive as a query param (supabase-js v2) or form-style
	// body (older clients). We read both and prefer the query parameter.
	grantType := c.Query("grant_type")
	if grantType == "" {
		grantType = c.PostForm("grant_type")
	}
	// If still empty, peek into a JSON body.
	if grantType == "" {
		var probe map[string]any
		if body, err := io.ReadAll(c.Request.Body); err == nil {
			// Restore body so the specific handler can re-read it.
			c.Request.Body = io.NopCloser(strings.NewReader(string(body)))
			_ = json.Unmarshal(body, &probe)
			if gt, ok := probe["grant_type"].(string); ok {
				grantType = gt
			}
		}
	}

	switch grantType {
	case "password":
		h.handlePasswordGrant(c)
	case "refresh_token":
		h.handleRefreshGrant(c)
	case "pkce":
		h.handlePKCEGrant(c)
	case "id_token":
		h.handleIDTokenGrant(c)
	default:
		problemJSON(c, 400, "bad_request", "Unsupported grant_type: "+grantType)
	}
}

func (h *AuthHandler) handlePasswordGrant(c *gin.Context) {
	var req struct {
		Email    string `json:"email"`
		Phone    string `json:"phone"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Invalid login request")
		return
	}
	if req.Email == "" {
		problemJSON(c, 400, "bad_request", "email is required")
		return
	}

	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx,
		`SELECT id::text, email, password_hash, email_verified, email_confirmed_at, last_sign_in_at, raw_app_meta_data, raw_user_meta_data, created_at, updated_at
		 FROM auth.users WHERE email = $1`, req.Email)
	if err != nil || row == nil {
		problemJSON(c, 401, "invalid_grant", "Invalid login credentials")
		return
	}

	passwordHash, _ := row["password_hash"].(string)
	if passwordHash == "" {
		problemJSON(c, 401, "invalid_grant", "Account uses OAuth login")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
		problemJSON(c, 401, "invalid_grant", "Invalid login credentials")
		return
	}

	emailVerified, _ := row["email_verified"].(bool)
	if h.cfg.Auth.Email != nil && h.cfg.Auth.Email.VerifyEmail && !emailVerified {
		problemJSON(c, 403, "email_not_confirmed", "Email not confirmed")
		return
	}

	userID := asString(row["id"])
	// Bump last_sign_in_at so the /user response reflects the login.
	_, _ = h.db.Exec(ctx, "UPDATE auth.users SET last_sign_in_at = NOW(), updated_at = NOW() WHERE id = $1::uuid", userID)
	row["last_sign_in_at"] = time.Now().UTC()

	ctx = ctxWithRequestMeta(ctx, c)
	session, err := h.buildSession(ctx, userID, row)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to generate token")
		return
	}
	c.JSON(200, session)
}

func (h *AuthHandler) handleRefreshGrant(c *gin.Context) {
	if !h.cfg.Auth.RefreshTokens {
		problemJSON(c, 400, "bad_request", "Refresh tokens are disabled")
		return
	}
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Missing refresh_token")
		return
	}

	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx,
		"SELECT user_id::text, expires_at FROM auth.refresh_tokens WHERE token = $1", req.RefreshToken)
	if err != nil || row == nil {
		problemJSON(c, 401, "invalid_grant", "Invalid refresh token")
		return
	}

	expiresAt, _ := row["expires_at"].(time.Time)
	if time.Now().After(expiresAt) {
		h.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE token = $1", req.RefreshToken)
		problemJSON(c, 401, "invalid_grant", "Refresh token expired")
		return
	}

	userID := asString(row["user_id"])

	// Rotation: each token is single-use.
	affected, _ := h.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE token = $1", req.RefreshToken)
	if affected == 0 {
		h.logger.Warn("refresh token reuse detected, revoking all tokens", "user_id", userID)
		h.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE user_id = $1::uuid", userID)
		problemJSON(c, 401, "invalid_grant", "Refresh token reuse detected. All sessions revoked.")
		return
	}

	userRow, err := h.db.QueryRow(ctx,
		fmt.Sprintf("SELECT %s FROM auth.users WHERE id = $1::uuid", userSelectCols), userID)
	if err != nil || userRow == nil {
		problemJSON(c, 401, "invalid_grant", "User not found")
		return
	}

	ctx = ctxWithRequestMeta(ctx, c)
	session, err := h.buildSession(ctx, userID, userRow)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to generate token")
		return
	}
	c.JSON(200, session)
}

func (h *AuthHandler) handlePKCEGrant(c *gin.Context) {
	var req struct {
		AuthCode     string `json:"auth_code" binding:"required"`
		CodeVerifier string `json:"code_verifier" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Missing auth_code or code_verifier")
		return
	}

	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx,
		"SELECT user_id::text, code_challenge, code_challenge_method FROM auth.flow_state WHERE auth_code = $1 AND provider_type = 'pkce' AND auth_code_issued_at > NOW() - INTERVAL '5 minutes'",
		req.AuthCode)
	if err != nil || row == nil {
		problemJSON(c, 401, "invalid_grant", "Invalid or expired auth code")
		return
	}

	h.db.Exec(ctx, "DELETE FROM auth.flow_state WHERE auth_code = $1 AND provider_type = 'pkce'", req.AuthCode)

	codeChallenge, _ := row["code_challenge"].(string)
	codeChallengeMethod, _ := row["code_challenge_method"].(string)

	if !verifyCodeChallenge(codeChallengeMethod, req.CodeVerifier, codeChallenge) {
		problemJSON(c, 401, "invalid_grant", "Code verifier does not match challenge")
		return
	}

	userID, _ := row["user_id"].(string)
	userRow, err := h.db.QueryRow(ctx,
		fmt.Sprintf("SELECT %s FROM auth.users WHERE id = $1::uuid", userSelectCols), userID)
	if err != nil || userRow == nil {
		problemJSON(c, 401, "invalid_grant", "User not found")
		return
	}

	ctx = ctxWithRequestMeta(ctx, c)
	session, err := h.buildSession(ctx, userID, userRow)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to generate token")
		return
	}
	c.JSON(200, session)
}

func (h *AuthHandler) handleIDTokenGrant(c *gin.Context) {
	var req struct {
		Provider string `json:"provider" binding:"required"`
		Token    string `json:"token" binding:"required"`
		Nonce    string `json:"nonce"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Missing provider or token")
		return
	}

	var clientID string
	switch req.Provider {
	case "google":
		if h.cfg.Auth.Google == nil {
			problemJSON(c, 400, "bad_request", "Google provider not configured")
			return
		}
		clientID = h.cfg.Auth.Google.ClientID
	default:
		problemJSON(c, 400, "bad_request", "Unsupported provider for ID token: "+req.Provider)
		return
	}

	claims, err := verifyIDToken(req.Provider, req.Token, clientID, req.Nonce)
	if err != nil {
		h.logger.Error("id token verification failed", "provider", req.Provider, "error", err)
		problemJSON(c, 401, "invalid_token", "ID token verification failed: "+err.Error())
		return
	}

	email, _ := claims["email"].(string)
	if email == "" {
		problemJSON(c, 400, "bad_request", "ID token does not contain email")
		return
	}
	sub, _ := claims["sub"].(string)
	name, _ := claims["name"].(string)

	ctx := c.Request.Context()
	row, _ := h.db.QueryRow(ctx,
		fmt.Sprintf("SELECT %s FROM auth.users WHERE email = $1", userSelectCols), email)
	var userID string

	if row == nil {
		metaJSON, _ := json.Marshal(map[string]any{
			"provider":       req.Provider,
			"full_name":      name,
			"email":          email,
			"email_verified": true,
		})
		appMetaJSON, _ := json.Marshal(map[string]any{
			"provider":  req.Provider,
			"providers": []string{req.Provider},
		})
		newRow, err := h.db.QueryRow(ctx,
			"INSERT INTO auth.users (email, email_verified, email_confirmed_at, raw_user_meta_data, raw_app_meta_data) VALUES ($1, true, NOW(), $2::jsonb, $3::jsonb) RETURNING "+userSelectCols,
			email, string(metaJSON), string(appMetaJSON))
		if err != nil {
			row, err = h.db.QueryRow(ctx,
				fmt.Sprintf("SELECT %s FROM auth.users WHERE email = $1", userSelectCols), email)
			if err != nil || row == nil {
				problemJSON(c, 500, "internal", "Failed to create or find user")
				return
			}
			userID = asString(row["id"])
		} else {
			row = newRow
			userID = asString(newRow["id"])
		}
	} else {
		userID = asString(row["id"])
		h.db.Exec(ctx, "UPDATE auth.users SET email_verified = true, email_confirmed_at = COALESCE(email_confirmed_at, NOW()), last_sign_in_at = NOW(), updated_at = NOW() WHERE id = $1::uuid", userID)
	}

	h.db.Exec(ctx,
		`INSERT INTO auth.identities (user_id, provider, provider_user_id, email, last_sign_in_at, updated_at)
		 VALUES ($1::uuid, $2, $3, $4, NOW(), NOW())
		 ON CONFLICT (provider, provider_user_id)
		 DO UPDATE SET last_sign_in_at = EXCLUDED.last_sign_in_at, updated_at = EXCLUDED.updated_at`,
		userID, req.Provider, sub, email)

	ctx = ctxWithRequestMeta(ctx, c)
	session, err := h.buildSession(ctx, userID, row)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to generate token")
		return
	}
	c.JSON(200, session)
}

// ---------- /user ----------

func (h *AuthHandler) handleGetUser(c *gin.Context) {
	session := getSession(c)
	ctx := c.Request.Context()

	row, err := h.db.QueryRow(ctx,
		fmt.Sprintf("SELECT %s FROM auth.users WHERE id = $1::uuid", userSelectCols), session.UserID)
	if err != nil || row == nil {
		problemJSON(c, 404, "not_found", "User not found")
		return
	}
	c.JSON(200, h.buildUser(session.UserID, row))
}

func (h *AuthHandler) handleUpdateUser(c *gin.Context) {
	session := getSession(c)
	var req struct {
		Email    *string        `json:"email"`
		Password *string        `json:"password"`
		Data     map[string]any `json:"data"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Invalid update request")
		return
	}

	sets := []string{"updated_at = NOW()"}
	args := []any{}
	argIdx := 1

	if req.Email != nil && *req.Email != "" {
		sets = append(sets, fmt.Sprintf("email = $%d", argIdx))
		args = append(args, *req.Email)
		argIdx++
		// A newly-set address is unverified until the user proves control of
		// it. Carrying over the previous verified status to an unconfirmed
		// address would let a stolen session quietly swap in an attacker email
		// while keeping it "verified". (Full GoTrue-style double-confirm — send
		// a token to the new address and only swap on verify — is a larger
		// follow-up; at minimum we must not keep the verified flag.)
		if !strings.EqualFold(strings.TrimSpace(*req.Email), strings.TrimSpace(session.Email)) {
			sets = append(sets, "email_verified = false", "email_confirmed_at = NULL")
		}
	}
	if req.Password != nil && *req.Password != "" {
		if len(*req.Password) < 8 {
			problemJSON(c, 400, "bad_request", "Password must be at least 8 characters")
			return
		}
		hash, ok := hashPassword(c, *req.Password)
		if !ok {
			return
		}
		sets = append(sets, fmt.Sprintf("password_hash = $%d", argIdx))
		args = append(args, hash)
		argIdx++
	}
	if req.Data != nil {
		metaJSON := marshalJSONBDefault(req.Data)
		sets = append(sets, fmt.Sprintf("raw_user_meta_data = raw_user_meta_data || $%d::jsonb", argIdx))
		args = append(args, string(metaJSON))
		argIdx++
	}

	args = append(args, session.UserID)
	query := fmt.Sprintf(
		"UPDATE auth.users SET %s WHERE id = $%d::uuid RETURNING %s",
		strings.Join(sets, ", "), argIdx, userSelectCols)

	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx, query, args...)
	if err != nil || row == nil {
		if isDuplicateKeyErr(err) {
			problemJSON(c, 409, "conflict", "Email already registered")
			return
		}
		problemJSON(c, 500, "internal", "Failed to update user")
		return
	}
	c.JSON(200, h.buildUser(session.UserID, row))
}

// ---------- /logout ----------

func (h *AuthHandler) handleLogout(c *gin.Context) {
	session := getSession(c)
	scope := c.DefaultQuery("scope", "global")

	ctx := c.Request.Context()
	if h.cfg.Auth.RefreshTokens && session.UserID != "" {
		sessionID := h.extractSessionID(session.JWT)
		switch scope {
		case "local":
			if sessionID != "" {
				h.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE session_id = $1", sessionID)
			} else {
				h.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE user_id = $1::uuid", session.UserID)
			}
		case "others":
			if sessionID != "" {
				h.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE user_id = $1::uuid AND (session_id IS NULL OR session_id != $2)", session.UserID, sessionID)
			} else {
				h.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE user_id = $1::uuid", session.UserID)
			}
		default: // global
			h.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE user_id = $1::uuid", session.UserID)
		}
	}
	c.Status(204)
}

// ---------- /recover (password reset request) ----------

func (h *AuthHandler) handleRecover(c *gin.Context) {
	var req struct {
		Email      string `json:"email" binding:"required,email"`
		RedirectTo string `json:"redirect_to"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Invalid email")
		return
	}

	// Drop a disallowed redirect_to rather than emailing an attacker URL.
	// The flow still succeeds (and still returns 200) using the base URL.
	redirectTo := req.RedirectTo
	if !h.redirectAllowed(redirectTo) {
		h.logger.Warn("rejected disallowed recovery redirect_to", "redirect_to", redirectTo)
		redirectTo = ""
	}

	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx, "SELECT id::text FROM auth.users WHERE email = $1", req.Email)
	if err == nil && row != nil {
		userID := asString(row["id"])
		token := generateRandomToken()
		expiresAt := time.Now().Add(1 * time.Hour)
		h.db.Exec(ctx,
			"INSERT INTO auth.one_time_tokens (user_id, token, purpose, expires_at) VALUES ($1::uuid, $2, 'recovery', $3)",
			userID, token, expiresAt)
		if h.email != nil {
			go h.sendPasswordResetEmail(req.Email, token, redirectTo)
		}
	}
	// Always return 200 (email enumeration protection).
	c.Status(200)
}

// ---------- /verify ----------

// maxOTPAttempts is the number of failed verifications a single numeric OTP
// row tolerates before it is destroyed, bounding brute-force of the 10^6 code
// space to a handful of guesses per issued code.
const maxOTPAttempts = 5

// handleVerify implements POST /verify {type, token, email} — the
// supabase-js verifyOtp entrypoint. On success it consumes the token and
// returns a full session so the client can transition to a recovery /
// confirmed state.
func (h *AuthHandler) handleVerify(c *gin.Context) {
	var req struct {
		Type  string `json:"type" binding:"required"`
		Token string `json:"token" binding:"required"`
		Email string `json:"email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Invalid verify request")
		return
	}

	ctx := c.Request.Context()
	// Two lookup shapes:
	//   1. Long opaque token (magiclink click, signup confirmation, admin
	//      generate_link) — globally unique, lookup by token alone.
	//   2. 6-digit numeric code (signInWithOtp + verifyOtp) — scoped by
	//      email because 10^6 is small enough for collisions. We require
	//      the caller to supply `email` when the token is short numeric.
	var row map[string]any
	var err error
	isNumericCode := len(req.Token) == 6 && strings.IndexFunc(req.Token, func(r rune) bool {
		return r < '0' || r > '9'
	}) == -1
	if isNumericCode && req.Email != "" {
		// Numeric codes live in a 10^6 space, so the verify endpoint must be
		// brute-force resistant. Fetch the most recent code-bearing token for
		// the email, enforce a per-token attempt cap, and compare the code in
		// constant time. On too many failures the token is destroyed so the
		// attacker has to trigger a fresh send (rate-limited elsewhere). All
		// failure paths return the same generic error to avoid leaking whether
		// the email or attempt budget exists.
		cand, cerr := h.db.QueryRow(ctx,
			`SELECT id, user_id::text, purpose, expires_at, token, code, attempts
			   FROM auth.one_time_tokens
			  WHERE email = $1 AND code IS NOT NULL
			  ORDER BY created_at DESC LIMIT 1`,
			req.Email)
		if cerr != nil || cand == nil {
			problemJSON(c, 401, "invalid_grant", "Invalid or expired token")
			return
		}
		if ts, _ := cand["expires_at"].(time.Time); time.Now().After(ts) {
			h.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE id = $1", cand["id"])
			problemJSON(c, 401, "invalid_grant", "Token expired")
			return
		}
		attempts := asInt64(cand["attempts"])
		codeOK := constantTimeEqual(asString(cand["code"]), req.Token)
		if attempts >= maxOTPAttempts || !codeOK {
			// Burn the token once the budget is exhausted (or on the attempt
			// that reaches it); otherwise just record the failed try.
			if !codeOK && attempts+1 < maxOTPAttempts {
				h.db.Exec(ctx, "UPDATE auth.one_time_tokens SET attempts = attempts + 1 WHERE id = $1", cand["id"])
			} else {
				h.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE id = $1", cand["id"])
			}
			problemJSON(c, 401, "invalid_grant", "Invalid or expired token")
			return
		}
		row = cand
	} else {
		row, err = h.db.QueryRow(ctx,
			"SELECT user_id::text, purpose, expires_at, token FROM auth.one_time_tokens WHERE token = $1",
			req.Token)
		if err != nil || row == nil {
			problemJSON(c, 401, "invalid_grant", "Invalid or expired token")
			return
		}
	}
	// Row token (the 32-byte hex) is the canonical delete key; req.Token
	// may be the 6-digit code.
	rowToken := asString(row["token"])
	expiresAt, _ := row["expires_at"].(time.Time)
	if time.Now().After(expiresAt) {
		h.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE token = $1", rowToken)
		problemJSON(c, 401, "invalid_grant", "Token expired")
		return
	}

	userID := asString(row["user_id"])
	purpose, _ := row["purpose"].(string)

	// Side-effect based on purpose
	switch req.Type {
	case "signup", "email", "email_change":
		// "email" is supabase-js's signInWithOtp type and is served by the
		// magiclink flow (same row in auth.one_time_tokens, purpose
		// set by handleOTP). Accept both signup and magiclink purposes so
		// first-time users verifying via 6-digit code aren't rejected.
		if purpose != "" && purpose != "signup" && purpose != "magiclink" {
			problemJSON(c, 400, "bad_request", "Token purpose mismatch")
			return
		}
		h.db.Exec(ctx, "UPDATE auth.users SET email_verified = true, email_confirmed_at = NOW(), updated_at = NOW() WHERE id = $1::uuid", userID)
	case "recovery":
		if purpose != "" && purpose != "recovery" {
			problemJSON(c, 400, "bad_request", "Token purpose mismatch")
			return
		}
		// No immediate update — the client will call PUT /user with the
		// new password using the session we issue below.
	case "magiclink":
		// Treat as a login; no state change.
	default:
		problemJSON(c, 400, "bad_request", "Unsupported verify type")
		return
	}

	// Consume the token (single-use). Always delete by the canonical
	// `token` column so 6-digit code flows also clear the row.
	h.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE token = $1", rowToken)

	userRow, err := h.db.QueryRow(ctx,
		fmt.Sprintf("SELECT %s FROM auth.users WHERE id = $1::uuid", userSelectCols), userID)
	if err != nil || userRow == nil {
		problemJSON(c, 500, "internal", "User not found")
		return
	}

	ctx = ctxWithRequestMeta(ctx, c)
	session, err := h.buildSession(ctx, userID, userRow)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to generate token")
		return
	}
	c.JSON(200, session)
}

// handleVerifyGET handles a click on an email verification link. For
// signup/email tokens it marks the email as verified and returns a plain
// text confirmation. For recovery tokens it generates a full session and
// redirects the user to the app's redirect_to URL with the access token
// in the URL fragment — matching the GoTrue flow that supabase-js expects
// for password reset (the client picks up the fragment and fires a
// PASSWORD_RECOVERY auth state change event).
func (h *AuthHandler) handleVerifyGET(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		problemJSON(c, 400, "bad_request", "Missing token")
		return
	}
	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx,
		"SELECT user_id::text, purpose, expires_at FROM auth.one_time_tokens WHERE token = $1",
		token)
	if err != nil || row == nil {
		c.String(400, "Invalid verification token")
		return
	}
	expiresAt, _ := row["expires_at"].(time.Time)
	if time.Now().After(expiresAt) {
		h.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE token = $1", token)
		c.String(400, "Verification token expired")
		return
	}

	userID := asString(row["user_id"])
	purpose, _ := row["purpose"].(string)

	// Consume the token (single-use).
	h.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE token = $1", token)

	verifyType := c.DefaultQuery("type", "")

	if purpose == "recovery" || verifyType == "recovery" {
		// Recovery flow: build a session and redirect to the app with
		// the access token in the URL fragment so supabase-js can pick
		// it up and fire the PASSWORD_RECOVERY event.
		userRow, err := h.db.QueryRow(ctx,
			fmt.Sprintf("SELECT %s FROM auth.users WHERE id = $1::uuid", userSelectCols), userID)
		if err != nil || userRow == nil {
			c.String(500, "User not found")
			return
		}
		ctx = ctxWithRequestMeta(ctx, c)
		session, err := h.buildSession(ctx, userID, userRow)
		if err != nil {
			c.String(500, "Failed to generate session")
			return
		}
		accessToken, _ := session["access_token"].(string)
		refreshToken, _ := session["refresh_token"].(string)
		expiresIn := "3600"
		if v, ok := session["expires_in"].(int); ok {
			expiresIn = fmt.Sprintf("%d", v)
		} else if v, ok := session["expires_in"].(float64); ok {
			expiresIn = fmt.Sprintf("%d", int(v))
		}
		tokenType, _ := session["token_type"].(string)
		if tokenType == "" {
			tokenType = "bearer"
		}

		// resolveRedirect falls back to the base URL for an empty or
		// disallowed target — critical here because the fragment carries the
		// user's access and refresh tokens.
		redirectTo := h.resolveRedirect(c.Query("redirect_to"))
		fragment := fmt.Sprintf("access_token=%s&token_type=%s&expires_in=%s&refresh_token=%s&type=recovery",
			accessToken, tokenType, expiresIn, refreshToken)
		c.Redirect(303, redirectTo+"#"+fragment)
		return
	}

	// Default: email verification — mark as verified and show confirmation.
	h.db.Exec(ctx, "UPDATE auth.users SET email_verified = true, email_confirmed_at = NOW(), updated_at = NOW() WHERE id = $1::uuid", userID)

	if rt := c.Query("redirect_to"); rt != "" && h.redirectAllowed(rt) {
		c.Redirect(303, rt)
		return
	}
	c.String(200, "Email verified successfully")
}

// ---------- /otp (magic link) ----------

// handleOTP implements POST /auth/v1/otp — supabase-js calls this from
// auth.signInWithOtp({email}). When create_user is true (the default) we
// upsert a user row, then generate a magiclink token in
// auth.one_time_tokens and dispatch the email. The handler always
// returns 200 to prevent enumeration attacks.
func (h *AuthHandler) handleOTP(c *gin.Context) {
	var req struct {
		Email      string         `json:"email" binding:"required,email"`
		CreateUser *bool          `json:"create_user"`
		Data       map[string]any `json:"data"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Invalid otp request: "+err.Error())
		return
	}
	createUser := true
	if req.CreateUser != nil {
		createUser = *req.CreateUser
	}

	ctx := c.Request.Context()
	row, _ := h.db.QueryRow(ctx, "SELECT id::text FROM auth.users WHERE email = $1", req.Email)
	var userID string
	if row != nil {
		userID = asString(row["id"])
	} else if createUser {
		userMetaJSON := marshalJSONBDefault(req.Data)
		newRow, err := h.db.QueryRow(ctx,
			`INSERT INTO auth.users (email, raw_user_meta_data) VALUES ($1, $2::jsonb) RETURNING id::text`,
			req.Email, string(userMetaJSON))
		if err != nil || newRow == nil {
			h.logger.Error("otp create user failed", "error", err)
			c.Status(200)
			return
		}
		userID = asString(newRow["id"])
	} else {
		c.Status(200)
		return
	}

	token := generateRandomToken()
	code := generateNumericCode(6)
	expiresAt := time.Now().Add(1 * time.Hour)
	_, err := h.db.Exec(ctx,
		"INSERT INTO auth.one_time_tokens (user_id, token, code, email, purpose, expires_at) VALUES ($1::uuid, $2, $3, $4, 'magiclink', $5)",
		userID, token, code, req.Email, expiresAt)
	if err != nil {
		h.logger.Error("otp token insert failed", "error", err)
		c.Status(200)
		return
	}
	if h.email != nil {
		go h.sendMagicLinkEmail(req.Email, token, code)
	}
	c.Status(200)
}

// ---------- /admin/generate_link ----------

// handleGenerateLink mirrors GoTrue's admin.generateLink. It creates a
// verification token without sending an email and returns the fully
// formed action_link so the caller can embed it in a custom message. Only
// accessible via adminKeyAuth.
func (h *AuthHandler) handleGenerateLink(c *gin.Context) {
	var req struct {
		Type       string         `json:"type" binding:"required"`
		Email      string         `json:"email" binding:"required,email"`
		Password   string         `json:"password"`
		Data       map[string]any `json:"data"`
		RedirectTo string         `json:"redirect_to"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Invalid generate_link request: "+err.Error())
		return
	}

	var purpose string
	switch req.Type {
	case "signup":
		purpose = "signup"
	case "magiclink":
		purpose = "magiclink"
	case "recovery":
		purpose = "recovery"
	default:
		problemJSON(c, 400, "bad_request", "Unsupported link type: "+req.Type)
		return
	}

	ctx := c.Request.Context()
	row, _ := h.db.QueryRow(ctx,
		fmt.Sprintf("SELECT %s FROM auth.users WHERE email = $1", userSelectCols), req.Email)

	var userID string
	if row == nil {
		if req.Type != "signup" {
			problemJSON(c, 404, "not_found", "User not found")
			return
		}
		var hash string
		if req.Password != "" {
			var ok bool
			hash, ok = hashPassword(c, req.Password)
			if !ok {
				return
			}
		}
		metaJSON := marshalJSONBDefault(req.Data)
		newRow, err := h.db.QueryRow(ctx,
			`INSERT INTO auth.users (email, password_hash, raw_user_meta_data)
			 VALUES ($1, $2, $3::jsonb)
			 RETURNING `+userSelectCols,
			req.Email, hash, string(metaJSON))
		if err != nil || newRow == nil {
			h.logger.Error("generate_link create user failed", "error", err)
			problemJSON(c, 500, "internal", "Failed to create user")
			return
		}
		row = newRow
		userID = asString(newRow["id"])
	} else {
		userID = asString(row["id"])
	}

	token := generateRandomToken()
	expiresAt := time.Now().Add(1 * time.Hour)
	_, err := h.db.Exec(ctx,
		"INSERT INTO auth.one_time_tokens (user_id, token, purpose, expires_at) VALUES ($1::uuid, $2, $3, $4)",
		userID, token, purpose, expiresAt)
	if err != nil {
		h.logger.Error("generate_link token insert failed", "error", err)
		problemJSON(c, 500, "internal", "Failed to create verification token")
		return
	}

	actionLink := fmt.Sprintf("%s/auth/v1/verify?token=%s&type=%s", h.baseURL(), token, req.Type)
	if req.RedirectTo != "" {
		actionLink += "&redirect_to=" + url.QueryEscape(req.RedirectTo)
	}

	resp := gin.H{
		"action_link":       actionLink,
		"email_otp":         token,
		"hashed_token":      token,
		"verification_type": req.Type,
		"redirect_to":       req.RedirectTo,
		"user":              h.buildUser(userID, row),
	}
	c.JSON(200, resp)
}

// ---------- admin user management ----------

const userSelectCols = `id::text, email, email_verified, email_confirmed_at, last_sign_in_at, raw_app_meta_data, raw_user_meta_data, created_at, updated_at`

// handleAdminCreateUser intentionally ignores cfg.Auth.AllowSignup. The whole
// point of the flag is to disable *public* signup while keeping admin-keyed
// user creation open (admin portals, AI-app-builder workflows that mint
// users via a backend). Access is gated by the adminKeyAuth() middleware on
// the route, not by the signup flag.
func (h *AuthHandler) handleAdminCreateUser(c *gin.Context) {
	var req struct {
		Email        string         `json:"email"`
		Password     string         `json:"password"`
		EmailConfirm bool           `json:"email_confirm"`
		UserMetadata map[string]any `json:"user_metadata"`
		AppMetadata  map[string]any `json:"app_metadata"`
		BanDuration  string         `json:"ban_duration"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Invalid request: "+err.Error())
		return
	}
	if req.Email == "" {
		problemJSON(c, 400, "bad_request", "Email is required")
		return
	}

	var hash string
	if req.Password != "" {
		var ok bool
		hash, ok = hashPassword(c, req.Password)
		if !ok {
			return
		}
	}

	userMeta := marshalJSONBDefault(req.UserMetadata)
	appMeta := marshalJSONBDefault(req.AppMetadata)
	if string(appMeta) == "{}" {
		appMeta = []byte(`{"provider":"email","providers":["email"]}`)
	}

	cols := []string{"email", "password_hash", "raw_user_meta_data", "raw_app_meta_data"}
	placeholders := []string{"$1", "$2", "$3::jsonb", "$4::jsonb"}
	args := []any{req.Email, hash, string(userMeta), string(appMeta)}
	argIdx := len(args) + 1

	if req.EmailConfirm {
		cols = append(cols, "email_verified", "email_confirmed_at")
		placeholders = append(placeholders, "true", "NOW()")
	}

	if req.BanDuration != "" && req.BanDuration != "none" {
		cols = append(cols, "banned_until")
		placeholders = append(placeholders, fmt.Sprintf("NOW() + $%d::interval", argIdx))
		args = append(args, req.BanDuration)
	}

	ctx := c.Request.Context()
	query := fmt.Sprintf(
		"INSERT INTO auth.users (%s) VALUES (%s) RETURNING %s",
		strings.Join(cols, ", "), strings.Join(placeholders, ", "), userSelectCols)

	row, err := h.db.QueryRow(ctx, query, args...)
	if err != nil {
		if isDuplicateKeyErr(err) {
			problemJSON(c, 422, "user_already_exists", "A user with this email address has already been registered")
			return
		}
		h.logger.Error("admin create user failed", "error", err)
		problemJSON(c, 500, "internal", "Failed to create user")
		return
	}

	c.JSON(200, h.buildUser(asString(row["id"]), row))
}

func (h *AuthHandler) handleAdminListUsers(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "50"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 1000 {
		perPage = 50
	}
	offset := (page - 1) * perPage

	ctx := c.Request.Context()

	rows, err := h.db.Query(ctx,
		fmt.Sprintf("SELECT %s, count(*) OVER() AS _total FROM auth.users ORDER BY created_at DESC LIMIT $1 OFFSET $2", userSelectCols),
		perPage, offset)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to list users")
		return
	}

	total := 0
	users := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		if total == 0 {
			switch n := row["_total"].(type) {
			case int64:
				total = int(n)
			case int32:
				total = int(n)
			case float64:
				total = int(n)
			}
		}
		users = append(users, h.buildUser(asString(row["id"]), row))
	}

	lastPage := int(math.Ceil(float64(total) / float64(perPage)))
	if lastPage < 1 {
		lastPage = 1
	}

	c.Header("x-total-count", strconv.Itoa(total))
	if lastPage > 1 {
		var links []string
		if page < lastPage {
			links = append(links, fmt.Sprintf("<%s/auth/v1/admin/users?page=%d&per_page=%d>; rel=\"next\"", h.baseURL(), page+1, perPage))
		}
		links = append(links, fmt.Sprintf("<%s/auth/v1/admin/users?page=%d&per_page=%d>; rel=\"last\"", h.baseURL(), lastPage, perPage))
		c.Header("link", strings.Join(links, ", "))
	}

	c.JSON(200, gin.H{"users": users, "aud": "authenticated"})
}

func (h *AuthHandler) handleAdminGetUser(c *gin.Context) {
	uid := c.Param("uid")
	ctx := c.Request.Context()

	row, err := h.db.QueryRow(ctx,
		fmt.Sprintf("SELECT %s FROM auth.users WHERE id = $1::uuid", userSelectCols), uid)
	if err != nil || row == nil {
		problemJSON(c, 404, "user_not_found", "User not found")
		return
	}

	c.JSON(200, h.buildUser(asString(row["id"]), row))
}

func (h *AuthHandler) handleAdminUpdateUser(c *gin.Context) {
	uid := c.Param("uid")
	var req struct {
		Email        *string        `json:"email"`
		Password     *string        `json:"password"`
		EmailConfirm *bool          `json:"email_confirm"`
		UserMetadata map[string]any `json:"user_metadata"`
		AppMetadata  map[string]any `json:"app_metadata"`
		BanDuration  *string        `json:"ban_duration"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Invalid request: "+err.Error())
		return
	}

	sets := []string{"updated_at = NOW()"}
	args := []any{}
	argIdx := 1

	if req.Email != nil && *req.Email != "" {
		sets = append(sets, fmt.Sprintf("email = $%d", argIdx))
		args = append(args, *req.Email)
		argIdx++
	}
	if req.Password != nil && *req.Password != "" {
		hash, ok := hashPassword(c, *req.Password)
		if !ok {
			return
		}
		sets = append(sets, fmt.Sprintf("password_hash = $%d", argIdx))
		args = append(args, hash)
		argIdx++
	}
	if req.EmailConfirm != nil && *req.EmailConfirm {
		sets = append(sets, "email_verified = true", "email_confirmed_at = NOW()")
	}
	if req.UserMetadata != nil {
		sets = append(sets, fmt.Sprintf("raw_user_meta_data = raw_user_meta_data || $%d::jsonb", argIdx))
		args = append(args, string(marshalJSONBDefault(req.UserMetadata)))
		argIdx++
	}
	if req.AppMetadata != nil {
		sets = append(sets, fmt.Sprintf("raw_app_meta_data = raw_app_meta_data || $%d::jsonb", argIdx))
		args = append(args, string(marshalJSONBDefault(req.AppMetadata)))
		argIdx++
	}
	if req.BanDuration != nil {
		if *req.BanDuration == "none" {
			sets = append(sets, "banned_until = NULL")
		} else {
			sets = append(sets, fmt.Sprintf("banned_until = NOW() + $%d::interval", argIdx))
			args = append(args, *req.BanDuration)
			argIdx++
		}
	}

	args = append(args, uid)
	query := fmt.Sprintf(
		"UPDATE auth.users SET %s WHERE id = $%d::uuid RETURNING %s",
		strings.Join(sets, ", "), argIdx, userSelectCols)

	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx, query, args...)
	if isDuplicateKeyErr(err) {
		problemJSON(c, 422, "email_exists", "A user with this email address has already been registered")
		return
	}
	if err != nil {
		h.logger.Error("admin update user failed", "error", err)
		problemJSON(c, 500, "internal", "Failed to update user")
		return
	}
	if row == nil {
		problemJSON(c, 404, "user_not_found", "User not found")
		return
	}

	c.JSON(200, h.buildUser(asString(row["id"]), row))
}

func (h *AuthHandler) handleAdminDeleteUser(c *gin.Context) {
	uid := c.Param("uid")
	ctx := c.Request.Context()

	// Clean up auth artifacts first.
	h.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE user_id = $1::uuid", uid)
	h.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE user_id = $1::uuid", uid)
	h.db.Exec(ctx, "DELETE FROM auth.mfa_factors WHERE user_id = $1::uuid", uid)

	affected, err := h.db.Exec(ctx, "DELETE FROM auth.users WHERE id = $1::uuid", uid)
	if err != nil {
		h.logger.Error("admin delete user failed", "error", err)
		problemJSON(c, 500, "internal", "Failed to delete user")
		return
	}
	if affected == 0 {
		problemJSON(c, 404, "user_not_found", "User not found")
		return
	}

	c.JSON(200, gin.H{})
}

// handleAdminInvite intentionally ignores cfg.Auth.AllowSignup for the same
// reason as handleAdminCreateUser: the admin key path is the supported way
// to add users to a project that has public registration turned off.
func (h *AuthHandler) handleAdminInvite(c *gin.Context) {
	var req struct {
		Email string         `json:"email" binding:"required,email"`
		Data  map[string]any `json:"data"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Invalid request: "+err.Error())
		return
	}

	ctx := c.Request.Context()

	existing, _ := h.db.QueryRow(ctx, "SELECT id::text FROM auth.users WHERE email = $1", req.Email)
	if existing != nil {
		problemJSON(c, 422, "user_already_exists", "A user with this email address has already been registered")
		return
	}

	userMeta := marshalJSONBDefault(req.Data)
	appMeta, _ := json.Marshal(map[string]any{"provider": "email", "providers": []string{"email"}})

	row, err := h.db.QueryRow(ctx,
		fmt.Sprintf("INSERT INTO auth.users (email, raw_user_meta_data, raw_app_meta_data) VALUES ($1, $2::jsonb, $3::jsonb) RETURNING %s", userSelectCols),
		req.Email, string(userMeta), string(appMeta))
	if err != nil || row == nil {
		h.logger.Error("admin invite failed", "error", err)
		problemJSON(c, 500, "internal", "Failed to create invited user")
		return
	}

	userID := asString(row["id"])

	if h.cfg.Auth.Email != nil && h.email != nil {
		go h.sendVerificationEmail(userID, req.Email)
	}

	c.JSON(200, h.buildUser(userID, row))
}

// ---------- /settings ----------

func (h *AuthHandler) handleSettings(c *gin.Context) {
	providers := gin.H{}
	if h.cfg.Auth.Google != nil {
		providers["google"] = true
	}
	if h.cfg.Auth.GitHub != nil {
		providers["github"] = true
	}
	c.JSON(200, gin.H{
		"external":           providers,
		"disable_signup":     false,
		"mailer_autoconfirm": h.cfg.Auth.Email == nil || !h.cfg.Auth.Email.VerifyEmail,
		"phone_autoconfirm":  false,
		"sms_provider":       "",
		"mfa_enabled":        false,
		"saml_enabled":       false,
	})
}

// ---------- OAuth ----------

func (h *AuthHandler) handleAuthorize(c *gin.Context) {
	provider := c.Query("provider")
	redirectTo := c.Query("redirect_to")
	codeChallenge := c.Query("code_challenge")
	codeChallengeMethod := c.Query("code_challenge_method")

	// Reject a disallowed redirect_to up front so an attacker URL never gets
	// stored in flow_state / a cookie and later receives the auth code.
	if !h.redirectAllowed(redirectTo) {
		problemJSON(c, 400, "bad_request", "redirect_to is not an allowed URL")
		return
	}

	var cfg *domain.OAuthProvider
	switch provider {
	case "google":
		cfg = h.cfg.Auth.Google
	case "github":
		cfg = h.cfg.Auth.GitHub
	}
	if cfg == nil {
		problemJSON(c, 400, "bad_request", "Unsupported or unconfigured provider: "+provider)
		return
	}

	state := generateRandomToken()

	if codeChallenge != "" {
		if codeChallengeMethod == "" {
			codeChallengeMethod = "S256"
		}
		ctx := c.Request.Context()
		_, err := h.db.Exec(ctx,
			"INSERT INTO auth.flow_state (auth_code, code_challenge, code_challenge_method, redirect_to, provider_type, authentication_method, linking_user_id, auth_code_issued_at) VALUES ($1, $2, $3, $4, 'oauth', 'oauth', $5, NOW())",
			state, codeChallenge, codeChallengeMethod, redirectTo, nil)
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to store OAuth state")
			return
		}
	} else {
		c.SetCookie("oauth_state", state, 600, "/", "", false, true)
		c.SetCookie("oauth_redirect_to", redirectTo, 600, "/", "", false, true)
	}

	var authURL string
	switch provider {
	case "google":
		authURL = fmt.Sprintf(
			"https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=openid+email+profile&state=%s",
			cfg.ClientID, url.QueryEscape(cfg.RedirectURL), state)
	case "github":
		authURL = fmt.Sprintf(
			"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&scope=user:email&state=%s",
			cfg.ClientID, url.QueryEscape(cfg.RedirectURL), state)
	}
	c.Redirect(http.StatusTemporaryRedirect, authURL)
}

func (h *AuthHandler) handleOAuthCallback(provider string) gin.HandlerFunc {
	return func(c *gin.Context) {
		state := c.Query("state")
		ctx := c.Request.Context()

		// Try DB-stored state first (PKCE / identity linking), fall back to cookie.
		var dbState map[string]any
		var redirectTo string
		var isPKCE bool
		var linkingUserID string

		dbState, _ = h.db.QueryRow(ctx,
			"SELECT code_challenge, code_challenge_method, redirect_to, linking_user_id FROM auth.flow_state WHERE auth_code = $1 AND provider_type = 'oauth' AND auth_code_issued_at > NOW() - INTERVAL '10 minutes'",
			state)
		if dbState != nil {
			h.db.Exec(ctx, "DELETE FROM auth.flow_state WHERE auth_code = $1 AND provider_type = 'oauth'", state)
			redirectTo, _ = dbState["redirect_to"].(string)
			if cc, _ := dbState["code_challenge"].(string); cc != "" {
				isPKCE = true
			}
			if lu, _ := dbState["linking_user_id"].(string); lu != "" {
				linkingUserID = lu
			}
		} else {
			savedState, _ := c.Cookie("oauth_state")
			if state == "" || state != savedState {
				problemJSON(c, 400, "bad_request", "Invalid OAuth state")
				return
			}
			redirectTo, _ = c.Cookie("oauth_redirect_to")
		}

		code := c.Query("code")
		if code == "" {
			problemJSON(c, 400, "bad_request", "Missing authorization code")
			return
		}

		oauthToken, err := h.exchangeCode(provider, code)
		if err != nil {
			h.logger.Error("oauth code exchange failed", "provider", provider, "error", err)
			problemJSON(c, 502, "oauth_error", "Failed to exchange authorization code")
			return
		}
		userInfo, err := h.fetchUserInfo(provider, oauthToken)
		if err != nil {
			h.logger.Error("oauth user info failed", "provider", provider, "error", err)
			problemJSON(c, 502, "oauth_error", "Failed to fetch user info from provider")
			return
		}
		if userInfo.Email == "" {
			problemJSON(c, 400, "bad_request", "Could not retrieve email from OAuth provider")
			return
		}

		// Identity linking: just add the identity to the existing user
		if linkingUserID != "" {
			h.db.Exec(ctx,
				`INSERT INTO auth.identities (user_id, provider, provider_user_id, email, last_sign_in_at, updated_at)
				 VALUES ($1::uuid, $2, $3, $4, NOW(), NOW())
				 ON CONFLICT (provider, provider_user_id) DO NOTHING`,
				linkingUserID, provider, userInfo.ProviderID, userInfo.Email)
			if redirectTo != "" && h.redirectAllowed(redirectTo) {
				c.Redirect(http.StatusFound, redirectTo+"#message=identity_linked")
				return
			}
			c.JSON(200, gin.H{"message": "Identity linked"})
			return
		}

		row, _ := h.db.QueryRow(ctx,
			fmt.Sprintf("SELECT %s FROM auth.users WHERE email = $1", userSelectCols), userInfo.Email)
		var userID string

		if row == nil {
			metaJSON, _ := json.Marshal(map[string]any{
				"provider":       provider,
				"full_name":      userInfo.Name,
				"email":          userInfo.Email,
				"email_verified": true,
			})
			appMetaJSON, _ := json.Marshal(map[string]any{
				"provider":  provider,
				"providers": []string{provider},
			})
			newRow, err := h.db.QueryRow(ctx,
				"INSERT INTO auth.users (email, email_verified, email_confirmed_at, raw_user_meta_data, raw_app_meta_data) VALUES ($1, true, NOW(), $2::jsonb, $3::jsonb) RETURNING "+userSelectCols,
				userInfo.Email, string(metaJSON), string(appMetaJSON))
			if err != nil {
				// Race: another request created the user between lookup and insert.
				row, err = h.db.QueryRow(ctx,
					fmt.Sprintf("SELECT %s FROM auth.users WHERE email = $1", userSelectCols), userInfo.Email)
				if err != nil || row == nil {
					problemJSON(c, 500, "internal", "Failed to create or find user")
					return
				}
				userID = asString(row["id"])
			} else {
				row = newRow
				userID = asString(newRow["id"])
			}
		} else {
			userID = asString(row["id"])
			h.db.Exec(ctx, "UPDATE auth.users SET email_verified = true, email_confirmed_at = COALESCE(email_confirmed_at, NOW()), last_sign_in_at = NOW(), updated_at = NOW() WHERE id = $1::uuid", userID)
		}

		h.db.Exec(ctx,
			`INSERT INTO auth.identities (user_id, provider, provider_user_id, email, last_sign_in_at, updated_at)
			 VALUES ($1::uuid, $2, $3, $4, NOW(), NOW())
			 ON CONFLICT (provider, provider_user_id)
			 DO UPDATE SET last_sign_in_at = EXCLUDED.last_sign_in_at, updated_at = EXCLUDED.updated_at`,
			userID, provider, userInfo.ProviderID, userInfo.Email)

		// PKCE flow: return an auth code instead of tokens directly
		if isPKCE {
			authCode := generateRandomToken()
			codeChallenge, _ := dbState["code_challenge"].(string)
			codeChallengeMethod, _ := dbState["code_challenge_method"].(string)
			if codeChallengeMethod == "" {
				codeChallengeMethod = "S256"
			}
			_, err := h.db.Exec(ctx,
				"INSERT INTO auth.flow_state (auth_code, user_id, code_challenge, code_challenge_method, provider_type, authentication_method, auth_code_issued_at) VALUES ($1, $2::uuid, $3, $4, 'pkce', 'pkce', NOW())",
				authCode, userID, codeChallenge, codeChallengeMethod)
			if err != nil {
				problemJSON(c, 500, "internal", "Failed to store auth code")
				return
			}
			if redirectTo != "" && h.redirectAllowed(redirectTo) {
				sep := "?"
				if strings.Contains(redirectTo, "?") {
					sep = "&"
				}
				c.Redirect(http.StatusFound, redirectTo+sep+"code="+authCode)
				return
			}
			c.JSON(200, gin.H{"code": authCode})
			return
		}

		ctx = ctxWithRequestMeta(ctx, c)
		session, err := h.buildSession(ctx, userID, row)
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to generate token")
			return
		}

		if redirectTo != "" && h.redirectAllowed(redirectTo) {
			frag := url.Values{}
			frag.Set("access_token", session["access_token"].(string))
			if rt, ok := session["refresh_token"].(string); ok && rt != "" {
				frag.Set("refresh_token", rt)
			}
			frag.Set("token_type", "bearer")
			frag.Set("expires_in", fmt.Sprintf("%d", session["expires_in"].(int)))
			frag.Set("expires_at", fmt.Sprintf("%d", session["expires_at"].(int64)))
			frag.Set("provider_token", oauthToken)
			frag.Set("type", "oauth")
			c.Redirect(http.StatusFound, redirectTo+"#"+frag.Encode())
			return
		}
		c.JSON(200, session)
	}
}

// ---------- session / user builders ----------

// buildSession issues a JWT access token (and optional refresh token) and
// returns the GoTrue-shaped session payload: {access_token, token_type,
// expires_in, expires_at, refresh_token, user}.
func (h *AuthHandler) buildSession(ctx context.Context, userID string, userRow map[string]any) (gin.H, error) {
	key, err := h.jwtKeys.Active(ctx)
	if err != nil {
		return nil, fmt.Errorf("jwt key: %w", err)
	}

	expiry, err := time.ParseDuration(h.cfg.Auth.JWTExpiry)
	if err != nil || expiry == 0 {
		expiry = 15 * time.Minute
	}

	now := time.Now()
	exp := now.Add(expiry)
	sessionID := generateRandomToken()
	email, _ := userRow["email"].(string)

	appMeta := decodeJSONB(userRow["raw_app_meta_data"])
	if appMeta == nil {
		appMeta = map[string]any{}
	}
	// Default provider metadata — supabase-js reads identities/provider
	// off this blob in a few places.
	if _, ok := appMeta["provider"]; !ok {
		appMeta["provider"] = "email"
	}
	if _, ok := appMeta["providers"]; !ok {
		appMeta["providers"] = []string{"email"}
	}
	userMeta := decodeJSONB(userRow["raw_user_meta_data"])
	if userMeta == nil {
		userMeta = map[string]any{}
	}

	// is_anonymous is sourced from raw_app_meta_data — anonymous signup
	// stores {"is_anonymous": true} there so the claim survives refreshes.
	isAnon, _ := appMeta["is_anonymous"].(bool)
	if col, ok := userRow["is_anonymous"].(bool); ok && col {
		isAnon = true
	}

	claims := jwt.MapClaims{
		"iss":           h.issuer(),
		"sub":           userID,
		"aud":           "authenticated",
		"role":          "authenticated",
		"email":         email,
		"iat":           now.Unix(),
		"exp":           exp.Unix(),
		"session_id":    sessionID,
		"app_metadata":  appMeta,
		"user_metadata": userMeta,
		"is_anonymous":  isAnon,
	}

	var signingMethod jwt.SigningMethod
	var signingKey any
	switch key.Algorithm {
	case "RS256":
		signingMethod = jwt.SigningMethodRS256
		signingKey = key.PrivateKey
	default:
		signingMethod = jwt.SigningMethodHS256
		signingKey = key.Secret
	}
	token := jwt.NewWithClaims(signingMethod, claims)
	token.Header["kid"] = key.KID
	tokenStr, err := token.SignedString(signingKey)
	if err != nil {
		return nil, err
	}

	result := gin.H{
		"access_token": tokenStr,
		"token_type":   "bearer",
		"expires_in":   int(expiry.Seconds()),
		"expires_at":   exp.Unix(),
		"user":         h.buildUser(userID, userRow),
	}

	if h.cfg.Auth.RefreshTokens {
		refreshToken := generateRandomToken()
		refreshExpiry, _ := time.ParseDuration(h.cfg.Auth.RefreshTokenExpiry)
		if refreshExpiry == 0 {
			refreshExpiry = 7 * 24 * time.Hour
		}
		ip, _ := ctx.Value(ctxKeyIP).(string)
		ua, _ := ctx.Value(ctxKeyUA).(string)
		_, err := h.db.Exec(context.Background(),
			"INSERT INTO auth.refresh_tokens (user_id, token, session_id, ip, user_agent, expires_at) VALUES ($1::uuid, $2, $3, $4, $5, $6)",
			userID, refreshToken, sessionID, ip, ua, time.Now().Add(refreshExpiry))
		if err != nil {
			return nil, err
		}
		result["refresh_token"] = refreshToken
	}

	return result, nil
}

// buildUser produces the GoTrue user object. Field names and nesting are
// load-bearing: supabase-js stores this in localStorage and reads specific
// paths from it. Missing fields won't error — they'll surface as undefined
// downstream.
func (h *AuthHandler) buildUser(userID string, row map[string]any) gin.H {
	email, _ := row["email"].(string)
	emailVerified, _ := row["email_verified"].(bool)
	createdAt := asTimeString(row["created_at"])
	updatedAt := asTimeString(row["updated_at"])
	emailConfirmedAt := asTimeString(row["email_confirmed_at"])
	lastSignInAt := asTimeString(row["last_sign_in_at"])

	appMeta := decodeJSONB(row["raw_app_meta_data"])
	if appMeta == nil {
		appMeta = map[string]any{"provider": "email", "providers": []string{"email"}}
	}
	userMeta := decodeJSONB(row["raw_user_meta_data"])
	if userMeta == nil {
		userMeta = map[string]any{}
	}

	var confirmedAt string
	if emailVerified {
		confirmedAt = emailConfirmedAt
	}

	return gin.H{
		"id":                 userID,
		"aud":                "authenticated",
		"role":               "authenticated",
		"email":              email,
		"email_confirmed_at": emailConfirmedAt,
		"phone":              "",
		"confirmed_at":       confirmedAt,
		"last_sign_in_at":    lastSignInAt,
		"app_metadata":       appMeta,
		"user_metadata":      userMeta,
		"identities":         []any{},
		"created_at":         createdAt,
		"updated_at":         updatedAt,
	}
}

// ---------- helpers ----------

// asString coerces a pgx value (which may be string, []byte, [16]byte for
// UUID, or nil) into a string. UUID columns selected with ::text always
// arrive as strings, but we fall back for the untyped case.
func asString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case [16]byte:
		return fmt.Sprintf("%x-%x-%x-%x-%x", x[0:4], x[4:6], x[6:8], x[8:10], x[10:16])
	default:
		return fmt.Sprintf("%v", x)
	}
}

// asInt64 coerces an integer-typed column value (pgx may decode as int32/int64)
// to int64, returning 0 for nil/unknown types.
func asInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int32:
		return int64(x)
	case int:
		return int64(x)
	default:
		return 0
	}
}

// asTimeString returns an RFC3339 string for a time column, or "" for nil.
func asTimeString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case time.Time:
		if x.IsZero() {
			return ""
		}
		return x.UTC().Format(time.RFC3339)
	case string:
		return x
	default:
		return ""
	}
}

// decodeJSONB coerces a pgx JSONB value (string or []byte) into a map.
func decodeJSONB(v any) map[string]any {
	var raw []byte
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		raw = []byte(x)
	case []byte:
		raw = x
	case map[string]any:
		return x
	default:
		return nil
	}
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// issuer returns the JWT `iss` claim — the canonical base URL of this
// instancez instance with /auth/v1 appended, matching GoTrue's convention.
func (h *AuthHandler) issuer() string {
	return h.baseURL() + "/auth/v1"
}

// ---------- OAuth provider helpers (unchanged) ----------

// oauthUserInfo holds provider user details.
type oauthUserInfo struct {
	Email      string
	Name       string
	ProviderID string
}

func (h *AuthHandler) exchangeCode(provider, code string) (string, error) {
	var cfg *domain.OAuthProvider
	var tokenURL string

	switch provider {
	case "google":
		cfg = h.cfg.Auth.Google
		tokenURL = "https://oauth2.googleapis.com/token"
	case "github":
		cfg = h.cfg.Auth.GitHub
		tokenURL = "https://github.com/login/oauth/access_token"
	default:
		return "", fmt.Errorf("unsupported provider: %s", provider)
	}

	data := url.Values{
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURL},
		"grant_type":    {"authorization_code"},
	}

	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, body)
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to parse token response: %w", err)
	}
	if tokenResp.Error != "" {
		return "", fmt.Errorf("oauth error: %s", tokenResp.Error)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("no access_token in response")
	}

	return tokenResp.AccessToken, nil
}

func (h *AuthHandler) fetchUserInfo(provider, accessToken string) (*oauthUserInfo, error) {
	switch provider {
	case "google":
		return h.fetchGoogleUser(accessToken)
	case "github":
		return h.fetchGitHubUser(accessToken)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}
}

func (h *AuthHandler) fetchGoogleUser(accessToken string) (*oauthUserInfo, error) {
	req, _ := http.NewRequest("GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("google userinfo returned %d: %s", resp.StatusCode, body)
	}

	var info struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, err
	}

	return &oauthUserInfo{
		Email:      info.Email,
		Name:       info.Name,
		ProviderID: info.ID,
	}, nil
}

func (h *AuthHandler) fetchGitHubUser(accessToken string) (*oauthUserInfo, error) {
	req, _ := http.NewRequest("GET", "https://api.github.com/user", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("github user returned %d: %s", resp.StatusCode, body)
	}

	var user struct {
		ID    int64  `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &user); err != nil {
		return nil, err
	}

	if user.Email == "" {
		user.Email, _ = h.fetchGitHubPrimaryEmail(accessToken)
	}

	return &oauthUserInfo{
		Email:      user.Email,
		Name:       user.Name,
		ProviderID: fmt.Sprintf("%d", user.ID),
	}, nil
}

func (h *AuthHandler) fetchGitHubPrimaryEmail(accessToken string) (string, error) {
	req, _ := http.NewRequest("GET", "https://api.github.com/user/emails", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.Unmarshal(body, &emails); err != nil {
		return "", err
	}

	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}
	for _, e := range emails {
		if e.Verified {
			return e.Email, nil
		}
	}
	return "", fmt.Errorf("no verified email found")
}

// ---------- email templates ----------

func (h *AuthHandler) sendVerificationEmail(userID, email string) {
	token := generateRandomToken()
	expiresAt := time.Now().Add(24 * time.Hour)

	ctx := context.Background()
	h.db.Exec(ctx,
		"INSERT INTO auth.one_time_tokens (user_id, token, purpose, expires_at) VALUES ($1::uuid, $2, 'signup', $3)",
		userID, token, expiresAt)

	var fromEmail string
	if h.cfg.Providers.Email != nil {
		fromEmail = h.cfg.Providers.Email.FromEmail
	}
	subject := "Verify your email"
	body := fmt.Sprintf("Please verify your email by clicking this link: %s/auth/v1/verify?token=%s", h.baseURL(), token)

	if h.cfg.Auth.Email != nil && h.cfg.Auth.Email.Templates != nil {
		if tmpl, ok := h.cfg.Auth.Email.Templates["verification"]; ok {
			if tmpl.Subject != "" {
				subject = tmpl.Subject
			}
			if tmpl.Body != "" {
				body = renderAuthTemplate(tmpl.Body, map[string]string{
					"token":    token,
					"email":    email,
					"base_url": h.baseURL(),
					"link":     fmt.Sprintf("%s/auth/v1/verify?token=%s", h.baseURL(), token),
				})
			}
		}
	}

	if err := h.email.Send(ctx, domain.EmailMessage{
		To:      []string{email},
		From:    fromEmail,
		Subject: subject,
		HTML:    body,
		Text:    body,
	}); err != nil {
		h.logger.Error("failed to send verification email", "email", email, "error", err)
	}
}

func (h *AuthHandler) sendMagicLinkEmail(email, token, code string) {
	var fromEmail string
	if h.cfg.Providers.Email != nil {
		fromEmail = h.cfg.Providers.Email.FromEmail
	}
	subject := "Your magic sign-in link"
	link := fmt.Sprintf("%s/auth/v1/verify?token=%s&type=magiclink", h.baseURL(), token)
	body := fmt.Sprintf("Click to sign in: %s\n\nOr enter this code: %s", link, code)

	if h.cfg.Auth.Email != nil && h.cfg.Auth.Email.Templates != nil {
		if tmpl, ok := h.cfg.Auth.Email.Templates["magiclink"]; ok {
			if tmpl.Subject != "" {
				subject = tmpl.Subject
			}
			if tmpl.Body != "" {
				body = renderAuthTemplate(tmpl.Body, map[string]string{
					"token":    token,
					"code":     code,
					"email":    email,
					"base_url": h.baseURL(),
					"link":     link,
				})
			}
		}
	}

	ctx := context.Background()
	if err := h.email.Send(ctx, domain.EmailMessage{
		To:      []string{email},
		From:    fromEmail,
		Subject: subject,
		HTML:    body,
		Text:    body,
	}); err != nil {
		h.logger.Error("failed to send magic link email", "email", email, "error", err)
	}
}

func (h *AuthHandler) sendPasswordResetEmail(email, token, redirectTo string) {
	var fromEmail string
	if h.cfg.Providers.Email != nil {
		fromEmail = h.cfg.Providers.Email.FromEmail
	}
	subject := "Reset your password"
	// Build the verification link that points to GET /auth/v1/verify so the
	// handler can validate the token, generate a session, and redirect the
	// user to the app with access_token in the URL fragment — matching the
	// GoTrue flow that supabase-js expects.
	verifyLink := fmt.Sprintf("%s/auth/v1/verify?token=%s&type=recovery", h.baseURL(), token)
	if redirectTo != "" {
		verifyLink += "&redirect_to=" + url.QueryEscape(redirectTo)
	}
	body := fmt.Sprintf("Reset your password by clicking this link: %s", verifyLink)

	if h.cfg.Auth.Email != nil && h.cfg.Auth.Email.Templates != nil {
		if tmpl, ok := h.cfg.Auth.Email.Templates["reset"]; ok {
			if tmpl.Subject != "" {
				subject = tmpl.Subject
			}
			if tmpl.Body != "" {
				body = renderAuthTemplate(tmpl.Body, map[string]string{
					"token":    token,
					"email":    email,
					"base_url": h.baseURL(),
					"link":     verifyLink,
				})
			}
		}
	}

	ctx := context.Background()
	if err := h.email.Send(ctx, domain.EmailMessage{
		To:      []string{email},
		From:    fromEmail,
		Subject: subject,
		HTML:    body,
		Text:    body,
	}); err != nil {
		h.logger.Error("failed to send password reset email", "email", email, "error", err)
	}
}

func (h *AuthHandler) baseURL() string {
	if base := os.Getenv("INSTANCEZ_BASE_URL"); base != "" {
		return strings.TrimRight(base, "/")
	}
	return fmt.Sprintf("http://localhost:%d", h.cfg.Server.Port)
}

// redirectAllowed reports whether target is a safe redirect/return destination
// under the configured allowlist (see domain.Auth.IsRedirectAllowed).
func (h *AuthHandler) redirectAllowed(target string) bool {
	return h.cfg.Auth.IsRedirectAllowed(target, h.baseURL())
}

// resolveRedirect returns target when it is an allowed destination, otherwise
// the server base URL. A rejected non-empty target is logged. This is the
// chokepoint that prevents an attacker-supplied redirect_to from receiving the
// session tokens or auth code appended to post-auth redirects.
func (h *AuthHandler) resolveRedirect(target string) string {
	if h.redirectAllowed(target) && target != "" {
		return target
	}
	if target != "" {
		h.logger.Warn("rejected disallowed redirect_to", "redirect_to", target)
	}
	return h.baseURL()
}

func renderAuthTemplate(tmpl string, vars map[string]string) string {
	result := tmpl
	for k, v := range vars {
		result = strings.ReplaceAll(result, "{{"+k+"}}", v)
	}
	return result
}

// generateNumericCode returns an n-digit zero-padded numeric string drawn
// from crypto/rand. Used for email OTP codes (n=6) that a human types into
// a form; callers must scope the lookup by email to avoid birthday
// collisions on the small 10^n space.
func generateNumericCode(n int) string {
	const digits = "0123456789"
	out := make([]byte, 0, n)
	// Rejection sampling: only accept bytes in [0,250) so each maps to a digit
	// with uniform probability (250 is the largest multiple of 10 ≤ 256).
	// Using b%10 over the full byte range would bias digits 0–5. crypto/rand
	// failures are fatal for token security, so panic rather than emit a
	// predictable code.
	buf := make([]byte, n)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
		for _, x := range buf {
			if x >= 250 {
				continue
			}
			out = append(out, digits[x%10])
			if len(out) == n {
				break
			}
		}
	}
	return string(out)
}

func isDuplicateKeyErr(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique"))
}

func hashPassword(c *gin.Context, password string) (string, bool) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to hash password")
		return "", false
	}
	return string(b), true
}

func marshalJSONBDefault(v any) []byte {
	b, _ := json.Marshal(v)
	if len(b) == 0 || string(b) == "null" {
		return []byte("{}")
	}
	return b
}

func generateRandomToken() string {
	b := make([]byte, 32)
	// A read failure would otherwise yield an all-zero (predictable) token.
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func base64RawURL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func bigIntBytes(i int) []byte {
	b := make([]byte, 0, 4)
	for i > 0 {
		b = append([]byte{byte(i & 0xff)}, b...)
		i >>= 8
	}
	if len(b) == 0 {
		b = []byte{0}
	}
	return b
}

// ---------- /resend ----------

func (h *AuthHandler) handleResend(c *gin.Context) {
	var req struct {
		Type  string `json:"type" binding:"required"`
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Invalid resend request: "+err.Error())
		return
	}

	purposeMap := map[string]string{
		"signup":       "signup",
		"email_change": "email_change",
		"magiclink":    "magiclink",
	}
	purpose, ok := purposeMap[req.Type]
	if !ok {
		problemJSON(c, 400, "bad_request", "type must be one of: signup, email_change, magiclink")
		return
	}

	ctx := c.Request.Context()
	row, _ := h.db.QueryRow(ctx, "SELECT id::text FROM auth.users WHERE email = $1", req.Email)
	if row == nil {
		c.Status(200)
		return
	}
	userID := asString(row["id"])

	h.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE user_id = $1::uuid AND purpose = $2", userID, purpose)

	token := generateRandomToken()
	code := generateNumericCode(6)
	expiresAt := time.Now().Add(1 * time.Hour)
	_, err := h.db.Exec(ctx,
		"INSERT INTO auth.one_time_tokens (user_id, token, code, email, purpose, expires_at) VALUES ($1::uuid, $2, $3, $4, $5, $6)",
		userID, token, code, req.Email, purpose, expiresAt)
	if err != nil {
		h.logger.Error("resend token insert failed", "error", err)
		c.Status(200)
		return
	}
	if h.email != nil {
		go h.sendMagicLinkEmail(req.Email, token, code)
	}
	c.Status(200)
}

// ---------- /reauthenticate ----------

func (h *AuthHandler) handleReauthenticate(c *gin.Context) {
	session := getSession(c)
	if session.UserID == "" {
		problemJSON(c, 401, "unauthorized", "Not authenticated")
		return
	}

	ctx := c.Request.Context()
	row, _ := h.db.QueryRow(ctx, "SELECT email FROM auth.users WHERE id = $1::uuid", session.UserID)
	if row == nil {
		problemJSON(c, 404, "not_found", "User not found")
		return
	}
	email := asString(row["email"])
	if email == "" {
		problemJSON(c, 422, "unprocessable", "User has no email for reauthentication")
		return
	}

	token := generateRandomToken()
	code := generateNumericCode(6)
	expiresAt := time.Now().Add(10 * time.Minute)
	_, err := h.db.Exec(ctx,
		"INSERT INTO auth.one_time_tokens (user_id, token, code, email, purpose, expires_at) VALUES ($1::uuid, $2, $3, $4, 'reauthentication', $5)",
		session.UserID, token, code, email, expiresAt)
	if err != nil {
		h.logger.Error("reauthenticate token insert failed", "error", err)
		problemJSON(c, 500, "internal", "Failed to create reauthentication nonce")
		return
	}
	if h.email != nil {
		go h.sendMagicLinkEmail(email, token, code)
	}
	c.Status(200)
}

// ---------- /token/verify ----------

func (h *AuthHandler) handleTokenVerify(c *gin.Context) {
	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Invalid request: "+err.Error())
		return
	}

	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(req.Token, jwt.MapClaims{})
	if err != nil {
		problemJSON(c, 401, "invalid_token", "Malformed token")
		return
	}

	kid, _ := token.Header["kid"].(string)
	key, err := h.jwtKeys.Get(c.Request.Context(), kid)
	if err != nil {
		problemJSON(c, 401, "invalid_token", "Unknown signing key")
		return
	}

	verified, err := jwt.Parse(req.Token, func(t *jwt.Token) (any, error) {
		switch t.Method.(type) {
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
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
	})
	if err != nil || !verified.Valid {
		problemJSON(c, 401, "invalid_token", "Token verification failed")
		return
	}

	claims, _ := verified.Claims.(jwt.MapClaims)
	c.JSON(200, claims)
}

// ---------- /.well-known/jwks.json ----------

func (h *AuthHandler) handleJWKS(c *gin.Context) {
	keys, err := h.jwtKeys.AllPublicKeys(c.Request.Context())
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to load keys")
		return
	}
	var jwks []gin.H
	for _, key := range keys {
		if key.PublicKey == nil {
			continue
		}
		jwks = append(jwks, gin.H{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": key.KID,
			"n":   base64RawURL(key.PublicKey.N.Bytes()),
			"e":   base64RawURL(bigIntBytes(key.PublicKey.E)),
		})
	}
	if jwks == nil {
		jwks = []gin.H{}
	}
	c.JSON(200, gin.H{"keys": jwks})
}

// ---------- /user/identities ----------

func (h *AuthHandler) handleListIdentities(c *gin.Context) {
	session := getSession(c)
	ctx := c.Request.Context()
	rows, err := h.db.Query(ctx,
		"SELECT id::text, provider, provider_user_id, identity_data, email, last_sign_in_at, created_at, updated_at FROM auth.identities WHERE user_id = $1::uuid ORDER BY created_at",
		session.UserID)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to list identities")
		return
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	c.JSON(200, gin.H{"identities": rows})
}

func (h *AuthHandler) handleLinkIdentity(c *gin.Context) {
	session := getSession(c)
	provider := c.Query("provider")
	redirectTo := c.Query("redirect_to")

	var cfg *domain.OAuthProvider
	switch provider {
	case "google":
		cfg = h.cfg.Auth.Google
	case "github":
		cfg = h.cfg.Auth.GitHub
	}
	if cfg == nil {
		problemJSON(c, 400, "bad_request", "Unsupported or unconfigured provider: "+provider)
		return
	}

	state := generateRandomToken()
	ctx := c.Request.Context()
	_, err := h.db.Exec(ctx,
		"INSERT INTO auth.flow_state (auth_code, redirect_to, provider_type, authentication_method, linking_user_id, auth_code_issued_at) VALUES ($1, $2, 'oauth', 'oauth', $3, NOW())",
		state, redirectTo, session.UserID)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to store OAuth state")
		return
	}

	var authURL string
	switch provider {
	case "google":
		authURL = fmt.Sprintf(
			"https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=openid+email+profile&state=%s",
			cfg.ClientID, url.QueryEscape(cfg.RedirectURL), state)
	case "github":
		authURL = fmt.Sprintf(
			"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&scope=user:email&state=%s",
			cfg.ClientID, url.QueryEscape(cfg.RedirectURL), state)
	}
	c.JSON(200, gin.H{"url": authURL})
}

func (h *AuthHandler) handleUnlinkIdentity(c *gin.Context) {
	session := getSession(c)
	identityID := c.Param("id")
	ctx := c.Request.Context()

	// Ensure user keeps at least one auth method
	rows, err := h.db.Query(ctx, "SELECT id::text FROM auth.identities WHERE user_id = $1::uuid", session.UserID)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to check identities")
		return
	}
	hasPassword := false
	pwRow, _ := h.db.QueryRow(ctx, "SELECT password_hash FROM auth.users WHERE id = $1::uuid", session.UserID)
	if pwRow != nil {
		if ph, _ := pwRow["password_hash"].(string); ph != "" {
			hasPassword = true
		}
	}
	if len(rows) <= 1 && !hasPassword {
		problemJSON(c, 400, "bad_request", "Cannot unlink the only identity without a password set")
		return
	}

	res, err := h.db.Exec(ctx,
		"DELETE FROM auth.identities WHERE id = $1::uuid AND user_id = $2::uuid",
		identityID, session.UserID)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to unlink identity")
		return
	}
	if res == 0 {
		problemJSON(c, 404, "not_found", "Identity not found")
		return
	}
	c.Status(200)
}

// ---------- admin: signOut + deleteFactor ----------

func (h *AuthHandler) handleAdminSignOut(c *gin.Context) {
	uid := c.Param("uid")
	ctx := c.Request.Context()
	h.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE user_id = $1::uuid", uid)
	c.Status(204)
}

func (h *AuthHandler) handleAdminDeleteFactor(c *gin.Context) {
	uid := c.Param("uid")
	factorID := c.Param("factor_id")
	ctx := c.Request.Context()
	res, err := h.db.Exec(ctx,
		"DELETE FROM auth.mfa_factors WHERE id = $1::uuid AND user_id = $2::uuid",
		factorID, uid)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to delete factor")
		return
	}
	if res == 0 {
		problemJSON(c, 404, "not_found", "Factor not found")
		return
	}
	c.Status(200)
}

// extractSessionID parses the session_id claim from a raw JWT without
// re-verifying the signature (already done by middleware).
func (h *AuthHandler) extractSessionID(rawJWT string) string {
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(rawJWT, jwt.MapClaims{})
	if err != nil {
		return ""
	}
	claims, _ := token.Claims.(jwt.MapClaims)
	sid, _ := claims["session_id"].(string)
	return sid
}
