package http

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
)

// MountMFA registers /auth/v1/factors/* endpoints. TOTP is the only
// factor type currently supported; the shape mirrors GoTrue so supabase-js
// auth.mfa.{enroll,challenge,verify,unenroll,listFactors} works unchanged.
func (h *AuthHandler) MountMFA(auth *gin.RouterGroup) {
	factors := auth.Group("/factors", jwtAuth(h.jwtKeys, true))
	factors.GET("", h.handleListFactors)
	factors.POST("", h.handleEnrollFactor)
	factors.DELETE("/:factor_id", h.handleUnenrollFactor)
	factors.POST("/:factor_id/challenge", h.handleChallengeFactor)
	factors.POST("/:factor_id/verify", h.handleVerifyFactor)
}

// handleEnrollFactor creates an unverified TOTP factor, returning the
// shared secret + otpauth URI so the caller can render a QR code. Until
// verify succeeds the factor is marked 'unverified' and does not change
// the session's AAL.
func (h *AuthHandler) handleEnrollFactor(c *gin.Context) {
	session := getSession(c)
	var req struct {
		FactorType   string `json:"factor_type"`
		FriendlyName string `json:"friendly_name"`
		Issuer       string `json:"issuer"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Invalid enroll request")
		return
	}
	if req.FactorType == "" {
		req.FactorType = "totp"
	}
	if req.FactorType != "totp" {
		problemJSON(c, 400, "bad_request", "Unsupported factor_type: "+req.FactorType)
		return
	}

	issuer := req.Issuer
	if issuer == "" {
		issuer = "ultrabase"
	}
	accountName := session.Email
	if accountName == "" {
		accountName = session.UserID
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
	})
	if err != nil {
		h.logger.Error("totp generate failed", "error", err)
		problemJSON(c, 500, "internal", "Failed to generate TOTP secret")
		return
	}

	ctx := c.Request.Context()
	row, err := h.db.QueryRow(ctx,
		`INSERT INTO auth.mfa_factors (user_id, friendly_name, factor_type, status, secret)
		 VALUES ($1::uuid, $2, 'totp', 'unverified', $3)
		 RETURNING id::text, friendly_name, factor_type, status, created_at, updated_at`,
		session.UserID, req.FriendlyName, key.Secret())
	if err != nil || row == nil {
		h.logger.Error("mfa enroll insert failed", "error", err)
		problemJSON(c, 500, "internal", "Failed to enroll factor")
		return
	}

	c.JSON(200, gin.H{
		"id":            asString(row["id"]),
		"type":          "totp",
		"friendly_name": req.FriendlyName,
		"totp": gin.H{
			"qr_code": key.URL(),
			"secret":  key.Secret(),
			"uri":     key.URL(),
		},
	})
}

// handleChallengeFactor creates a challenge row the caller will consume
// via handleVerifyFactor. Challenges live for 5 minutes.
func (h *AuthHandler) handleChallengeFactor(c *gin.Context) {
	session := getSession(c)
	factorID := c.Param("factor_id")
	if factorID == "" {
		problemJSON(c, 400, "bad_request", "Missing factor_id")
		return
	}
	ctx := c.Request.Context()

	// Ownership check: factor must belong to the caller.
	owner, err := h.db.QueryRow(ctx,
		"SELECT user_id::text FROM auth.mfa_factors WHERE id = $1::uuid", factorID)
	if err != nil || owner == nil || asString(owner["user_id"]) != session.UserID {
		problemJSON(c, 404, "not_found", "Factor not found")
		return
	}

	row, err := h.db.QueryRow(ctx,
		`INSERT INTO auth.mfa_challenges (factor_id) VALUES ($1::uuid)
		 RETURNING id::text, created_at`,
		factorID)
	if err != nil || row == nil {
		problemJSON(c, 500, "internal", "Failed to create challenge")
		return
	}
	createdAt, _ := row["created_at"].(time.Time)
	c.JSON(200, gin.H{
		"id":         asString(row["id"]),
		"type":       "totp",
		"expires_at": createdAt.Add(5 * time.Minute).Unix(),
	})
}

// handleVerifyFactor checks the TOTP code against the stored secret. On
// success: the factor flips to 'verified' (if first-time enrollment) and
// a fresh session is issued with aal=aal2 in app_metadata.
func (h *AuthHandler) handleVerifyFactor(c *gin.Context) {
	session := getSession(c)
	factorID := c.Param("factor_id")
	var req struct {
		ChallengeID string `json:"challenge_id"`
		Code        string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Invalid verify request")
		return
	}
	ctx := c.Request.Context()

	factorRow, err := h.db.QueryRow(ctx,
		"SELECT user_id::text, secret, status FROM auth.mfa_factors WHERE id = $1::uuid",
		factorID)
	if err != nil || factorRow == nil || asString(factorRow["user_id"]) != session.UserID {
		problemJSON(c, 404, "not_found", "Factor not found")
		return
	}
	secret, _ := factorRow["secret"].(string)
	status, _ := factorRow["status"].(string)

	// Challenge must exist, belong to this factor, and be unverified.
	if req.ChallengeID != "" {
		ch, err := h.db.QueryRow(ctx,
			"SELECT factor_id::text, verified_at, created_at FROM auth.mfa_challenges WHERE id = $1::uuid",
			req.ChallengeID)
		if err != nil || ch == nil || asString(ch["factor_id"]) != factorID {
			problemJSON(c, 404, "not_found", "Challenge not found")
			return
		}
		if _, verified := ch["verified_at"].(time.Time); verified {
			problemJSON(c, 400, "bad_request", "Challenge already verified")
			return
		}
		createdAt, _ := ch["created_at"].(time.Time)
		if time.Since(createdAt) > 5*time.Minute {
			problemJSON(c, 401, "expired", "Challenge expired")
			return
		}
	}

	if !totp.Validate(req.Code, secret) {
		problemJSON(c, 401, "invalid_code", "Invalid TOTP code")
		return
	}

	if req.ChallengeID != "" {
		h.db.Exec(ctx, "UPDATE auth.mfa_challenges SET verified_at = NOW() WHERE id = $1::uuid", req.ChallengeID)
	}
	if status == "unverified" {
		h.db.Exec(ctx, "UPDATE auth.mfa_factors SET status = 'verified', updated_at = NOW() WHERE id = $1::uuid", factorID)
	}

	userRow, err := h.db.QueryRow(ctx,
		`SELECT id::text, email, email_verified, email_confirmed_at, last_sign_in_at, raw_app_meta_data, raw_user_meta_data, created_at, updated_at
		 FROM auth.users WHERE id = $1::uuid`, session.UserID)
	if err != nil || userRow == nil {
		problemJSON(c, 500, "internal", "User not found")
		return
	}
	// Flag aal=aal2 in raw_app_meta_data so buildSession's JWT claim
	// reflects the elevated assurance level. This does NOT persist the
	// change — AAL is per-session.
	appMeta := decodeJSONB(userRow["raw_app_meta_data"])
	if appMeta == nil {
		appMeta = map[string]any{}
	}
	appMeta["aal"] = "aal2"
	userRow["raw_app_meta_data"] = appMeta

	sess, err := h.buildSession(ctx, session.UserID, userRow)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to generate token")
		return
	}
	c.JSON(200, sess)
}

// handleUnenrollFactor deletes a factor row. Challenges cascade via the
// FK. No session reissue — the caller keeps the current token until it
// expires.
func (h *AuthHandler) handleUnenrollFactor(c *gin.Context) {
	session := getSession(c)
	factorID := c.Param("factor_id")
	ctx := c.Request.Context()

	n, err := h.db.Exec(ctx,
		"DELETE FROM auth.mfa_factors WHERE id = $1::uuid AND user_id = $2::uuid",
		factorID, session.UserID)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to unenroll factor")
		return
	}
	if n == 0 {
		problemJSON(c, 404, "not_found", "Factor not found")
		return
	}
	c.JSON(200, gin.H{"id": factorID})
}

// handleListFactors returns all factors belonging to the caller. The
// secret column is intentionally excluded; once enrolled the shared
// secret is not retrievable.
func (h *AuthHandler) handleListFactors(c *gin.Context) {
	session := getSession(c)
	ctx := c.Request.Context()

	rows, err := h.db.Query(ctx,
		`SELECT id::text, friendly_name, factor_type, status, created_at, updated_at
		 FROM auth.mfa_factors WHERE user_id = $1::uuid ORDER BY created_at ASC`,
		session.UserID)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to list factors")
		return
	}
	// GoTrue returns {all: [...], totp: [...], phone: [...]} — supabase-js
	// reads `all` to render the full list regardless of type.
	all := []any{}
	totp := []any{}
	phone := []any{}
	for _, r := range rows {
		typeName, _ := r["factor_type"].(string)
		entry := gin.H{
			"id":            asString(r["id"]),
			"type":          typeName,
			"friendly_name": r["friendly_name"],
			"status":        r["status"],
			"created_at":    asTimeString(r["created_at"]),
			"updated_at":    asTimeString(r["updated_at"]),
		}
		all = append(all, entry)
		switch typeName {
		case "totp":
			totp = append(totp, entry)
		case "phone":
			phone = append(phone, entry)
		}
	}
	c.JSON(200, gin.H{"all": all, "totp": totp, "phone": phone})
}
