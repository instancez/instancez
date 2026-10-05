package http

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// verifyCodeChallenge verifies a PKCE code_verifier against the stored code_challenge.
// method is matched case-insensitively: real @supabase/supabase-js sends a
// lowercase "s256" (see getCodeChallengeAndMethod in auth-js), not the
// uppercase "S256" the PKCE RFC uses in prose.
func verifyCodeChallenge(method, verifier, challenge string) bool {
	switch strings.ToUpper(method) {
	case "S256", "":
		h := sha256.Sum256([]byte(verifier))
		computed := base64.RawURLEncoding.EncodeToString(h[:])
		return computed == challenge
	case "PLAIN":
		return verifier == challenge
	default:
		return false
	}
}
