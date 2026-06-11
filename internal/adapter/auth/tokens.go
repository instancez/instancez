package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
)

// generateRandomToken generates a URL-safe random token of the given byte length.
func generateRandomToken(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// generateNumericCode generates a numeric OTP of the given digit count.
func generateNumericCode(digits int) (string, error) {
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", fmt.Errorf("generate code: %w", err)
	}
	return fmt.Sprintf("%0*d", digits, n), nil
}
