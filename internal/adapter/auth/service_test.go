package auth

import (
	"context"
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
func (f *fakeDB) RecordMigration(ctx context.Context, checksum, sql, configJSON string) error {
	return nil
}
func (f *fakeDB) ExecDDL(ctx context.Context, sql string) error { return nil }
func (f *fakeDB) EnsureDataTable(ctx context.Context) error     { return nil }
func (f *fakeDB) GetAppliedData(ctx context.Context) ([]domain.DataRecord, error) {
	return nil, nil
}
func (f *fakeDB) RecordData(ctx context.Context, tx domain.Tx, key, tableName, source, checksum string, rowCount int) error {
	return nil
}
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
