package telemetry

import (
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
)

const maxMessageRunes = 300

var (
	quotedRe = regexp.MustCompile("(^|[^A-Za-z0-9])'[^'\\n]*'|\"(?:[^\"\\\\]|\\\\.)*\"|`[^`]*`")
	x509Re   = regexp.MustCompile(`(x509: [a-z ,]*?(?:valid for|wanted to match|certificate for|none matched)) [^\n]*`)
	pathRe   = regexp.MustCompile(`(?:^|[\s(=\[,])(?:~[/\\]|[A-Za-z]:\\|/)[^\s:"'` + "`" + `()\[\],]+`)
	hostRe   = regexp.MustCompile(`\b(lookup|dial (?:tcp|udp)[46]?) (?:\[[^\]]*\]|[^\s:]+)(?::\d+)?`)
	addrRe   = regexp.MustCompile(`(?:\[[^\]]*\]|[^\s:(]+):\d+ \([^)\s]*\)`)

	dsnRe    = regexp.MustCompile("([A-Za-z][A-Za-z0-9+.-]*://)[^/\\n\"'`]*@[^\\s\"'`]*")
	queryRe  = regexp.MustCompile("(?i)([?&][\\w-]*(?:secret|token|signature|password|pwd|key)[\\w-]*=)[^&\\s\"'`]+")
	kwPassRe = regexp.MustCompile(`(?i)\b([\w-]*(?:pass|pwd|pw|secret|token|api_?key)[\w-]*)(\\?["']?\s*[:=]\s*\\?)(?:'[^']*'|"[^"]*"|[^\s"'\\,;&}]+)`)
	kwConnRe = regexp.MustCompile("(?i)(^|[^A-Za-z])(user|database|dbname|host)=(?:'[^']*'|\"[^\"]*\"|[^\\s`'\"]+)")
	userRe   = regexp.MustCompile(`(?i)(/Users/|/home/|[A-Z]:\\Users\\)[^/\\\s"'` + "`" + `]+`)
	emailRe  = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*`)
	tokenRe  = regexp.MustCompile(`-----BEGIN [A-Z ]+-----[\s\S]*?-----END [A-Z ]+-----` +
		`|eyJ[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+){0,2}` +
		`|\b(?:sk|rk|pk|whsec|phc|npm|gh[pousr]|github_pat)_[A-Za-z0-9_]{6,}` +
		`|\b(?:inz_secret|inz_publishable|instancez_pat)_[A-Za-z0-9_-]{8,}|\bAKIA[0-9A-Z]{16}\b|\bxox[abposr]-[A-Za-z0-9-]+|\bsk-[A-Za-z0-9_-]{20,}|\bAIza[0-9A-Za-z_-]{35}` +
		"|hooks\\.slack\\.com/services/[^\\s\"'`]+" +
		`|\b(?:Bearer|Basic) [A-Za-z0-9._~+/=-]{6,}` +
		`|[A-Za-z0-9+/]{32,}={0,2}`)
	runRe = regexp.MustCompile(`^[A-Za-z0-9+/]+=*$`)

	identRe    = regexp.MustCompile(`^(?:[a-z_][a-z0-9_.-]{0,24}|-{1,2}[a-z][a-z0-9-]*)$`)
	valueCtxRe = regexp.MustCompile(`(?i)(?:\buser|\brole|\bdatabase|pass\w*|secret|token|\bkey|syntax for type \S+:|value for enum \S+:|\bargument|\bparsing)["'\s:=]*$`)
	knownNames atomic.Pointer[map[string]bool]
)

// SetKnownNames lists command and flag names that stay readable when quoted.
func SetKnownNames(names []string) {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	knownNames.Store(&m)
}

// redactQuoted keeps quoted identifiers and known names unless they follow a value-bearing word.
func redactQuoted(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range quotedRe.FindAllStringSubmatchIndex(s, -1) {
		start := m[0]
		if m[2] >= 0 {
			start = m[3]
		}
		inner := s[start+1 : m[1]-1]
		b.WriteString(s[last:start])
		last = m[1]
		known := knownNames.Load()
		keep := (identRe.MatchString(inner) || (known != nil && (*known)[inner])) && !valueCtxRe.MatchString(s[max(0, start-40):start])
		if keep {
			b.WriteString(s[start:m[1]])
		} else {
			b.WriteString("<value>")
		}
	}
	b.WriteString(s[last:])
	return b.String()
}

// ScrubSecrets redacts credentials and token-looking strings.
func ScrubSecrets(text string) string {
	text = dsnRe.ReplaceAllString(text, "${1}<redacted>")
	text = queryRe.ReplaceAllString(text, "${1}<redacted>")
	text = kwPassRe.ReplaceAllString(text, "${1}${2}<redacted>")
	text = kwConnRe.ReplaceAllString(text, "${1}${2}=<redacted>")
	return tokenRe.ReplaceAllStringFunc(text, func(m string) string {
		// A slashy run with no digit is a code path, not a secret.
		if runRe.MatchString(m) && strings.Contains(m, "/") && !strings.ContainsAny(m, "0123456789") {
			return m
		}
		return redacted
	})
}

// scrub is the pattern layer every outbound text gets.
func scrub(s, home string) string {
	s = stripHome(s, home)
	s = userRe.ReplaceAllString(s, "${1}<user>")
	return emailRe.ReplaceAllString(ScrubSecrets(s), "<email>")
}

// SanitizeStack replaces quoted values, hosts and absolute paths, then scrubs secrets.
func SanitizeStack(s, home string) string {
	s = redactQuoted(stripHome(s, home))
	s = x509Re.ReplaceAllString(s, "${1} <host>")
	s = addrRe.ReplaceAllString(hostRe.ReplaceAllString(s, "${1} <host>"), "<host>")
	s = pathRe.ReplaceAllStringFunc(s, func(m string) string {
		lead := ""
		if strings.ContainsAny(m[:1], " \t\n\r(=[,") {
			lead, m = m[:1], m[1:]
		}
		return lead + "<path>/" + m[strings.LastIndexAny(m, `/\`)+1:]
	})
	return scrub(s, home)
}

// Sanitize is SanitizeStack cut to 300 runes.
func Sanitize(msg, home string) string {
	msg = SanitizeStack(msg, home)
	if r := []rune(msg); len(r) > maxMessageRunes {
		return string(r[:maxMessageRunes])
	}
	return msg
}

func stripHome(s, home string) string {
	if real, err := filepath.EvalSymlinks(home); err == nil && real != home {
		s = stripOne(s, real)
	}
	return stripOne(s, home)
}

func stripOne(s, home string) string {
	if home = strings.TrimRight(home, `/\`); home == "" {
		return s
	}
	re, ok := homeRes.Load(home)
	if !ok {
		re, _ = homeRes.LoadOrStore(home, regexp.MustCompile(regexp.QuoteMeta(home)+`(/|\\|$|[^\w.-])`))
	}
	return re.(*regexp.Regexp).ReplaceAllString(s, "~$1")
}

var homeRes sync.Map
