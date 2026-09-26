package http

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/instancez/instancez/internal/domain"
)

// MountMFA registers /auth/v1/factors/* endpoints. TOTP is the only
// factor type currently supported; the shape mirrors GoTrue so supabase-js
// auth.mfa.{enroll,challenge,verify,unenroll,listFactors} works unchanged.
func (h *AuthHandler) MountMFA(auth *gin.RouterGroup) {
	factors := auth.Group("/factors", apiKeyGuard(h.jwtKeys), jwtAuth(h.jwtKeys, true))
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
	ctx := c.Request.Context()
	statuses, err := h.factorStatuses(ctx, session.UserID)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to enroll factor")
		return
	}
	if sessionAAL(session) != "aal2" && hasVerified(statuses) {
		problemJSON(c, 403, "insufficient_aal", "AAL2 required to enroll a new factor")
		return
	}

	issuer := req.Issuer
	if issuer == "" {
		issuer = "instancez"
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

	factorID, err := h.authSvc.EnrollFactor(ctx, session.UserID, req.FriendlyName, key.Secret())
	if err != nil {
		h.logger.Error("mfa enroll insert failed", "error", err)
		problemJSON(c, 500, "internal", "Failed to enroll factor")
		return
	}

	c.JSON(200, gin.H{
		"id":            factorID,
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

	challengeID, createdAt, err := h.authSvc.CreateChallenge(ctx, factorID, session.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		problemJSON(c, 404, "not_found", "Factor not found")
		return
	}
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to create challenge")
		return
	}
	c.JSON(200, gin.H{
		"id":         challengeID,
		"type":       "totp",
		"expires_at": createdAt.Add(5 * time.Minute).Unix(),
	})
}

// handleVerifyFactor checks a TOTP code against a challenge and, on success,
// reissues the caller's session at aal2 (top-level aal/amr claims).
func (h *AuthHandler) handleVerifyFactor(c *gin.Context) {
	session := getSession(c)
	factorID := c.Param("factor_id")
	var req struct {
		ChallengeID string `json:"challenge_id" binding:"required"`
		Code        string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		problemJSON(c, 400, "bad_request", "Invalid verify request")
		return
	}
	ctx := c.Request.Context()

	factor, err := h.authSvc.GetFactorForVerify(ctx, factorID, session.UserID)
	if err != nil {
		problemJSON(c, 404, "not_found", "Factor not found")
		return
	}
	// A factor planted before the enroll gate existed must not self-elevate past a verified one.
	if factor.Status == "unverified" && sessionAAL(session) != "aal2" {
		statuses, err := h.factorStatuses(ctx, session.UserID)
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to verify factor")
			return
		}
		if hasVerified(statuses) {
			problemJSON(c, 403, "insufficient_aal", "AAL2 required to verify a new factor")
			return
		}
	}

	switch err := h.authSvc.ValidateChallenge(ctx, req.ChallengeID, factorID); {
	case errors.Is(err, domain.ErrChallengeUsed):
		problemJSON(c, 400, "bad_request", "Challenge already verified")
		return
	case errors.Is(err, domain.ErrChallengeExpired):
		problemJSON(c, 401, "expired", "Challenge expired")
		return
	case errors.Is(err, domain.ErrChallengeTooManyAttempts):
		problemJSON(c, 429, "too_many_attempts", "Too many verification attempts")
		return
	case err != nil:
		problemJSON(c, 404, "not_found", "Challenge not found")
		return
	}

	step, ok := matchTOTPStep(req.Code, factor.Secret, time.Now())
	if !ok {
		problemJSON(c, 401, "invalid_code", "Invalid TOTP code")
		return
	}
	fresh, err := h.authSvc.ConsumeTOTPStep(ctx, factorID, step)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to verify factor")
		return
	}
	if !fresh {
		problemJSON(c, 401, "invalid_code", "Invalid TOTP code")
		return
	}
	if err := h.authSvc.MarkChallengeVerified(ctx, req.ChallengeID); err != nil {
		if errors.Is(err, domain.ErrChallengeUsed) {
			problemJSON(c, 400, "bad_request", "Challenge already verified")
			return
		}
		problemJSON(c, 500, "internal", "Failed to mark challenge verified")
		return
	}
	firstVerify := factor.Status == "unverified"
	if firstVerify {
		if err := h.authSvc.PromoteFactorToVerified(ctx, factorID); err != nil {
			problemJSON(c, 500, "internal", "Failed to verify factor")
			return
		}
	}

	userRow, err := h.authSvc.GetUserByID(ctx, session.UserID)
	if err != nil || userRow == nil {
		problemJSON(c, 500, "internal", "User not found")
		return
	}
	claims := jwtClaims(session.JWT)
	sid, _ := claims["session_id"].(string)
	meta := domain.SessionMeta{
		SessionID: sid,
		AAL:       "aal2",
		AMR:       addAMR(domain.ParseAMR(claims["amr"]), domain.AMREntry{Method: "totp", Timestamp: time.Now().Unix()}),
	}
	sess, err := h.issueSession(ctxWithRequestMeta(ctx, c), session.UserID, userRow, meta)
	if err != nil {
		sessionError(c, err)
		return
	}
	// A new factor drops every aal1 session, like GoTrue; a step-up drops this session's aal1 token.
	if err := h.authSvc.RevokeBelowAAL2(ctx, session.UserID, sid, firstVerify); err != nil {
		h.logger.Error("revoke aal1 sessions after mfa verify failed", "error", err)
	}
	c.JSON(200, sess)
}

// matchTOTPStep returns the 30s step code matches within ±1 step of now.
func matchTOTPStep(code, secret string, now time.Time) (int64, bool) {
	if len(code) != 6 || secret == "" {
		return 0, false
	}
	for _, off := range []time.Duration{0, -30 * time.Second, 30 * time.Second} {
		at := now.Add(off)
		want, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err == nil && constantTimeEqual(want, code) {
			return at.Unix() / 30, true
		}
	}
	return 0, false
}

func sessionAAL(s domain.Session) string {
	if aal, _ := jwtClaims(s.JWT)["aal"].(string); aal != "" {
		return aal
	}
	return "aal1"
}

func (h *AuthHandler) factorStatuses(ctx context.Context, userID string) (map[string]string, error) {
	rows, err := h.authSvc.ListFactors(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[asString(r["id"])] = asString(r["status"])
	}
	return out, nil
}

func hasVerified(statuses map[string]string) bool {
	return slices.Contains(slices.Collect(maps.Values(statuses)), "verified")
}

// addAMR records e and keeps one entry per method, newest first.
func addAMR(amr []domain.AMREntry, e domain.AMREntry) []domain.AMREntry {
	all := append([]domain.AMREntry{e}, amr...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Timestamp > all[j].Timestamp })
	seen := map[string]bool{}
	out := all[:0]
	for _, a := range all {
		if !seen[a.Method] {
			seen[a.Method] = true
			out = append(out, a)
		}
	}
	return out
}

// handleUnenrollFactor deletes a factor row. Challenges cascade via the
// FK. No session reissue — the caller keeps the current token until it
// expires.
func (h *AuthHandler) handleUnenrollFactor(c *gin.Context) {
	session := getSession(c)
	factorID := c.Param("factor_id")
	ctx := c.Request.Context()

	statuses, err := h.factorStatuses(ctx, session.UserID)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to unenroll factor")
		return
	}
	if statuses[factorID] == "verified" && sessionAAL(session) != "aal2" {
		problemJSON(c, 422, "insufficient_aal", "AAL2 required to unenroll verified factor")
		return
	}
	err = h.authSvc.DeleteFactorForUser(ctx, factorID, session.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		problemJSON(c, 404, "not_found", "Factor not found")
		return
	}
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to unenroll factor")
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

	rows, err := h.authSvc.ListFactors(ctx, session.UserID)
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
