package app

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// keyRowsDB serves auth.jwt_keys rows and counts reloads.
type keyRowsDB struct {
	fakeDB
	rows    []map[string]any
	queries int
}

func (d *keyRowsDB) Query(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
	d.queries++
	return d.rows, nil
}

// blockingKeyDB holds every Query until release is closed.
type blockingKeyDB struct {
	fakeDB
	rows    []map[string]any
	entered chan struct{}
	release chan struct{}
	queries atomic.Int32
}

func newBlockingKeyDB(rows ...map[string]any) *blockingKeyDB {
	return &blockingKeyDB{rows: rows, entered: make(chan struct{}), release: make(chan struct{})}
}

func (d *blockingKeyDB) Query(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
	if d.queries.Add(1) == 1 {
		close(d.entered)
	}
	<-d.release
	return d.rows, nil
}

func (d *blockingKeyDB) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-d.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("key reload query never started")
	}
}

func keyRow(t *testing.T, k *JWTKey, retiredAt time.Time) map[string]any {
	t.Helper()
	row := map[string]any{
		"kid":        k.KID,
		"secret":     pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k.PrivateKey)}),
		"algorithm":  "RS256",
		"created_at": k.CreatedAt,
		"retired_at": nil,
	}
	if !retiredAt.IsZero() {
		row["retired_at"] = retiredAt
	}
	return row
}

func newKey(t *testing.T) *JWTKey {
	t.Helper()
	k, err := generateRS256Key()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func memManagerWith(t *testing.T, retiredAgo time.Duration) (*JWTKeyManager, string) {
	t.Helper()
	k := newKey(t)
	k.RetiredAt = time.Now().Add(-retiredAgo)
	return &JWTKeyManager{byKID: map[string]*JWTKey{k.KID: k}}, k.KID
}

func TestGet_RetiredKeyGrace(t *testing.T) {
	cases := []struct {
		name     string
		lifetime time.Duration
		ago      time.Duration
		ok       bool
	}{
		{"within configured lifetime", time.Hour, 50 * time.Minute, true},
		{"inside leeway", time.Hour, time.Hour + 10*time.Second, true},
		{"past lifetime plus leeway", time.Hour, time.Hour + time.Minute, false},
		{"default 15m, inside", 0, 14 * time.Minute, true},
		{"default 15m, past", 0, 20 * time.Minute, false},
		{"negative lifetime falls back to 15m, inside", -time.Hour, 14 * time.Minute, true},
		{"negative lifetime falls back to 15m, past", -time.Hour, 20 * time.Minute, false},
		{"huge lifetime does not overflow", math.MaxInt64, 100 * 365 * 24 * time.Hour, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, kid := memManagerWith(t, c.ago)
			m.SetMaxTokenLifetime(c.lifetime)
			_, err := m.Get(context.Background(), kid)
			if (err == nil) != c.ok {
				t.Fatalf("Get err = %v, want ok=%v", err, c.ok)
			}
		})
	}
}

func TestExpired_CutoffBoundary(t *testing.T) {
	m := &JWTKeyManager{}
	m.SetMaxTokenLifetime(time.Hour)
	k := &JWTKey{RetiredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	cutoff := k.RetiredAt.Add(time.Hour + verifyLeeway)
	if m.expired(k, cutoff) {
		t.Error("key rejected exactly at the cutoff")
	}
	if !m.expired(k, cutoff.Add(time.Nanosecond)) {
		t.Error("key accepted past the cutoff")
	}
	if m.expired(&JWTKey{}, cutoff.Add(1000*time.Hour)) {
		t.Error("active key treated as expired")
	}
}

func TestGet_EmptyAndUnknownKid(t *testing.T) {
	m, _ := memManagerWith(t, 0)
	if _, err := m.Get(context.Background(), ""); err == nil {
		t.Error("empty kid accepted")
	}
	if _, err := m.Get(context.Background(), "nope"); err == nil {
		t.Error("unknown kid accepted")
	}
}

func TestGet_UnknownKidsDoNotHammerDB(t *testing.T) {
	db := &keyRowsDB{}
	m := NewJWTKeyManager(db)
	for i := 0; i < 50; i++ {
		if _, err := m.Get(context.Background(), fmt.Sprintf("%016x", i)); err == nil {
			t.Fatal("unknown kid accepted")
		}
	}
	if db.queries != 1 {
		t.Fatalf("50 misses caused %d key loads, want 1", db.queries)
	}
}

func TestGet_MissPicksUpKeyMintedElsewhere(t *testing.T) {
	old := keyMissReloadInterval
	keyMissReloadInterval = 0
	t.Cleanup(func() { keyMissReloadInterval = old })

	k := newKey(t)
	db := &keyRowsDB{}
	m := NewJWTKeyManager(db)
	if _, err := m.Get(context.Background(), k.KID); err == nil {
		t.Fatal("kid found before it existed")
	}
	db.rows = []map[string]any{keyRow(t, k, time.Time{})}
	if _, err := m.Get(context.Background(), k.KID); err != nil {
		t.Fatalf("new kid not found on miss: %v", err)
	}
}

func TestGet_ReloadDropsActiveRetiredElsewhere(t *testing.T) {
	k := newKey(t)
	db := &keyRowsDB{rows: []map[string]any{keyRow(t, k, time.Now())}}
	m := NewJWTKeyManager(db)
	m.active = k
	m.byKID[k.KID] = k
	if _, err := m.Get(context.Background(), k.KID); err != nil {
		t.Fatalf("retired key within grace: %v", err)
	}
	if m.active != nil {
		t.Fatal("active key retired by another instance must be dropped so Active reloads")
	}
}

func TestGet_NotBlockedByInFlightReload(t *testing.T) {
	cachedKey, other := newKey(t), newKey(t)
	db := newBlockingKeyDB(keyRow(t, cachedKey, time.Time{}), keyRow(t, other, time.Time{}))
	m := NewJWTKeyManager(db)
	m.active = cachedKey
	m.byKID[cachedKey.KID] = cachedKey

	loader := make(chan error, 1)
	go func() {
		_, err := m.Get(context.Background(), other.KID)
		loader <- err
	}()
	db.waitEntered(t)

	done := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func() {
			if _, err := m.Active(context.Background()); err != nil {
				done <- err
				return
			}
			_, err := m.Get(context.Background(), cachedKey.KID)
			done <- err
		}()
	}
	for i := 0; i < 20; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("cached lookup: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("cached Get/Active blocked behind the reload query")
		}
	}
	close(db.release)
	if err := <-loader; err != nil {
		t.Fatalf("loading Get: %v", err)
	}
	if n := db.queries.Load(); n != 1 {
		t.Fatalf("concurrent callers ran %d reloads, want 1", n)
	}
}

func TestGet_MissWaitsForInFlightReload(t *testing.T) {
	k := newKey(t)
	db := newBlockingKeyDB(keyRow(t, k, time.Time{}))
	m := NewJWTKeyManager(db)

	go func() { _, _ = m.Get(context.Background(), "deadbeefdeadbeef") }()
	db.waitEntered(t)

	got := make(chan error, 1)
	go func() {
		_, err := m.Get(context.Background(), k.KID)
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("miss returned before the reload finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(db.release)
	if err := <-got; err != nil {
		t.Fatalf("kid from the in-flight reload not found: %v", err)
	}
}

func TestRefresh_StaleReloadKeepsConcurrentRotation(t *testing.T) {
	old := newKey(t)
	db := newBlockingKeyDB(keyRow(t, old, time.Time{}))
	m := NewJWTKeyManager(db)
	m.active = old
	m.byKID[old.KID] = old

	loader := make(chan struct{})
	go func() {
		_, _ = m.Get(context.Background(), old.KID)
		close(loader)
	}()
	db.waitEntered(t)
	rotated, err := m.RotateActive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	close(db.release)
	<-loader

	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active != rotated || m.byKID[rotated.KID] != rotated {
		t.Fatal("stale reload overwrote the rotated key")
	}
	if m.byKID[old.KID].RetiredAt.IsZero() {
		t.Fatal("stale reload cleared the retired stamp")
	}
	if !m.loadedAt.IsZero() {
		t.Fatal("discarded reload must force the next Get to reload")
	}
}

func TestRotateActive_StampsPreviousKeyRetired(t *testing.T) {
	m := NewJWTKeyManager(&fakeDB{})
	first, err := m.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.RotateActive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if m.byKID[first.KID].RetiredAt.IsZero() {
		t.Fatal("previous active key not marked retired in cache")
	}
	if !first.RetiredAt.IsZero() {
		t.Fatal("shared key mutated in place; must copy")
	}
	if !m.byKID[second.KID].RetiredAt.IsZero() {
		t.Fatal("new key marked retired")
	}
}

// readErrDB fails every row read, like a dropped connection.
type readErrDB struct{ fakeDB }

func (d *readErrDB) QueryRow(ctx context.Context, q string, args ...any) (map[string]any, error) {
	return nil, fmt.Errorf("conn reset")
}

func TestActive_LoadErrorDoesNotMintKey(t *testing.T) {
	db := &readErrDB{}
	m := NewJWTKeyManager(db)
	if key, err := m.Active(context.Background()); err == nil {
		t.Fatalf("Active returned key %v on a load error", kidOf(key))
	}
	if len(db.execs) != 0 {
		t.Fatalf("load error inserted a new key: %v", db.execs)
	}
	if m.active != nil {
		t.Fatal("load error cached an active key")
	}
}

func TestActive_NoRowMintsOnce(t *testing.T) {
	db := &fakeDB{}
	m := NewJWTKeyManager(db)
	first, err := m.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Active(context.Background())
	if err != nil || second != first {
		t.Fatalf("second Active = %v, %v; want cached key", kidOf(second), err)
	}
	if len(db.execs) != 1 || !strings.Contains(db.execs[0], "INSERT INTO auth.jwt_keys") {
		t.Fatalf("want exactly one insert, got %v", db.execs)
	}
}

// memKeysDB stores inserted keys so reloads see them, like a real table.
type memKeysDB struct {
	fakeDB
	mu   sync.Mutex
	rows []map[string]any
}

func (d *memKeysDB) Exec(ctx context.Context, q string, args ...any) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.execs = append(d.execs, q)
	d.rows = append(d.rows, map[string]any{"kid": args[0], "secret": args[1], "algorithm": args[2], "created_at": args[3]})
	return 1, nil
}

func (d *memKeysDB) Query(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]map[string]any(nil), d.rows...), nil
}

func (d *memKeysDB) QueryRow(ctx context.Context, q string, args ...any) (map[string]any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.rows) == 0 {
		return nil, nil
	}
	return d.rows[len(d.rows)-1], nil
}

func TestActive_ConcurrentOnEmptyTableMintsOnce(t *testing.T) {
	db := &memKeysDB{}
	m := NewJWTKeyManager(db)
	keys := make([]*JWTKey, 16)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			k, err := m.Active(context.Background())
			if err != nil {
				t.Error(err)
			}
			keys[i] = k
		}()
	}
	wg.Wait()
	for _, k := range keys {
		if k != keys[0] {
			t.Fatal("concurrent Active returned different keys")
		}
	}
	if len(db.execs) != 1 {
		t.Fatalf("want exactly one insert, got %d", len(db.execs))
	}
}

// rotatedElsewhereDB reports old as retired and next as the active key.
type rotatedElsewhereDB struct {
	fakeDB
	rows   []map[string]any
	active map[string]any
}

func (d *rotatedElsewhereDB) Query(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
	return d.rows, nil
}

func (d *rotatedElsewhereDB) QueryRow(ctx context.Context, q string, args ...any) (map[string]any, error) {
	return d.active, nil
}

func TestActive_PicksUpRotationElsewhere(t *testing.T) {
	old, next := newKey(t), newKey(t)
	db := &rotatedElsewhereDB{
		rows:   []map[string]any{keyRow(t, old, time.Now().Add(-time.Hour)), keyRow(t, next, time.Time{})},
		active: keyRow(t, next, time.Time{}),
	}
	m := NewJWTKeyManager(db)
	m.active = old
	m.byKID[old.KID] = old
	got, err := m.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.KID != next.KID {
		t.Fatalf("Active kept signing with %s, retired by another instance; want %s", got.KID, next.KID)
	}
	if len(db.execs) != 0 {
		t.Fatalf("Active minted a key instead of loading the new one: %v", db.execs)
	}
}
