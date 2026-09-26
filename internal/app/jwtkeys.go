package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/instancez/instancez/internal/domain"
)

// JWTKey is a signing key used to sign and verify JWTs.
type JWTKey struct {
	KID        string
	Secret     []byte // legacy HS256 secret; nil for RS256 keys
	Algorithm  string // "HS256" or "RS256"
	PrivateKey *rsa.PrivateKey
	PublicKey  *rsa.PublicKey
	CreatedAt  time.Time // when the signing key was created; zero if unknown
	RetiredAt  time.Time // zero while active
}

// SymmetricSecret returns a non-empty key suitable for HMAC operations that
// are not JWTs themselves (e.g. signed storage upload tokens). For HS256 keys
// it is the secret directly; for RS256 keys — where Secret is nil — it is
// derived deterministically from the private key material so the value stays
// stable across restarts yet remains unguessable to anyone without the key.
// Returns nil only when the key carries no usable secret material, in which
// case callers MUST fail closed rather than HMAC with an empty key.
func (k *JWTKey) SymmetricSecret() []byte {
	if k == nil {
		return nil
	}
	if len(k.Secret) > 0 {
		return k.Secret
	}
	if k.PrivateKey != nil {
		sum := sha256.Sum256(x509.MarshalPKCS1PrivateKey(k.PrivateKey))
		return sum[:]
	}
	return nil
}

// PublicJWK returns the public half of an RS256 key as a JWK map. It never
// includes private material, so it is safe to expose. Returns an error for keys
// that carry no RSA public key.
func (k *JWTKey) PublicJWK() (map[string]any, error) {
	if k == nil || k.PublicKey == nil {
		return nil, fmt.Errorf("jwt key: no RSA public key for kid %q", kidOf(k))
	}
	n := base64.RawURLEncoding.EncodeToString(k.PublicKey.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.PublicKey.E)).Bytes())
	return map[string]any{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": k.KID,
		"n":   n,
		"e":   e,
	}, nil
}

func kidOf(k *JWTKey) string {
	if k == nil {
		return ""
	}
	return k.KID
}

// JWTKeyManager loads and caches JWT signing keys from the database.
type JWTKeyManager struct {
	db               domain.Database
	maxTokenLifetime atomic.Int64 // nanoseconds; <= 0 means defaultMaxTokenLifetime

	mu       sync.RWMutex
	active   *JWTKey
	byKID    map[string]*JWTKey
	loadedAt time.Time
	loading  chan struct{} // non-nil while a reload query runs
	gen      uint64        // bumped by local key writes so a stale reload can't overwrite them
}

// Key cache reload intervals; vars so tests can shorten them.
var (
	keyReloadInterval     = 30 * time.Second
	keyMissReloadInterval = time.Second
)

const (
	defaultMaxTokenLifetime = 15 * time.Minute
	verifyLeeway            = 30 * time.Second // matches jwt.WithLeeway in verifySignedJWT
)

// SetMaxTokenLifetime sets how long a retired key keeps verifying (plus leeway).
func (m *JWTKeyManager) SetMaxTokenLifetime(d time.Duration) {
	m.maxTokenLifetime.Store(int64(d))
}

func (m *JWTKeyManager) retiredGrace() time.Duration {
	d := time.Duration(m.maxTokenLifetime.Load())
	if d <= 0 {
		d = defaultMaxTokenLifetime
	}
	if d > math.MaxInt64-verifyLeeway {
		return math.MaxInt64
	}
	return d + verifyLeeway
}

func (m *JWTKeyManager) expired(k *JWTKey, now time.Time) bool {
	return !k.RetiredAt.IsZero() && now.Sub(k.RetiredAt) > m.retiredGrace()
}

func (m *JWTKeyManager) cached(kid string) (*JWTKey, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	k, ok := m.byKID[kid]
	return k, ok
}

// refresh reloads verifiable keys when the cache is older than maxAge, querying without
// holding the lock. If a reload is already running, wait decides whether to block on it.
func (m *JWTKeyManager) refresh(ctx context.Context, maxAge time.Duration, wait bool) {
	if m.db == nil {
		return
	}
	m.mu.RLock()
	idle := m.loading == nil && time.Since(m.loadedAt) < maxAge
	m.mu.RUnlock()
	if idle {
		return
	}

	m.mu.Lock()
	if ch := m.loading; ch != nil {
		m.mu.Unlock()
		if wait {
			select {
			case <-ch:
			case <-ctx.Done():
			}
		}
		return
	}
	if time.Since(m.loadedAt) < maxAge {
		m.mu.Unlock()
		return
	}
	ch := make(chan struct{})
	m.loading = ch
	m.loadedAt = time.Now()
	gen := m.gen
	cutoff := m.loadedAt.Add(-m.retiredGrace())
	m.mu.Unlock()

	rows, err := m.db.Query(ctx,
		`SELECT kid, secret, algorithm, created_at, retired_at FROM auth.jwt_keys
		 WHERE retired_at IS NULL OR retired_at >= $1`, cutoff)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.loading = nil
	close(ch)
	if err != nil {
		return
	}
	if gen != m.gen {
		m.loadedAt = time.Time{}
		return
	}
	byKID := make(map[string]*JWTKey, len(rows))
	for _, row := range rows {
		if key, err := rowToKey(row); err == nil {
			byKID[key.KID] = key
		}
	}
	if m.active != nil {
		if k, ok := byKID[m.active.KID]; !ok || !k.RetiredAt.IsZero() {
			m.active = nil
		}
	}
	m.byKID = byKID
}

func NewJWTKeyManager(db domain.Database) *JWTKeyManager {
	return &JWTKeyManager{
		db:    db,
		byKID: make(map[string]*JWTKey),
	}
}

// NewInMemoryJWTKeyManager builds a key manager with a single pre-seeded
// RS256 key and no database backing. If privateKey is nil, generates one.
func NewInMemoryJWTKeyManager(kid string, privateKey *rsa.PrivateKey) (*JWTKeyManager, error) {
	if kid == "" {
		return nil, fmt.Errorf("jwt key: kid required")
	}
	if privateKey == nil {
		var err error
		privateKey, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, fmt.Errorf("jwt key: generate: %w", err)
		}
	}
	key := &JWTKey{
		KID:        kid,
		Algorithm:  "RS256",
		PrivateKey: privateKey,
		PublicKey:  &privateKey.PublicKey,
		CreatedAt:  time.Now().UTC(),
	}
	return &JWTKeyManager{
		active: key,
		byKID:  map[string]*JWTKey{kid: key},
	}, nil
}

// Active returns the current signing key, creating one on first use.
func (m *JWTKeyManager) Active(ctx context.Context) (*JWTKey, error) {
	m.mu.RLock()
	if m.active != nil {
		key := m.active
		m.mu.RUnlock()
		return key, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active != nil {
		return m.active, nil
	}

	// Try to load the most recent non-retired key.
	row, err := m.db.QueryRow(ctx,
		`SELECT kid, secret, algorithm, created_at FROM auth.jwt_keys
		 WHERE retired_at IS NULL ORDER BY created_at DESC LIMIT 1`)
	if err != nil {
		return nil, fmt.Errorf("jwt key: load active: %w", err)
	}
	if row != nil {
		key, kerr := rowToKey(row)
		if kerr != nil {
			return nil, kerr
		}
		m.active = key
		m.byKID[key.KID] = key
		return key, nil
	}

	// No key exists yet. Generate RS256 and insert.
	key, err := generateRS256Key()
	if err != nil {
		return nil, fmt.Errorf("jwt key: generate: %w", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key.PrivateKey),
	})
	// created_at is written explicitly (not left to the column default) so the
	// in-memory key and the row agree — deterministic anon-key claims are
	// anchored to it.
	_, err = m.db.Exec(ctx,
		`INSERT INTO auth.jwt_keys (kid, secret, algorithm, created_at) VALUES ($1, $2, $3, $4)`,
		key.KID, privPEM, key.Algorithm, key.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("jwt key: insert: %w", err)
	}
	m.gen++
	m.active = key
	m.byKID[key.KID] = key
	return key, nil
}

// Get returns the key for kid. A retired key verifies only until tokens it signed have expired.
func (m *JWTKeyManager) Get(ctx context.Context, kid string) (*JWTKey, error) {
	if kid == "" {
		return nil, fmt.Errorf("jwt key: empty kid")
	}
	m.refresh(ctx, keyReloadInterval, false)
	key, ok := m.cached(kid)
	if !ok {
		m.refresh(ctx, keyMissReloadInterval, true)
		key, ok = m.cached(kid)
	}
	if !ok {
		return nil, fmt.Errorf("jwt key: unknown kid %s", kid)
	}
	if m.expired(key, time.Now()) {
		return nil, fmt.Errorf("jwt key: kid %s is retired", kid)
	}
	return key, nil
}

// AllPublicKeys returns all non-retired public keys for the JWKS endpoint.
func (m *JWTKeyManager) AllPublicKeys(ctx context.Context) ([]*JWTKey, error) {
	rows, err := m.db.Query(ctx,
		`SELECT kid, secret, algorithm, created_at FROM auth.jwt_keys WHERE retired_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	var keys []*JWTKey
	for _, row := range rows {
		key, err := rowToKey(row)
		if err != nil {
			continue
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func rowToKey(row map[string]any) (*JWTKey, error) {
	kid, _ := row["kid"].(string)
	alg, _ := row["algorithm"].(string)
	if kid == "" || alg == "" {
		return nil, fmt.Errorf("jwt key: malformed row")
	}
	secret, err := coerceBytes(row["secret"])
	if err != nil {
		return nil, fmt.Errorf("jwt key: secret: %w", err)
	}

	key := &JWTKey{KID: kid, Algorithm: alg}
	if created, ok := row["created_at"].(time.Time); ok {
		key.CreatedAt = created.UTC()
	}
	if retired, ok := row["retired_at"].(time.Time); ok {
		key.RetiredAt = retired.UTC()
	}

	switch alg {
	case "RS256":
		block, _ := pem.Decode(secret)
		if block == nil {
			return nil, fmt.Errorf("jwt key: invalid PEM for kid %s", kid)
		}
		priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("jwt key: parse RSA key %s: %w", kid, err)
		}
		key.PrivateKey = priv
		key.PublicKey = &priv.PublicKey
	case "HS256":
		key.Secret = secret
	default:
		return nil, fmt.Errorf("jwt key: unsupported algorithm %s", alg)
	}

	return key, nil
}

func coerceBytes(v any) ([]byte, error) {
	switch b := v.(type) {
	case []byte:
		return b, nil
	case string:
		return []byte(b), nil
	default:
		return nil, fmt.Errorf("unexpected type %T", v)
	}
}

// RotateActive generates a fresh RS256 signing key and makes it active. Every
// previously active key is marked retired (retired_at = now()) so tokens it
// signed still verify until they expire, while all new tokens use the new key.
// The retire and insert are committed in a single transaction so a partial
// failure cannot leave the table with no active key. The publishable and secret
// API keys are opaque and independent of the signing key, so they are
// unaffected by a rotation. Requires a db-backed manager.
func (m *JWTKeyManager) RotateActive(ctx context.Context) (*JWTKey, error) {
	if m.db == nil {
		return nil, fmt.Errorf("jwt key: rotate requires a database-backed manager")
	}

	key, err := generateRS256Key()
	if err != nil {
		return nil, fmt.Errorf("jwt key: generate: %w", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key.PrivateKey),
	})

	m.mu.Lock()
	defer m.mu.Unlock()

	// Retire and insert run in one transaction: a partial failure (retire
	// commits, insert does not) would otherwise leave the table with every key
	// retired and no active key.
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("jwt key: begin: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE auth.jwt_keys SET retired_at = now() WHERE retired_at IS NULL`); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("jwt key: retire current: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO auth.jwt_keys (kid, secret, algorithm, created_at) VALUES ($1, $2, $3, $4)`,
		key.KID, privPEM, key.Algorithm, key.CreatedAt); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("jwt key: insert new: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("jwt key: commit: %w", err)
	}

	// Only update in-memory state after the write commits, so a failed rotation
	// leaves the manager pointing at the still-valid prior key.
	m.gen++
	if m.active != nil {
		old := *m.active
		old.RetiredAt = time.Now().UTC()
		m.byKID[old.KID] = &old
	}
	m.active = key
	m.byKID[key.KID] = key
	return key, nil
}

func generateRS256Key() (*JWTKey, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	kidBytes := make([]byte, 8)
	if _, err := rand.Read(kidBytes); err != nil {
		return nil, err
	}
	return &JWTKey{
		KID:        hex.EncodeToString(kidBytes),
		Algorithm:  "RS256",
		PrivateKey: priv,
		PublicKey:  &priv.PublicKey,
		CreatedAt:  time.Now().UTC(),
	}, nil
}
