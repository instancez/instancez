// Package vet statically lints an instancez.yaml for security problems.
package vet

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
	"gopkg.in/yaml.v3"
)

type Options struct {
	Ignore []string
}

type ctx struct {
	cfg      *domain.Config
	doc      *yaml.Node
	findings []Finding
}

func (c *ctx) add(rule string, sev Severity, path []any, title, msg, fix string) {
	c.findings = append(c.findings, Finding{
		Rule: rule, Severity: sev, Path: formatPath(path), Line: lineOf(c.doc, path),
		Title: title, Message: msg, Fix: fix,
	})
}

// rules is the registry; a new rule is one func plus one line here.
var rules = []func(*ctx){ruleRLSDisabled, rulePolicies, ruleBuckets, ruleRPC, ruleFunctionSecrets,
	ruleHardcodedSecret, ruleJWTExpiry, ruleSignupUnverified, ruleAnonymousSignins, ruleRedirects, ruleCORS, ruleMaxLimit}

// Run parses src without env interpolation and returns the ranked findings.
func Run(src []byte, opts Options) (*Report, error) {
	return run(src, opts, rules)
}

func run(src []byte, opts Options, rs []func(*ctx)) (*Report, error) {
	cfg, err := config.ParseBytesRaw(src, "instancez.yaml")
	if err != nil {
		return nil, err
	}
	var unknown []Finding
	for _, e := range cfg.UnknownKeys {
		if !strings.HasPrefix(e.Message, "unknown key") {
			return nil, fmt.Errorf("invalid config: %s (run `inz validate` for details)", e.Message)
		}
		unknown = append(unknown, Finding{Rule: "unknown-key", Severity: Medium, Path: e.Path, Line: e.Line,
			Title: "Unknown config key", Message: fmt.Sprintf("%s under %s is not a known setting, so it has no effect.", e.Message, e.Path),
			Fix: "Fix the spelling or remove the key."})
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	c := &ctx{cfg: cfg, doc: &doc, findings: unknown}
	for _, r := range rs {
		r(c)
	}
	inline := inlineIgnores(src)
	kept := c.findings[:0]
	for _, f := range c.findings {
		if !ignored(f, opts.Ignore, inline) {
			kept = append(kept, f)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		if a.Severity != b.Severity {
			return a.Severity > b.Severity
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Rule < b.Rule
	})
	return newReport(kept), nil
}

var ignoreComment = regexp.MustCompile(`#\s*inz-vet-ignore:\s*([A-Za-z0-9_,\s-]+)`)

// inlineIgnores maps a 1-based line to rule ids from an ignore comment on it; a comment-only line also covers the next line.
func inlineIgnores(src []byte) map[int][]string {
	out := map[int][]string{}
	for i, line := range strings.Split(string(src), "\n") {
		m := ignoreComment.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for _, item := range strings.Split(m[1], ",") {
			f := strings.Fields(item)
			if len(f) == 0 {
				continue
			}
			out[i+1] = append(out[i+1], f[0])
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				out[i+2] = append(out[i+2], f[0])
			}
		}
	}
	return out
}

func ignored(f Finding, global []string, inline map[int][]string) bool {
	return slices.Contains(global, f.Rule) || (f.Line > 0 && slices.Contains(inline[f.Line], f.Rule))
}
