package auth

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
)

// constantTimeEqual reports whether a and b are equal without leaking their
// relationship through comparison timing. Used for OTP code comparison.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return fmt.Sprintf("%v", s)
	}
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}

// jsonbArg marshals a map for use as a JSONB Postgres parameter.
func jsonbArg(m map[string]any) []byte {
	if m == nil {
		return []byte("{}")
	}
	b, _ := json.Marshal(m)
	return b
}
