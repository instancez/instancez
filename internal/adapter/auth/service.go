// Package auth implements the domain.AuthService port using Postgres.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

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
	return &Service{db: serviceRoleDB{db}, cfg: cfg, logger: logger}
}

// serviceRoleDB runs every auth query as service_role; anon/authenticated have no grants on auth.*.
type serviceRoleDB struct{ domain.Database }

// pin fails closed: a WithRLS error must never let a query run unpinned.
func (d serviceRoleDB) pin(ctx context.Context) (context.Context, error) {
	return d.WithRLS(ctx, domain.Session{Role: domain.JWTRoleService, IsAuthenticated: true})
}

func (d serviceRoleDB) Query(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
	c, err := d.pin(ctx)
	if err != nil {
		return nil, err
	}
	return d.Database.Query(c, q, args...)
}

func (d serviceRoleDB) QueryRow(ctx context.Context, q string, args ...any) (map[string]any, error) {
	c, err := d.pin(ctx)
	if err != nil {
		return nil, err
	}
	return d.Database.QueryRow(c, q, args...)
}

func (d serviceRoleDB) Exec(ctx context.Context, q string, args ...any) (int64, error) {
	c, err := d.pin(ctx)
	if err != nil {
		return 0, err
	}
	return d.Database.Exec(c, q, args...)
}

func (d serviceRoleDB) Begin(ctx context.Context) (domain.Tx, error) {
	c, err := d.pin(ctx)
	if err != nil {
		return nil, err
	}
	return d.Database.Begin(c)
}

// userSelectCols is the canonical auth.users projection consumed by the HTTP
// handler's buildUser/buildSession. mfa_handler.go keeps its own copy of this
// column list; any change here must be mirrored there.
const userSelectCols = `id::text, email, email_verified, email_confirmed_at, last_sign_in_at, banned_until, raw_app_meta_data, raw_user_meta_data, created_at, updated_at, COALESCE(banned_until > NOW(), false) AS is_banned`

// maxOTPAttempts bounds brute-force of the 10^6 numeric-code space.
const maxOTPAttempts = 5

// maxMFAAttempts bounds brute-force of the 10^6 TOTP code space per
// challenge, mirroring maxOTPAttempts.
const maxMFAAttempts = 5

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

	// email is UNIQUE, and Postgres treats '' as a real value subject to that
	// constraint (unlike NULL, which is never considered a duplicate). Every
	// credentialed signup requires a non-empty validated email; the only
	// caller that passes "" is anonymous signup. Store NULL there so a second
	// anonymous user doesn't collide with the first.
	var emailArg any = p.Email
	if p.Email == "" {
		emailArg = nil
	}
	add("email", fmt.Sprintf("$%d", idx()), emailArg)
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
		`SELECT id::text, email, password_hash, email_verified, email_confirmed_at, last_sign_in_at, raw_app_meta_data, raw_user_meta_data, created_at, updated_at, COALESCE(banned_until > NOW(), false) AS is_banned
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

// refreshReuseInterval lets concurrent refreshes (e.g. two tabs) share one rotation.
const refreshReuseInterval = 10 * time.Second

func (s *Service) InsertRefreshToken(ctx context.Context, userID, token string, meta domain.SessionMeta, expiresAt int64) error {
	aal := meta.AAL
	if aal == "" {
		aal = "aal1"
	}
	amr := meta.AMR
	if amr == nil {
		amr = []domain.AMREntry{}
	}
	amrJSON, err := json.Marshal(amr)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx,
		"INSERT INTO auth.refresh_tokens (user_id, token, session_id, ip, user_agent, expires_at, aal, amr) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8::jsonb)",
		userID, token, meta.SessionID, meta.IP, meta.UserAgent, time.Unix(expiresAt, 0), aal, string(amrJSON))
	return err
}

func (s *Service) ConsumeRefreshToken(ctx context.Context, token string, next domain.RefreshRotation) (map[string]any, domain.SessionMeta, string, error) {
	fail := func(err error) (map[string]any, domain.SessionMeta, string, error) {
		return nil, domain.SessionMeta{}, "", err
	}
	if token == "" {
		return fail(domain.ErrUnauthorized)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row, err := tx.QueryRow(ctx,
		`UPDATE auth.refresh_tokens SET revoked_at = NOW() WHERE token = $1 AND revoked_at IS NULL
		 RETURNING user_id::text, session_id, aal, amr, expires_at <= NOW() AS expired`, token)
	if err != nil {
		return fail(domain.ErrUnauthorized)
	}
	var meta domain.SessionMeta
	var refreshToken string
	if row != nil {
		if expired, _ := row["expired"].(bool); expired {
			return fail(domain.ErrRefreshExpired)
		}
		meta = sessionMetaFromRow(row)
		if meta.SessionID == "" {
			meta.SessionID = uuid.NewString()
		}
		amrJSON, err := json.Marshal(meta.AMR)
		if err != nil {
			return fail(err)
		}
		if _, err := tx.Exec(ctx,
			"INSERT INTO auth.refresh_tokens (user_id, token, session_id, ip, user_agent, expires_at, aal, amr) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8::jsonb)",
			asString(row["user_id"]), next.Token, meta.SessionID, next.IP, next.UserAgent, time.Unix(next.ExpiresAt, 0), meta.AAL, string(amrJSON)); err != nil {
			return fail(err)
		}
		refreshToken = next.Token
	} else {
		row, err = tx.QueryRow(ctx,
			`SELECT user_id::text, session_id, aal, revoked_at > NOW() - $2::float8 * INTERVAL '1 second' AS in_grace
			 FROM auth.refresh_tokens WHERE token = $1`, token, refreshReuseInterval.Seconds())
		if err != nil || row == nil {
			return fail(domain.ErrUnauthorized)
		}
		userID, sessionID := asString(row["user_id"]), asString(row["session_id"])
		var child map[string]any
		if inGrace, _ := row["in_grace"].(bool); inGrace && sessionID != "" {
			child, _ = tx.QueryRow(ctx,
				`SELECT token, session_id, aal, amr FROM auth.refresh_tokens
				 WHERE session_id = $1 AND aal = $2 AND revoked_at IS NULL AND expires_at > NOW()
				 ORDER BY id DESC LIMIT 1`, sessionID, asString(row["aal"]))
		}
		if child == nil {
			s.logger.Warn("refresh token reuse detected, revoking session", "user_id", userID, "session_id", sessionID)
			if sessionID != "" {
				_, err = tx.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE session_id = $1", sessionID)
			} else {
				_, err = tx.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE user_id = $1::uuid", userID)
			}
			if err == nil {
				err = tx.Commit(ctx)
			}
			if err != nil {
				return fail(err)
			}
			return fail(domain.ErrRefreshReuse)
		}
		meta = sessionMetaFromRow(child)
		refreshToken = asString(child["token"])
	}

	// ponytail: pruned only when the session rotates again; add a sweeper if idle sessions pile up.
	if _, err := tx.Exec(ctx, "DELETE FROM auth.refresh_tokens WHERE session_id = $1 AND revoked_at IS NOT NULL AND expires_at < NOW()", meta.SessionID); err != nil {
		return fail(err)
	}
	userRow, err := tx.QueryRow(ctx, "SELECT "+userSelectCols+" FROM auth.users WHERE id = $1::uuid", asString(row["user_id"]))
	if err != nil || userRow == nil {
		return fail(domain.ErrUnauthorized)
	}
	if err := tx.Commit(ctx); err != nil {
		return fail(err)
	}
	return userRow, meta, refreshToken, nil
}

func sessionMetaFromRow(row map[string]any) domain.SessionMeta {
	meta := domain.SessionMeta{SessionID: asString(row["session_id"]), AAL: asString(row["aal"]), AMR: domain.ParseAMR(row["amr"])}
	if meta.AAL == "" {
		meta.AAL = "aal1"
	}
	if meta.AMR == nil {
		meta.AMR = []domain.AMREntry{}
	}
	return meta
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

func (s *Service) RevokeBelowAAL2(ctx context.Context, userID, sessionID string, allSessions bool) error {
	_, err := s.db.Exec(ctx,
		"DELETE FROM auth.refresh_tokens WHERE user_id = $1::uuid AND aal <> 'aal2' AND ($3 OR session_id = $2)",
		userID, sessionID, allSessions)
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
		"DELETE FROM auth.flow_state WHERE auth_code = $1 AND provider_type = 'oauth' RETURNING code_challenge, code_challenge_method, redirect_to, linking_user_id, auth_code_issued_at > NOW() - INTERVAL '10 minutes' AS fresh",
		state)
	if err != nil || row == nil || row["fresh"] != true {
		return domain.FlowState{}, domain.ErrNotFound
	}
	return domain.FlowState{
		CodeChallenge:       asString(row["code_challenge"]),
		CodeChallengeMethod: asString(row["code_challenge_method"]),
		RedirectTo:          asString(row["redirect_to"]),
		LinkingUserID:       asString(row["linking_user_id"]),
	}, nil
}

// ---------- OAuth / ID-token user provisioning ----------

func (s *Service) UpsertOAuthUser(ctx context.Context, in domain.OAuthLogin) (map[string]any, error) {
	if in.Provider == "" || in.ProviderUserID == "" {
		return nil, fmt.Errorf("oauth login needs a provider and provider user id")
	}
	row, err := s.db.QueryRow(ctx,
		"SELECT "+userSelectCols+" FROM auth.users WHERE id = (SELECT user_id FROM auth.identities WHERE provider = $1 AND provider_user_id = $2)",
		in.Provider, in.ProviderUserID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		if !in.EmailVerified || in.Email == "" {
			return nil, domain.ErrProviderEmailUnverified
		}
		if row, err = s.linkOrCreateOAuthUser(ctx, in); err != nil {
			return nil, err
		}
	}
	userID := asString(row["id"])
	s.RecordSignIn(ctx, userID)
	_, _ = s.db.Exec(ctx,
		`INSERT INTO auth.identities (user_id, provider, provider_user_id, email, last_sign_in_at, updated_at)
		 VALUES ($1::uuid, $2, $3, $4, NOW(), NOW())
		 ON CONFLICT (provider, provider_user_id)
		 DO UPDATE SET email = EXCLUDED.email, last_sign_in_at = EXCLUDED.last_sign_in_at, updated_at = EXCLUDED.updated_at`,
		userID, in.Provider, in.ProviderUserID, in.Email)
	return row, nil
}

// oauthByEmail prefers a verified match; claimed means someone can already sign in to the account.
const oauthByEmail = "SELECT " + userSelectCols + `,
	COALESCE(password_hash, '') <> ''
	OR EXISTS (SELECT 1 FROM auth.identities i WHERE i.user_id = users.id)
	OR EXISTS (SELECT 1 FROM auth.refresh_tokens r WHERE r.user_id = users.id)
	OR is_anonymous AS claimed
	FROM auth.users WHERE lower(email) = lower($1) ORDER BY email_verified DESC, created_at LIMIT 1`

func (s *Service) linkOrCreateOAuthUser(ctx context.Context, in domain.OAuthLogin) (map[string]any, error) {
	row, err := s.db.QueryRow(ctx, oauthByEmail, in.Email)
	if err != nil {
		return nil, err
	}
	if row == nil {
		if !in.AllowSignup {
			return nil, domain.ErrSignupDisabled
		}
		userMeta, _ := json.Marshal(map[string]any{"provider": in.Provider, "full_name": in.Name, "email": in.Email, "email_verified": true})
		appMeta, _ := json.Marshal(map[string]any{"provider": in.Provider, "providers": []string{in.Provider}})
		created, insErr := s.db.QueryRow(ctx,
			"INSERT INTO auth.users (email, email_verified, email_confirmed_at, raw_user_meta_data, raw_app_meta_data) VALUES ($1, true, NOW(), $2::jsonb, $3::jsonb) RETURNING "+userSelectCols,
			in.Email, string(userMeta), string(appMeta))
		if insErr == nil {
			return created, nil
		}
		// Lost an insert race: judge the winner's row like any other match.
		if row, err = s.db.QueryRow(ctx, oauthByEmail, in.Email); err != nil || row == nil {
			return nil, fmt.Errorf("create or find user: %w", insErr)
		}
	}
	claimed, _ := row["claimed"].(bool)
	delete(row, "claimed")
	if verified, _ := row["email_verified"].(bool); verified {
		return row, nil
	}
	// Whoever holds an unproven account may have squatted this address.
	if claimed {
		return nil, domain.ErrOAuthLinkRefused
	}
	return s.db.QueryRow(ctx,
		"UPDATE auth.users SET email_verified = true, email_confirmed_at = NOW(), updated_at = NOW() WHERE id = $1::uuid RETURNING "+userSelectCols,
		row["id"])
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

// challengeTTL bounds how long an MFA challenge can be verified after
// creation. It also doubles as the sliding window for maxChallengesPerFactor:
// a challenge older than this can never be verified, so it never needs to
// count against the cap.
const challengeTTL = 5 * time.Minute

// maxChallengesPerFactor bounds how many challenges can be created for one
// factor within challengeTTL. Without this, an aal1 session can cycle
// challenges indefinitely and brute-force the factor's TOTP code past
// maxMFAAttempts (5 guesses per challenge).
const maxChallengesPerFactor = 10

func (s *Service) EnrollFactor(ctx context.Context, userID, friendlyName, secret string) (string, error) {
	row, err := s.db.QueryRow(ctx,
		`INSERT INTO auth.mfa_factors (user_id, friendly_name, factor_type, status, secret)
		 VALUES ($1::uuid, $2, 'totp', 'unverified', $3)
		 RETURNING id::text, friendly_name, factor_type, status, created_at, updated_at`,
		userID, friendlyName, secret)
	if err != nil {
		return "", err
	}
	if row == nil {
		return "", fmt.Errorf("enroll factor returned no row")
	}
	return asString(row["id"]), nil
}

func (s *Service) CreateChallenge(ctx context.Context, factorID, userID string) (string, time.Time, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the factor row so concurrent callers on the same factor serialize:
	// the count-then-insert below must see every sibling's committed insert.
	owner, err := tx.QueryRow(ctx,
		"SELECT 1 FROM auth.mfa_factors WHERE id = $1::uuid AND user_id = $2::uuid FOR UPDATE", factorID, userID)
	if err != nil {
		return "", time.Time{}, err
	}
	if owner == nil {
		return "", time.Time{}, domain.ErrNotFound
	}

	row, err := tx.QueryRow(ctx,
		`INSERT INTO auth.mfa_challenges (factor_id)
		 SELECT $1::uuid WHERE (
		   SELECT count(*) FROM auth.mfa_challenges
		    WHERE factor_id = $1::uuid AND created_at > NOW() - make_interval(secs => $2)
		 ) < $3
		 RETURNING id::text, created_at`,
		factorID, challengeTTL.Seconds(), maxChallengesPerFactor)
	if err != nil {
		return "", time.Time{}, err
	}
	if row == nil {
		return "", time.Time{}, domain.ErrChallengeRateLimited
	}

	// Prune challenges outside the window; they can never verify again.
	if _, err := tx.Exec(ctx,
		"DELETE FROM auth.mfa_challenges WHERE factor_id = $1::uuid AND created_at <= NOW() - make_interval(secs => $2)",
		factorID, challengeTTL.Seconds()); err != nil {
		return "", time.Time{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", time.Time{}, err
	}
	createdAt, _ := row["created_at"].(time.Time)
	return asString(row["id"]), createdAt, nil
}

func (s *Service) GetFactorForVerify(ctx context.Context, factorID, userID string) (domain.MFAFactor, error) {
	row, err := s.db.QueryRow(ctx,
		"SELECT user_id::text, secret, status FROM auth.mfa_factors WHERE id = $1::uuid",
		factorID)
	if err != nil {
		return domain.MFAFactor{}, err
	}
	if row == nil || asString(row["user_id"]) != userID {
		return domain.MFAFactor{}, domain.ErrNotFound
	}
	secret, _ := row["secret"].(string)
	status, _ := row["status"].(string)
	return domain.MFAFactor{Secret: secret, Status: status}, nil
}

func (s *Service) ValidateChallenge(ctx context.Context, challengeID, factorID string) error {
	reserved, err := s.db.QueryRow(ctx,
		`UPDATE auth.mfa_challenges SET attempts = attempts + 1
		  WHERE id = $1::uuid AND factor_id = $2::uuid AND verified_at IS NULL
		    AND created_at > NOW() - make_interval(secs => $3) AND attempts < $4
		  RETURNING id::text`,
		challengeID, factorID, challengeTTL.Seconds(), maxMFAAttempts)
	if err != nil {
		return err
	}
	if reserved != nil {
		return nil
	}
	ch, err := s.db.QueryRow(ctx,
		"SELECT factor_id::text, verified_at, created_at, attempts FROM auth.mfa_challenges WHERE id = $1::uuid",
		challengeID)
	if err != nil {
		return err
	}
	if ch == nil || asString(ch["factor_id"]) != factorID {
		return domain.ErrNotFound
	}
	if _, verified := ch["verified_at"].(time.Time); verified {
		return domain.ErrChallengeUsed
	}
	if createdAt, _ := ch["created_at"].(time.Time); time.Since(createdAt) > challengeTTL {
		return domain.ErrChallengeExpired
	}
	return domain.ErrChallengeTooManyAttempts
}

func (s *Service) MarkChallengeVerified(ctx context.Context, challengeID string) error {
	affected, err := s.db.Exec(ctx,
		"UPDATE auth.mfa_challenges SET verified_at = NOW() WHERE id = $1::uuid AND verified_at IS NULL", challengeID)
	if err != nil {
		return err
	}
	if affected == 0 {
		return domain.ErrChallengeUsed
	}
	return nil
}

func (s *Service) ConsumeTOTPStep(ctx context.Context, factorID string, step int64) (bool, error) {
	affected, err := s.db.Exec(ctx,
		`UPDATE auth.mfa_factors SET last_totp_step = $2, updated_at = NOW()
		  WHERE id = $1::uuid AND (last_totp_step IS NULL OR last_totp_step < $2)`, factorID, step)
	return affected == 1, err
}

func (s *Service) PromoteFactorToVerified(ctx context.Context, factorID string) error {
	_, err := s.db.Exec(ctx,
		`WITH promoted AS (
		   UPDATE auth.mfa_factors SET status = 'verified', updated_at = NOW() WHERE id = $1::uuid RETURNING user_id
		 )
		 DELETE FROM auth.mfa_factors f USING promoted p
		  WHERE f.user_id = p.user_id AND f.status = 'unverified' AND f.id <> $1::uuid`, factorID)
	return err
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

func (s *Service) DeleteFactorForUser(ctx context.Context, factorID, userID string, allowVerified bool) error {
	affected, err := s.db.Exec(ctx,
		`DELETE FROM auth.mfa_factors
		  WHERE id = $1::uuid AND user_id = $2::uuid AND (status = 'unverified' OR $3::bool)`,
		factorID, userID, allowVerified)
	if err != nil {
		return err
	}
	if affected == 0 {
		return domain.ErrNotFound
	}
	return nil
}
