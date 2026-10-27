package report

import (
	"fmt"
	"io"
	"strings"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiBlue   = "\x1b[34m"
	ansiGreen  = "\x1b[32m"
)

type styler struct {
	enabled bool
}

func (s styler) paint(code, text string) string {
	if !s.enabled || text == "" {
		return text
	}
	return code + text + ansiReset
}

// Text writes the human-readable report.
func Text(w io.Writer, r Report, min Severity, color bool) {
	s := styler{enabled: color}
	visible := r.AtOrAbove(min)

	fmt.Fprintf(w, "%s %s  %s\n\n",
		s.paint(ansiBold, r.Tool),
		r.Version,
		s.paint(ansiDim, r.Root))

	if len(visible) == 0 {
		fmt.Fprintf(w, "%s\n", s.paint(ansiGreen, "Nothing here will behave differently in CI."))
		writeWarnings(w, s, r.Warnings)
		return
	}

	fmt.Fprintf(w, "%s\n\n", s.paint(ansiBold, headline(len(visible))))

	for i, f := range visible {
		if i > 0 {
			fmt.Fprintln(w)
		}
		level := s.severity(f.Severity)
		loc := f.Location()

		if loc != "" {
			fmt.Fprintf(w, "  %-7s %s\n", level, s.paint(ansiDim, loc))
		} else {
			fmt.Fprintf(w, "  %s\n", level)
		}

		fmt.Fprintf(w, "          %s\n", f.Title)
		if f.Detail != "" {
			for _, line := range wrap(f.Detail, 92) {
				fmt.Fprintf(w, "          %s\n", line)
			}
		}
		if f.Evidence != "" {
			fmt.Fprintf(w, "          %s\n", s.paint(ansiDim, "evidence: "+truncate(f.Evidence, 140)))
		}
		if f.Fix != "" {
			fmt.Fprintf(w, "          %s %s\n", s.paint(ansiBlue, "fix:"), f.Fix)
		}
	}

	hidden := len(r.Findings) - len(visible)
	if hidden > 0 {
		fmt.Fprintf(w, "\n%s\n", s.paint(ansiDim, fmt.Sprintf("%d lower-severity finding(s) hidden; add --min-severity low to see them.", hidden)))
	}

	writeWarnings(w, s, r.Warnings)
}

func writeWarnings(w io.Writer, s styler, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s\n", s.paint(ansiDim, "notes:"))
	for _, warn := range warnings {
		fmt.Fprintf(w, "  %s\n", s.paint(ansiDim, "- "+warn))
	}
}

func headline(n int) string {
	if n == 1 {
		return "1 thing will behave differently in CI"
	}
	return fmt.Sprintf("%d things will behave differently in CI", n)
}

func (s styler) severity(sev Severity) string {
	switch sev {
	case SeverityHigh:
		return s.paint(ansiRed, "high")
	case SeverityMedium:
		return s.paint(ansiYellow, "medium")
	default:
		return s.paint(ansiDim, "low")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// wrap breaks a paragraph on word boundaries at a fixed width.
func wrap(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	cur := words[0]
	for _, word := range words[1:] {
		if len(cur)+1+len(word) > width {
			lines = append(lines, cur)
			cur = word
			continue
		}
		cur += " " + word
	}
	return append(lines, cur)
}
