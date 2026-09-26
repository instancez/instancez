package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/domain"
)

// fakeDB is a minimal domain.Database stub with hookable QueryRow/Exec used to
// exercise the auth Service SQL-orchestration logic without a real Postgres.
type fakeDB struct {
	queryRowFn func(ctx context.Context, q string, args ...any) (map[string]any, error)
	queryFn    func(ctx context.Context, q string, args ...any) ([]map[string]any, error)
	execFn     func(ctx context.Context, q string, args ...any) (int64, error)
}

func (f *fakeDB) Close() error                                    { return nil }
func (f *fakeDB) Ping(ctx context.Context) error                  { return nil }
func (f *fakeDB) EnsureMigrationsTable(ctx context.Context) error { return nil }
func (f *fakeDB) GetLastMigration(ctx context.Context) (*domain.Migration, error) {
	return nil, nil
}
func (f *fakeDB) ExecDDL(ctx context.Context, sql string) error { return nil }
func (f *fakeDB) Query(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
	if f.queryFn != nil {
		return f.queryFn(ctx, q, args...)
	}
	return nil, nil
}
func (f *fakeDB) QueryRow(ctx context.Context, q string, args ...any) (map[string]any, error) {
	if f.queryRowFn != nil {
		return f.queryRowFn(ctx, q, args...)
	}
	return nil, nil
}
func (f *fakeDB) Exec(ctx context.Context, q string, args ...any) (int64, error) {
	if f.execFn != nil {
		return f.execFn(ctx, q, args...)
	}
	return 0, nil
}
func (f *fakeDB) WithRLS(ctx context.Context, session domain.Session) (context.Context, error) {
	return ctx, nil
}
func (f *fakeDB) Begin(ctx context.Context) (domain.Tx, error) { return nil, nil }

func newTestService(db domain.Database) *Service {
	return NewService(db, &domain.Config{Auth: &domain.Auth{}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestVerifyOTP_NumericCodeAttemptLimit verifies that a wrong 6-digit code
// increments the attempt counter (not deletes) on the first guess.
func TestVerifyOTP_NumericCodeAttemptLimit(t *testing.T) {
	incremented, deleted := false, false
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			if strings.Contains(q, "code IS NOT NULL") {
				return map[string]any{
					"id":         int64(7),
					"user_id":    "u1",
					"purpose":    "magiclink",
					"expires_at": time.Now().Add(time.Hour),
					"token":      "longtoken",
					"code":       "123456",
					"attempts":   int64(0),
				}, nil
			}
			return nil, nil
		},
		execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
			if strings.Contains(q, "SET attempts = attempts + 1") {
				incremented = true
			}
			if strings.Contains(q, "DELETE FROM auth.one_time_tokens") {
				deleted = true
			}
			return 1, nil
		},
	}
	s := newTestService(db)
	_, err := s.VerifyOTP(context.Background(), "000000", "otp@example.com", []string{"signup", "magiclink"})
	if err != domain.ErrInvalidToken {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
	if !incremented {
		t.Error("expected attempts bumped on a wrong guess")
	}
	if deleted {
		t.Error("token should not be deleted on the first wrong guess")
	}
}

// TestVerifyOTP_BurnsTokenAtCap verifies the token is destroyed once the
// attempt budget is exhausted — even for a correct code.
func TestVerifyOTP_BurnsTokenAtCap(t *testing.T) {
	deleted := false
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			if strings.Contains(q, "code IS NOT NULL") {
				return map[string]any{
					"id":         int64(7),
					"user_id":    "u1",
					"purpose":    "magiclink",
					"expires_at": time.Now().Add(time.Hour),
					"token":      "longtoken",
					"code":       "123456",
					"attempts":   int64(maxOTPAttempts),
				}, nil
			}
			return nil, nil
		},
		execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
			if strings.Contains(q, "DELETE FROM auth.one_time_tokens") {
				deleted = true
			}
			return 1, nil
		},
	}
	s := newTestService(db)
	_, err := s.VerifyOTP(context.Background(), "123456", "otp@example.com", nil)
	if err != domain.ErrInvalidToken {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
	if !deleted {
		t.Error("token should be destroyed once the attempt budget is exhausted")
	}
}

// TestVerifyOTP_LongTokenUsesTokenLookup asserts a non-numeric token uses the
// token-only lookup (not the email/code path) and consumes the row.
func TestVerifyOTP_LongTokenUsesTokenLookup(t *testing.T) {
	var lookupQ string
	consumed := false
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			if strings.Contains(q, "auth.one_time_tokens") {
				lookupQ = q
				return map[string]any{
					"user_id":    "u1",
					"purpose":    "magiclink",
					"expires_at": time.Now().Add(time.Hour),
					"token":      "aaaaaaaabbbbbbbbccccccccdddddddd",
				}, nil
			}
			return nil, nil
		},
		execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
			if strings.Contains(q, "DELETE FROM auth.one_time_tokens WHERE token = $1") {
				consumed = true
			}
			return 1, nil
		},
	}
	s := newTestService(db)
	row, err := s.VerifyOTP(context.Background(), "aaaaaaaabbbbbbbbccccccccdddddddd", "u@e.com", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.UserID != "u1" {
		t.Errorf("user id = %q", row.UserID)
	}
	if strings.Contains(lookupQ, "code = $2") || !strings.Contains(lookupQ, "WHERE token = $1") {
		t.Errorf("long token should use token-only lookup, got: %s", lookupQ)
	}
	if !consumed {
		t.Error("token should be consumed (deleted) on success")
	}
}

// TestVerifyOTP_PurposeMismatch returns ErrPurposeMismatch without consuming.
func TestVerifyOTP_PurposeMismatch(t *testing.T) {
	deleted := false
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			if strings.Contains(q, "auth.one_time_tokens") {
				return map[string]any{
					"user_id":    "u1",
					"purpose":    "recovery",
					"expires_at": time.Now().Add(time.Hour),
					"token":      "tok",
				}, nil
			}
			return nil, nil
		},
		execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
			if strings.Contains(q, "DELETE") {
				deleted = true
			}
			return 1, nil
		},
	}
	s := newTestService(db)
	_, err := s.VerifyOTP(context.Background(), "tok", "", []string{"signup", "magiclink"})
	if err != domain.ErrPurposeMismatch {
		t.Fatalf("want ErrPurposeMismatch, got %v", err)
	}
	if deleted {
		t.Error("token must not be consumed on a purpose mismatch")
	}
}

// TestValidateChallenge_UnderCap allows verification while attempts remain
// below maxMFAAttempts.
func TestValidateChallenge_UnderCap(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			return map[string]any{
				"factor_id":   "f1",
				"verified_at": nil,
				"created_at":  time.Now(),
				"attempts":    int64(maxMFAAttempts - 1),
			}, nil
		},
	}
	s := newTestService(db)
	if err := s.ValidateChallenge(context.Background(), "c1", "f1"); err != nil {
		t.Fatalf("expected challenge to validate under the attempt cap, got %v", err)
	}
}

// TestValidateChallenge_AtCapRejects mirrors the OTP brute-force guard: once a
// challenge has hit maxMFAAttempts wrong TOTP guesses, further attempts are
// rejected even before the code is compared.
func TestValidateChallenge_AtCapRejects(t *testing.T) {
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			return map[string]any{
				"factor_id":   "f1",
				"verified_at": nil,
				"created_at":  time.Now(),
				"attempts":    int64(maxMFAAttempts),
			}, nil
		},
	}
	s := newTestService(db)
	err := s.ValidateChallenge(context.Background(), "c1", "f1")
	if err != domain.ErrChallengeTooManyAttempts {
		t.Fatalf("want ErrChallengeTooManyAttempts, got %v", err)
	}
}

// TestIncrementChallengeAttempt_IssuesUpdate verifies the SQL shape so a typo
// in the column/table name fails loudly rather than silently no-op'ing the cap.
func TestIncrementChallengeAttempt_IssuesUpdate(t *testing.T) {
	var gotQuery string
	db := &fakeDB{
		execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
			gotQuery = q
			return 1, nil
		},
	}
	s := newTestService(db)
	if err := s.IncrementChallengeAttempt(context.Background(), "c1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotQuery, "auth.mfa_challenges") || !strings.Contains(gotQuery, "attempts = attempts + 1") {
		t.Fatalf("expected an attempts-increment UPDATE on auth.mfa_challenges, got query: %q", gotQuery)
	}
}

// TestCreateUser_AnonymousEmailIsNULLNotEmptyString: email is UNIQUE, and
// Postgres treats '' as a real value subject to that constraint (unlike NULL,
// which never collides). Anonymous signup passes Email == "", so the insert
// must bind NULL there — otherwise a second anonymous sign-in hits a
// duplicate-key error on the first anonymous user's row.
func TestCreateUser_AnonymousEmailIsNULLNotEmptyString(t *testing.T) {
	var gotEmailArg any
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			gotEmailArg = args[0]
			return map[string]any{"id": "u1"}, nil
		},
	}
	s := newTestService(db)
	_, err := s.CreateUser(context.Background(), domain.CreateUserParams{Anonymous: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotEmailArg != nil {
		t.Fatalf("expected anonymous signup to bind NULL for email, got %#v", gotEmailArg)
	}
}

// TestCreateUser_CredentialedEmailIsPreserved is the companion case: a real
// signup's non-empty email must still be bound as-is, not nulled out.
func TestCreateUser_CredentialedEmailIsPreserved(t *testing.T) {
	var gotEmailArg any
	db := &fakeDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			gotEmailArg = args[0]
			return map[string]any{"id": "u1"}, nil
		},
	}
	s := newTestService(db)
	_, err := s.CreateUser(context.Background(), domain.CreateUserParams{Email: "user@example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotEmailArg != "user@example.com" {
		t.Fatalf("expected email to be preserved, got %#v", gotEmailArg)
	}
}

type sessionCtxKey struct{}

// sessionRecordingDB records the DB session every auth query runs under.
type sessionRecordingDB struct {
	fakeDB
	sessions []domain.Session
}

func (d *sessionRecordingDB) WithRLS(ctx context.Context, s domain.Session) (context.Context, error) {
	return context.WithValue(ctx, sessionCtxKey{}, s), nil
}

func (d *sessionRecordingDB) record(ctx context.Context) {
	s, _ := ctx.Value(sessionCtxKey{}).(domain.Session)
	d.sessions = append(d.sessions, s)
}

func (d *sessionRecordingDB) Exec(ctx context.Context, q string, args ...any) (int64, error) {
	d.record(ctx)
	return 1, nil
}

func (d *sessionRecordingDB) QueryRow(ctx context.Context, q string, args ...any) (map[string]any, error) {
	d.record(ctx)
	return nil, nil
}

func (d *sessionRecordingDB) Query(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
	d.record(ctx)
	return nil, nil
}

// Regression: auth_handler.go issued refresh-token and signup-token writes on
// context.Background(), which the request pool runs as anon.
func TestService_BareContextRunsAsServiceRole(t *testing.T) {
	db := &sessionRecordingDB{}
	svc := newTestService(db)
	ctx := context.Background()
	exp := time.Now().Add(time.Hour).Unix()

	_ = svc.InsertRefreshToken(ctx, "u1", "tok", domain.SessionMeta{}, exp)
	_ = svc.CreateOneTimeToken(ctx, "u1", "tok", "signup", exp)
	_, _ = svc.GetUserByID(ctx, "u1")
	_, _ = svc.ListIdentities(ctx, "u1")

	if len(db.sessions) != 4 {
		t.Fatalf("want 4 queries, got %d", len(db.sessions))
	}
	for i, s := range db.sessions {
		if s.Role != domain.JWTRoleService || !s.IsAuthenticated {
			t.Errorf("query %d ran as %+v, want service_role", i, s)
		}
	}
}

func TestService_OverridesCallerSession(t *testing.T) {
	db := &sessionRecordingDB{}
	svc := newTestService(db)
	ctx, _ := db.WithRLS(context.Background(), domain.Session{Role: domain.JWTRoleAnon})

	_, _ = svc.GetUserByID(ctx, "u1")

	if len(db.sessions) != 1 || db.sessions[0].Role != domain.JWTRoleService {
		t.Fatalf("caller's anon session leaked into auth query: %+v", db.sessions)
	}
}

var errWithRLS = errors.New("withrls boom")

// erroringWithRLSDB simulates a WithRLS failure (e.g. a poisoned session GUC).
type erroringWithRLSDB struct {
	fakeDB
}

func (d *erroringWithRLSDB) WithRLS(ctx context.Context, s domain.Session) (context.Context, error) {
	return ctx, errWithRLS
}

// Regression: pin() must fail closed. A WithRLS error must never let a query
// run under the caller's ambient (unpinned) session.
func TestService_PinFailsClosedOnWithRLSError(t *testing.T) {
	ran := false
	db := &erroringWithRLSDB{
		fakeDB: fakeDB{
			queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
				ran = true
				return nil, nil
			},
		},
	}
	svc := newTestService(db)

	_, err := svc.GetUserByID(context.Background(), "u1")

	if ran {
		t.Fatal("query ran despite WithRLS error")
	}
	if !errors.Is(err, errWithRLS) {
		t.Fatalf("want errWithRLS, got %v", err)
	}
}

func TestInsertRefreshToken_PersistsAALAndAMR(t *testing.T) {
	var got []any
	s := newTestService(&fakeDB{execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
		got = args
		return 1, nil
	}})
	meta := domain.SessionMeta{SessionID: "s1", AAL: "aal2", AMR: []domain.AMREntry{{Method: "totp", Timestamp: 7}}}
	if err := s.InsertRefreshToken(context.Background(), "u1", "raw", meta, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if got[6] != "aal2" || got[7] != `[{"method":"totp","timestamp":7}]` {
		t.Fatalf("aal/amr args = %v / %v", got[6], got[7])
	}
}

func TestInsertRefreshToken_DefaultsEmptySessionState(t *testing.T) {
	var got []any
	s := newTestService(&fakeDB{execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
		got = args
		return 1, nil
	}})
	_ = s.InsertRefreshToken(context.Background(), "u1", "raw", domain.SessionMeta{}, time.Now().Unix())
	if got[6] != "aal1" || got[7] != "[]" {
		t.Fatalf("defaults = %v / %v, want aal1 / []", got[6], got[7])
	}
}

// refreshDB scripts the rotate UPDATE, the fallback SELECT, and the user fetch.
func refreshDB(rotated, existing map[string]any, execs *[]string) *fakeDB {
	return &fakeDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			switch {
			case strings.HasPrefix(q, "UPDATE auth.refresh_tokens"):
				return rotated, nil
			case strings.Contains(q, "FROM auth.refresh_tokens"):
				return existing, nil
			case strings.Contains(q, "FROM auth.users"):
				return map[string]any{"id": "u1"}, nil
			}
			return nil, nil
		},
		execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
			*execs = append(*execs, q)
			return 1, nil
		},
	}
}

func TestConsumeRefreshToken_RotatesAndReturnsSessionState(t *testing.T) {
	var execs []string
	s := newTestService(refreshDB(map[string]any{
		"user_id": "u1", "session_id": "s1", "aal": "aal2",
		"amr":        []any{map[string]any{"method": "totp", "timestamp": float64(9)}},
		"expires_at": time.Now().Add(time.Hour),
	}, nil, &execs))
	row, meta, err := s.ConsumeRefreshToken(context.Background(), "raw")
	if err != nil || row["id"] != "u1" {
		t.Fatalf("row=%v err=%v", row, err)
	}
	if meta.SessionID != "s1" || meta.AAL != "aal2" || len(meta.AMR) != 1 || meta.AMR[0].Method != "totp" {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestConsumeRefreshToken_ReuseWithinGraceSucceeds(t *testing.T) {
	var execs []string
	s := newTestService(refreshDB(nil, map[string]any{
		"user_id": "u1", "session_id": "s1", "aal": "aal1", "amr": "[]",
		"expires_at": time.Now().Add(time.Hour), "revoked_at": time.Now().Add(-2 * time.Second),
	}, &execs))
	if _, _, err := s.ConsumeRefreshToken(context.Background(), "raw"); err != nil {
		t.Fatalf("concurrent refresh inside grace must succeed, got %v", err)
	}
	for _, q := range execs {
		if !strings.Contains(q, "expires_at < NOW()") {
			t.Fatalf("grace reuse may only prune expired rows, got %q", q)
		}
	}
}

func TestConsumeRefreshToken_ReuseAfterGraceRevokesFamily(t *testing.T) {
	var execs []string
	s := newTestService(refreshDB(nil, map[string]any{
		"user_id": "u1", "session_id": "s1", "aal": "aal1", "amr": "[]",
		"expires_at": time.Now().Add(time.Hour), "revoked_at": time.Now().Add(-time.Minute),
	}, &execs))
	_, _, err := s.ConsumeRefreshToken(context.Background(), "raw")
	if !errors.Is(err, domain.ErrRefreshReuse) {
		t.Fatalf("want ErrRefreshReuse, got %v", err)
	}
	if len(execs) != 1 || !strings.Contains(execs[0], "WHERE session_id = $1") {
		t.Fatalf("expected one session-family revoke, got %v", execs)
	}
}

func TestConsumeRefreshToken_LegacyNullSessionRevokesAllUserTokens(t *testing.T) {
	var execs []string
	s := newTestService(refreshDB(nil, map[string]any{
		"user_id": "u1", "session_id": nil, "aal": "aal1", "amr": "[]",
		"expires_at": time.Now().Add(time.Hour), "revoked_at": time.Now().Add(-time.Minute),
	}, &execs))
	_, _, err := s.ConsumeRefreshToken(context.Background(), "raw")
	if !errors.Is(err, domain.ErrRefreshReuse) || len(execs) != 1 || !strings.Contains(execs[0], "WHERE user_id = $1") {
		t.Fatalf("err=%v execs=%v", err, execs)
	}
}

func TestConsumeRefreshToken_UnknownAndExpired(t *testing.T) {
	var execs []string
	s := newTestService(refreshDB(nil, nil, &execs))
	if _, _, err := s.ConsumeRefreshToken(context.Background(), ""); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("empty/unknown token: want ErrUnauthorized, got %v", err)
	}
	s = newTestService(refreshDB(map[string]any{
		"user_id": "u1", "session_id": "s1", "aal": "aal1", "amr": "[]", "expires_at": time.Now().Add(-time.Second),
	}, nil, &execs))
	if _, _, err := s.ConsumeRefreshToken(context.Background(), "raw"); !errors.Is(err, domain.ErrRefreshExpired) {
		t.Fatalf("expired: want ErrRefreshExpired, got %v", err)
	}
}

func TestParseAMR(t *testing.T) {
	cases := map[string]any{
		"nil":    nil,
		"string": `[{"method":"password","timestamp":1}]`,
		"bytes":  []byte(`[{"method":"password","timestamp":1}]`),
		"claim":  []any{map[string]any{"method": "password", "timestamp": float64(1)}},
		"junk":   "not json",
	}
	for name, in := range cases {
		got := domain.ParseAMR(in)
		switch name {
		case "nil", "junk":
			if len(got) != 0 {
				t.Errorf("%s: want empty, got %v", name, got)
			}
		default:
			if len(got) != 1 || got[0] != (domain.AMREntry{Method: "password", Timestamp: 1}) {
				t.Errorf("%s: got %v", name, got)
			}
		}
	}
}
