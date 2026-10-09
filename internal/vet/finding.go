package vet

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Severity int

const (
	Info Severity = iota
	Low
	Medium
	High
	Critical
)

var severityNames = [...]string{"info", "low", "medium", "high", "critical"}

func (s Severity) String() string {
	if s < Info || s > Critical {
		return fmt.Sprintf("severity(%d)", int(s))
	}
	return severityNames[s]
}

func (s Severity) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

func ParseSeverity(name string) (Severity, error) {
	want := strings.ToLower(strings.TrimSpace(name))
	for i, n := range severityNames {
		if n == want {
			return Severity(i), nil
		}
	}
	return 0, fmt.Errorf("unknown severity %q (want one of %s)", name, strings.Join(severityNames[:], ", "))
}

type Finding struct {
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Path     string   `json:"path"`
	Line     int      `json:"line"`
	Title    string   `json:"title"`
	Message  string   `json:"message"`
	Fix      string   `json:"fix"`
	Edit     *Edit    `json:"edit,omitempty"`
}

// Edit is the config change that resolves a finding. Path holds map keys and list indexes.
// Remove deletes the value there instead of setting it; Value is then the value expected at Path, so a stale edit can be refused.
type Edit struct {
	Path   []any `json:"path"`
	Value  any   `json:"value"`
	Remove bool  `json:"remove,omitempty"`
}

type Report struct {
	Findings []Finding      `json:"findings"`
	Counts   map[string]int `json:"counts"`
	Checks   Checks         `json:"checks"`
}

// Checks counts the catalog rules; Passed is those with no finding.
type Checks struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
}

// checkIDs is every rule a user can fail; keep it in sync with the docs table.
var checkIDs = []string{
	"rls-disabled", "policy-open-write", "policy-open-read", "policy-authed-read-sensitive", "policy-no-identity-write",
	"bucket-open-write", "bucket-open-read", "bucket-no-rls", "bucket-public",
	"rpc-definer-no-auth", "rpc-definer-search-path", "rpc-dynamic-sql",
	"function-public-secrets", "hardcoded-secret", "jwt-expiry-long", "signup-unverified-email",
	"anonymous-signins", "redirect-insecure", "cors-wildcard", "cors-null-origin", "max-limit-disabled",
}

// newReport always fills every severity count and a non-nil findings slice.
func newReport(findings []Finding) *Report {
	if findings == nil {
		findings = []Finding{}
	}
	counts := make(map[string]int, len(severityNames))
	for _, n := range severityNames {
		counts[n] = 0
	}
	failed := map[string]bool{}
	for _, f := range findings {
		counts[f.Severity.String()]++
		failed[f.Rule] = true
	}
	passed := 0
	for _, id := range checkIDs {
		if !failed[id] {
			passed++
		}
	}
	return &Report{Findings: findings, Counts: counts, Checks: Checks{Total: len(checkIDs), Passed: passed}}
}

// Max returns the highest severity present and whether there are any findings.
func (r *Report) Max() (Severity, bool) {
	if r == nil || len(r.Findings) == 0 {
		return Info, false
	}
	top := r.Findings[0].Severity
	for _, f := range r.Findings[1:] {
		top = max(top, f.Severity)
	}
	return top, true
}
