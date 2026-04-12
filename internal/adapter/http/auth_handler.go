package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/saedx1/ultrabase/internal/app"
	"github.com/saedx1/ultrabase/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

// AuthHandler serves GoTrue-compatible authentication endpoints under
// /auth/v1/*. Response shapes mirror Supabase's gotrue-js contract so that
// @supabase/supabase-js can drive ultrabase unmodified.
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
		db:      deps.DB,
		logger:  deps.Logger,
		email:   deps.Email,
		jwtKeys: deps.JWTKeys,
	}
}

// Mount registers the /auth/v1/* routes on the root router group.
func (h *AuthHandler) Mount(root *gin.RouterGroup) {
	auth := root.Group("/auth/v1")
	auth.POST("/signup", h.handleSignup)
	auth.POST("/token", h.handleToken)
	auth.GET("/user", jwtAuth(h.jwtKeys, true), h.handleGetUser)
	auth.PUT("/user", jwtAuth(h.jwtKeys, true), h.handleUpdateUser)
	auth.POST("/logout", jwtAuth(h.jwtKeys, true), h.handleLogout)
	auth.GET("/settings", h.handleSettings)

	if h.cfg.Auth.Email != nil {
		auth.POST("/recover", h.handleRecover)
		auth.POST("/verify", h.handleVerify)
		auth.GET("/verify", h.handleVerifyGET)
	}

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

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to hash password")
		return
	}

	userMetaJSON, _ := json.Marshal(req.Data)
	if len(userMetaJSON) == 0 {
		userMetaJSON = []byte("{}")
	}

	// Signup always sets raw_user_meta_data (JSONB). Custom auth.fields are
	// also promoted to top-level columns when present in `data`.
	cols := []string{"email", "password_hash", "raw_user_meta_data"}
	placeholders := []string{"$1", "$2", "$3::jsonb"}
	vals := []any{req.Email, string(hash), string(userMetaJSON)}
	argIdx := 4

	if h.cfg.Auth.Fields != nil && req.Data != nil {
		for name := range h.cfg.Auth.Fields {
			if val, ok := req.Data[name]; ok {
				cols = append(cols, name)
				placeholders = append(placeholders, fmt.Sprintf("$%d", argIdx))
				vals = append(vals, val)
				argIdx++
			}
		}
	}

	query := fmt.Sprintf(
		"INSERT INTO users (%s) VALUES (%s) RETURNING id::text, email, email_verified, email_confirmed_at, last_sign_in_at, raw_app_meta_data, raw_user_meta_data, created_at, updated_at",
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "))

	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx, query, vals...)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique") {
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
		 FROM users WHERE email = $1`, req.Email)
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
	_, _ = h.db.Exec(ctx, "UPDATE users SET last_sign_in_at = NOW(), updated_at = NOW() WHERE id = $1::uuid", userID)
	row["last_sign_in_at"] = time.Now().UTC()

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
		"SELECT user_id::text, expires_at FROM _refresh_tokens WHERE token = $1", req.RefreshToken)
	if err != nil || row == nil {
		problemJSON(c, 401, "invalid_grant", "Invalid refresh token")
		return
	}

	expiresAt, _ := row["expires_at"].(time.Time)
	if time.Now().After(expiresAt) {
		h.db.Exec(ctx, "DELETE FROM _refresh_tokens WHERE token = $1", req.RefreshToken)
		problemJSON(c, 401, "invalid_grant", "Refresh token expired")
		return
	}

	userID := asString(row["user_id"])

	// Rotation: each token is single-use.
	affected, _ := h.db.Exec(ctx, "DELETE FROM _refresh_tokens WHERE token = $1", req.RefreshToken)
	if affected == 0 {
		h.logger.Warn("refresh token reuse detected, revoking all tokens", "user_id", userID)
		h.db.Exec(ctx, "DELETE FROM _refresh_tokens WHERE user_id = $1::uuid", userID)
		problemJSON(c, 401, "invalid_grant", "Refresh token reuse detected. All sessions revoked.")
		return
	}

	userRow, err := h.db.QueryRow(ctx,
		`SELECT id::text, email, email_verified, email_confirmed_at, last_sign_in_at, raw_app_meta_data, raw_user_meta_data, created_at, updated_at
		 FROM users WHERE id = $1::uuid`, userID)
	if err != nil || userRow == nil {
		problemJSON(c, 401, "invalid_grant", "User not found")
		return
	}

	session, err := h.buildSession(ctx, userID, userRow)
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
		`SELECT id::text, email, email_verified, email_confirmed_at, last_sign_in_at, raw_app_meta_data, raw_user_meta_data, created_at, updated_at
		 FROM users WHERE id = $1::uuid`, session.UserID)
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
	}
	if req.Password != nil && *req.Password != "" {
		if len(*req.Password) < 8 {
			problemJSON(c, 400, "bad_request", "Password must be at least 8 characters")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to hash password")
			return
		}
		sets = append(sets, fmt.Sprintf("password_hash = $%d", argIdx))
		args = append(args, string(hash))
		argIdx++
	}
	if req.Data != nil {
		metaJSON, _ := json.Marshal(req.Data)
		sets = append(sets, fmt.Sprintf("raw_user_meta_data = raw_user_meta_data || $%d::jsonb", argIdx))
		args = append(args, string(metaJSON))
		argIdx++
	}

	args = append(args, session.UserID)
	query := fmt.Sprintf(
		`UPDATE users SET %s WHERE id = $%d::uuid
		 RETURNING id::text, email, email_verified, email_confirmed_at, last_sign_in_at, raw_app_meta_data, raw_user_meta_data, created_at, updated_at`,
		strings.Join(sets, ", "), argIdx)

	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx, query, args...)
	if err != nil || row == nil {
		if err != nil && (strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique")) {
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
		switch scope {
		case "local", "others":
			// GoTrue's "local" revokes only the current session; we don't
			// track session_id per refresh row yet, so fall through to
			// global for now. "others" means all except current — same.
			fallthrough
		case "global":
			h.db.Exec(ctx, "DELETE FROM _refresh_tokens WHERE user_id = $1::uuid", session.UserID)
		}
	}
	c.Status(204)
}

// ---------- /recover (password reset request) ----------

func (h *AuthHandler) handleRecover(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Invalid email")
		return
	}

	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx, "SELECT id::text FROM users WHERE email = $1", req.Email)
	if err == nil && row != nil {
		userID := asString(row["id"])
		token := generateRandomToken()
		expiresAt := time.Now().Add(1 * time.Hour)
		h.db.Exec(ctx,
			"INSERT INTO _auth_email_verifications (user_id, token, purpose, expires_at) VALUES ($1::uuid, $2, 'recovery', $3)",
			userID, token, expiresAt)
		if h.email != nil {
			go h.sendPasswordResetEmail(req.Email, token)
		}
	}
	// Always return 200 (email enumeration protection).
	c.Status(200)
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
	row, err := h.db.QueryRow(ctx,
		"SELECT user_id::text, purpose, expires_at FROM _auth_email_verifications WHERE token = $1",
		req.Token)
	if err != nil || row == nil {
		problemJSON(c, 401, "invalid_grant", "Invalid or expired token")
		return
	}
	expiresAt, _ := row["expires_at"].(time.Time)
	if time.Now().After(expiresAt) {
		h.db.Exec(ctx, "DELETE FROM _auth_email_verifications WHERE token = $1", req.Token)
		problemJSON(c, 401, "invalid_grant", "Token expired")
		return
	}

	userID := asString(row["user_id"])
	purpose, _ := row["purpose"].(string)

	// Side-effect based on purpose
	switch req.Type {
	case "signup", "email", "email_change":
		if purpose != "" && purpose != "signup" {
			problemJSON(c, 400, "bad_request", "Token purpose mismatch")
			return
		}
		h.db.Exec(ctx, "UPDATE users SET email_verified = true, email_confirmed_at = NOW(), updated_at = NOW() WHERE id = $1::uuid", userID)
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

	// Consume the token (single-use)
	h.db.Exec(ctx, "DELETE FROM _auth_email_verifications WHERE token = $1", req.Token)

	userRow, err := h.db.QueryRow(ctx,
		`SELECT id::text, email, email_verified, email_confirmed_at, last_sign_in_at, raw_app_meta_data, raw_user_meta_data, created_at, updated_at
		 FROM users WHERE id = $1::uuid`, userID)
	if err != nil || userRow == nil {
		problemJSON(c, 500, "internal", "User not found")
		return
	}

	session, err := h.buildSession(ctx, userID, userRow)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to generate token")
		return
	}
	c.JSON(200, session)
}

// handleVerifyGET handles a click on an email verification link. It
// performs the same token consumption as handleVerify but returns a plain
// text confirmation so users land on a readable page instead of JSON.
func (h *AuthHandler) handleVerifyGET(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		problemJSON(c, 400, "bad_request", "Missing token")
		return
	}
	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx,
		"SELECT user_id::text, purpose, expires_at FROM _auth_email_verifications WHERE token = $1",
		token)
	if err != nil || row == nil {
		c.String(400, "Invalid verification token")
		return
	}
	expiresAt, _ := row["expires_at"].(time.Time)
	if time.Now().After(expiresAt) {
		c.String(400, "Verification token expired")
		return
	}
	userID := asString(row["user_id"])
	h.db.Exec(ctx, "UPDATE users SET email_verified = true, email_confirmed_at = NOW(), updated_at = NOW() WHERE id = $1::uuid", userID)
	h.db.Exec(ctx, "DELETE FROM _auth_email_verifications WHERE token = $1", token)
	c.String(200, "Email verified successfully")
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
		"external":                   providers,
		"disable_signup":             false,
		"mailer_autoconfirm":         h.cfg.Auth.Email == nil || !h.cfg.Auth.Email.VerifyEmail,
		"phone_autoconfirm":          false,
		"sms_provider":               "",
		"mfa_enabled":                false,
		"saml_enabled":               false,
	})
}

// ---------- OAuth ----------

func (h *AuthHandler) handleAuthorize(c *gin.Context) {
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
	// Stash state + redirect_to so the callback can pick them up.
	c.SetCookie("oauth_state", state, 600, "/", "", false, true)
	c.SetCookie("oauth_redirect_to", redirectTo, 600, "/", "", false, true)

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
		savedState, _ := c.Cookie("oauth_state")
		if state == "" || state != savedState {
			problemJSON(c, 400, "bad_request", "Invalid OAuth state")
			return
		}
		redirectTo, _ := c.Cookie("oauth_redirect_to")

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

		ctx := c.Request.Context()

		row, _ := h.db.QueryRow(ctx,
			`SELECT id::text, email, email_verified, email_confirmed_at, last_sign_in_at, raw_app_meta_data, raw_user_meta_data, created_at, updated_at
			 FROM users WHERE email = $1`, userInfo.Email)
		var userID string

		if row == nil {
			metaJSON, _ := json.Marshal(map[string]any{
				"provider":      provider,
				"full_name":     userInfo.Name,
				"email":         userInfo.Email,
				"email_verified": true,
			})
			appMetaJSON, _ := json.Marshal(map[string]any{
				"provider":  provider,
				"providers": []string{provider},
			})
			newRow, err := h.db.QueryRow(ctx,
				`INSERT INTO users (email, email_verified, email_confirmed_at, raw_user_meta_data, raw_app_meta_data)
				 VALUES ($1, true, NOW(), $2::jsonb, $3::jsonb)
				 RETURNING id::text, email, email_verified, email_confirmed_at, last_sign_in_at, raw_app_meta_data, raw_user_meta_data, created_at, updated_at`,
				userInfo.Email, string(metaJSON), string(appMetaJSON))
			if err != nil {
				// Race: another request created the user between lookup and insert.
				row, err = h.db.QueryRow(ctx,
					`SELECT id::text, email, email_verified, email_confirmed_at, last_sign_in_at, raw_app_meta_data, raw_user_meta_data, created_at, updated_at
					 FROM users WHERE email = $1`, userInfo.Email)
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
			h.db.Exec(ctx, "UPDATE users SET email_verified = true, email_confirmed_at = COALESCE(email_confirmed_at, NOW()), last_sign_in_at = NOW(), updated_at = NOW() WHERE id = $1::uuid", userID)
		}

		h.db.Exec(ctx,
			`INSERT INTO _user_identities (user_id, provider, provider_user_id, email, last_sign_in_at, updated_at)
			 VALUES ($1::uuid, $2, $3, $4, NOW(), NOW())
			 ON CONFLICT (provider, provider_user_id)
			 DO UPDATE SET last_sign_in_at = EXCLUDED.last_sign_in_at, updated_at = EXCLUDED.updated_at`,
			userID, provider, userInfo.ProviderID, userInfo.Email)

		session, err := h.buildSession(ctx, userID, row)
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to generate token")
			return
		}

		// supabase-js's detectSessionInUrl reads the session from the URL
		// fragment. If the caller gave us a redirect_to, bounce there with
		// the tokens appended; otherwise return JSON as a fallback so API
		// callers aren't broken.
		if redirectTo != "" {
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
		"is_anonymous":  false,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["kid"] = key.KID
	tokenStr, err := token.SignedString(key.Secret)
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
		_, err := h.db.Exec(context.Background(),
			"INSERT INTO _refresh_tokens (user_id, token, expires_at) VALUES ($1::uuid, $2, $3)",
			userID, refreshToken, time.Now().Add(refreshExpiry))
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
		"role":                "authenticated",
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
// ultrabase instance with /auth/v1 appended, matching GoTrue's convention.
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
		"INSERT INTO _auth_email_verifications (user_id, token, purpose, expires_at) VALUES ($1::uuid, $2, 'signup', $3)",
		userID, token, expiresAt)

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
		Subject: subject,
		HTML:    body,
		Text:    body,
	}); err != nil {
		h.logger.Error("failed to send verification email", "email", email, "error", err)
	}
}

func (h *AuthHandler) sendPasswordResetEmail(email, token string) {
	subject := "Reset your password"
	body := fmt.Sprintf("Reset your password using this token: %s", token)

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
					"link":     fmt.Sprintf("%s/reset-password?token=%s", h.baseURL(), token),
				})
			}
		}
	}

	ctx := context.Background()
	if err := h.email.Send(ctx, domain.EmailMessage{
		To:      []string{email},
		Subject: subject,
		HTML:    body,
		Text:    body,
	}); err != nil {
		h.logger.Error("failed to send password reset email", "email", email, "error", err)
	}
}

func (h *AuthHandler) baseURL() string {
	if base := os.Getenv("ULTRABASE_BASE_URL"); base != "" {
		return strings.TrimRight(base, "/")
	}
	return fmt.Sprintf("http://localhost:%d", h.cfg.Server.Port)
}

func renderAuthTemplate(tmpl string, vars map[string]string) string {
	result := tmpl
	for k, v := range vars {
		result = strings.ReplaceAll(result, "{{"+k+"}}", v)
	}
	return result
}

func generateRandomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
