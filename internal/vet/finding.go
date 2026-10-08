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
}

type Report struct {
	Findings []Finding      `json:"findings"`
	Counts   map[string]int `json:"counts"`
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
	for _, f := range findings {
		counts[f.Severity.String()]++
	}
	return &Report{Findings: findings, Counts: counts}
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
