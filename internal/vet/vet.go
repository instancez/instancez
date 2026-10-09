// Package vet statically lints an instancez.yaml for security problems.
package vet

import (
	"fmt"
	"sort"
	"strings"

	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
	"gopkg.in/yaml.v3"
)

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

// withEdit attaches the one-click fix to the finding just added.
func (c *ctx) withEdit(e Edit) {
	c.findings[len(c.findings)-1].Edit = &e
}

// rules is the registry; a new rule is one func plus one line here.
var rules = []func(*ctx){ruleRLSDisabled, rulePolicies, ruleBuckets, ruleRPC, ruleFunctionSecrets,
	ruleHardcodedSecret, ruleJWTExpiry, ruleSignupUnverified, ruleAnonymousSignins, ruleRedirects, ruleCORS, ruleMaxLimit}

// Run parses src without env interpolation and returns the ranked findings.
func Run(src []byte) (*Report, error) {
	return run(src, rules)
}

func run(src []byte, rs []func(*ctx)) (*Report, error) {
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
	kept := c.findings
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
