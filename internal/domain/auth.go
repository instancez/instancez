package domain

import (
	"context"
	"encoding/json"
	"time"
)

// CreateUserParams holds fields for auth user creation.
type CreateUserParams struct {
	Email          string
	Password       string // bcrypt hash; empty = no password auth
	AppMetadata    map[string]any
	UserMetadata   map[string]any
	EmailConfirmed bool
	Anonymous      bool   // sets is_anonymous = true
	BanDuration    string // Postgres interval string; "" or "none" = not banned
}

// UpdateUserParams holds fields for auth user updates. Nil pointer = no change.
type UpdateUserParams struct {
	Email          *string
	Password       *string // bcrypt hash; nil = no change
	AppMetadata    map[string]any
	UserMetadata   map[string]any
	EmailConfirmed *bool
	Banned         *bool
	// BanDuration mirrors GoTrue's admin ban_duration. "none" clears the ban
	// (banned_until = NULL); any other value sets banned_until = NOW() +
	// interval. Nil = no change. Distinct from the permanent Banned flag.
	BanDuration *string
	// ClearEmailVerified, when true, sets email_verified = false and
	// email_confirmed_at = NULL (used when the user swaps in a new, unproven
	// email address). Mutually informative with Email.
	ClearEmailVerified bool
}

// AMREntry is one entry of the Supabase JWT amr claim.
type AMREntry struct {
	Method    string `json:"method"`
	Timestamp int64  `json:"timestamp"`
}

// SessionMeta is a session's state, persisted on each refresh-token row.
type SessionMeta struct {
	SessionID string
	IP        string
	UserAgent string
	AAL       string
	AMR       []AMREntry
}

// Normalize defaults AAL to aal1 and AMR to an empty slice when unset.
func (m SessionMeta) Normalize() SessionMeta {
	if m.AAL == "" {
		m.AAL = "aal1"
	}
	if m.AMR == nil {
		m.AMR = []AMREntry{}
	}
	return m
}

// RefreshRotation is the child token a refresh grant mints when it rotates.
type RefreshRotation struct {
	Token     string
	ExpiresAt int64
	IP        string
	UserAgent string
}

// ParseAMR decodes an amr value from a JSONB column or a decoded JWT claim.
func ParseAMR(v any) []AMREntry {
	var b []byte
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		b = x
	case string:
		b = []byte(x)
	default:
		b, _ = json.Marshal(x)
	}
	var out []AMREntry
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

// OTPRow is a consumed/validated one-time token; the handler inspects Purpose
// to branch verify side-effects, then re-fetches the user row.
type OTPRow struct {
	UserID  string
	Purpose string
}

// OAuthLogin is one external-provider sign-in handed to UpsertOAuthUser.
type OAuthLogin struct {
	Provider, ProviderUserID, Email, Name string
	EmailVerified                         bool // provider asserts the address is verified
	AllowSignup                           bool // create a user when nothing matches
}

// FlowState is the OAuth/PKCE flow_state row read by the OAuth callback.
type FlowState struct {
	CodeChallenge       string
	CodeChallengeMethod string
	RedirectTo          string
	LinkingUserID       string
}

// MFAFactor is the secret + status of a TOTP factor, read for verification.
// The handler validates the TOTP code (a CPU operation, not a query) against
// Secret; Status drives the one-time promotion to 'verified'.
type MFAFactor struct {
	Secret string
	Status string
}

// AuthService is the port for all authentication data operations.
// The raw-map return type mirrors auth.users rows so AuthHandler.buildUser()
// can format them for the Supabase-compatible wire response unchanged.
type AuthService interface {
	// ---- user lifecycle (return the raw auth.users row) ----
	CreateUser(ctx context.Context, p CreateUserParams) (map[string]any, error)
	GetUserByID(ctx context.Context, id string) (map[string]any, error)
	GetUserByEmail(ctx context.Context, email string) (map[string]any, error)
	GetUserIDByEmail(ctx context.Context, email string) (string, error)
	UpdateUser(ctx context.Context, id string, p UpdateUserParams) (map[string]any, error)
	DeleteUser(ctx context.Context, id string) error
	ListUsers(ctx context.Context, page, perPage int) ([]map[string]any, int, error)

	// ---- password ----
	// VerifyPassword returns the user row when credentials are valid.
	// Errors: ErrUnauthorized (no user / bad password), ErrOAuthOnlyAccount.
	VerifyPassword(ctx context.Context, email, password string) (map[string]any, error)

	// GetUserEmail returns the user's email, or ErrNotFound.
	GetUserEmail(ctx context.Context, userID string) (string, error)
	// HasPassword reports whether the user has a password_hash set.
	HasPassword(ctx context.Context, userID string) (bool, error)

	// ---- sign-in audit ----
	// RecordSignIn bumps last_sign_in_at/updated_at (fire-and-forget).
	RecordSignIn(ctx context.Context, userID string)

	// ---- sessions / refresh tokens ----
	// InsertRefreshToken persists a refresh token row carrying request meta.
	InsertRefreshToken(ctx context.Context, userID, token string, meta SessionMeta, expiresAt int64) error
	// ConsumeRefreshToken rotates the token (a replay in the reuse interval gets the current child).
	ConsumeRefreshToken(ctx context.Context, token string, next RefreshRotation) (userRow map[string]any, meta SessionMeta, refreshToken string, err error)
	// RevokeSessionByID deletes refresh tokens for a single session.
	RevokeSessionByID(ctx context.Context, sessionID string) error
	// RevokeOtherSessions deletes the user's refresh tokens except keepSessionID.
	RevokeOtherSessions(ctx context.Context, userID, keepSessionID string) error
	// RevokeBelowAAL2 deletes the user's sub-aal2 refresh tokens, in sessionID only unless allSessions.
	RevokeBelowAAL2(ctx context.Context, userID, sessionID string, allSessions bool) error
	// RevokeAllUserSessions deletes every refresh token for the user.
	RevokeAllUserSessions(ctx context.Context, userID string) error

	// ---- one-time tokens (recovery / signup / magiclink / reauth) ----
	// CreateOneTimeToken inserts an opaque token (no numeric code).
	CreateOneTimeToken(ctx context.Context, userID, token, purpose string, expiresAt int64) error
	// CreateOTPCode inserts a token + 6-digit code keyed on email.
	CreateOTPCode(ctx context.Context, userID, token, code, email, purpose string, expiresAt int64) error
	// DeleteUserTokensByPurpose clears outstanding tokens for a purpose (resend).
	DeleteUserTokensByPurpose(ctx context.Context, userID, purpose string) error
	// RecentOTPSent reports whether a token for (userID, purpose) was issued within the window.
	RecentOTPSent(ctx context.Context, userID, purpose string, within time.Duration) (bool, error)

	// VerifyOTP consumes a one-time token for POST /verify. It handles both the
	// numeric-code (email + 6 digits) and opaque-token flows, including attempt
	// tracking, expiry, and single-use deletion. allowedPurposes nil/empty means
	// any purpose is accepted (caller enforces). Returns the consumed row.
	// Errors: ErrTokenExpired, ErrInvalidToken, ErrPurposeMismatch.
	VerifyOTP(ctx context.Context, token, email string, allowedPurposes []string) (OTPRow, error)
	// PeekOneTimeToken reads (and on expiry deletes) an opaque token for the
	// GET /verify link-click flow without consuming it. Returns the row;
	// caller consumes via DeleteOneTimeToken. Errors: ErrInvalidToken, ErrTokenExpired.
	PeekOneTimeToken(ctx context.Context, token string) (OTPRow, error)
	// DeleteOneTimeToken removes a token by its canonical token column.
	// Errors: ErrInvalidToken when already consumed.
	DeleteOneTimeToken(ctx context.Context, token string) error

	// MarkEmailVerified sets email_verified = true and confirms the address.
	MarkEmailVerified(ctx context.Context, userID string)

	// ---- PKCE flow ----
	GetPKCEFlowState(ctx context.Context, authCode string) (codeChallenge, method, userID string, err error)
	DeletePKCEFlowState(ctx context.Context, authCode string) error
	CreatePKCEFlowState(ctx context.Context, authCode, userID, codeChallenge, method string) error

	// ---- OAuth flow state ----
	CreateOAuthFlowState(ctx context.Context, state, codeChallenge, method, redirectTo, linkingUserID string) error
	ConsumeOAuthFlowState(ctx context.Context, state string) (FlowState, error)

	// ---- OAuth / ID-token user provisioning ----
	// UpsertOAuthUser resolves an OAuth login by identity, then verified email, then signup.
	UpsertOAuthUser(ctx context.Context, in OAuthLogin) (map[string]any, error)
	// LinkIdentity adds an identity to an existing user (best-effort).
	LinkIdentity(ctx context.Context, userID, provider, providerUserID, email string)

	// ---- identity management ----
	ListIdentities(ctx context.Context, userID string) ([]map[string]any, error)
	// CountIdentities returns the number of linked identities for a user.
	CountIdentities(ctx context.Context, userID string) (int, error)
	// DeleteIdentityByID removes one identity owned by the user. Returns
	// ErrNotFound if no row matched.
	DeleteIdentityByID(ctx context.Context, identityID, userID string) error

	// ---- MFA ----
	// EnrollFactor inserts an unverified TOTP factor with the given shared
	// secret and returns the new factor id. The handler generates the secret
	// and otpauth URI; the service only persists.
	EnrollFactor(ctx context.Context, userID, friendlyName, secret string) (factorID string, err error)
	// CreateChallenge inserts a challenge for the user's factor, rate-limited per factor.
	CreateChallenge(ctx context.Context, factorID, userID string) (challengeID string, createdAt time.Time, err error)
	// GetFactorForVerify returns the factor's secret + status when it belongs
	// to userID, so the handler can validate the TOTP code. Errors: ErrNotFound.
	GetFactorForVerify(ctx context.Context, factorID, userID string) (MFAFactor, error)
	// ValidateChallenge atomically spends one attempt on a live, unverified challenge.
	ValidateChallenge(ctx context.Context, challengeID, factorID string) error
	// ConsumeTOTPStep marks a TOTP step used; fresh=false means a replayed code.
	ConsumeTOTPStep(ctx context.Context, factorID string, step int64) (fresh bool, err error)
	// MarkChallengeVerified stamps verified_at, or returns ErrChallengeUsed.
	MarkChallengeVerified(ctx context.Context, challengeID string) error
	// PromoteFactorToVerified verifies the factor and deletes the user's other unverified ones.
	PromoteFactorToVerified(ctx context.Context, factorID string) error
	// ListFactors returns the caller's factors (secret excluded) ordered by
	// created_at, for the GoTrue listFactors response.
	ListFactors(ctx context.Context, userID string) ([]map[string]any, error)
	// DeleteFactorForUser deletes a factor, verified ones only when allowVerified.
	DeleteFactorForUser(ctx context.Context, factorID, userID string, allowVerified bool) error
}
