package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"sync"

	"github.com/instancez/instancez/internal/domain"
)

// JWTKey is a signing key used to sign and verify JWTs.
type JWTKey struct {
	KID        string
	Secret     []byte // legacy HS256 secret; nil for RS256 keys
	Algorithm  string // "HS256" or "RS256"
	PrivateKey *rsa.PrivateKey
	PublicKey  *rsa.PublicKey
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

// JWTKeyManager loads and caches JWT signing keys from the database.
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
		`SELECT kid, secret, algorithm FROM auth.jwt_keys
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

	// No key exists yet. Generate RS256 and insert.
	key, err := generateRS256Key()
	if err != nil {
		return nil, fmt.Errorf("jwt key: generate: %w", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key.PrivateKey),
	})
	_, err = m.db.Exec(ctx,
		`INSERT INTO auth.jwt_keys (kid, secret, algorithm) VALUES ($1, $2, $3)`,
		key.KID, privPEM, key.Algorithm)
	if err != nil {
		return nil, fmt.Errorf("jwt key: insert: %w", err)
	}
	m.active = key
	m.byKID[key.KID] = key
	return key, nil
}

// Get fetches a key by its KID. Retired keys are still returned for
// verification of tokens signed before rotation.
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
		`SELECT kid, secret, algorithm FROM auth.jwt_keys WHERE kid = $1`, kid)
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

// AllPublicKeys returns all non-retired public keys for the JWKS endpoint.
func (m *JWTKeyManager) AllPublicKeys(ctx context.Context) ([]*JWTKey, error) {
	rows, err := m.db.Query(ctx,
		`SELECT kid, secret, algorithm FROM auth.jwt_keys WHERE retired_at IS NULL ORDER BY created_at DESC`)
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
	}, nil
}
