package domain

import "context"

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

// SessionMeta carries request-scoped metadata persisted on a refresh token row.
type SessionMeta struct {
	SessionID string
	IP        string
	UserAgent string
}

// OTPRow is a consumed/validated one-time token; the handler inspects Purpose
// to branch verify side-effects, then re-fetches the user row.
type OTPRow struct {
	UserID  string
	Purpose string
}

// FlowState is the OAuth/PKCE flow_state row read by the OAuth callback.
type FlowState struct {
	CodeChallenge       string
	CodeChallengeMethod string
	RedirectTo          string
	LinkingUserID       string
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
	// ConsumeRefreshToken validates + rotates a refresh token. It returns the
	// user row for a valid, single-use token. Errors: ErrRefreshExpired,
	// ErrRefreshReuse (all sessions revoked), ErrUnauthorized.
	ConsumeRefreshToken(ctx context.Context, token string) (userRow map[string]any, err error)
	// RevokeSessionByID deletes refresh tokens for a single session.
	RevokeSessionByID(ctx context.Context, sessionID string) error
	// RevokeOtherSessions deletes the user's refresh tokens except keepSessionID.
	RevokeOtherSessions(ctx context.Context, userID, keepSessionID string) error
	// RevokeAllUserSessions deletes every refresh token for the user.
	RevokeAllUserSessions(ctx context.Context, userID string) error

	// ---- one-time tokens (recovery / signup / magiclink / reauth) ----
	// CreateOneTimeToken inserts an opaque token (no numeric code).
	CreateOneTimeToken(ctx context.Context, userID, token, purpose string, expiresAt int64) error
	// CreateOTPCode inserts a token + 6-digit code keyed on email.
	CreateOTPCode(ctx context.Context, userID, token, code, email, purpose string, expiresAt int64) error
	// DeleteUserTokensByPurpose clears outstanding tokens for a purpose (resend).
	DeleteUserTokensByPurpose(ctx context.Context, userID, purpose string) error

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
	// UpsertOAuthUser finds-or-creates a user by email for an OAuth/OIDC login,
	// marks the email verified, bumps sign-in, and upserts the identity row.
	// Returns the user row.
	UpsertOAuthUser(ctx context.Context, provider, providerUserID, email, name string) (map[string]any, error)
	// LinkIdentity adds an identity to an existing user (best-effort).
	LinkIdentity(ctx context.Context, userID, provider, providerUserID, email string)

	// ---- identity management ----
	ListIdentities(ctx context.Context, userID string) ([]map[string]any, error)
	// CountIdentities returns the number of linked identities for a user.
	CountIdentities(ctx context.Context, userID string) (int, error)
	// DeleteIdentityByID removes one identity owned by the user. Returns
	// ErrNotFound if no row matched.
	DeleteIdentityByID(ctx context.Context, identityID, userID string) error

	// ---- MFA (kept only where mfa_handler already matches; migration TBD) ----
	DeleteFactorForUser(ctx context.Context, factorID, userID string) error
}
