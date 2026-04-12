package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/saedx1/ultrabase/internal/domain"
)

// JWTKey is a signing key used to sign and verify JWTs.
type JWTKey struct {
	KID       string
	Secret    []byte
	Algorithm string
}

// JWTKeyManager loads and caches JWT signing keys from the database. Keys are
// stored in the _auth_jwt_keys table, which is created by the migrator when
// auth is configured.
//
// On first call to Active, if no key row exists, a new HS256 key is generated
// and inserted. This makes the JWT secret fully managed: operators never need
// to configure or rotate it manually for v0.
type JWTKeyManager struct {
	db domain.Database

	mu     sync.RWMutex
	active *JWTKey
	byKID  map[string]*JWTKey
}

func NewJWTKeyManager(db domain.Database) *JWTKeyManager {
	return &JWTKeyManager{
		db:    db,
		byKID: make(map[string]*JWTKey),
	}
}

// NewInMemoryJWTKeyManager builds a key manager with a single pre-seeded key
// and no database backing. Intended for tests and ephemeral dev setups.
func NewInMemoryJWTKeyManager(kid string, secret []byte) (*JWTKeyManager, error) {
	if kid == "" || len(secret) == 0 {
		return nil, fmt.Errorf("jwt key: kid and secret required")
	}
	key := &JWTKey{KID: kid, Secret: secret, Algorithm: "HS256"}
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
		`SELECT kid, secret, algorithm FROM _auth_jwt_keys
		 WHERE retired_at IS NULL ORDER BY created_at DESC LIMIT 1`)
	if err == nil && row != nil {
		key, kerr := rowToKey(row)
		if kerr != nil {
			return nil, kerr
		}
		m.active = key
		m.byKID[key.KID] = key
		return key, nil
	}

	// No key exists yet. Generate one and insert.
	key, err := generateHS256Key()
	if err != nil {
		return nil, fmt.Errorf("jwt key: generate: %w", err)
	}
	_, err = m.db.Exec(ctx,
		`INSERT INTO _auth_jwt_keys (kid, secret, algorithm) VALUES ($1, $2, $3)`,
		key.KID, key.Secret, key.Algorithm)
	if err != nil {
		return nil, fmt.Errorf("jwt key: insert: %w", err)
	}
	m.active = key
	m.byKID[key.KID] = key
	return key, nil
}

// Get fetches a key by its KID. Retired keys are still returned so that
// verification of tokens signed just before a rotation continues to work until
// the token itself expires. V0 never rotates, so in practice every key lookup
// hits either the cache or the one active row.
func (m *JWTKeyManager) Get(ctx context.Context, kid string) (*JWTKey, error) {
	if kid == "" {
		return nil, fmt.Errorf("jwt key: empty kid")
	}

	m.mu.RLock()
	if key, ok := m.byKID[kid]; ok {
		m.mu.RUnlock()
		return key, nil
	}
	m.mu.RUnlock()

	row, err := m.db.QueryRow(ctx,
		`SELECT kid, secret, algorithm FROM _auth_jwt_keys WHERE kid = $1`, kid)
	if err != nil {
		return nil, fmt.Errorf("jwt key: lookup %s: %w", kid, err)
	}
	if row == nil {
		return nil, fmt.Errorf("jwt key: unknown kid %s", kid)
	}

	key, err := rowToKey(row)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.byKID[key.KID] = key
	m.mu.Unlock()
	return key, nil
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
	return &JWTKey{KID: kid, Secret: secret, Algorithm: alg}, nil
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

func generateHS256Key() (*JWTKey, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	kidBytes := make([]byte, 8)
	if _, err := rand.Read(kidBytes); err != nil {
		return nil, err
	}
	return &JWTKey{
		KID:       hex.EncodeToString(kidBytes),
		Secret:    secret,
		Algorithm: "HS256",
	}, nil
}
