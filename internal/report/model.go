// Package report defines the findings produced by workshere and renders them.
package report

import (
	"fmt"
	"sort"
	"strings"
)

// Severity is how likely a finding is to bite in CI. It is deliberately about
// consequence, not about how interesting the check was to write.
type Severity int

const (
	SeverityLow Severity = iota
	SeverityMedium
	SeverityHigh
)

func (s Severity) String() string {
	switch s {
	case SeverityHigh:
		return "high"
	case SeverityMedium:
		return "medium"
	default:
		return "low"
	}
}

// ParseSeverity accepts the strings a user is likely to type.
func ParseSeverity(v string) (Severity, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "low":
		return SeverityLow, true
	case "medium", "med":
		return SeverityMedium, true
	case "high":
		return SeverityHigh, true
	}
	return SeverityLow, false
}

// Finding is one reason the code will behave differently somewhere else.
type Finding struct {
	Check    string   `json:"check"`
	Severity Severity `json:"-"`
	Level    string   `json:"level"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Fix      string   `json:"fix,omitempty"`
	File     string   `json:"file,omitempty"`
	Line     int      `json:"line,omitempty"`
	Evidence string   `json:"evidence,omitempty"`
}

// New starts a finding. Detail should say why it matters, not repeat the title.
func New(check string, sev Severity, title, detail string) Finding {
	return Finding{Check: check, Severity: sev, Level: sev.String(), Title: title, Detail: detail}
}

// At attaches a location.
func (f Finding) At(file string, line int) Finding {
	f.File = file
	f.Line = line
	return f
}

// WithFix attaches the one-line remedy.
func (f Finding) WithFix(fix string) Finding {
	f.Fix = fix
	return f
}

// WithEvidence attaches the exact text that triggered the check.
func (f Finding) WithEvidence(evidence string) Finding {
	f.Evidence = evidence
	return f
}

// Location is "file:line" or just "file" when the line is unknown.
func (f Finding) Location() string {
	if f.File == "" {
		return ""
	}
	if f.Line <= 0 {
		return f.File
	}
	return fmt.Sprintf("%s:%d", f.File, f.Line)
}

// Report is everything one run produced.
type Report struct {
	Tool     string         `json:"tool"`
	Version  string         `json:"version"`
	Root     string         `json:"root"`
	Findings []Finding      `json:"findings"`
	Counts   map[string]int `json:"counts"`
	Warnings []string       `json:"warnings,omitempty"`
}

// Build assembles a report and sorts findings so the dangerous ones come first.
func Build(tool, version, root string, findings []Finding, warnings []string) Report {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return findings[i].Severity > findings[j].Severity
		}
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		if findings[i].Line != findings[j].Line {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].Title < findings[j].Title
	})

	counts := map[string]int{"high": 0, "medium": 0, "low": 0}
	for _, f := range findings {
		counts[f.Level]++
	}

	return Report{
		Tool:     tool,
		Version:  version,
		Root:     root,
		Findings: findings,
		Counts:   counts,
		Warnings: warnings,
	}
}

// AtOrAbove returns the findings that meet the severity floor.
func (r Report) AtOrAbove(min Severity) []Finding {
	out := make([]Finding, 0, len(r.Findings))
	for _, f := range r.Findings {
		if f.Severity >= min {
			out = append(out, f)
		}
	}
	return out
}

// Total is the number of findings at or above the floor.
func (r Report) Total(min Severity) int {
	return len(r.AtOrAbove(min))
}
