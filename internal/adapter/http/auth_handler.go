package http

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	adapterauth "github.com/instancez/instancez/internal/adapter/auth"
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
	logger  *slog.Logger
	email   domain.EmailSender
	jwtKeys *app.JWTKeyManager
	authSvc domain.AuthService // auth data operations via the service port
}

func NewAuthHandler(deps ServerDeps) *AuthHandler {
	return &AuthHandler{
		cfg:     deps.Config,
		logger:  deps.Logger,
		email:   deps.Email,
		jwtKeys: deps.JWTKeys,
		authSvc: adapterauth.NewService(deps.DB.Database, deps.Config, deps.Logger),
	}
}

// Mount registers the /auth/v1/* routes on the root router group.
func (h *AuthHandler) Mount(root *gin.RouterGroup) {
	auth := root.Group("/auth/v1")
	// Per-IP rate limiting for these sensitive endpoints is enforced at the
	// edge (Traefik, configured by the deployer) rather than here: in the
	// hosted deployment the backend runs as a Lambda behind a proxy, so it
	// can't see the real client IP and an in-memory limiter wouldn't survive
	// across Lambda execution environments. The per-token OTP attempt cap in
	// handleVerify is the credential-level brute-force defense and stays in the
	// app (it's backed by the database, shared across invocations).
	auth.POST("/signup", h.handleSignupDispatch)
	auth.POST("/token", h.handleToken)
	auth.GET("/user", jwtAuth(h.jwtKeys, true), h.handleGetUser)
	auth.PUT("/user", jwtAuth(h.jwtKeys, true), h.handleUpdateUser)
	auth.POST("/logout", jwtAuth(h.jwtKeys, true), h.handleLogout)
	auth.GET("/settings", h.handleSettings)

	if h.cfg.Auth.Email != nil {
		auth.POST("/recover", h.handleRecover)
		auth.POST("/verify", h.handleVerify)
		auth.GET("/verify", h.handleVerifyGET)
		auth.POST("/otp", h.handleOTP)
		auth.POST("/resend", h.handleResend)
		// supabase-js calls reauthenticate() over GET (matching GoTrue); keep
		// POST too so existing direct callers don't break.
		auth.GET("/reauthenticate", jwtAuth(h.jwtKeys, true), h.handleReauthenticate)
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
	for name, cfg := range h.cfg.Auth.OAuth {
		if cfg == nil {
			continue
		}
		if _, ok := adapterauth.OAuthRegistry(name); ok {
			auth.GET("/callback/"+name, h.handleOAuthCallback(name))
		}
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

	ctx := c.Request.Context()
	row, err := h.authSvc.CreateUser(ctx, domain.CreateUserParams{
		Anonymous:    true,
		UserMetadata: userData,
		AppMetadata: map[string]any{
			"provider":     "anonymous",
			"providers":    []string{"anonymous"},
			"is_anonymous": true,
		},
	})
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

	// Signup always sets raw_user_meta_data (JSONB). Signup data is stored
	// in raw_user_meta_data rather than promoted to top-level columns.
	ctx := c.Request.Context()
	row, err := h.authSvc.CreateUser(ctx, domain.CreateUserParams{
		Email:        req.Email,
		Password:     hash,
		UserMetadata: req.Data,
	})
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
	row, err := h.authSvc.VerifyPassword(ctx, req.Email, req.Password)
	if err != nil {
		if errors.Is(err, domain.ErrOAuthOnlyAccount) {
			problemJSON(c, 401, "invalid_grant", "Account uses OAuth login")
			return
		}
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
	h.authSvc.RecordSignIn(ctx, userID)
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
	userRow, err := h.authSvc.ConsumeRefreshToken(ctx, req.RefreshToken)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrRefreshExpired):
			problemJSON(c, 401, "invalid_grant", "Refresh token expired")
		case errors.Is(err, domain.ErrRefreshReuse):
			problemJSON(c, 401, "invalid_grant", "Refresh token reuse detected. All sessions revoked.")
		default:
			problemJSON(c, 401, "invalid_grant", "Invalid refresh token")
		}
		return
	}

	userID := asString(userRow["id"])

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
	codeChallenge, codeChallengeMethod, userID, err := h.authSvc.GetPKCEFlowState(ctx, req.AuthCode)
	if err != nil {
		problemJSON(c, 401, "invalid_grant", "Invalid or expired auth code")
		return
	}

	_ = h.authSvc.DeletePKCEFlowState(ctx, req.AuthCode)

	if !verifyCodeChallenge(codeChallengeMethod, req.CodeVerifier, codeChallenge) {
		problemJSON(c, 401, "invalid_grant", "Code verifier does not match challenge")
		return
	}

	userRow, err := h.authSvc.GetUserByID(ctx, userID)
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

	g := h.cfg.Auth.OAuth["google"]
	if req.Provider != "google" || g == nil {
		if req.Provider == "google" {
			problemJSON(c, 400, "bad_request", "Google provider not configured")
			return
		}
		problemJSON(c, 400, "bad_request", "Unsupported provider for ID token: "+req.Provider)
		return
	}
	clientID := g.ClientID

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
	row, err := h.authSvc.UpsertOAuthUser(ctx, req.Provider, sub, email, name)
	if err != nil || row == nil {
		problemJSON(c, 500, "internal", "Failed to create or find user")
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

// ---------- /user ----------

func (h *AuthHandler) handleGetUser(c *gin.Context) {
	session := getSession(c)
	ctx := c.Request.Context()

	row, err := h.authSvc.GetUserByID(ctx, session.UserID)
	if err != nil || row == nil {
		problemJSON(c, 404, "not_found", "User not found")
		return
	}
	c.JSON(200, h.buildUser(session.UserID, row, h.userIdentities(ctx, session.UserID)))
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

	params := domain.UpdateUserParams{
		Email:        req.Email,
		UserMetadata: req.Data,
	}
	if req.Email != nil && *req.Email != "" {
		// A newly-set address is unverified until the user proves control of
		// it. Carrying over the previous verified status to an unconfirmed
		// address would let a stolen session quietly swap in an attacker email
		// while keeping it "verified". (Full GoTrue-style double-confirm — send
		// a token to the new address and only swap on verify — is a larger
		// follow-up; at minimum we must not keep the verified flag.)
		if !strings.EqualFold(strings.TrimSpace(*req.Email), strings.TrimSpace(session.Email)) {
			params.ClearEmailVerified = true
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
		params.Password = &hash
	}

	ctx := c.Request.Context()
	row, err := h.authSvc.UpdateUser(ctx, session.UserID, params)
	if err != nil || row == nil {
		if isDuplicateKeyErr(err) {
			problemJSON(c, 409, "conflict", "Email already registered")
			return
		}
		problemJSON(c, 500, "internal", "Failed to update user")
		return
	}
	c.JSON(200, h.buildUser(session.UserID, row, h.userIdentities(ctx, session.UserID)))
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
				_ = h.authSvc.RevokeSessionByID(ctx, sessionID)
			} else {
				_ = h.authSvc.RevokeAllUserSessions(ctx, session.UserID)
			}
		case "others":
			if sessionID != "" {
				_ = h.authSvc.RevokeOtherSessions(ctx, session.UserID, sessionID)
			} else {
				_ = h.authSvc.RevokeAllUserSessions(ctx, session.UserID)
			}
		default: // global
			_ = h.authSvc.RevokeAllUserSessions(ctx, session.UserID)
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
	userID, err := h.authSvc.GetUserIDByEmail(ctx, req.Email)
	if err == nil && userID != "" {
		token := generateRandomToken()
		expiresAt := time.Now().Add(1 * time.Hour)
		_ = h.authSvc.CreateOneTimeToken(ctx, userID, token, "recovery", expiresAt.Unix())
		if h.email != nil {
			go h.sendPasswordResetEmail(req.Email, token, redirectTo)
		}
	}
	// Always return 200 (email enumeration protection). supabase-js parses
	// the response body as JSON, so emit an empty object rather than a bare
	// status with no body (which trips "Unexpected end of JSON input").
	c.JSON(200, gin.H{})
}

// ---------- /verify ----------

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

	// Determine which one-time-token purposes are acceptable for the requested
	// verify type. The service handles the two lookup shapes (opaque token vs
	// 6-digit code), attempt tracking, expiry, and single-use consumption.
	var allowedPurposes []string
	switch req.Type {
	case "signup", "email", "email_change":
		// "email" is supabase-js's signInWithOtp type and is served by the
		// magiclink flow (same row in auth.one_time_tokens, purpose set by
		// handleOTP). Accept both signup and magiclink purposes so first-time
		// users verifying via 6-digit code aren't rejected.
		allowedPurposes = []string{"signup", "magiclink"}
	case "recovery":
		allowedPurposes = []string{"recovery"}
	case "magiclink":
		// Treat as a login; any purpose is accepted (no state change).
		allowedPurposes = nil
	default:
		problemJSON(c, 400, "bad_request", "Unsupported verify type")
		return
	}

	otp, err := h.authSvc.VerifyOTP(ctx, req.Token, req.Email, allowedPurposes)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrTokenExpired):
			problemJSON(c, 401, "invalid_grant", "Token expired")
		case errors.Is(err, domain.ErrPurposeMismatch):
			problemJSON(c, 400, "bad_request", "Token purpose mismatch")
		default:
			problemJSON(c, 401, "invalid_grant", "Invalid or expired token")
		}
		return
	}

	userID := otp.UserID

	// Side-effect based on purpose. Recovery and magiclink make no immediate
	// change; the email-confirmation types mark the address verified.
	switch req.Type {
	case "signup", "email", "email_change":
		h.authSvc.MarkEmailVerified(ctx, userID)
	}

	userRow, err := h.authSvc.GetUserByID(ctx, userID)
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
	otp, err := h.authSvc.PeekOneTimeToken(ctx, token)
	if err != nil {
		if errors.Is(err, domain.ErrTokenExpired) {
			c.String(400, "Verification token expired")
			return
		}
		c.String(400, "Invalid verification token")
		return
	}

	userID := otp.UserID
	purpose := otp.Purpose

	// Consume the token (single-use).
	_ = h.authSvc.DeleteOneTimeToken(ctx, token)

	verifyType := c.DefaultQuery("type", "")

	if purpose == "recovery" || verifyType == "recovery" {
		// Recovery flow: build a session and redirect to the app with
		// the access token in the URL fragment so supabase-js can pick
		// it up and fire the PASSWORD_RECOVERY event.
		userRow, err := h.authSvc.GetUserByID(ctx, userID)
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
	h.authSvc.MarkEmailVerified(ctx, userID)

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
	userID, err := h.authSvc.GetUserIDByEmail(ctx, req.Email)
	if err != nil || userID == "" {
		if !createUser {
			c.JSON(200, gin.H{})
			return
		}
		newRow, cerr := h.authSvc.CreateUser(ctx, domain.CreateUserParams{
			Email:        req.Email,
			UserMetadata: req.Data,
		})
		if cerr != nil || newRow == nil {
			h.logger.Error("otp create user failed", "error", cerr)
			c.JSON(200, gin.H{})
			return
		}
		userID = asString(newRow["id"])
	}

	token := generateRandomToken()
	code := generateNumericCode(6)
	expiresAt := time.Now().Add(1 * time.Hour)
	if err := h.authSvc.CreateOTPCode(ctx, userID, token, code, req.Email, "magiclink", expiresAt.Unix()); err != nil {
		h.logger.Error("otp token insert failed", "error", err)
		c.JSON(200, gin.H{})
		return
	}
	if h.email != nil {
		go h.sendMagicLinkEmail(req.Email, token, code)
	}
	c.JSON(200, gin.H{})
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
	row, err := h.authSvc.GetUserByEmail(ctx, req.Email)
	if errors.Is(err, domain.ErrNotFound) {
		row = nil
	}

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
		newRow, cerr := h.authSvc.CreateUser(ctx, domain.CreateUserParams{
			Email:        req.Email,
			Password:     hash,
			UserMetadata: req.Data,
		})
		if cerr != nil || newRow == nil {
			h.logger.Error("generate_link create user failed", "error", cerr)
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
	if err := h.authSvc.CreateOneTimeToken(ctx, userID, token, purpose, expiresAt.Unix()); err != nil {
		h.logger.Error("generate_link token insert failed", "error", err)
		problemJSON(c, 500, "internal", "Failed to create verification token")
		return
	}

	actionLink := fmt.Sprintf("%s/auth/v1/verify?token=%s&type=%s", h.publicAuthBaseURL(), token, req.Type)
	if req.RedirectTo != "" {
		actionLink += "&redirect_to=" + url.QueryEscape(req.RedirectTo)
	}

	resp := gin.H{
		"action_link":       actionLink,
		"email_otp":         token,
		"hashed_token":      token,
		"verification_type": req.Type,
		"redirect_to":       req.RedirectTo,
		"user":              h.buildUser(userID, row, nil),
	}
	c.JSON(200, resp)
}

// ---------- admin user management ----------

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
	if _, err := mail.ParseAddress(req.Email); err != nil {
		problemJSON(c, 400, "bad_request", "Invalid email address")
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

	appMeta := req.AppMetadata
	if len(appMeta) == 0 {
		appMeta = map[string]any{"provider": "email", "providers": []string{"email"}}
	}

	ctx := c.Request.Context()
	row, err := h.authSvc.CreateUser(ctx, domain.CreateUserParams{
		Email:          req.Email,
		Password:       hash,
		UserMetadata:   req.UserMetadata,
		AppMetadata:    appMeta,
		EmailConfirmed: req.EmailConfirm,
		BanDuration:    req.BanDuration,
	})
	if err != nil {
		if isDuplicateKeyErr(err) {
			problemJSON(c, 422, "user_already_exists", "A user with this email address has already been registered")
			return
		}
		h.logger.Error("admin create user failed", "error", err)
		problemJSON(c, 500, "internal", "Failed to create user")
		return
	}

	c.JSON(200, h.buildUser(asString(row["id"]), row, nil))
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

	ctx := c.Request.Context()

	rows, total, err := h.authSvc.ListUsers(ctx, page, perPage)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to list users")
		return
	}

	users := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		users = append(users, h.buildUser(asString(row["id"]), row, nil))
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

	row, err := h.authSvc.GetUserByID(ctx, uid)
	if err != nil || row == nil {
		problemJSON(c, 404, "user_not_found", "User not found")
		return
	}

	c.JSON(200, h.buildUser(asString(row["id"]), row, nil))
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

	if req.Email != nil && *req.Email != "" {
		if _, err := mail.ParseAddress(*req.Email); err != nil {
			problemJSON(c, 400, "bad_request", "Invalid email address")
			return
		}
	}

	params := domain.UpdateUserParams{
		Email:          req.Email,
		EmailConfirmed: req.EmailConfirm,
		UserMetadata:   req.UserMetadata,
		AppMetadata:    req.AppMetadata,
	}
	if req.Password != nil && *req.Password != "" {
		hash, ok := hashPassword(c, *req.Password)
		if !ok {
			return
		}
		params.Password = &hash
	}
	if req.BanDuration != nil {
		params.BanDuration = req.BanDuration
	}

	ctx := c.Request.Context()
	row, err := h.authSvc.UpdateUser(ctx, uid, params)
	if isDuplicateKeyErr(err) {
		problemJSON(c, 422, "email_exists", "A user with this email address has already been registered")
		return
	}
	if errors.Is(err, domain.ErrNotFound) || row == nil {
		problemJSON(c, 404, "user_not_found", "User not found")
		return
	}
	if err != nil {
		h.logger.Error("admin update user failed", "error", err)
		problemJSON(c, 500, "internal", "Failed to update user")
		return
	}

	c.JSON(200, h.buildUser(asString(row["id"]), row, nil))
}

func (h *AuthHandler) handleAdminDeleteUser(c *gin.Context) {
	uid := c.Param("uid")
	ctx := c.Request.Context()

	if err := h.authSvc.DeleteUser(ctx, uid); err != nil {
		if strings.Contains(err.Error(), "not found") {
			problemJSON(c, 404, "user_not_found", "User not found")
			return
		}
		h.logger.Error("admin delete user failed", "error", err)
		problemJSON(c, 500, "internal", "Failed to delete user")
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

	if existingID, err := h.authSvc.GetUserIDByEmail(ctx, req.Email); err == nil && existingID != "" {
		problemJSON(c, 422, "user_already_exists", "A user with this email address has already been registered")
		return
	}

	row, err := h.authSvc.CreateUser(ctx, domain.CreateUserParams{
		Email:        req.Email,
		UserMetadata: req.Data,
		AppMetadata:  map[string]any{"provider": "email", "providers": []string{"email"}},
	})
	if err != nil || row == nil {
		h.logger.Error("admin invite failed", "error", err)
		problemJSON(c, 500, "internal", "Failed to create invited user")
		return
	}

	userID := asString(row["id"])

	if h.cfg.Auth.Email != nil && h.email != nil {
		go h.sendVerificationEmail(userID, req.Email)
	}

	c.JSON(200, h.buildUser(userID, row, nil))
}

// ---------- /settings ----------

func (h *AuthHandler) handleSettings(c *gin.Context) {
	providers := gin.H{}
	for name, p := range h.cfg.Auth.OAuth {
		if p != nil {
			providers[name] = true
		}
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
	//
	// redirect_to is unrelated to auth.oauth.<provider>.redirect_url: that
	// field is the fixed URL the provider (Google/GitHub/...) redirects back
	// to and must match its console config; redirect_to is where the app
	// wants the user to land afterwards, checked against auth.redirect_urls.
	// There is no SITE_URL-style default — if the client omits redirect_to,
	// the final callback returns the session as raw JSON instead of
	// redirecting, so most callers should pass it explicitly.
	if !h.redirectAllowed(redirectTo) {
		problemJSON(c, 400, "bad_request",
			"redirect_to is not an allowed URL — add its origin to auth.redirect_urls")
		return
	}

	cfg := h.cfg.Auth.OAuth[provider]
	prov, ok := adapterauth.OAuthRegistry(provider)
	if cfg == nil || !ok {
		problemJSON(c, 400, "bad_request", "Unsupported or unconfigured provider: "+provider)
		return
	}

	state := generateRandomToken()

	if codeChallenge != "" {
		if codeChallengeMethod == "" {
			codeChallengeMethod = "S256"
		}
		ctx := c.Request.Context()
		if err := h.authSvc.CreateOAuthFlowState(ctx, state, codeChallenge, codeChallengeMethod, redirectTo, ""); err != nil {
			problemJSON(c, 500, "internal", "Failed to store OAuth state")
			return
		}
	} else {
		c.SetCookie("oauth_state", state, 600, "/", "", false, true)
		c.SetCookie("oauth_redirect_to", redirectTo, 600, "/", "", false, true)
	}

	c.Redirect(http.StatusTemporaryRedirect, prov.AuthorizeURL(cfg, state))
}

func (h *AuthHandler) handleOAuthCallback(provider string) gin.HandlerFunc {
	return func(c *gin.Context) {
		state := c.Query("state")
		ctx := c.Request.Context()

		// Try DB-stored state first (PKCE / identity linking), fall back to cookie.
		var redirectTo string
		var isPKCE bool
		var linkingUserID string
		var flowChallenge, flowChallengeMethod string

		flow, ferr := h.authSvc.ConsumeOAuthFlowState(ctx, state)
		if ferr == nil {
			redirectTo = flow.RedirectTo
			flowChallenge = flow.CodeChallenge
			flowChallengeMethod = flow.CodeChallengeMethod
			if flow.CodeChallenge != "" {
				isPKCE = true
			}
			if flow.LinkingUserID != "" {
				linkingUserID = flow.LinkingUserID
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

		providerCfg := h.cfg.Auth.OAuth[provider]
		prov, ok := adapterauth.OAuthRegistry(provider)
		if providerCfg == nil || !ok {
			problemJSON(c, 400, "bad_request", "Unconfigured provider: "+provider)
			return
		}
		oauthToken, err := prov.ExchangeCode(providerCfg, code)
		if err != nil {
			h.logger.Error("oauth code exchange failed", "provider", provider, "error", err)
			problemJSON(c, 502, "oauth_error", "Failed to exchange authorization code")
			return
		}
		userInfo, err := prov.FetchUser(oauthToken)
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
			h.authSvc.LinkIdentity(ctx, linkingUserID, provider, userInfo.ProviderID, userInfo.Email)
			if redirectTo != "" && h.redirectAllowed(redirectTo) {
				c.Redirect(http.StatusFound, redirectTo+"#message=identity_linked")
				return
			}
			c.JSON(200, gin.H{"message": "Identity linked"})
			return
		}

		row, err := h.authSvc.UpsertOAuthUser(ctx, provider, userInfo.ProviderID, userInfo.Email, userInfo.Name)
		if err != nil || row == nil {
			problemJSON(c, 500, "internal", "Failed to create or find user")
			return
		}
		userID := asString(row["id"])

		// PKCE flow: return an auth code instead of tokens directly
		if isPKCE {
			authCode := generateRandomToken()
			codeChallengeMethod := flowChallengeMethod
			if codeChallengeMethod == "" {
				codeChallengeMethod = "S256"
			}
			if err := h.authSvc.CreatePKCEFlowState(ctx, authCode, userID, flowChallenge, codeChallengeMethod); err != nil {
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
		// No redirect_to (or a disallowed one): instancez has no SITE_URL-style
		// default to fall back to, so the session is returned as raw JSON
		// instead of a redirect. Callers that want the browser to land back
		// in the app must pass options.redirectTo to signInWithOAuth.
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
		"user":         h.buildUser(userID, userRow, h.userIdentities(ctx, userID)),
	}

	if h.cfg.Auth.RefreshTokens {
		refreshToken := generateRandomToken()
		refreshExpiry, _ := time.ParseDuration(h.cfg.Auth.RefreshTokenExpiry)
		if refreshExpiry == 0 {
			refreshExpiry = 7 * 24 * time.Hour
		}
		ip, _ := ctx.Value(ctxKeyIP).(string)
		ua, _ := ctx.Value(ctxKeyUA).(string)
		meta := domain.SessionMeta{SessionID: sessionID, IP: ip, UserAgent: ua}
		if err := h.authSvc.InsertRefreshToken(context.Background(), userID, refreshToken, meta, time.Now().Add(refreshExpiry).Unix()); err != nil {
			return nil, err
		}
		result["refresh_token"] = refreshToken
	}

	return result, nil
}

// buildUser produces the user object embedded in auth responses and stored by
// the client. Field names and nesting are read by supabase-js, so renaming a
// key surfaces as undefined downstream. identities holds the user's linked
// identities; pass nil (e.g. for admin or bulk responses) to render an empty
// array.
func (h *AuthHandler) buildUser(userID string, row map[string]any, identities []map[string]any) gin.H {
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

	if identities == nil {
		identities = []map[string]any{}
	}

	return gin.H{
		"id":                 userID,
		"aud":                "authenticated",
		"role":               "authenticated",
		"email":              email,
		"email_confirmed_at": emailConfirmedAt,
		"confirmed_at":       confirmedAt,
		"last_sign_in_at":    lastSignInAt,
		"banned_until":       asTimeString(row["banned_until"]),
		"app_metadata":       appMeta,
		"user_metadata":      userMeta,
		"identities":         identities,
		"created_at":         createdAt,
		"updated_at":         updatedAt,
	}
}

// userIdentities returns the user's linked identities for embedding in the
// user object. It returns nil on lookup failure, which buildUser renders as an
// empty array rather than failing the whole response.
func (h *AuthHandler) userIdentities(ctx context.Context, userID string) []map[string]any {
	ids, err := h.authSvc.ListIdentities(ctx, userID)
	if err != nil {
		h.logger.Error("list identities for user object failed", "error", err, "user_id", userID)
		return nil
	}
	return ids
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

// ---------- email templates ----------

func (h *AuthHandler) sendVerificationEmail(userID, email string) {
	token := generateRandomToken()
	expiresAt := time.Now().Add(24 * time.Hour)

	ctx := context.Background()
	_ = h.authSvc.CreateOneTimeToken(ctx, userID, token, "signup", expiresAt.Unix())

	var fromEmail string
	if h.cfg.Providers.Email != nil {
		fromEmail = h.cfg.Providers.Email.DefaultFromEmail
	}
	subject, body := h.resolveEmailTemplate("verification", map[string]string{
		"token":    token,
		"email":    email,
		"base_url": h.baseURL(),
		"link":     fmt.Sprintf("%s/auth/v1/verify?token=%s", h.publicAuthBaseURL(), token),
	})

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
		fromEmail = h.cfg.Providers.Email.DefaultFromEmail
	}
	link := fmt.Sprintf("%s/auth/v1/verify?token=%s&type=magiclink", h.publicAuthBaseURL(), token)
	subject, body := h.resolveEmailTemplate("magiclink", map[string]string{
		"token":    token,
		"code":     code,
		"email":    email,
		"base_url": h.baseURL(),
		"link":     link,
	})

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
		fromEmail = h.cfg.Providers.Email.DefaultFromEmail
	}
	// Build the verification link that points to GET /auth/v1/verify so the
	// handler can validate the token, generate a session, and redirect the
	// user to the app with access_token in the URL fragment — matching the
	// GoTrue flow that supabase-js expects.
	verifyLink := fmt.Sprintf("%s/auth/v1/verify?token=%s&type=recovery", h.publicAuthBaseURL(), token)
	if redirectTo != "" {
		verifyLink += "&redirect_to=" + url.QueryEscape(redirectTo)
	}
	subject, body := h.resolveEmailTemplate("reset", map[string]string{
		"token":    token,
		"email":    email,
		"base_url": h.baseURL(),
		"link":     verifyLink,
	})

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

// publicAuthBaseURL returns the externally reachable base for links that a
// user's browser must follow to hit this handler (email verification, magic
// link, password recovery). baseURL() is the app's own frontend origin; the
// Traefik IngressRoute in front of the Lambda only forwards paths under
// /api (stripping that prefix before the request reaches this process), so
// a bare "<baseURL>/auth/v1/..." link 404s at the frontend instead of
// reaching the auth handler. Do not use this for post-auth redirect_to
// targets — those must stay unprefixed since they land on the frontend
// itself, not this API.
func (h *AuthHandler) publicAuthBaseURL() string {
	return h.baseURL() + "/api"
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
	userID, err := h.authSvc.GetUserIDByEmail(ctx, req.Email)
	if err != nil || userID == "" {
		c.JSON(200, gin.H{})
		return
	}

	_ = h.authSvc.DeleteUserTokensByPurpose(ctx, userID, purpose)

	token := generateRandomToken()
	code := generateNumericCode(6)
	expiresAt := time.Now().Add(1 * time.Hour)
	if err := h.authSvc.CreateOTPCode(ctx, userID, token, code, req.Email, purpose, expiresAt.Unix()); err != nil {
		h.logger.Error("resend token insert failed", "error", err)
		c.JSON(200, gin.H{})
		return
	}
	if h.email != nil {
		go h.sendMagicLinkEmail(req.Email, token, code)
	}
	c.JSON(200, gin.H{})
}

// ---------- /reauthenticate ----------

func (h *AuthHandler) handleReauthenticate(c *gin.Context) {
	session := getSession(c)
	if session.UserID == "" {
		problemJSON(c, 401, "unauthorized", "Not authenticated")
		return
	}

	ctx := c.Request.Context()
	email, err := h.authSvc.GetUserEmail(ctx, session.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		problemJSON(c, 404, "not_found", "User not found")
		return
	}
	if email == "" {
		problemJSON(c, 422, "unprocessable", "User has no email for reauthentication")
		return
	}

	token := generateRandomToken()
	code := generateNumericCode(6)
	expiresAt := time.Now().Add(10 * time.Minute)
	if err := h.authSvc.CreateOTPCode(ctx, session.UserID, token, code, email, "reauthentication", expiresAt.Unix()); err != nil {
		h.logger.Error("reauthenticate token insert failed", "error", err)
		problemJSON(c, 500, "internal", "Failed to create reauthentication nonce")
		return
	}
	if h.email != nil {
		go h.sendMagicLinkEmail(email, token, code)
	}
	c.JSON(200, gin.H{})
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
	rows, err := h.authSvc.ListIdentities(ctx, session.UserID)
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

	cfg := h.cfg.Auth.OAuth[provider]
	prov, ok := adapterauth.OAuthRegistry(provider)
	if cfg == nil || !ok {
		problemJSON(c, 400, "bad_request", "Unsupported or unconfigured provider: "+provider)
		return
	}

	state := generateRandomToken()
	ctx := c.Request.Context()
	if err := h.authSvc.CreateOAuthFlowState(ctx, state, "", "", redirectTo, session.UserID); err != nil {
		problemJSON(c, 500, "internal", "Failed to store OAuth state")
		return
	}

	c.JSON(200, gin.H{"url": prov.AuthorizeURL(cfg, state)})
}

func (h *AuthHandler) handleUnlinkIdentity(c *gin.Context) {
	session := getSession(c)
	identityID := c.Param("id")
	ctx := c.Request.Context()

	// Ensure user keeps at least one auth method
	count, err := h.authSvc.CountIdentities(ctx, session.UserID)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to check identities")
		return
	}
	hasPassword, _ := h.authSvc.HasPassword(ctx, session.UserID)
	if count <= 1 && !hasPassword {
		problemJSON(c, 400, "bad_request", "Cannot unlink the only identity without a password set")
		return
	}

	if err := h.authSvc.DeleteIdentityByID(ctx, identityID, session.UserID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			problemJSON(c, 404, "not_found", "Identity not found")
			return
		}
		problemJSON(c, 500, "internal", "Failed to unlink identity")
		return
	}
	c.Status(200)
}

// ---------- admin: signOut + deleteFactor ----------

func (h *AuthHandler) handleAdminSignOut(c *gin.Context) {
	uid := c.Param("uid")
	ctx := c.Request.Context()
	_ = h.authSvc.RevokeAllUserSessions(ctx, uid)
	c.Status(204)
}

func (h *AuthHandler) handleAdminDeleteFactor(c *gin.Context) {
	uid := c.Param("uid")
	factorID := c.Param("factor_id")
	ctx := c.Request.Context()
	if err := h.authSvc.DeleteFactorForUser(ctx, factorID, uid); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			problemJSON(c, 404, "not_found", "Factor not found")
			return
		}
		problemJSON(c, 500, "internal", "Failed to delete factor")
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
