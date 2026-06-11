package domain

import "context"

// CreateUserParams holds fields for auth user creation.
type CreateUserParams struct {
	Email          string
	Phone          string
	Password       string // bcrypt hash; empty = no password auth
	AppMetadata    map[string]any
	UserMetadata   map[string]any
	EmailConfirmed bool
	PhoneConfirmed bool
}

// UpdateUserParams holds fields for auth user updates. Nil pointer = no change.
type UpdateUserParams struct {
	Email          *string
	Phone          *string
	Password       *string // bcrypt hash; nil = no change
	AppMetadata    map[string]any
	UserMetadata   map[string]any
	EmailConfirmed *bool
	Banned         *bool
}

// AuthService is the port for all authentication data operations.
// The raw-map return type mirrors auth.users rows so AuthHandler.buildUser()
// can format them for the Supabase-compatible wire response unchanged.
type AuthService interface {
	// User lifecycle — return the raw auth.users row
	CreateUser(ctx context.Context, p CreateUserParams) (map[string]any, error)
	GetUserByID(ctx context.Context, id string) (map[string]any, error)
	GetUserByEmail(ctx context.Context, email string) (map[string]any, error)
	GetUserByPhone(ctx context.Context, phone string) (map[string]any, error)
	UpdateUser(ctx context.Context, id string, p UpdateUserParams) (map[string]any, error)
	DeleteUser(ctx context.Context, id string) error
	ListUsers(ctx context.Context, page, perPage int) ([]map[string]any, int, error)

	// Password
	VerifyPassword(ctx context.Context, email, password string) (map[string]any, error) // returns user row if valid
	SetPassword(ctx context.Context, userID, bcryptHash string) error

	// Sessions & tokens
	CreateSession(ctx context.Context, userID string) (sessionID, refreshToken string, err error)
	VerifyRefreshToken(ctx context.Context, token string) (userRow map[string]any, sessionID string, err error)
	RevokeSession(ctx context.Context, sessionID string) error
	RevokeAllUserSessions(ctx context.Context, userID string) error

	// OTP / magic link / verification codes
	CreateOTPCode(ctx context.Context, userID, kind string) (token, code string, err error)
	VerifyOTPToken(ctx context.Context, token, kind string) (map[string]any, error)
	VerifyOTPCode(ctx context.Context, userID, kind, code string) error

	// PKCE flow
	CreateFlowState(ctx context.Context, provider, codeChallenge, codeChallengeMethod string) (authCode string, err error)
	GetFlowState(ctx context.Context, authCode string) (codeChallenge, method, userID string, err error)
	DeleteFlowState(ctx context.Context, authCode string) error

	// Identity linking
	GetOrCreateIdentity(ctx context.Context, provider, providerID string, userMeta map[string]any) (userRow map[string]any, created bool, err error)
	ListIdentities(ctx context.Context, userID string) ([]map[string]any, error)
	DeleteIdentity(ctx context.Context, userID, provider string) error

	// MFA
	CreateFactor(ctx context.Context, userID, factorType, friendlyName string) (map[string]any, error)
	VerifyFactor(ctx context.Context, factorID, code string) error
	DeleteFactor(ctx context.Context, factorID string) error
	ListFactors(ctx context.Context, userID string) ([]map[string]any, error)

	// Audit
	RecordSignIn(ctx context.Context, userID string)
}
