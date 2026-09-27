package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/instancez/instancez/internal/domain"
)

// fakeDB is a minimal domain.Database stub with hookable QueryRow/Exec used to
// exercise the auth Service SQL-orchestration logic without a real Postgres.
type fakeDB struct {
	queryRowFn func(ctx context.Context, q string, args ...any) (map[string]any, error)
	queryFn    func(ctx context.Context, q string, args ...any) ([]map[string]any, error)
	execFn     func(ctx context.Context, q string, args ...any) (int64, error)
	committed  bool
}

// fakeTx runs statements through its fakeDB and records Commit.
type fakeTx struct{ db *fakeDB }

func (t fakeTx) Query(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
	return t.db.Query(ctx, q, args...)
}
func (t fakeTx) QueryRow(ctx context.Context, q string, args ...any) (map[string]any, error) {
	return t.db.QueryRow(ctx, q, args...)
}
func (t fakeTx) Exec(ctx context.Context, q string, args ...any) (int64, error) {
	return t.db.Exec(ctx, q, args...)
}
func (t fakeTx) Commit(ctx context.Context) error   { t.db.committed = true; return nil }
func (t fakeTx) Rollback(ctx context.Context) error { return nil }

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
func (f *fakeDB) Begin(ctx context.Context) (domain.Tx, error) { return fakeTx{f}, nil }

func newTestService(db domain.Database) *Service {
	return NewService(db, &domain.Config{Auth: &domain.Auth{}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func otpRow(attempts int64) map[string]any {
	return map[string]any{"id": int64(7), "user_id": "u1", "purpose": "magiclink",
		"expires_at": time.Now().Add(time.Hour), "token": "longtoken", "code": "123456", "attempts": attempts}
}

// TestVerifyOTP_NumericCodeAttemptLimit checks a wrong guess spends an attempt
// in one capped UPDATE and leaves the code alive.
func TestVerifyOTP_NumericCodeAttemptLimit(t *testing.T) {
	var spendQ string
	var spendArgs []any
	deleted := false
	s := newTestService(&fakeDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			spendQ, spendArgs = q, args
			return otpRow(1), nil
		},
		execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
			deleted = deleted || strings.Contains(q, "DELETE")
			return 1, nil
		},
	})
	if _, err := s.VerifyOTP(context.Background(), "000000", "otp@example.com", []string{"signup", "magiclink"}); err != domain.ErrInvalidToken {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
	if !strings.Contains(spendQ, "SET attempts = attempts + 1") || !strings.Contains(spendQ, "attempts < $2") {
		t.Errorf("attempt must be spent atomically under the cap: %s", spendQ)
	}
	if len(spendArgs) != 2 || spendArgs[0] != "otp@example.com" || spendArgs[1] != maxOTPAttempts {
		t.Errorf("args = %v", spendArgs)
	}
	if deleted {
		t.Error("token should not be deleted on the first wrong guess")
	}
}

// TestVerifyOTP_BurnsTokenAtCap checks a spent budget rejects even the right code.
func TestVerifyOTP_BurnsTokenAtCap(t *testing.T) {
	s := newTestService(&fakeDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) { return nil, nil },
		execFn:     func(ctx context.Context, q string, args ...any) (int64, error) { return 1, nil },
	})
	if _, err := s.VerifyOTP(context.Background(), "123456", "otp@example.com", nil); err != domain.ErrInvalidToken {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
}

func TestVerifyOTP_CorrectCodeOnLastAttemptSucceeds(t *testing.T) {
	s := newTestService(&fakeDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			return otpRow(maxOTPAttempts), nil
		},
		execFn: func(ctx context.Context, q string, args ...any) (int64, error) { return 1, nil },
	})
	if row, err := s.VerifyOTP(context.Background(), "123456", "otp@example.com", nil); err != nil || row.UserID != "u1" {
		t.Fatalf("row=%v err=%v", row, err)
	}
}

func TestVerifyOTP_WrongCodeOnLastAttemptBurns(t *testing.T) {
	deleted := false
	s := newTestService(&fakeDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			return otpRow(maxOTPAttempts), nil
		},
		execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
			deleted = deleted || strings.Contains(q, "DELETE")
			return 1, nil
		},
	})
	if _, err := s.VerifyOTP(context.Background(), "000000", "otp@example.com", nil); err != domain.ErrInvalidToken || !deleted {
		t.Fatalf("err=%v deleted=%v", err, deleted)
	}
}

func TestVerifyOTP_LosingConsumeRaceIsInvalid(t *testing.T) {
	for name, tok := range map[string]string{"code": "123456", "opaque": "aaaaaaaabbbbbbbbccccccccdddddddd"} {
		s := newTestService(&fakeDB{
			queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) { return otpRow(1), nil },
			execFn:     func(ctx context.Context, q string, args ...any) (int64, error) { return 0, nil },
		})
		if _, err := s.VerifyOTP(context.Background(), tok, "otp@example.com", nil); err != domain.ErrInvalidToken {
			t.Errorf("%s: second consumer must fail, got %v", name, err)
		}
	}
}

func TestDeleteOneTimeToken_ReportsMissing(t *testing.T) {
	for affected, want := range map[int64]error{1: nil, 0: domain.ErrInvalidToken} {
		s := newTestService(&fakeDB{execFn: func(ctx context.Context, q string, args ...any) (int64, error) { return affected, nil }})
		if err := s.DeleteOneTimeToken(context.Background(), "t"); !errors.Is(err, want) {
			t.Errorf("affected=%d: got %v", affected, err)
		}
	}
}

func TestRecentOTPSent(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name string
		row  map[string]any
		err  error
		want bool
	}{
		{"hit", map[string]any{"hit": int64(1)}, nil, true},
		{"miss", nil, nil, false},
		{"db error", nil, boom, false},
	} {
		var gotQ string
		var gotArgs []any
		s := newTestService(&fakeDB{queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			gotQ, gotArgs = q, args
			return tc.row, tc.err
		}})
		got, err := s.RecentOTPSent(context.Background(), "u1", "recovery", 90*time.Second)
		if got != tc.want || !errors.Is(err, tc.err) {
			t.Errorf("%s: got %v, %v", tc.name, got, err)
		}
		if !strings.Contains(gotQ, "NOW()") || len(gotArgs) != 3 || gotArgs[0] != "u1" || gotArgs[1] != "recovery" || gotArgs[2] != 90.0 {
			t.Errorf("%s: must use DB time per (user, purpose): %s %v", tc.name, gotQ, gotArgs)
		}
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

func TestValidateChallenge_SpendsAttemptAtomically(t *testing.T) {
	var q string
	s := newTestService(&fakeDB{queryRowFn: func(ctx context.Context, sql string, args ...any) (map[string]any, error) {
		q = sql
		return map[string]any{"id": "c1"}, nil
	}})
	if err := s.ValidateChallenge(context.Background(), "c1", "f1"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"UPDATE auth.mfa_challenges", "attempts = attempts + 1", "verified_at IS NULL", "attempts < $4"} {
		if !strings.Contains(q, want) {
			t.Errorf("reserve query missing %q: %s", want, q)
		}
	}
}

func TestValidateChallenge_ClassifiesRejection(t *testing.T) {
	cases := map[string]struct {
		row  map[string]any
		want error
	}{
		"missing":      {nil, domain.ErrNotFound},
		"other factor": {map[string]any{"factor_id": "f2", "created_at": time.Now(), "attempts": int64(0)}, domain.ErrNotFound},
		"used":         {map[string]any{"factor_id": "f1", "verified_at": time.Now(), "created_at": time.Now(), "attempts": int64(1)}, domain.ErrChallengeUsed},
		"expired":      {map[string]any{"factor_id": "f1", "created_at": time.Now().Add(-10 * time.Minute), "attempts": int64(0)}, domain.ErrChallengeExpired},
		"at cap":       {map[string]any{"factor_id": "f1", "created_at": time.Now(), "attempts": int64(maxMFAAttempts)}, domain.ErrChallengeTooManyAttempts},
	}
	for name, tc := range cases {
		s := newTestService(&fakeDB{queryRowFn: func(ctx context.Context, sql string, args ...any) (map[string]any, error) {
			if strings.HasPrefix(sql, "UPDATE") {
				return nil, nil
			}
			return tc.row, nil
		}})
		if err := s.ValidateChallenge(context.Background(), "c1", "f1"); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v want %v", name, err, tc.want)
		}
	}
}

func TestMarkChallengeVerified_LosingRaceIsUsed(t *testing.T) {
	for affected, want := range map[int64]error{1: nil, 0: domain.ErrChallengeUsed} {
		s := newTestService(&fakeDB{execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
			if !strings.Contains(q, "verified_at IS NULL") {
				t.Errorf("mark must be conditional: %s", q)
			}
			return affected, nil
		}})
		if err := s.MarkChallengeVerified(context.Background(), "c1"); !errors.Is(err, want) {
			t.Errorf("affected=%d: got %v want %v", affected, err, want)
		}
	}
}

func TestConsumeTOTPStep(t *testing.T) {
	for affected, want := range map[int64]bool{1: true, 0: false} {
		s := newTestService(&fakeDB{execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
			if !strings.Contains(q, "last_totp_step < $2") || args[1] != int64(42) {
				t.Errorf("bad query/args: %s %v", q, args)
			}
			return affected, nil
		}})
		got, err := s.ConsumeTOTPStep(context.Background(), "f1", 42)
		if err != nil || got != want {
			t.Errorf("affected=%d: got %v,%v", affected, got, err)
		}
	}
}

func TestCreateChallenge_LocksFactorAndChecksWindowCap(t *testing.T) {
	var lockQ, insertQ string
	var lockArgs, insertArgs []any
	s := newTestService(&fakeDB{queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
		switch {
		case strings.Contains(q, "FOR UPDATE"):
			lockQ, lockArgs = q, args
			return map[string]any{"exists": int64(1)}, nil
		case strings.Contains(q, "INSERT INTO auth.mfa_challenges"):
			insertQ, insertArgs = q, args
			return map[string]any{"id": "c1", "created_at": time.Now()}, nil
		}
		return nil, nil
	}})
	id, _, err := s.CreateChallenge(context.Background(), "f1", "u1")
	if err != nil || id != "c1" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if !strings.Contains(lockQ, "auth.mfa_factors") || !strings.Contains(lockQ, "user_id = $2") {
		t.Errorf("lock query missing ownership check: %s", lockQ)
	}
	if lockArgs[0] != "f1" || lockArgs[1] != "u1" {
		t.Errorf("lock args = %v", lockArgs)
	}
	for _, want := range []string{"count(*)", "< $3", "created_at >"} {
		if !strings.Contains(insertQ, want) {
			t.Errorf("insert query missing %q: %s", want, insertQ)
		}
	}
	if insertArgs[2] != maxChallengesPerFactor {
		t.Errorf("cap arg = %v, want %d", insertArgs[2], maxChallengesPerFactor)
	}
}

func TestCreateChallenge_UnknownOrForeignFactor(t *testing.T) {
	s := newTestService(&fakeDB{queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
		return nil, nil // the FOR UPDATE lock finds no matching (id, user_id) row
	}})
	if _, _, err := s.CreateChallenge(context.Background(), "f1", "u1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
}

func TestCreateChallenge_AtCapIsRateLimited(t *testing.T) {
	s := newTestService(&fakeDB{queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
		if strings.Contains(q, "FOR UPDATE") {
			return map[string]any{"exists": int64(1)}, nil
		}
		return nil, nil // the capped insert's WHERE was false, no row returned
	}})
	if _, _, err := s.CreateChallenge(context.Background(), "f1", "u1"); !errors.Is(err, domain.ErrChallengeRateLimited) {
		t.Fatalf("got %v want ErrChallengeRateLimited", err)
	}
}

func TestDeleteFactorForUser_ConditionOnStatusOrAAL2(t *testing.T) {
	var q string
	var args []any
	s := newTestService(&fakeDB{execFn: func(ctx context.Context, sql string, a ...any) (int64, error) {
		q, args = sql, a
		return 1, nil
	}})
	if err := s.DeleteFactorForUser(context.Background(), "f1", "u1", true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"status = 'unverified'", "OR $3"} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q: %s", want, q)
		}
	}
	if args[2] != true {
		t.Errorf("allowVerified arg = %v, want true", args[2])
	}
}

func TestDeleteFactorForUser_ZeroRowsIsNotFound(t *testing.T) {
	s := newTestService(&fakeDB{execFn: func(ctx context.Context, sql string, a ...any) (int64, error) {
		return 0, nil
	}})
	if err := s.DeleteFactorForUser(context.Background(), "f1", "u1", false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
}

// TestCreateUser_AnonymousEmailIsNULLNotEmptyString: email is UNIQUE, and
// Postgres treats ” as a real value subject to that constraint (unlike NULL,
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

// refreshDB scripts the rotate UPDATE, the parent lookup, the grace child lookup and the user fetch.
func refreshDB(rotated, parent, child map[string]any, execs *[]string) *fakeDB {
	return &fakeDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			switch {
			case strings.HasPrefix(q, "UPDATE auth.refresh_tokens"):
				return rotated, nil
			case strings.Contains(q, "AS in_grace"):
				return parent, nil
			case strings.Contains(q, "ORDER BY id DESC"):
				return child, nil
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

var testRotation = domain.RefreshRotation{Token: "child-new", ExpiresAt: 1 << 40}

func TestConsumeRefreshToken_RotatesAndReturnsSessionState(t *testing.T) {
	var execs []string
	db := refreshDB(map[string]any{
		"user_id": "u1", "session_id": "s1", "aal": "aal2", "expired": false,
		"amr": []any{map[string]any{"method": "totp", "timestamp": float64(9)}},
	}, nil, nil, &execs)
	row, meta, rt, err := newTestService(db).ConsumeRefreshToken(context.Background(), "raw", testRotation)
	if err != nil || row["id"] != "u1" || rt != "child-new" {
		t.Fatalf("row=%v rt=%q err=%v", row, rt, err)
	}
	if meta.SessionID != "s1" || meta.AAL != "aal2" || len(meta.AMR) != 1 || meta.AMR[0].Method != "totp" {
		t.Fatalf("meta = %+v", meta)
	}
	if len(execs) == 0 || !strings.HasPrefix(execs[0], "INSERT INTO auth.refresh_tokens") {
		t.Fatalf("child not inserted: %v", execs)
	}
	if !db.committed {
		t.Fatal("rotation must commit")
	}
}

func TestConsumeRefreshToken_LegacyNullSessionGetsUUIDOnRotate(t *testing.T) {
	var inserted []any
	db := refreshDB(map[string]any{"user_id": "u1", "session_id": nil, "aal": nil, "amr": nil, "expired": false}, nil, nil, new([]string))
	db.execFn = func(ctx context.Context, q string, args ...any) (int64, error) {
		if strings.HasPrefix(q, "INSERT") {
			inserted = args
		}
		return 1, nil
	}
	_, meta, _, err := newTestService(db).ConsumeRefreshToken(context.Background(), "raw", testRotation)
	if err != nil {
		t.Fatal(err)
	}
	if _, perr := uuid.Parse(meta.SessionID); perr != nil || inserted[2] != meta.SessionID {
		t.Fatalf("sid %q (insert %v) must be a uuid", meta.SessionID, inserted[2])
	}
	if meta.AAL != "aal1" || inserted[6] != "aal1" || inserted[7] != "[]" {
		t.Fatalf("defaults lost: meta=%+v insert=%v", meta, inserted)
	}
}

func TestConsumeRefreshToken_ExpiredDoesNotBurnToken(t *testing.T) {
	var execs []string
	db := refreshDB(map[string]any{"user_id": "u1", "session_id": "s1", "aal": "aal1", "amr": "[]", "expired": true}, nil, nil, &execs)
	_, _, _, err := newTestService(db).ConsumeRefreshToken(context.Background(), "raw", testRotation)
	if !errors.Is(err, domain.ErrRefreshExpired) || db.committed || len(execs) != 0 {
		t.Fatalf("err=%v committed=%v execs=%v", err, db.committed, execs)
	}
}

func TestConsumeRefreshToken_ReuseWithinGraceReturnsCurrentChild(t *testing.T) {
	var execs []string
	var childArgs []any
	db := refreshDB(nil,
		map[string]any{"user_id": "u1", "session_id": "s1", "aal": "aal2", "in_grace": true},
		map[string]any{"token": "child-live", "session_id": "s1", "aal": "aal2", "amr": "[]"}, &execs)
	inner := db.queryRowFn
	db.queryRowFn = func(ctx context.Context, q string, args ...any) (map[string]any, error) {
		if strings.Contains(q, "ORDER BY id DESC") {
			childArgs = args
		}
		return inner(ctx, q, args...)
	}
	_, meta, rt, err := newTestService(db).ConsumeRefreshToken(context.Background(), "raw", testRotation)
	if err != nil || rt != "child-live" || meta.SessionID != "s1" {
		t.Fatalf("grace replay must return the live child, got rt=%q meta=%+v err=%v", rt, meta, err)
	}
	if childArgs[0] != "s1" || childArgs[1] != "aal2" {
		t.Fatalf("child lookup must stay in the parent's session and aal, got %v", childArgs)
	}
	for _, q := range execs {
		if !strings.Contains(q, "expires_at < NOW()") {
			t.Fatalf("grace reuse may not mint or revoke, got %q", q)
		}
	}
}

func TestConsumeRefreshToken_ReuseWithinGraceWithoutChildRevokesFamily(t *testing.T) {
	var execs []string
	db := refreshDB(nil, map[string]any{"user_id": "u1", "session_id": "s1", "aal": "aal1", "in_grace": true}, nil, &execs)
	_, _, _, err := newTestService(db).ConsumeRefreshToken(context.Background(), "raw", testRotation)
	if !errors.Is(err, domain.ErrRefreshReuse) || len(execs) != 1 || !strings.Contains(execs[0], "WHERE session_id = $1") {
		t.Fatalf("err=%v execs=%v", err, execs)
	}
}

func TestConsumeRefreshToken_ReuseAfterGraceRevokesFamily(t *testing.T) {
	var execs []string
	db := refreshDB(nil,
		map[string]any{"user_id": "u1", "session_id": "s1", "aal": "aal1", "in_grace": false},
		map[string]any{"token": "child-live"}, &execs)
	_, _, _, err := newTestService(db).ConsumeRefreshToken(context.Background(), "raw", testRotation)
	if !errors.Is(err, domain.ErrRefreshReuse) {
		t.Fatalf("want ErrRefreshReuse, got %v", err)
	}
	if len(execs) != 1 || !strings.Contains(execs[0], "WHERE session_id = $1") || !db.committed {
		t.Fatalf("expected one committed session-family revoke, got %v committed=%v", execs, db.committed)
	}
}

func TestConsumeRefreshToken_LegacyNullSessionRevokesAllUserTokens(t *testing.T) {
	var execs []string
	db := refreshDB(nil, map[string]any{"user_id": "u1", "session_id": nil, "aal": "aal1", "in_grace": true}, nil, &execs)
	_, _, _, err := newTestService(db).ConsumeRefreshToken(context.Background(), "raw", testRotation)
	if !errors.Is(err, domain.ErrRefreshReuse) || len(execs) != 1 || !strings.Contains(execs[0], "WHERE user_id = $1") {
		t.Fatalf("err=%v execs=%v", err, execs)
	}
}

func TestConsumeRefreshToken_UnknownAndEmpty(t *testing.T) {
	var execs []string
	s := newTestService(refreshDB(nil, nil, nil, &execs))
	for _, tok := range []string{"", "nope"} {
		if _, _, _, err := s.ConsumeRefreshToken(context.Background(), tok, testRotation); !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("%q: want ErrUnauthorized, got %v", tok, err)
		}
	}
	if len(execs) != 0 {
		t.Fatalf("unknown token must not write, got %v", execs)
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
