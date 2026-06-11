// Package auth implements the domain.AuthService port using Postgres.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/instancez/instancez/internal/domain"
)

// Compile-time interface check.
var _ domain.AuthService = (*Service)(nil)

// Service implements domain.AuthService via direct Postgres queries.
type Service struct {
	db     domain.Database
	cfg    *domain.Config
	logger *slog.Logger
}

// NewService creates an AuthService backed by db.
func NewService(db domain.Database, cfg *domain.Config, logger *slog.Logger) *Service {
	return &Service{db: db, cfg: cfg, logger: logger}
}

// userSelectCols mirrors the constant in auth_handler.go. Any change there
// must be reflected here and vice versa.
const userSelectCols = `id::text, email, email_verified, email_confirmed_at, last_sign_in_at, raw_app_meta_data, raw_user_meta_data, created_at, updated_at`

// ---------- user lifecycle ----------

func (s *Service) GetUserByID(ctx context.Context, id string) (map[string]any, error) {
	row, err := s.db.QueryRow(ctx,
		"SELECT "+userSelectCols+" FROM auth.users WHERE id = $1::uuid", id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("user not found")
	}
	return row, nil
}

func (s *Service) GetUserByEmail(ctx context.Context, email string) (map[string]any, error) {
	row, err := s.db.QueryRow(ctx,
		"SELECT "+userSelectCols+" FROM auth.users WHERE email = $1", email)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("user not found")
	}
	return row, nil
}

// GetUserByPhone is defined in the interface but auth.users has no phone
// column in this schema. Return "not found" for any input.
func (s *Service) GetUserByPhone(_ context.Context, _ string) (map[string]any, error) {
	return nil, fmt.Errorf("user not found")
}

func (s *Service) CreateUser(ctx context.Context, p domain.CreateUserParams) (map[string]any, error) {
	userMeta, _ := json.Marshal(p.UserMetadata)
	if len(userMeta) == 0 || string(userMeta) == "null" {
		userMeta = []byte("{}")
	}
	appMeta, _ := json.Marshal(p.AppMetadata)
	if len(appMeta) == 0 || string(appMeta) == "null" {
		appMeta = []byte("{}")
	}

	cols := []string{"email", "password_hash", "raw_user_meta_data", "raw_app_meta_data"}
	placeholders := []string{"$1", "$2", "$3::jsonb", "$4::jsonb"}
	args := []any{p.Email, p.Password, string(userMeta), string(appMeta)}

	if p.EmailConfirmed {
		cols = append(cols, "email_verified", "email_confirmed_at")
		placeholders = append(placeholders, "true", "NOW()")
	}

	query := fmt.Sprintf(
		"INSERT INTO auth.users (%s) VALUES (%s) RETURNING %s",
		strings.Join(cols, ", "), strings.Join(placeholders, ", "), userSelectCols)

	row, err := s.db.QueryRow(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("create user returned no row")
	}
	return row, nil
}

func (s *Service) UpdateUser(ctx context.Context, id string, p domain.UpdateUserParams) (map[string]any, error) {
	sets := []string{"updated_at = NOW()"}
	args := []any{}
	argIdx := 1

	if p.Email != nil && *p.Email != "" {
		sets = append(sets, fmt.Sprintf("email = $%d", argIdx))
		args = append(args, *p.Email)
		argIdx++
	}
	if p.Password != nil && *p.Password != "" {
		sets = append(sets, fmt.Sprintf("password_hash = $%d", argIdx))
		args = append(args, *p.Password)
		argIdx++
	}
	if p.EmailConfirmed != nil && *p.EmailConfirmed {
		sets = append(sets, "email_verified = true", "email_confirmed_at = NOW()")
	}
	if p.UserMetadata != nil {
		metaJSON, _ := json.Marshal(p.UserMetadata)
		sets = append(sets, fmt.Sprintf("raw_user_meta_data = raw_user_meta_data || $%d::jsonb", argIdx))
		args = append(args, string(metaJSON))
		argIdx++
	}
	if p.AppMetadata != nil {
		metaJSON, _ := json.Marshal(p.AppMetadata)
		sets = append(sets, fmt.Sprintf("raw_app_meta_data = raw_app_meta_data || $%d::jsonb", argIdx))
		args = append(args, string(metaJSON))
		argIdx++
	}
	if p.Banned != nil {
		if *p.Banned {
			// Permanent ban: far future date.
			sets = append(sets, "banned_until = 'infinity'::timestamptz")
		} else {
			sets = append(sets, "banned_until = NULL")
		}
	}

	args = append(args, id)
	query := fmt.Sprintf(
		"UPDATE auth.users SET %s WHERE id = $%d::uuid RETURNING %s",
		strings.Join(sets, ", "), argIdx, userSelectCols)

	row, err := s.db.QueryRow(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("user not found")
	}
	return row, nil
}

func (s *Service) DeleteUser(ctx context.Context, id string) error {
	// Clean up auth artifacts first (mirrors handleAdminDeleteUser).
	s.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE user_id = $1::uuid", id)       //nolint:errcheck
	s.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE user_id = $1::uuid", id)      //nolint:errcheck
	s.db.Exec(ctx, "DELETE FROM auth.mfa_factors WHERE user_id = $1::uuid", id)          //nolint:errcheck

	affected, err := s.db.Exec(ctx, "DELETE FROM auth.users WHERE id = $1::uuid", id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func (s *Service) ListUsers(ctx context.Context, page, perPage int) ([]map[string]any, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 1000 {
		perPage = 50
	}
	offset := (page - 1) * perPage

	rows, err := s.db.Query(ctx,
		"SELECT "+userSelectCols+", count(*) OVER() AS _total FROM auth.users ORDER BY created_at DESC LIMIT $1 OFFSET $2",
		perPage, offset)
	if err != nil {
		return nil, 0, err
	}

	total := 0
	result := make([]map[string]any, 0, len(rows))
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
		delete(row, "_total")
		result = append(result, row)
	}
	return result, total, nil
}

// ---------- password ----------

func (s *Service) VerifyPassword(ctx context.Context, email, password string) (map[string]any, error) {
	row, err := s.db.QueryRow(ctx,
		`SELECT id::text, email, password_hash, email_verified, email_confirmed_at, last_sign_in_at, raw_app_meta_data, raw_user_meta_data, created_at, updated_at
		 FROM auth.users WHERE email = $1`, email)
	if err != nil || row == nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	passwordHash, _ := row["password_hash"].(string)
	if passwordHash == "" {
		return nil, fmt.Errorf("account uses OAuth login")
	}
	if err := checkPassword(passwordHash, password); err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}
	return row, nil
}

func (s *Service) SetPassword(ctx context.Context, userID, bcryptHash string) error {
	_, err := s.db.Exec(ctx,
		"UPDATE auth.users SET password_hash = $1, updated_at = NOW() WHERE id = $2::uuid",
		bcryptHash, userID)
	return err
}

// ---------- sessions ----------

func (s *Service) CreateSession(ctx context.Context, userID string) (sessionID, refreshToken string, err error) {
	sessionID, err = generateRandomToken(32)
	if err != nil {
		return "", "", fmt.Errorf("generate session id: %w", err)
	}
	refreshToken, err = generateRandomToken(32)
	if err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}

	refreshExpiry, _ := time.ParseDuration(s.cfg.Auth.RefreshTokenExpiry)
	if refreshExpiry == 0 {
		refreshExpiry = 7 * 24 * time.Hour
	}

	_, err = s.db.Exec(ctx,
		"INSERT INTO auth.refresh_tokens (user_id, token, session_id, expires_at) VALUES ($1::uuid, $2, $3, $4)",
		userID, refreshToken, sessionID, time.Now().Add(refreshExpiry))
	if err != nil {
		return "", "", fmt.Errorf("insert refresh token: %w", err)
	}
	return sessionID, refreshToken, nil
}

func (s *Service) VerifyRefreshToken(ctx context.Context, token string) (userRow map[string]any, sessionID string, err error) {
	row, err := s.db.QueryRow(ctx,
		"SELECT user_id::text, session_id, expires_at FROM auth.refresh_tokens WHERE token = $1", token)
	if err != nil || row == nil {
		return nil, "", fmt.Errorf("invalid refresh token")
	}

	expiresAt, _ := row["expires_at"].(time.Time)
	if time.Now().After(expiresAt) {
		s.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE token = $1", token) //nolint:errcheck
		return nil, "", fmt.Errorf("refresh token expired")
	}

	userID := asString(row["user_id"])
	sessionID = asString(row["session_id"])

	userRow, err = s.db.QueryRow(ctx,
		"SELECT "+userSelectCols+" FROM auth.users WHERE id = $1::uuid", userID)
	if err != nil || userRow == nil {
		return nil, "", fmt.Errorf("user not found")
	}
	return userRow, sessionID, nil
}

func (s *Service) RevokeSession(ctx context.Context, sessionID string) error {
	_, err := s.db.Exec(ctx,
		"DELETE FROM auth.refresh_tokens WHERE session_id = $1", sessionID)
	return err
}

func (s *Service) RevokeAllUserSessions(ctx context.Context, userID string) error {
	_, err := s.db.Exec(ctx,
		"DELETE FROM auth.refresh_tokens WHERE user_id = $1::uuid", userID)
	return err
}

func (s *Service) RecordSignIn(ctx context.Context, userID string) {
	// Fire-and-forget: update last_sign_in_at.
	s.db.Exec(ctx, "UPDATE auth.users SET last_sign_in_at = NOW(), updated_at = NOW() WHERE id = $1::uuid", userID) //nolint:errcheck
}

// ---------- OTP / magic link ----------

// CreateOTPCode creates an OTP entry in auth.one_time_tokens.
// The email is sourced from the user row so it can be stored on the token.
func (s *Service) CreateOTPCode(ctx context.Context, userID, kind string) (token, code string, err error) {
	token, err = generateRandomToken(32)
	if err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	code, err = generateNumericCode(6)
	if err != nil {
		return "", "", fmt.Errorf("generate code: %w", err)
	}

	// Look up the user's email to store on the OTP row (matches handler pattern).
	emailRow, _ := s.db.QueryRow(ctx, "SELECT email FROM auth.users WHERE id = $1::uuid", userID)
	email := ""
	if emailRow != nil {
		email, _ = emailRow["email"].(string)
	}

	expiresAt := time.Now().Add(1 * time.Hour)
	_, err = s.db.Exec(ctx,
		"INSERT INTO auth.one_time_tokens (user_id, token, code, email, purpose, expires_at) VALUES ($1::uuid, $2, $3, $4, $5, $6)",
		userID, token, code, email, kind, expiresAt)
	if err != nil {
		return "", "", fmt.Errorf("insert otp: %w", err)
	}
	return token, code, nil
}

func (s *Service) VerifyOTPToken(ctx context.Context, token, kind string) (map[string]any, error) {
	row, err := s.db.QueryRow(ctx,
		"SELECT user_id::text, purpose, expires_at FROM auth.one_time_tokens WHERE token = $1",
		token)
	if err != nil || row == nil {
		return nil, fmt.Errorf("invalid or expired token")
	}

	expiresAt, _ := row["expires_at"].(time.Time)
	if time.Now().After(expiresAt) {
		s.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE token = $1", token) //nolint:errcheck
		return nil, fmt.Errorf("token expired")
	}

	purpose, _ := row["purpose"].(string)
	if purpose != kind {
		return nil, fmt.Errorf("token purpose mismatch")
	}

	// Consume the token (single-use).
	s.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE token = $1", token) //nolint:errcheck

	userID := asString(row["user_id"])
	userRow, err := s.db.QueryRow(ctx,
		"SELECT "+userSelectCols+" FROM auth.users WHERE id = $1::uuid", userID)
	if err != nil || userRow == nil {
		return nil, fmt.Errorf("user not found")
	}
	return userRow, nil
}

func (s *Service) VerifyOTPCode(ctx context.Context, userID, kind, code string) error {
	row, err := s.db.QueryRow(ctx,
		`SELECT id, expires_at, code, attempts FROM auth.one_time_tokens
		 WHERE user_id = $1::uuid AND purpose = $2 AND code IS NOT NULL
		 ORDER BY created_at DESC LIMIT 1`,
		userID, kind)
	if err != nil || row == nil {
		return fmt.Errorf("invalid or expired token")
	}

	expiresAt, _ := row["expires_at"].(time.Time)
	if time.Now().After(expiresAt) {
		s.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE id = $1", row["id"]) //nolint:errcheck
		return fmt.Errorf("token expired")
	}

	attempts := asInt64(row["attempts"])
	storedCode, _ := row["code"].(string)
	if attempts >= 5 || storedCode != code {
		if storedCode != code && attempts+1 < 5 {
			s.db.Exec(ctx, "UPDATE auth.one_time_tokens SET attempts = attempts + 1 WHERE id = $1", row["id"]) //nolint:errcheck
		} else {
			s.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE id = $1", row["id"]) //nolint:errcheck
		}
		return fmt.Errorf("invalid or expired token")
	}

	// Consume the token.
	s.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE id = $1", row["id"]) //nolint:errcheck
	return nil
}

// ---------- PKCE flow ----------

func (s *Service) CreateFlowState(ctx context.Context, provider, codeChallenge, codeChallengeMethod string) (authCode string, err error) {
	authCode, err = generateRandomToken(32)
	if err != nil {
		return "", fmt.Errorf("generate auth code: %w", err)
	}
	if codeChallengeMethod == "" {
		codeChallengeMethod = "S256"
	}
	_, err = s.db.Exec(ctx,
		"INSERT INTO auth.flow_state (auth_code, code_challenge, code_challenge_method, provider_type, authentication_method, auth_code_issued_at) VALUES ($1, $2, $3, 'pkce', $4, NOW())",
		authCode, codeChallenge, codeChallengeMethod, provider)
	if err != nil {
		return "", fmt.Errorf("insert flow state: %w", err)
	}
	return authCode, nil
}

func (s *Service) GetFlowState(ctx context.Context, authCode string) (codeChallenge, method, userID string, err error) {
	row, err := s.db.QueryRow(ctx,
		"SELECT user_id::text, code_challenge, code_challenge_method FROM auth.flow_state WHERE auth_code = $1 AND provider_type = 'pkce' AND auth_code_issued_at > NOW() - INTERVAL '5 minutes'",
		authCode)
	if err != nil || row == nil {
		return "", "", "", fmt.Errorf("invalid or expired auth code")
	}
	codeChallenge, _ = row["code_challenge"].(string)
	method, _ = row["code_challenge_method"].(string)
	userID = asString(row["user_id"])
	return codeChallenge, method, userID, nil
}

func (s *Service) DeleteFlowState(ctx context.Context, authCode string) error {
	_, err := s.db.Exec(ctx,
		"DELETE FROM auth.flow_state WHERE auth_code = $1 AND provider_type = 'pkce'", authCode)
	return err
}

// ---------- identity linking ----------

// GetOrCreateIdentity looks up an identity by provider+providerID. If not
// found it creates a new user row (using email from userMeta) and an identity
// row. Returns the user row, whether a new user was created, and any error.
func (s *Service) GetOrCreateIdentity(ctx context.Context, provider, providerID string, userMeta map[string]any) (userRow map[string]any, created bool, err error) {
	// First try to find the identity.
	idRow, _ := s.db.QueryRow(ctx,
		"SELECT user_id::text FROM auth.identities WHERE provider = $1 AND provider_user_id = $2",
		provider, providerID)
	if idRow != nil {
		userID := asString(idRow["user_id"])
		userRow, err = s.db.QueryRow(ctx,
			"SELECT "+userSelectCols+" FROM auth.users WHERE id = $1::uuid", userID)
		if err != nil || userRow == nil {
			return nil, false, fmt.Errorf("user not found for identity")
		}
		return userRow, false, nil
	}

	// Not found — create user + identity.
	email, _ := userMeta["email"].(string)
	appMeta, _ := json.Marshal(map[string]any{
		"provider":  provider,
		"providers": []string{provider},
	})
	metaJSON, _ := json.Marshal(userMeta)

	userRow, err = s.db.QueryRow(ctx,
		"INSERT INTO auth.users (email, email_verified, email_confirmed_at, raw_user_meta_data, raw_app_meta_data) VALUES ($1, true, NOW(), $2::jsonb, $3::jsonb) RETURNING "+userSelectCols,
		email, string(metaJSON), string(appMeta))
	if err != nil {
		// Race: try to find the user by email.
		if email != "" {
			userRow, err2 := s.db.QueryRow(ctx,
				"SELECT "+userSelectCols+" FROM auth.users WHERE email = $1", email)
			if err2 == nil && userRow != nil {
				userID := asString(userRow["id"])
				s.db.Exec(ctx, //nolint:errcheck
					`INSERT INTO auth.identities (user_id, provider, provider_user_id, email, last_sign_in_at, updated_at)
					 VALUES ($1::uuid, $2, $3, $4, NOW(), NOW())
					 ON CONFLICT (provider, provider_user_id) DO UPDATE SET last_sign_in_at = EXCLUDED.last_sign_in_at, updated_at = EXCLUDED.updated_at`,
					userID, provider, providerID, email)
				return userRow, false, nil
			}
		}
		return nil, false, fmt.Errorf("create user: %w", err)
	}
	if userRow == nil {
		return nil, false, fmt.Errorf("create user returned no row")
	}

	userID := asString(userRow["id"])
	s.db.Exec(ctx, //nolint:errcheck
		`INSERT INTO auth.identities (user_id, provider, provider_user_id, email, last_sign_in_at, updated_at)
		 VALUES ($1::uuid, $2, $3, $4, NOW(), NOW())
		 ON CONFLICT (provider, provider_user_id) DO UPDATE SET last_sign_in_at = EXCLUDED.last_sign_in_at, updated_at = EXCLUDED.updated_at`,
		userID, provider, providerID, email)

	return userRow, true, nil
}

func (s *Service) ListIdentities(ctx context.Context, userID string) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx,
		"SELECT id::text, provider, provider_user_id, identity_data, email, last_sign_in_at, created_at, updated_at FROM auth.identities WHERE user_id = $1::uuid ORDER BY created_at",
		userID)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return []map[string]any{}, nil
	}
	return rows, nil
}

func (s *Service) DeleteIdentity(ctx context.Context, userID, provider string) error {
	affected, err := s.db.Exec(ctx,
		"DELETE FROM auth.identities WHERE user_id = $1::uuid AND provider = $2",
		userID, provider)
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("identity not found")
	}
	return nil
}

// ---------- MFA ----------

func (s *Service) CreateFactor(ctx context.Context, userID, factorType, friendlyName string) (map[string]any, error) {
	row, err := s.db.QueryRow(ctx,
		`INSERT INTO auth.mfa_factors (user_id, friendly_name, factor_type, status, secret)
		 VALUES ($1::uuid, $2, $3, 'unverified', '')
		 RETURNING id::text, friendly_name, factor_type, status, created_at, updated_at`,
		userID, friendlyName, factorType)
	if err != nil {
		return nil, fmt.Errorf("create factor: %w", err)
	}
	if row == nil {
		return nil, fmt.Errorf("create factor returned no row")
	}
	return row, nil
}

func (s *Service) VerifyFactor(ctx context.Context, factorID, _ string) error {
	_, err := s.db.Exec(ctx,
		"UPDATE auth.mfa_factors SET status = 'verified', updated_at = NOW() WHERE id = $1::uuid",
		factorID)
	return err
}

func (s *Service) DeleteFactor(ctx context.Context, factorID string) error {
	affected, err := s.db.Exec(ctx,
		"DELETE FROM auth.mfa_factors WHERE id = $1::uuid", factorID)
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("factor not found")
	}
	return nil
}

func (s *Service) ListFactors(ctx context.Context, userID string) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id::text, friendly_name, factor_type, status, created_at, updated_at
		 FROM auth.mfa_factors WHERE user_id = $1::uuid ORDER BY created_at ASC`,
		userID)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return []map[string]any{}, nil
	}
	return rows, nil
}
