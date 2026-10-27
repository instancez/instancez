package telemetry

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/posthog/posthog-go"
	"gopkg.in/yaml.v3"
)

const (
	// maxEventJSON stays under posthog-go's 500,000-byte message limit, measured after JSON encoding.
	maxEventJSON = 450_000
	maxTruncated = 50
	redacted     = "<redacted>"
	configName   = "instancez.yaml"
)

var (
	envRefRe = regexp.MustCompile(`^\$\{[^}]*\}$`)
	sqlLitRe = regexp.MustCompile(`'(?:[^']|'')*'`)
	keepKeys = keySet("type", "references", "on_delete", "on_update", "primary_key", "nullable", "unique", "index",
		"rls_enabled", "schema", "public", "file", "runtime", "method", "returns", "language", "volatility", "operations", "version")
	sqlKeys = keySet("using", "with_check", "check", "default", "body", "sql", "where")
)

func keySet(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// CrashReport is the optional payload attached to a crash's $exception event.
type CrashReport struct {
	Command, ErrClass, ErrMessage string
	Frames                        []posthog.StackFrame
	ConfigYAML                    string
	Functions                     map[string]string
	Truncated                     []string
	Remote                        bool
}

// RedactYAML drops comments and redacts every value except schema structure.
func RedactYAML(raw []byte, home string) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", err
	}
	redactNode(&doc, "", "", false, home, map[*yaml.Node]bool{})
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// redactNode walks the tree; strict redacts every scalar, as under env, merge keys and aliases.
func redactNode(n *yaml.Node, key, parent string, strict bool, home string, seen map[*yaml.Node]bool) {
	n.HeadComment, n.LineComment, n.FootComment = "", "", ""
	switch n.Kind {
	case yaml.MappingNode:
		keys := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			k.HeadComment, k.LineComment, k.FootComment = "", "", ""
			redactNode(n.Content[i+1], k.Value, key, strict || k.Value == "env" || k.Value == "<<", home, seen)
			if v := scrub(k.Value, home); v != k.Value {
				k.Value = uniqueKey(v, keys)
			}
			keys[k.Value] = true
		}
	case yaml.AliasNode:
		if !seen[n.Alias] {
			seen[n.Alias] = true
			redactNode(n.Alias, key, parent, true, home, seen)
		}
	case yaml.ScalarNode:
		redactScalar(n, key, parent, strict, home)
	default:
		for _, c := range n.Content {
			redactNode(c, key, parent, strict, home, seen)
		}
	}
}

func uniqueKey(k string, used map[string]bool) string {
	for i, base := 2, k; used[k]; i++ {
		k = fmt.Sprintf("%s_%d", base, i)
	}
	return k
}

func redactScalar(n *yaml.Node, key, parent string, strict bool, home string) {
	if n.Value == "" || envRefRe.MatchString(n.Value) {
		return
	}
	if !strict {
		switch {
		case n.Tag == "!!bool" || n.Tag == "!!int" || n.Tag == "!!float" || n.Tag == "!!null":
			return
		case keepKeys[key] || (key == "name" && (parent == "fields" || parent == "args")):
			n.Value = scrub(n.Value, home)
			return
		case sqlKeys[key] && (key != "default" || strings.Contains(n.Value, "(")):
			n.Value = scrub(sqlLitRe.ReplaceAllString(n.Value, "'<value>'"), home)
			return
		}
	}
	n.Value, n.Tag, n.Style = redacted, "!!str", 0
}

// BuildCrashReport collects the error, redacted config and function source within the size cap.
// An empty configPath means the config source is remote and nothing is attached.
func BuildCrashReport(command string, err error, frames []posthog.StackFrame, configPath string, funcs map[string]string,
	getClass func(error) (string, bool, string), home string) CrashReport {
	r := CrashReport{Command: command, Frames: SanitizeFrames(frames, home), Truncated: []string{}, Functions: map[string]string{}}
	if getClass != nil && err != nil {
		class, _, msg := getClass(err)
		r.ErrClass, r.ErrMessage = class, Sanitize(msg, home)
	}
	if configPath == "" {
		r.Remote = true
		return r
	}
	if raw, rerr := os.ReadFile(configPath); rerr == nil {
		if y, yerr := RedactYAML(raw, home); yerr != nil {
			r.Truncated = append(r.Truncated, filepath.Base(configPath)+" (unparseable)")
		} else {
			r.ConfigYAML = y
		}
	}
	paths := make([]string, 0, len(funcs))
	for p := range funcs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if why := excludedFunc(filepath.Dir(configPath), p); why != "" {
			r.Truncated = append(r.Truncated, p+" ("+why+")")
			continue
		}
		r.Functions[p] = redactSource(funcs[p], home)
	}
	r.fit()
	return r
}

// fit drops the largest attachment until the encoded event fits under maxEventJSON.
func (r *CrashReport) fit() {
	for {
		b, _ := json.Marshal(r.Props(nil))
		if len(b) <= maxEventJSON {
			return
		}
		name, size := "", 0
		for p, body := range r.Functions {
			if len(body) > size || (len(body) == size && p < name) {
				name, size = p, len(body)
			}
		}
		switch {
		case name != "":
			r.Truncated = append(r.Truncated, name+" (too large)")
			delete(r.Functions, name)
		case r.ConfigYAML != "":
			r.Truncated, r.ConfigYAML = append(r.Truncated, configName+" (too large)"), ""
		case len(r.Truncated) > maxTruncated+1:
			r.Truncated = append(r.Truncated[:maxTruncated:maxTruncated], "…")
		case len(r.Frames) > 1:
			r.Frames = r.Frames[len(r.Frames)-max(1, len(r.Frames)/2):]
		default:
			return
		}
	}
}

func excludedFunc(dir, p string) string {
	c := path.Clean(filepath.ToSlash(p))
	switch {
	case strings.HasPrefix(path.Base(c), ".env"):
		return "env file"
	case !strings.HasPrefix(c, "functions/") || !insideFunctions(dir, c):
		return "outside functions/"
	}
	return ""
}

// insideFunctions resolves symlinks so a link cannot point out of functions/.
func insideFunctions(dir, rel string) bool {
	root, err := filepath.EvalSymlinks(filepath.Join(dir, "functions"))
	if err != nil {
		return false
	}
	real, err := filepath.EvalSymlinks(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	r, err := filepath.Rel(root, real)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

// CaptureStack returns the caller's stack, outermost first, minus the panic machinery above a deferred recover.
func CaptureStack() []posthog.StackFrame {
	return posthog.DefaultStackTraceExtractor{InAppDecider: posthog.SimpleInAppDecider}.GetStackTrace(5).Frames
}

var absFileRe = regexp.MustCompile(`^(?:/|[A-Za-z]:)`)

// SanitizeFrames copies frames, marks only instancez code in_app, and replaces home replaced by ~ and other absolute paths cut to their basename.
func SanitizeFrames(frames []posthog.StackFrame, home string) []posthog.StackFrame {
	out := make([]posthog.StackFrame, len(frames))
	for i, f := range frames {
		f.Filename = stripHome(f.Filename, home)
		f.InApp = strings.HasPrefix(f.Function, "github.com/instancez/instancez/")
		if absFileRe.MatchString(f.Filename) {
			f.Filename = "<path>/" + path.Base(filepath.ToSlash(f.Filename))
		}
		out[i] = f
	}
	return out
}

// ExceptionList builds the $exception_list value; no frames means no stacktrace.
func ExceptionList(class, msg string, frames []posthog.StackFrame) []map[string]any {
	item := map[string]any{"type": class, "value": msg}
	if len(frames) > 0 {
		item["stacktrace"] = map[string]any{"type": "raw", "frames": frames}
	}
	return []map[string]any{item}
}

// Props returns base plus the crash attachments, as one $exception event.
func (r CrashReport) Props(base map[string]any) map[string]any {
	p := make(map[string]any, len(base)+4)
	for k, v := range base {
		p[k] = v
	}
	p["$exception_list"] = ExceptionList(r.ErrClass, r.ErrMessage, r.Frames)
	p["config_yaml"] = r.ConfigYAML
	p["function_files"] = r.Functions
	p["truncated_files"] = r.Truncated
	return p
}

// CanReport is true when telemetry is on, or a source build is in debug mode.
func (c *Client) CanReport() bool {
	return c.reason == "" || (c.reason == sourceBuild && c.debug())
}

func (r CrashReport) promptText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "inz %s failed. Send a crash report to the instancez team?\nIt includes:\n  - the error and stack trace\n", r.Command)
	switch {
	case r.Remote:
		b.WriteString("  - config and function source: not included (remote source)\n")
	default:
		if r.ConfigYAML != "" {
			fmt.Fprintf(&b, "  - %s (comments dropped; values other than schema structure and ${ENV} references replaced with <redacted>)\n", configName)
		}
		if len(r.Functions) > 0 {
			paths, size := make([]string, 0, len(r.Functions)), 0
			for p, body := range r.Functions {
				paths, size = append(paths, p), size+len(body)
			}
			sort.Strings(paths)
			fmt.Fprintf(&b, "  - function source: %s (%d files, %.1f KB; comments and long string literals replaced)\n", strings.Join(paths, ", "), len(paths), float64(size)/1024)
		}
		if len(r.Truncated) > 0 {
			fmt.Fprintf(&b, "  - left out: %s\n", strings.Join(r.Truncated, ", "))
		}
	}
	b.WriteString("It never includes: .env files, environment variables, database data, node_modules.\n")
	b.WriteString("Redaction is best-effort. Answer v to see the exact payload.\n")
	return b.String()
}

// PromptCrash asks once whether to attach the report; it never sends anything itself.
func (c *Client) PromptCrash(r CrashReport, base map[string]any, in io.Reader, out io.Writer, isTTY bool) bool {
	if !isTTY || in == nil {
		return false
	}
	_, _ = io.WriteString(out, r.promptText())
	if f, ok := in.(*os.File); ok {
		flushInput(f)
	}
	lines := make(chan string, 1)
	go func() {
		defer func() {
			if recover() != nil {
				close(lines)
			}
		}()
		rd := bufio.NewReader(in)
		for {
			line, err := rd.ReadString('\n')
			lines <- line
			if err != nil {
				close(lines)
				return
			}
		}
	}()
	for {
		_, _ = io.WriteString(out, "Send? [y/N/v=view] ")
		var line string
		var open bool
		select {
		case line, open = <-lines:
		case <-time.After(promptTimeout):
			_, _ = io.WriteString(out, "\nno answer, not sent\n")
			return false
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true
		case "v":
			b, _ := json.MarshalIndent(map[string]any{"event": "$exception", "properties": c.wireProps("$exception", r.Props(c.withBase(base)))}, "", "  ")
			_, _ = fmt.Fprintf(out, "%s\n", b)
		default:
			return false
		}
		if !open {
			return false
		}
	}
}

var (
	jsTokenRe  = regexp.MustCompile("//[^\\n]*|/\\*[\\s\\S]*?(?:\\*/|$)|\"(?:[^\"\\\\\\n]|\\\\.)*\"?|'(?:[^'\\\\\\n]|\\\\.)*'?|`(?:[^`\\\\]|\\\\[\\s\\S])*`?")
	jsModuleRe = regexp.MustCompile(`(?:\bfrom|\bimport\s*\(?|\brequire\s*\()\s*$`)
	jsKeyRe    = regexp.MustCompile(`^\s*:`)
	jsRunRe    = regexp.MustCompile(`\S{12,}`)
	userPassRe = regexp.MustCompile("[^\\s:/@'\"`]+:[^\\s@'\"`/]+@")
	dashPRe    = regexp.MustCompile(`(^|\s)-p\S+`)

	promptTimeout = 30 * time.Second
)

// redactRuns hides credential forms and long mixed runs inside kept literals that contain whitespace.
func redactRuns(s string) string {
	s = dashPRe.ReplaceAllString(userPassRe.ReplaceAllString(s, redacted+"@"), "${1}-p"+redacted)
	return jsRunRe.ReplaceAllStringFunc(s, func(m string) string {
		letters := strings.IndexFunc(m, unicode.IsLetter) >= 0
		other := strings.IndexFunc(m, func(r rune) bool { return !unicode.IsLetter(r) }) >= 0
		if letters && other && m != redacted+"@" {
			return redacted
		}
		return m
	})
}

// isKey is true for a literal written as an object key: after { or , and before :.
func isKey(body string, start, end int) bool {
	before := strings.TrimRight(body[max(0, start-24):start], " \t\r\n")
	return before != "" && strings.ContainsAny(before[len(before)-1:], "{,") && jsKeyRe.MatchString(body[end:min(len(body), end+24)])
}

// redactSource keeps code but redacts string literals of 12+ chars with no whitespace,
// except module specifiers and object keys.
func redactSource(body, home string) string {
	var b strings.Builder
	last := 0
	for _, m := range jsTokenRe.FindAllStringIndex(body, -1) {
		tok := body[m[0]:m[1]]
		b.WriteString(body[last:m[0]])
		last = m[1]
		switch {
		case strings.HasPrefix(tok, "//"):
			b.WriteString("//")
			continue
		case strings.HasPrefix(tok, "/*"):
			b.WriteString("/*" + strings.Repeat("\n", strings.Count(tok, "\n")) + "*/")
			continue
		}
		q, inner := tok[:1], tok[1:]
		closed := inner != "" && strings.HasSuffix(inner, q)
		if closed {
			inner = inner[:len(inner)-1]
		}
		switch {
		case jsModuleRe.MatchString(body[max(0, m[0]-24):m[0]]) || isKey(body, m[0], m[1]):
			b.WriteString(tok)
		case strings.ContainsAny(inner, " \t\r\n"):
			b.WriteString(q + redactRuns(inner))
			if closed {
				b.WriteString(q)
			}
		case len(inner) < 12:
			b.WriteString(tok)
		default:
			b.WriteString(q + redacted + q)
		}
	}
	b.WriteString(body[last:])
	return scrub(b.String(), home)
}
