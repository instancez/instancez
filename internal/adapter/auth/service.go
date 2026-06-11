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

// userSelectCols is the canonical auth.users projection consumed by the HTTP
// handler's buildUser/buildSession. mfa_handler.go keeps its own copy of this
// column list; any change here must be mirrored there.
const userSelectCols = `id::text, email, email_verified, email_confirmed_at, last_sign_in_at, raw_app_meta_data, raw_user_meta_data, created_at, updated_at`

// maxOTPAttempts bounds brute-force of the 10^6 numeric-code space.
const maxOTPAttempts = 5

// ---------- user lifecycle ----------

func (s *Service) GetUserByID(ctx context.Context, id string) (map[string]any, error) {
	row, err := s.db.QueryRow(ctx,
		"SELECT "+userSelectCols+" FROM auth.users WHERE id = $1::uuid", id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, domain.ErrNotFound
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
		return nil, domain.ErrNotFound
	}
	return row, nil
}

func (s *Service) GetUserIDByEmail(ctx context.Context, email string) (string, error) {
	row, err := s.db.QueryRow(ctx, "SELECT id::text FROM auth.users WHERE email = $1", email)
	if err != nil {
		return "", err
	}
	if row == nil {
		return "", domain.ErrNotFound
	}
	return asString(row["id"]), nil
}

// CreateUser inserts an auth.users row. It supports the signup, anonymous,
// admin-create, invite, and generate_link insert shapes via CreateUserParams.
func (s *Service) CreateUser(ctx context.Context, p domain.CreateUserParams) (map[string]any, error) {
	userMeta := jsonbArg(p.UserMetadata)
	appMeta := jsonbArg(p.AppMetadata)

	cols := []string{}
	placeholders := []string{}
	args := []any{}
	add := func(col, ph string, val any) {
		cols = append(cols, col)
		placeholders = append(placeholders, ph)
		args = append(args, val)
	}
	idx := func() int { return len(args) + 1 }

	add("email", fmt.Sprintf("$%d", idx()), p.Email)
	add("password_hash", fmt.Sprintf("$%d", idx()), p.Password)
	add("raw_user_meta_data", fmt.Sprintf("$%d::jsonb", idx()), string(userMeta))
	add("raw_app_meta_data", fmt.Sprintf("$%d::jsonb", idx()), string(appMeta))

	if p.Anonymous {
		cols = append(cols, "is_anonymous")
		placeholders = append(placeholders, "true")
	}
	if p.EmailConfirmed {
		cols = append(cols, "email_verified", "email_confirmed_at")
		placeholders = append(placeholders, "true", "NOW()")
	}
	if p.BanDuration != "" && p.BanDuration != "none" {
		cols = append(cols, "banned_until")
		placeholders = append(placeholders, fmt.Sprintf("NOW() + $%d::interval", idx()))
		args = append(args, p.BanDuration)
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
	if p.ClearEmailVerified {
		sets = append(sets, "email_verified = false", "email_confirmed_at = NULL")
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
			sets = append(sets, "banned_until = 'infinity'::timestamptz")
		} else {
			sets = append(sets, "banned_until = NULL")
		}
	}
	if p.BanDuration != nil {
		if *p.BanDuration == "none" {
			sets = append(sets, "banned_until = NULL")
		} else {
			sets = append(sets, fmt.Sprintf("banned_until = NOW() + $%d::interval", argIdx))
			args = append(args, *p.BanDuration)
			argIdx++
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
		return nil, domain.ErrNotFound
	}
	return row, nil
}

func (s *Service) DeleteUser(ctx context.Context, id string) error {
	// Clean up auth artifacts first (mirrors handleAdminDeleteUser).
	_, _ = s.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE user_id = $1::uuid", id)
	_, _ = s.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE user_id = $1::uuid", id)
	_, _ = s.db.Exec(ctx, "DELETE FROM auth.mfa_factors WHERE user_id = $1::uuid", id)

	affected, err := s.db.Exec(ctx, "DELETE FROM auth.users WHERE id = $1::uuid", id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return domain.ErrNotFound
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
		return nil, domain.ErrUnauthorized
	}

	passwordHash, _ := row["password_hash"].(string)
	if passwordHash == "" {
		return nil, domain.ErrOAuthOnlyAccount
	}
	if err := checkPassword(passwordHash, password); err != nil {
		return nil, domain.ErrUnauthorized
	}
	return row, nil
}

func (s *Service) GetUserEmail(ctx context.Context, userID string) (string, error) {
	row, err := s.db.QueryRow(ctx, "SELECT email FROM auth.users WHERE id = $1::uuid", userID)
	if err != nil {
		return "", err
	}
	if row == nil {
		return "", domain.ErrNotFound
	}
	return asString(row["email"]), nil
}

func (s *Service) HasPassword(ctx context.Context, userID string) (bool, error) {
	row, err := s.db.QueryRow(ctx, "SELECT password_hash FROM auth.users WHERE id = $1::uuid", userID)
	if err != nil {
		return false, err
	}
	if row == nil {
		return false, nil
	}
	ph, _ := row["password_hash"].(string)
	return ph != "", nil
}

// ---------- sign-in audit ----------

func (s *Service) RecordSignIn(ctx context.Context, userID string) {
	// Fire-and-forget: update last_sign_in_at.
	_, _ = s.db.Exec(ctx, "UPDATE auth.users SET last_sign_in_at = NOW(), updated_at = NOW() WHERE id = $1::uuid", userID)
}

// ---------- sessions / refresh tokens ----------

func (s *Service) InsertRefreshToken(ctx context.Context, userID, token string, meta domain.SessionMeta, expiresAt int64) error {
	_, err := s.db.Exec(ctx,
		"INSERT INTO auth.refresh_tokens (user_id, token, session_id, ip, user_agent, expires_at) VALUES ($1::uuid, $2, $3, $4, $5, $6)",
		userID, token, meta.SessionID, meta.IP, meta.UserAgent, time.Unix(expiresAt, 0))
	return err
}

func (s *Service) ConsumeRefreshToken(ctx context.Context, token string) (map[string]any, error) {
	row, err := s.db.QueryRow(ctx,
		"SELECT user_id::text, expires_at FROM auth.refresh_tokens WHERE token = $1", token)
	if err != nil || row == nil {
		return nil, domain.ErrUnauthorized
	}

	expiresAt, _ := row["expires_at"].(time.Time)
	if time.Now().After(expiresAt) {
		_, _ = s.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE token = $1", token)
		return nil, domain.ErrRefreshExpired
	}

	userID := asString(row["user_id"])

	// Rotation: each token is single-use.
	affected, _ := s.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE token = $1", token)
	if affected == 0 {
		s.logger.Warn("refresh token reuse detected, revoking all tokens", "user_id", userID)
		_, _ = s.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE user_id = $1::uuid", userID)
		return nil, domain.ErrRefreshReuse
	}

	userRow, err := s.db.QueryRow(ctx,
		"SELECT "+userSelectCols+" FROM auth.users WHERE id = $1::uuid", userID)
	if err != nil || userRow == nil {
		return nil, domain.ErrUnauthorized
	}
	return userRow, nil
}

func (s *Service) RevokeSessionByID(ctx context.Context, sessionID string) error {
	_, err := s.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE session_id = $1", sessionID)
	return err
}

func (s *Service) RevokeOtherSessions(ctx context.Context, userID, keepSessionID string) error {
	_, err := s.db.Exec(ctx,
		"DELETE FROM auth.refresh_tokens WHERE user_id = $1::uuid AND (session_id IS NULL OR session_id != $2)",
		userID, keepSessionID)
	return err
}

func (s *Service) RevokeAllUserSessions(ctx context.Context, userID string) error {
	_, err := s.db.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE user_id = $1::uuid", userID)
	return err
}

// ---------- one-time tokens ----------

func (s *Service) CreateOneTimeToken(ctx context.Context, userID, token, purpose string, expiresAt int64) error {
	_, err := s.db.Exec(ctx,
		"INSERT INTO auth.one_time_tokens (user_id, token, purpose, expires_at) VALUES ($1::uuid, $2, $3, $4)",
		userID, token, purpose, time.Unix(expiresAt, 0))
	return err
}

func (s *Service) CreateOTPCode(ctx context.Context, userID, token, code, email, purpose string, expiresAt int64) error {
	_, err := s.db.Exec(ctx,
		"INSERT INTO auth.one_time_tokens (user_id, token, code, email, purpose, expires_at) VALUES ($1::uuid, $2, $3, $4, $5, $6)",
		userID, token, code, email, purpose, time.Unix(expiresAt, 0))
	return err
}

func (s *Service) DeleteUserTokensByPurpose(ctx context.Context, userID, purpose string) error {
	_, err := s.db.Exec(ctx,
		"DELETE FROM auth.one_time_tokens WHERE user_id = $1::uuid AND purpose = $2", userID, purpose)
	return err
}

func (s *Service) DeleteOneTimeToken(ctx context.Context, token string) error {
	_, err := s.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE token = $1", token)
	return err
}

// VerifyOTP consumes a one-time token for POST /verify. It handles both the
// numeric-code (email + 6 digits) and opaque-token flows, including attempt
// tracking, expiry, and single-use deletion.
func (s *Service) VerifyOTP(ctx context.Context, token, email string, allowedPurposes []string) (domain.OTPRow, error) {
	var row map[string]any

	isNumericCode := len(token) == 6 && strings.IndexFunc(token, func(r rune) bool {
		return r < '0' || r > '9'
	}) == -1

	if isNumericCode && email != "" {
		// Numeric codes live in a 10^6 space, so the verify endpoint must be
		// brute-force resistant. Fetch the most recent code-bearing token for
		// the email, enforce a per-token attempt cap, and compare the code in
		// constant time. On too many failures the token is destroyed.
		cand, cerr := s.db.QueryRow(ctx,
			`SELECT id, user_id::text, purpose, expires_at, token, code, attempts
			   FROM auth.one_time_tokens
			  WHERE email = $1 AND code IS NOT NULL
			  ORDER BY created_at DESC LIMIT 1`,
			email)
		if cerr != nil || cand == nil {
			return domain.OTPRow{}, domain.ErrInvalidToken
		}
		if ts, _ := cand["expires_at"].(time.Time); time.Now().After(ts) {
			_, _ = s.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE id = $1", cand["id"])
			return domain.OTPRow{}, domain.ErrTokenExpired
		}
		attempts := asInt64(cand["attempts"])
		codeOK := constantTimeEqual(asString(cand["code"]), token)
		if attempts >= maxOTPAttempts || !codeOK {
			if !codeOK && attempts+1 < maxOTPAttempts {
				_, _ = s.db.Exec(ctx, "UPDATE auth.one_time_tokens SET attempts = attempts + 1 WHERE id = $1", cand["id"])
			} else {
				_, _ = s.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE id = $1", cand["id"])
			}
			return domain.OTPRow{}, domain.ErrInvalidToken
		}
		row = cand
	} else {
		var err error
		row, err = s.db.QueryRow(ctx,
			"SELECT user_id::text, purpose, expires_at, token FROM auth.one_time_tokens WHERE token = $1",
			token)
		if err != nil || row == nil {
			return domain.OTPRow{}, domain.ErrInvalidToken
		}
	}

	// Row token (the opaque token) is the canonical delete key; the supplied
	// token may be the 6-digit code.
	rowToken := asString(row["token"])
	expiresAt, _ := row["expires_at"].(time.Time)
	if time.Now().After(expiresAt) {
		_, _ = s.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE token = $1", rowToken)
		return domain.OTPRow{}, domain.ErrTokenExpired
	}

	purpose, _ := row["purpose"].(string)
	if !purposeAllowed(purpose, allowedPurposes) {
		return domain.OTPRow{}, domain.ErrPurposeMismatch
	}

	// Consume the token (single-use). Always delete by the canonical token
	// column so 6-digit code flows also clear the row.
	_, _ = s.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE token = $1", rowToken)

	return domain.OTPRow{UserID: asString(row["user_id"]), Purpose: purpose}, nil
}

// purposeAllowed reports whether purpose is acceptable. An empty stored purpose
// is always accepted (legacy rows). An empty/nil allowed set accepts anything.
func purposeAllowed(purpose string, allowed []string) bool {
	if purpose == "" || len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if purpose == a {
			return true
		}
	}
	return false
}

func (s *Service) PeekOneTimeToken(ctx context.Context, token string) (domain.OTPRow, error) {
	row, err := s.db.QueryRow(ctx,
		"SELECT user_id::text, purpose, expires_at FROM auth.one_time_tokens WHERE token = $1",
		token)
	if err != nil || row == nil {
		return domain.OTPRow{}, domain.ErrInvalidToken
	}
	expiresAt, _ := row["expires_at"].(time.Time)
	if time.Now().After(expiresAt) {
		_, _ = s.db.Exec(ctx, "DELETE FROM auth.one_time_tokens WHERE token = $1", token)
		return domain.OTPRow{}, domain.ErrTokenExpired
	}
	return domain.OTPRow{UserID: asString(row["user_id"]), Purpose: asString(row["purpose"])}, nil
}

func (s *Service) MarkEmailVerified(ctx context.Context, userID string) {
	_, _ = s.db.Exec(ctx,
		"UPDATE auth.users SET email_verified = true, email_confirmed_at = NOW(), updated_at = NOW() WHERE id = $1::uuid",
		userID)
}

// ---------- PKCE flow ----------

func (s *Service) GetPKCEFlowState(ctx context.Context, authCode string) (codeChallenge, method, userID string, err error) {
	row, err := s.db.QueryRow(ctx,
		"SELECT user_id::text, code_challenge, code_challenge_method FROM auth.flow_state WHERE auth_code = $1 AND provider_type = 'pkce' AND auth_code_issued_at > NOW() - INTERVAL '5 minutes'",
		authCode)
	if err != nil || row == nil {
		return "", "", "", domain.ErrInvalidToken
	}
	codeChallenge, _ = row["code_challenge"].(string)
	method, _ = row["code_challenge_method"].(string)
	userID = asString(row["user_id"])
	return codeChallenge, method, userID, nil
}

func (s *Service) DeletePKCEFlowState(ctx context.Context, authCode string) error {
	_, err := s.db.Exec(ctx,
		"DELETE FROM auth.flow_state WHERE auth_code = $1 AND provider_type = 'pkce'", authCode)
	return err
}

func (s *Service) CreatePKCEFlowState(ctx context.Context, authCode, userID, codeChallenge, method string) error {
	if method == "" {
		method = "S256"
	}
	_, err := s.db.Exec(ctx,
		"INSERT INTO auth.flow_state (auth_code, user_id, code_challenge, code_challenge_method, provider_type, authentication_method, auth_code_issued_at) VALUES ($1, $2::uuid, $3, $4, 'pkce', 'pkce', NOW())",
		authCode, userID, codeChallenge, method)
	return err
}

// ---------- OAuth flow state ----------

func (s *Service) CreateOAuthFlowState(ctx context.Context, state, codeChallenge, method, redirectTo, linkingUserID string) error {
	var linking any
	if linkingUserID != "" {
		linking = linkingUserID
	}
	var cc, ccm any
	if codeChallenge != "" {
		cc = codeChallenge
		if method == "" {
			method = "S256"
		}
		ccm = method
	}
	_, err := s.db.Exec(ctx,
		"INSERT INTO auth.flow_state (auth_code, code_challenge, code_challenge_method, redirect_to, provider_type, authentication_method, linking_user_id, auth_code_issued_at) VALUES ($1, $2, $3, $4, 'oauth', 'oauth', $5, NOW())",
		state, cc, ccm, redirectTo, linking)
	return err
}

func (s *Service) ConsumeOAuthFlowState(ctx context.Context, state string) (domain.FlowState, error) {
	row, err := s.db.QueryRow(ctx,
		"SELECT code_challenge, code_challenge_method, redirect_to, linking_user_id FROM auth.flow_state WHERE auth_code = $1 AND provider_type = 'oauth' AND auth_code_issued_at > NOW() - INTERVAL '10 minutes'",
		state)
	if err != nil || row == nil {
		return domain.FlowState{}, domain.ErrNotFound
	}
	_, _ = s.db.Exec(ctx, "DELETE FROM auth.flow_state WHERE auth_code = $1 AND provider_type = 'oauth'", state)
	return domain.FlowState{
		CodeChallenge:       asString(row["code_challenge"]),
		CodeChallengeMethod: asString(row["code_challenge_method"]),
		RedirectTo:          asString(row["redirect_to"]),
		LinkingUserID:       asString(row["linking_user_id"]),
	}, nil
}

// ---------- OAuth / ID-token user provisioning ----------

func (s *Service) UpsertOAuthUser(ctx context.Context, provider, providerUserID, email, name string) (map[string]any, error) {
	row, _ := s.db.QueryRow(ctx,
		"SELECT "+userSelectCols+" FROM auth.users WHERE email = $1", email)

	var userID string
	if row == nil {
		metaJSON, _ := json.Marshal(map[string]any{
			"provider":       provider,
			"full_name":      name,
			"email":          email,
			"email_verified": true,
		})
		appMetaJSON, _ := json.Marshal(map[string]any{
			"provider":  provider,
			"providers": []string{provider},
		})
		newRow, err := s.db.QueryRow(ctx,
			"INSERT INTO auth.users (email, email_verified, email_confirmed_at, raw_user_meta_data, raw_app_meta_data) VALUES ($1, true, NOW(), $2::jsonb, $3::jsonb) RETURNING "+userSelectCols,
			email, string(metaJSON), string(appMetaJSON))
		if err != nil {
			// Race: another request created the user between lookup and insert.
			row, err = s.db.QueryRow(ctx,
				"SELECT "+userSelectCols+" FROM auth.users WHERE email = $1", email)
			if err != nil || row == nil {
				return nil, fmt.Errorf("create or find user: %w", err)
			}
			userID = asString(row["id"])
		} else {
			row = newRow
			userID = asString(newRow["id"])
		}
	} else {
		userID = asString(row["id"])
		_, _ = s.db.Exec(ctx, "UPDATE auth.users SET email_verified = true, email_confirmed_at = COALESCE(email_confirmed_at, NOW()), last_sign_in_at = NOW(), updated_at = NOW() WHERE id = $1::uuid", userID)
	}

	_, _ = s.db.Exec(ctx,
		`INSERT INTO auth.identities (user_id, provider, provider_user_id, email, last_sign_in_at, updated_at)
		 VALUES ($1::uuid, $2, $3, $4, NOW(), NOW())
		 ON CONFLICT (provider, provider_user_id)
		 DO UPDATE SET last_sign_in_at = EXCLUDED.last_sign_in_at, updated_at = EXCLUDED.updated_at`,
		userID, provider, providerUserID, email)

	return row, nil
}

func (s *Service) LinkIdentity(ctx context.Context, userID, provider, providerUserID, email string) {
	_, _ = s.db.Exec(ctx,
		`INSERT INTO auth.identities (user_id, provider, provider_user_id, email, last_sign_in_at, updated_at)
		 VALUES ($1::uuid, $2, $3, $4, NOW(), NOW())
		 ON CONFLICT (provider, provider_user_id) DO NOTHING`,
		userID, provider, providerUserID, email)
}

// ---------- identity management ----------

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

func (s *Service) CountIdentities(ctx context.Context, userID string) (int, error) {
	rows, err := s.db.Query(ctx, "SELECT id::text FROM auth.identities WHERE user_id = $1::uuid", userID)
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

func (s *Service) DeleteIdentityByID(ctx context.Context, identityID, userID string) error {
	affected, err := s.db.Exec(ctx,
		"DELETE FROM auth.identities WHERE id = $1::uuid AND user_id = $2::uuid",
		identityID, userID)
	if err != nil {
		return err
	}
	if affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ---------- MFA ----------

func (s *Service) DeleteFactorForUser(ctx context.Context, factorID, userID string) error {
	affected, err := s.db.Exec(ctx,
		"DELETE FROM auth.mfa_factors WHERE id = $1::uuid AND user_id = $2::uuid",
		factorID, userID)
	if err != nil {
		return err
	}
	if affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}
