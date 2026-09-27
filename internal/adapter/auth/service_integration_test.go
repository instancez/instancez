//go:build integration

package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

func newIntegrationService(t *testing.T, auth *domain.Auth) (*Service, domain.Database) {
	t.Helper()
	owner, req := dbboot.StartContainer(t)
	cfg := &domain.Config{Version: 1, Auth: auth}
	if err := app.NewMigrator(owner).Apply(context.Background(), cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Request pool, like production: every query switches to service_role.
	return NewService(req.Database, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))), owner.Database
}

func mustUser(t *testing.T, s *Service, email string) string {
	t.Helper()
	row, err := s.CreateUser(context.Background(), domain.CreateUserParams{Email: email})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return asString(row["id"])
}

func TestRefreshTokensIntegration(t *testing.T) {
	s, db := newIntegrationService(t, &domain.Auth{})
	ctx := context.Background()
	uid := mustUser(t, s, "refresh@example.com")
	exp := time.Now().Add(time.Hour).Unix()
	rot := func(tok string) domain.RefreshRotation { return domain.RefreshRotation{Token: tok, ExpiresAt: exp} }
	session := func(sid string) domain.SessionMeta {
		return domain.SessionMeta{SessionID: sid, AAL: "aal2", AMR: []domain.AMREntry{{Method: "totp", Timestamp: 5}}}
	}
	insert := func(t *testing.T, tok string, m domain.SessionMeta, exp int64) {
		t.Helper()
		if err := s.InsertRefreshToken(ctx, uid, tok, m, exp); err != nil {
			t.Fatal(err)
		}
	}
	backdateRevoked := func(t *testing.T, sid string) {
		t.Helper()
		if _, err := db.Exec(ctx, `UPDATE auth.refresh_tokens SET revoked_at = NOW() - INTERVAL '1 minute' WHERE session_id = $1 AND revoked_at IS NOT NULL`, sid); err != nil {
			t.Fatal(err)
		}
	}
	live := func(t *testing.T, sid string) []string {
		t.Helper()
		rows, err := db.Query(ctx, `SELECT token FROM auth.refresh_tokens WHERE session_id = $1 AND revoked_at IS NULL`, sid)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = asString(r["token"])
		}
		return out
	}

	t.Run("rotation carries session state to the new child", func(t *testing.T) {
		insert(t, "rt-1", session("sess-rot"), exp)
		_, got, rt, err := s.ConsumeRefreshToken(ctx, "rt-1", rot("rt-1-child"))
		if err != nil || rt != "rt-1-child" || got.SessionID != "sess-rot" || got.AAL != "aal2" || got.AMR[0].Method != "totp" {
			t.Fatalf("rt=%q meta=%+v err=%v", rt, got, err)
		}
		if l := live(t, "sess-rot"); len(l) != 1 || l[0] != "rt-1-child" {
			t.Fatalf("live tokens = %v, want [rt-1-child]", l)
		}
	})

	t.Run("concurrent refresh of one token converges on one live child", func(t *testing.T) {
		insert(t, "rt-race", session("sess-race"), exp)
		var wg sync.WaitGroup
		errs := make([]error, 8)
		got := make([]string, 8)
		for i := range errs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, _, got[i], errs[i] = s.ConsumeRefreshToken(ctx, "rt-race", rot(fmt.Sprintf("rt-race-child-%d", i)))
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
			}
			if got[i] != got[0] {
				t.Errorf("goroutine %d got %q, goroutine 0 got %q", i, got[i], got[0])
			}
		}
		if l := live(t, "sess-race"); len(l) != 1 || l[0] != got[0] {
			t.Fatalf("live tokens = %v, want exactly [%s]", l, got[0])
		}
	})

	t.Run("grace replay gets the legit child and a later old-chain use revokes the family", func(t *testing.T) {
		insert(t, "rt-stolen", session("sess-steal"), exp)
		if _, _, rt, err := s.ConsumeRefreshToken(ctx, "rt-stolen", rot("rt-legit")); err != nil || rt != "rt-legit" {
			t.Fatalf("legit rotation: rt=%q err=%v", rt, err)
		}
		if _, _, rt, err := s.ConsumeRefreshToken(ctx, "rt-stolen", rot("rt-fork")); err != nil || rt != "rt-legit" {
			t.Fatalf("grace replay must get the existing child, got rt=%q err=%v", rt, err)
		}
		if _, _, rt, err := s.ConsumeRefreshToken(ctx, "rt-legit", rot("rt-legit-2")); err != nil || rt != "rt-legit-2" {
			t.Fatalf("legit next rotation: rt=%q err=%v", rt, err)
		}
		backdateRevoked(t, "sess-steal")
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-legit", rot("x")); !errors.Is(err, domain.ErrRefreshReuse) {
			t.Fatalf("old-chain use after grace: want ErrRefreshReuse, got %v", err)
		}
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-legit-2", rot("y")); !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("family must be dead, got %v", err)
		}
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-stolen", rot("z")); !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("stolen token must be dead, got %v", err)
		}
	})

	t.Run("grace replay does not hand out a higher aal child", func(t *testing.T) {
		aal1 := domain.SessionMeta{SessionID: "sess-aal", AAL: "aal1"}
		insert(t, "rt-aal1", aal1, exp)
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-aal1", rot("rt-aal1-child")); err != nil {
			t.Fatal(err)
		}
		insert(t, "rt-aal2", domain.SessionMeta{SessionID: "sess-aal", AAL: "aal2"}, exp)
		_, m, rt, err := s.ConsumeRefreshToken(ctx, "rt-aal1", rot("x"))
		if err != nil || rt != "rt-aal1-child" || m.AAL != "aal1" {
			t.Fatalf("rt=%q aal=%q err=%v, want rt-aal1-child/aal1", rt, m.AAL, err)
		}
	})

	t.Run("reuse after grace revokes only that session family", func(t *testing.T) {
		insert(t, "rt-old", session("sess-a"), exp)
		insert(t, "rt-sibling", session("sess-a"), exp)
		insert(t, "rt-other", domain.SessionMeta{SessionID: "sess-b"}, exp)
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-old", rot("rt-old-child")); err != nil {
			t.Fatal(err)
		}
		backdateRevoked(t, "sess-a")
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-old", rot("x")); !errors.Is(err, domain.ErrRefreshReuse) {
			t.Fatalf("want ErrRefreshReuse, got %v", err)
		}
		for _, tok := range []string{"rt-sibling", "rt-old-child"} {
			if _, _, _, err := s.ConsumeRefreshToken(ctx, tok, rot("x")); !errors.Is(err, domain.ErrUnauthorized) {
				t.Fatalf("%s in revoked family must be dead, got %v", tok, err)
			}
		}
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-other", rot("rt-other-child")); err != nil {
			t.Fatalf("other session must survive, got %v", err)
		}
	})

	t.Run("rotation prunes expired revoked rows of the session", func(t *testing.T) {
		m := domain.SessionMeta{SessionID: "sess-prune"}
		insert(t, "rt-stale", m, exp)
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-stale", rot("rt-stale-child")); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `UPDATE auth.refresh_tokens SET expires_at = NOW() - INTERVAL '1 minute' WHERE session_id = 'sess-prune'`); err != nil {
			t.Fatal(err)
		}
		insert(t, "rt-live", m, exp)
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-live", rot("rt-live-child")); err != nil {
			t.Fatal(err)
		}
		r, err := db.QueryRow(ctx, `SELECT count(*) AS n FROM auth.refresh_tokens WHERE session_id = 'sess-prune' AND revoked_at IS NOT NULL AND expires_at < NOW()`)
		if err != nil || r["n"] != int64(0) {
			t.Fatalf("expired revoked rows left: %v (err %v)", r, err)
		}
	})

	t.Run("expired token is rejected without being burned", func(t *testing.T) {
		insert(t, "rt-exp", session("sess-exp"), time.Now().Add(-time.Minute).Unix())
		for i := 0; i < 2; i++ {
			if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-exp", rot("x")); !errors.Is(err, domain.ErrRefreshExpired) {
				t.Fatalf("attempt %d: want ErrRefreshExpired, got %v", i, err)
			}
		}
	})

	t.Run("failed child insert does not burn the token", func(t *testing.T) {
		insert(t, "rt-keep", session("sess-keep"), exp)
		if _, _, _, err := s.ConsumeRefreshToken(ctx, "rt-keep", rot("rt-keep")); err == nil {
			t.Fatal("duplicate child token must fail")
		}
		if l := live(t, "sess-keep"); len(l) != 1 || l[0] != "rt-keep" {
			t.Fatalf("token burned by failed rotation, live = %v", l)
		}
	})

	t.Run("unknown and empty tokens are rejected", func(t *testing.T) {
		for _, tok := range []string{"", "nope"} {
			if _, _, _, err := s.ConsumeRefreshToken(ctx, tok, rot("x")); !errors.Is(err, domain.ErrUnauthorized) {
				t.Errorf("%q: want ErrUnauthorized, got %v", tok, err)
			}
		}
	})
}

func TestBanIntegration(t *testing.T) {
	s, db := newIntegrationService(t, &domain.Auth{})
	ctx := context.Background()
	hash, _ := HashPassword("correct horse")
	row, err := s.CreateUser(ctx, domain.CreateUserParams{Email: "ban@example.com", Password: hash})
	if err != nil {
		t.Fatal(err)
	}
	uid := asString(row["id"])
	isBanned := func() bool {
		r, err := s.GetUserByID(ctx, uid)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := r["is_banned"].(bool)
		return b
	}
	yes, none, hour := true, "none", "1 hour"
	if isBanned() {
		t.Fatal("new user must not be banned")
	}
	_, _ = s.UpdateUser(ctx, uid, domain.UpdateUserParams{Banned: &yes})
	if !isBanned() {
		t.Fatal("permanent ban (infinity) not detected")
	}
	if r, _ := s.VerifyPassword(ctx, "ban@example.com", "correct horse"); r["is_banned"] != true {
		t.Fatalf("VerifyPassword must project is_banned, got %v", r["is_banned"])
	}
	_, _ = s.UpdateUser(ctx, uid, domain.UpdateUserParams{BanDuration: &none})
	if isBanned() {
		t.Fatal("ban_duration none must clear the ban")
	}
	_, _ = s.UpdateUser(ctx, uid, domain.UpdateUserParams{BanDuration: &hour})
	if !isBanned() {
		t.Fatal("timed ban not detected")
	}
	if _, err := db.Exec(ctx, `UPDATE auth.users SET banned_until = NOW() - INTERVAL '1 minute' WHERE id = $1::uuid`, uid); err != nil {
		t.Fatal(err)
	}
	if isBanned() {
		t.Fatal("expired ban must not block")
	}
}

func TestMFAIntegration(t *testing.T) {
	s, db := newIntegrationService(t, &domain.Auth{})
	ctx := context.Background()
	uid := mustUser(t, s, "mfa@example.com")
	f1, _ := s.EnrollFactor(ctx, uid, "a", "SECRET1")
	f2, _ := s.EnrollFactor(ctx, uid, "b", "SECRET2")

	t.Run("concurrent guesses never exceed the cap", func(t *testing.T) {
		ch, _, err := s.CreateChallenge(ctx, f1, uid)
		if err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		ok := 0
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if s.ValidateChallenge(ctx, ch, f1) == nil {
					mu.Lock()
					ok++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if ok != maxMFAAttempts {
			t.Fatalf("%d attempts allowed, want %d", ok, maxMFAAttempts)
		}
		if err := s.ValidateChallenge(ctx, ch, f1); !errors.Is(err, domain.ErrChallengeTooManyAttempts) {
			t.Fatalf("want too many attempts, got %v", err)
		}
	})

	t.Run("challenge verifies exactly once under concurrency", func(t *testing.T) {
		ch, _, _ := s.CreateChallenge(ctx, f1, uid)
		var mu sync.Mutex
		wins := 0
		var wg sync.WaitGroup
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if s.MarkChallengeVerified(ctx, ch) == nil {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if wins != 1 {
			t.Fatalf("%d winners, want 1", wins)
		}
		if err := s.ValidateChallenge(ctx, ch, f1); !errors.Is(err, domain.ErrChallengeUsed) {
			t.Fatalf("want used, got %v", err)
		}
	})

	t.Run("expired and foreign challenges rejected", func(t *testing.T) {
		ch, _, _ := s.CreateChallenge(ctx, f1, uid)
		if err := s.ValidateChallenge(ctx, ch, f2); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("wrong factor: %v", err)
		}
		_, _ = db.Exec(ctx, `UPDATE auth.mfa_challenges SET created_at = NOW() - INTERVAL '10 minutes' WHERE id = $1::uuid`, ch)
		if err := s.ValidateChallenge(ctx, ch, f1); !errors.Is(err, domain.ErrChallengeExpired) {
			t.Fatalf("want expired, got %v", err)
		}
		if _, _, err := s.CreateChallenge(ctx, f1, mustUser(t, s, "other@example.com")); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("foreign factor challenge: %v", err)
		}
	})

	t.Run("totp step is single-use and monotonic", func(t *testing.T) {
		steps := []struct {
			step int64
			want bool
		}{{100, true}, {100, false}, {99, false}, {101, true}}
		for _, st := range steps {
			if got, err := s.ConsumeTOTPStep(ctx, f1, st.step); err != nil || got != st.want {
				t.Fatalf("step %d: got %v,%v want %v", st.step, got, err, st.want)
			}
		}
		var mu sync.Mutex
		fresh := 0
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if ok, _ := s.ConsumeTOTPStep(ctx, f1, 200); ok {
					mu.Lock()
					fresh++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if fresh != 1 {
			t.Fatalf("same step accepted %d times", fresh)
		}
	})

	t.Run("promoting a factor drops other pending factors", func(t *testing.T) {
		if err := s.PromoteFactorToVerified(ctx, f1); err != nil {
			t.Fatal(err)
		}
		rows, _ := s.ListFactors(ctx, uid)
		if len(rows) != 1 || asString(rows[0]["id"]) != f1 || rows[0]["status"] != "verified" {
			t.Fatalf("factors after promote = %v", rows)
		}
	})

	t.Run("challenge creation is capped per factor in a window", func(t *testing.T) {
		f3, err := s.EnrollFactor(ctx, uid, "c", "SECRET3")
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < maxChallengesPerFactor; i++ {
			if _, _, err := s.CreateChallenge(ctx, f3, uid); err != nil {
				t.Fatalf("create %d: %v", i, err)
			}
		}
		if _, _, err := s.CreateChallenge(ctx, f3, uid); !errors.Is(err, domain.ErrChallengeRateLimited) {
			t.Fatalf("want rate limited, got %v", err)
		}
		// A second factor for the same user has its own budget.
		f4, _ := s.EnrollFactor(ctx, uid, "d", "SECRET4")
		if _, _, err := s.CreateChallenge(ctx, f4, uid); err != nil {
			t.Fatalf("sibling factor should be unaffected: %v", err)
		}
		// Once the window passes, the cap resets.
		if _, err := db.Exec(ctx, `UPDATE auth.mfa_challenges SET created_at = NOW() - INTERVAL '10 minutes' WHERE factor_id = $1::uuid`, f3); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.CreateChallenge(ctx, f3, uid); err != nil {
			t.Fatalf("after window: %v", err)
		}
	})

	t.Run("creating a challenge on a foreign factor is not found, not rate limited", func(t *testing.T) {
		f5, _ := s.EnrollFactor(ctx, uid, "e", "SECRET5")
		other := mustUser(t, s, "foreign-create@example.com")
		if _, _, err := s.CreateChallenge(ctx, f5, other); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("foreign caller: %v", err)
		}
	})

	t.Run("concurrent challenge creates on one factor never exceed the cap", func(t *testing.T) {
		f6, err := s.EnrollFactor(ctx, uid, "f", "SECRET6")
		if err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		ok, rateLimited := 0, 0
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _, err := s.CreateChallenge(ctx, f6, uid)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					ok++
				case errors.Is(err, domain.ErrChallengeRateLimited):
					rateLimited++
				default:
					t.Errorf("unexpected error: %v", err)
				}
			}()
		}
		wg.Wait()
		if ok != maxChallengesPerFactor || rateLimited != 20-maxChallengesPerFactor {
			t.Fatalf("ok=%d rateLimited=%d, want ok=%d", ok, rateLimited, maxChallengesPerFactor)
		}
	})

	t.Run("a verified factor survives an aal1 delete but not an aal2 one", func(t *testing.T) {
		f7, err := s.EnrollFactor(ctx, uid, "g", "SECRET7")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.PromoteFactorToVerified(ctx, f7); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteFactorForUser(ctx, f7, uid, false); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("aal1 delete of verified factor: %v", err)
		}
		rows, _ := s.ListFactors(ctx, uid)
		found := false
		for _, r := range rows {
			if asString(r["id"]) == f7 {
				found = true
			}
		}
		if !found {
			t.Fatal("verified factor was deleted by an aal1 caller")
		}
		if err := s.DeleteFactorForUser(ctx, f7, uid, true); err != nil {
			t.Fatalf("aal2 delete: %v", err)
		}
		rows, _ = s.ListFactors(ctx, uid)
		for _, r := range rows {
			if asString(r["id"]) == f7 {
				t.Fatal("verified factor survived an aal2 delete")
			}
		}
	})

	t.Run("an unverified factor can be deleted at aal1", func(t *testing.T) {
		f8, err := s.EnrollFactor(ctx, uid, "h", "SECRET8")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteFactorForUser(ctx, f8, uid, false); err != nil {
			t.Fatalf("aal1 delete of unverified factor: %v", err)
		}
	})

	t.Run("revoke below aal2 drops aal1 rows only", func(t *testing.T) {
		other := mustUser(t, s, "bystander@example.com")
		exp := time.Now().Add(time.Hour).Unix()
		seed := map[string]struct {
			user, sid, aal string
		}{
			"cur-aal1": {uid, "cur", "aal1"}, "cur-aal2": {uid, "cur", "aal2"},
			"old-aal1": {uid, "old", "aal1"}, "old-aal2": {uid, "old", "aal2"},
			"bystander": {other, "by", "aal1"},
		}
		alive := func() map[string]bool {
			rows, err := db.Query(ctx, `SELECT token FROM auth.refresh_tokens WHERE token = ANY($1)`,
				[]string{"cur-aal1", "cur-aal2", "old-aal1", "old-aal2", "bystander"})
			if err != nil {
				t.Fatal(err)
			}
			out := map[string]bool{}
			for _, r := range rows {
				out[asString(r["token"])] = true
			}
			return out
		}
		reseed := func() {
			for tok, r := range seed {
				_, _ = db.Exec(ctx, `DELETE FROM auth.refresh_tokens WHERE token = $1`, tok)
				if err := s.InsertRefreshToken(ctx, r.user, tok, domain.SessionMeta{SessionID: r.sid, AAL: r.aal}, exp); err != nil {
					t.Fatal(err)
				}
			}
		}
		cases := []struct {
			all  bool
			want []string
		}{
			{false, []string{"cur-aal2", "old-aal1", "old-aal2", "bystander"}},
			{true, []string{"cur-aal2", "old-aal2", "bystander"}},
		}
		for _, tc := range cases {
			reseed()
			if err := s.RevokeBelowAAL2(ctx, uid, "cur", tc.all); err != nil {
				t.Fatal(err)
			}
			got := alive()
			if len(got) != len(tc.want) {
				t.Fatalf("all=%v: alive %v want %v", tc.all, got, tc.want)
			}
			for _, w := range tc.want {
				if !got[w] {
					t.Fatalf("all=%v: alive %v want %v", tc.all, got, tc.want)
				}
			}
		}
	})
}

func TestOAuthIntegration(t *testing.T) {
	s, db := newIntegrationService(t, &domain.Auth{})
	ctx := context.Background()
	login := func(pid, email string, verified, signup bool) (map[string]any, error) {
		return s.UpsertOAuthUser(ctx, domain.OAuthLogin{Provider: "google", ProviderUserID: pid, Email: email, Name: "N", EmailVerified: verified, AllowSignup: signup})
	}
	col := func(email, expr string) any {
		r, _ := db.QueryRow(ctx, "SELECT "+expr+" AS v FROM auth.users WHERE email = $1", email)
		return r["v"]
	}
	identities := func(pid string) any {
		r, _ := db.QueryRow(ctx, "SELECT count(*) AS n FROM auth.identities WHERE provider_user_id = $1", pid)
		return r["n"]
	}

	t.Run("new verified login creates a verified user", func(t *testing.T) {
		row, err := login("g-new", "new@example.com", true, true)
		if err != nil || row["email_verified"] != true || identities("g-new") != int64(1) {
			t.Fatalf("row=%v err=%v", row, err)
		}
	})

	t.Run("known identity wins even if email changed and signups closed", func(t *testing.T) {
		first, _ := login("g-id", "first@example.com", true, true)
		again, err := login("g-id", "changed@elsewhere.com", false, false)
		if err != nil || again["id"] != first["id"] {
			t.Fatalf("again=%v err=%v", again, err)
		}
		if _, err := s.GetUserByEmail(ctx, "changed@elsewhere.com"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatal("a returning identity must not create a second user")
		}
	})

	t.Run("case-variant email links to verified local user", func(t *testing.T) {
		local, _ := s.CreateUser(ctx, domain.CreateUserParams{Email: "Carol@Example.com", EmailConfirmed: true})
		row, err := login("g-carol", "carol@EXAMPLE.com", true, false)
		if err != nil || row["id"] != local["id"] {
			t.Fatalf("row=%v err=%v", row, err)
		}
	})

	t.Run("verified case variant is preferred over an older unverified squatter", func(t *testing.T) {
		hash, _ := HashPassword("attacker-pass")
		_, _ = s.CreateUser(ctx, domain.CreateUserParams{Email: "DAVE@example.com", Password: hash})
		victim, _ := s.CreateUser(ctx, domain.CreateUserParams{Email: "dave@example.com", EmailConfirmed: true})
		row, err := login("g-dave", "dave@example.com", true, false)
		if err != nil || row["id"] != victim["id"] {
			t.Fatalf("row=%v err=%v", row, err)
		}
	})

	t.Run("unverified local account with password is never linked", func(t *testing.T) {
		hash, _ := HashPassword("attacker-pass")
		_, _ = s.CreateUser(ctx, domain.CreateUserParams{Email: "victim@example.com", Password: hash})
		if _, err := login("g-victim", "Victim@Example.com", true, true); !errors.Is(err, domain.ErrOAuthLinkRefused) {
			t.Fatalf("want ErrOAuthLinkRefused, got %v", err)
		}
		if col("victim@example.com", "password_hash") == nil || col("victim@example.com", "email_verified") != false {
			t.Fatal("refused link must not touch the local account")
		}
		if n := identities("g-victim"); n != int64(0) {
			t.Fatalf("no identity may be created, got %v", n)
		}
	})

	t.Run("unverified passwordless account is linked and verified", func(t *testing.T) {
		local, _ := s.CreateUser(ctx, domain.CreateUserParams{Email: "otp@example.com"})
		row, err := login("g-otp", "otp@example.com", true, false)
		if err != nil || row["id"] != local["id"] || row["email_verified"] != true {
			t.Fatalf("row=%v err=%v", row, err)
		}
		if _, ok := row["claimed"]; ok {
			t.Fatal("internal column leaked into the user row")
		}
	})

	t.Run("unverified passwordless account someone can already sign in to is refused", func(t *testing.T) {
		live, _ := s.CreateUser(ctx, domain.CreateUserParams{Email: "live@example.com"})
		exp := time.Now().Add(time.Hour).Unix()
		if err := s.InsertRefreshToken(ctx, asString(live["id"]), "live-tok", domain.SessionMeta{SessionID: "11111111-1111-1111-1111-111111111111"}, exp); err != nil {
			t.Fatal(err)
		}
		linked, _ := s.CreateUser(ctx, domain.CreateUserParams{Email: "linked@example.com"})
		s.LinkIdentity(ctx, asString(linked["id"]), "github", "gh-1", "linked@example.com")

		for _, email := range []string{"live@example.com", "linked@example.com"} {
			if _, err := login("g-"+email, email, true, true); !errors.Is(err, domain.ErrOAuthLinkRefused) {
				t.Errorf("%s: want ErrOAuthLinkRefused, got %v", email, err)
			}
			if col(email, "email_verified") != false {
				t.Errorf("%s: must stay unverified", email)
			}
		}
	})

	t.Run("unverified anonymous account with an email is refused", func(t *testing.T) {
		anon, err := s.CreateUser(ctx, domain.CreateUserParams{Anonymous: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, "UPDATE auth.users SET email = 'anon@example.com' WHERE id = $1::uuid", anon["id"]); err != nil {
			t.Fatal(err)
		}
		if _, err := login("g-anon", "anon@example.com", true, true); !errors.Is(err, domain.ErrOAuthLinkRefused) {
			t.Fatalf("want ErrOAuthLinkRefused, got %v", err)
		}
		if col("anon@example.com", "email_verified") != false || identities("g-anon") != int64(0) {
			t.Fatal("refused link must not touch the anonymous account")
		}
	})

	t.Run("unverified provider email without identity is refused", func(t *testing.T) {
		_, _ = s.CreateUser(ctx, domain.CreateUserParams{Email: "known@example.com", EmailConfirmed: true})
		for _, email := range []string{"unv@example.com", "known@example.com"} {
			if _, err := login("g-unv", email, false, true); !errors.Is(err, domain.ErrProviderEmailUnverified) {
				t.Fatalf("%s: got %v", email, err)
			}
		}
		if _, err := s.GetUserByEmail(ctx, "unv@example.com"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatal("no user may be created")
		}
		if n := identities("g-unv"); n != int64(0) {
			t.Fatalf("no identity may be created, got %v", n)
		}
	})

	t.Run("no match with signups closed is refused", func(t *testing.T) {
		if _, err := login("g-closed", "closed@example.com", true, false); !errors.Is(err, domain.ErrSignupDisabled) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("empty email with verified flag is refused and creates nothing", func(t *testing.T) {
		for _, signup := range []bool{false, true} {
			if _, err := login("g-empty", "", true, signup); !errors.Is(err, domain.ErrProviderEmailUnverified) {
				t.Fatalf("signup=%v: got %v", signup, err)
			}
		}
		if r, _ := db.QueryRow(ctx, "SELECT count(*) AS n FROM auth.users WHERE email = ''"); r["n"] != int64(0) {
			t.Fatalf("user with empty email created: %v", r["n"])
		}
	})

	t.Run("empty provider user id is rejected", func(t *testing.T) {
		if row, err := login("", "blank1@example.com", true, true); err == nil {
			t.Fatalf("blank id accepted: %v", row)
		}
		if n := identities(""); n != int64(0) {
			t.Fatalf("blank identity stored: %v", n)
		}
	})

	t.Run("concurrent first logins create one user", func(t *testing.T) {
		var wg sync.WaitGroup
		ids := make([]any, 8)
		for i := range ids {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				row, err := login("g-race", "race@example.com", true, true)
				if err != nil {
					t.Errorf("login %d: %v", i, err)
					return
				}
				ids[i] = row["id"]
			}(i)
		}
		wg.Wait()
		for _, id := range ids {
			if id != ids[0] {
				t.Fatalf("ids diverged: %v", ids)
			}
		}
	})
}

func TestOAuthFlowStateIntegration(t *testing.T) {
	s, _ := newIntegrationService(t, &domain.Auth{})
	ctx := context.Background()
	uid := mustUser(t, s, "linker@example.com")

	t.Run("round trip keeps linking user and is single use", func(t *testing.T) {
		if err := s.CreateOAuthFlowState(ctx, "st-1", "", "", "http://app/cb", uid); err != nil {
			t.Fatal(err)
		}
		flow, err := s.ConsumeOAuthFlowState(ctx, "st-1")
		if err != nil || flow.LinkingUserID != uid || flow.RedirectTo != "http://app/cb" || flow.CodeChallenge != "" {
			t.Fatalf("flow=%+v err=%v", flow, err)
		}
		if _, err := s.ConsumeOAuthFlowState(ctx, "st-1"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("reuse: want ErrNotFound, got %v", err)
		}
	})

	t.Run("unknown and empty state are not found", func(t *testing.T) {
		for _, st := range []string{"", "nope", "st'; DROP TABLE auth.flow_state; --"} {
			if _, err := s.ConsumeOAuthFlowState(ctx, st); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("%q: got %v", st, err)
			}
		}
	})

	t.Run("concurrent consumers get the state once", func(t *testing.T) {
		if err := s.CreateOAuthFlowState(ctx, "st-race", "", "", "", uid); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var wins atomic.Int32
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := s.ConsumeOAuthFlowState(ctx, "st-race"); err == nil {
					wins.Add(1)
				}
			}()
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("state consumed %d times", wins.Load())
		}
	})
}

func TestVerifyOTPReturnsPurposeIntegration(t *testing.T) {
	s, _ := newIntegrationService(t, &domain.Auth{Email: &domain.AuthEmail{}})
	ctx := context.Background()
	exp := time.Now().Add(time.Hour).Unix()
	for _, purpose := range []string{"magiclink", "signup"} {
		email := purpose + "-purpose@example.com"
		uid := mustUser(t, s, email)
		if err := s.CreateOneTimeToken(ctx, uid, "tok-"+purpose, purpose, exp); err != nil {
			t.Fatal(err)
		}
		if otp, err := s.VerifyOTP(ctx, "tok-"+purpose, "", nil); err != nil || otp.Purpose != purpose || otp.UserID != uid {
			t.Fatalf("opaque %s: otp=%+v err=%v", purpose, otp, err)
		}
		if err := s.CreateOTPCode(ctx, uid, "codetok-"+purpose, "123456", email, purpose, exp); err != nil {
			t.Fatal(err)
		}
		if otp, err := s.VerifyOTP(ctx, "123456", email, nil); err != nil || otp.Purpose != purpose || otp.UserID != uid {
			t.Fatalf("code %s: otp=%+v err=%v", purpose, otp, err)
		}
	}
}

func TestOTPIntegration(t *testing.T) {
	s, db := newIntegrationService(t, &domain.Auth{Email: &domain.AuthEmail{}})
	ctx := context.Background()
	exp := time.Now().Add(time.Hour).Unix()
	parallel := func(n int, fn func() bool) int {
		var wins atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if fn() {
					wins.Add(1)
				}
			}()
		}
		wg.Wait()
		return int(wins.Load())
	}
	mustCode := func(t *testing.T, uid, tok, code, email string) {
		t.Helper()
		if err := s.CreateOTPCode(ctx, uid, tok, code, email, "magiclink", exp); err != nil {
			t.Fatalf("create code: %v", err)
		}
	}

	t.Run("concurrent wrong guesses cannot exceed the cap", func(t *testing.T) {
		uid := mustUser(t, s, "guess@example.com")
		mustCode(t, uid, "tok-guess", "123456", "guess@example.com")
		parallel(20, func() bool { _, err := s.VerifyOTP(ctx, "000000", "guess@example.com", nil); return err == nil })
		if row, _ := db.QueryRow(ctx, `SELECT 1 AS x FROM auth.one_time_tokens WHERE token = 'tok-guess'`); row != nil {
			t.Fatal("code must be burned once the cap is spent")
		}
		if _, err := s.VerifyOTP(ctx, "123456", "guess@example.com", nil); !errors.Is(err, domain.ErrInvalidToken) {
			t.Fatalf("correct code after the cap: %v", err)
		}
	})

	t.Run("concurrent wrong guesses below the cap keep the last attempt", func(t *testing.T) {
		uid := mustUser(t, s, "edge@example.com")
		mustCode(t, uid, "tok-edge", "222222", "edge@example.com")
		parallel(maxOTPAttempts-1, func() bool { _, err := s.VerifyOTP(ctx, "000000", "edge@example.com", nil); return err == nil })
		if _, err := s.VerifyOTP(ctx, "222222", "edge@example.com", nil); err != nil {
			t.Fatalf("last attempt with the right code: %v", err)
		}
	})

	t.Run("spent budget rejects the correct code", func(t *testing.T) {
		uid := mustUser(t, s, "spent@example.com")
		mustCode(t, uid, "tok-spent", "333333", "spent@example.com")
		if _, err := db.Exec(ctx, `UPDATE auth.one_time_tokens SET attempts = $1 WHERE token = 'tok-spent'`, maxOTPAttempts); err != nil {
			t.Fatal(err)
		}
		if _, err := s.VerifyOTP(ctx, "333333", "spent@example.com", nil); !errors.Is(err, domain.ErrInvalidToken) {
			t.Fatalf("correct code past the cap: %v", err)
		}
		row, _ := db.QueryRow(ctx, `SELECT attempts FROM auth.one_time_tokens WHERE token = 'tok-spent'`)
		if row == nil || asInt64(row["attempts"]) != maxOTPAttempts {
			t.Fatalf("a rejected call must not spend past the cap: %v", row)
		}
	})

	t.Run("correct code consumed exactly once", func(t *testing.T) {
		uid := mustUser(t, s, "once@example.com")
		mustCode(t, uid, "tok-once", "654321", "once@example.com")
		if wins := parallel(20, func() bool { _, err := s.VerifyOTP(ctx, "654321", "once@example.com", nil); return err == nil }); wins != 1 {
			t.Fatalf("%d winners", wins)
		}
	})

	t.Run("opaque token consumed exactly once", func(t *testing.T) {
		uid := mustUser(t, s, "link@example.com")
		if err := s.CreateOneTimeToken(ctx, uid, "opaque-token-0123456789abcdef", "recovery", exp); err != nil {
			t.Fatal(err)
		}
		if wins := parallel(20, func() bool {
			_, err := s.VerifyOTP(ctx, "opaque-token-0123456789abcdef", "", []string{"recovery"})
			return err == nil
		}); wins != 1 {
			t.Fatalf("%d winners", wins)
		}
		if err := s.DeleteOneTimeToken(ctx, "opaque-token-0123456789abcdef"); !errors.Is(err, domain.ErrInvalidToken) {
			t.Fatalf("second delete: %v", err)
		}
	})

	t.Run("resend cooldown is per user and purpose", func(t *testing.T) {
		uid := mustUser(t, s, "cool@example.com")
		other := mustUser(t, s, "cool2@example.com")
		hit := func(uid, purpose string) bool {
			t.Helper()
			h, err := s.RecentOTPSent(ctx, uid, purpose, time.Minute)
			if err != nil {
				t.Fatalf("RecentOTPSent: %v", err)
			}
			return h
		}
		if hit(uid, "magiclink") {
			t.Fatal("no token yet")
		}
		mustCode(t, uid, "tok-cool", "111111", "cool@example.com")
		if !hit(uid, "magiclink") {
			t.Fatal("fresh token must trip the cooldown")
		}
		if hit(uid, "recovery") || hit(other, "magiclink") {
			t.Fatal("other purpose or user must not trip it")
		}
		if _, err := db.Exec(ctx, `UPDATE auth.one_time_tokens SET created_at = NOW() - INTERVAL '2 minutes' WHERE user_id = $1::uuid`, uid); err != nil {
			t.Fatal(err)
		}
		if hit(uid, "magiclink") {
			t.Fatal("cooldown must expire")
		}
	})
}
