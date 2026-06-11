package auth

import (
	"encoding/json"
	"fmt"
	"time"
)

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

func asTimeString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	case string:
		return t
	default:
		return fmt.Sprintf("%v", t)
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

func decodeJSONB(v any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	switch j := v.(type) {
	case map[string]any:
		return j
	case []byte:
		var m map[string]any
		_ = json.Unmarshal(j, &m)
		return m
	case string:
		var m map[string]any
		_ = json.Unmarshal([]byte(j), &m)
		return m
	default:
		return map[string]any{}
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
