package http

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
)

// planRequest is a parsed application/vnd.pgrst.plan Accept header.
type planRequest struct {
	format  string
	forType string
	options []string
}

var planOptions = map[string]string{"analyze": "ANALYZE", "verbose": "VERBOSE", "settings": "SETTINGS", "buffers": "BUFFERS", "wal": "WAL"}

// planForTypes are the result media types the list route can serve.
var planForTypes = map[string]bool{
	"application/json": true, "application/vnd.pgrst.object+json": true, "application/vnd.pgrst.object": true,
	"text/csv": true, "application/geo+json": true, "*/*": true,
}

// splitUnquoted splits on sep outside double quotes; mime.ParseMediaType rejects the `options=;` postgrest-js sends.
func splitUnquoted(s string, sep rune) []string {
	var out []string
	inQ, start := false, 0
	for i, r := range s {
		switch {
		case r == '"':
			inQ = !inQ
		case r == sep && !inQ:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func parsePlanAccept(accept string) (planRequest, bool, error) {
	parts := splitUnquoted(accept, ';')
	var p planRequest
	switch strings.ToLower(strings.TrimSpace(parts[0])) {
	case "application/vnd.pgrst.plan", "application/vnd.pgrst.plan+text":
		p.format = "text"
	case "application/vnd.pgrst.plan+json":
		p.format = "json"
	default:
		return p, false, nil
	}
	p.forType = "application/json"
	seen := map[string]bool{}
	for _, kv := range parts[1:] {
		k, v, _ := strings.Cut(kv, "=")
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "for":
			if base, _, _ := strings.Cut(v, ";"); strings.TrimSpace(base) != "" {
				p.forType = strings.ToLower(strings.TrimSpace(base))
			}
		case "options":
			for _, o := range strings.Split(v, "|") {
				if kw, ok := planOptions[strings.ToLower(strings.TrimSpace(o))]; ok && !seen[kw] {
					seen[kw] = true
					p.options = append(p.options, kw)
				}
			}
		}
	}
	if !planForTypes[p.forType] {
		return p, true, fmt.Errorf("unsupported plan media type %q", p.forType)
	}
	return p, true, nil
}

func (p planRequest) validate() error {
	if slices.Contains(p.options, "WAL") && !slices.Contains(p.options, "ANALYZE") {
		return errors.New("EXPLAIN option WAL requires ANALYZE")
	}
	return nil
}

// hasPlanMediaType reports whether any media range in an Accept list is a plan type.
func hasPlanMediaType(accept string) bool {
	for _, mr := range splitUnquoted(accept, ',') {
		if _, isPlan, _ := parsePlanAccept(mr); isPlan {
			return true
		}
	}
	return false
}

func (p planRequest) explainSQL(query string) string {
	opts := append([]string{"FORMAT " + strings.ToUpper(p.format)}, p.options...)
	return "EXPLAIN (" + strings.Join(opts, ", ") + ") " + query
}

func (p planRequest) contentType() string {
	return fmt.Sprintf(`application/vnd.pgrst.plan+%s; for="%s"; charset=utf-8`, p.format, p.forType)
}

// rejectPlan refuses plan requests on routes that would otherwise execute writes or RPCs.
func rejectPlan(c *gin.Context) {
	accept := c.GetHeader("Accept")
	if hasPlanMediaType(accept) {
		pgJSON(c, 406, "PGRST107", "None of these media types are available: "+accept, "", "")
		c.Abort()
	}
}
