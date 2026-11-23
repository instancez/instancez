package http

import (
	"errors"
	"strings"
	"unicode"

	"github.com/go-playground/validator/v10"
)

// bindMessage turns a gin bind error into a short client-safe message.
func bindMessage(err error) string {
	if err == nil {
		return "malformed request body"
	}
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) || len(ve) == 0 {
		return "malformed JSON"
	}
	parts := make([]string, 0, len(ve))
	for _, fe := range ve {
		field := snakeCase(fe.Field())
		switch fe.Tag() {
		case "required":
			parts = append(parts, field+" is required")
		case "email":
			parts = append(parts, field+" must be a valid email address")
		case "min":
			parts = append(parts, field+" must be at least "+fe.Param()+" characters")
		case "max":
			parts = append(parts, field+" must be at most "+fe.Param()+" characters")
		default:
			parts = append(parts, field+" is invalid")
		}
	}
	return strings.Join(parts, "; ")
}

func snakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
