package checks

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/sinnerk/workshere/internal/report"
	"github.com/sinnerk/workshere/internal/scan"
)

var envRefREs = []*regexp.Regexp{
	regexp.MustCompile(`\bprocess\.env\.([A-Z][A-Z0-9_]{2,})`),
	regexp.MustCompile(`\bprocess\.env\[\s*['"]([A-Z][A-Z0-9_]{2,})['"]\s*\]`),
	regexp.MustCompile(`\bos\.Getenv\(\s*"([A-Z][A-Z0-9_]{2,})"\s*\)`),
	regexp.MustCompile(`\bos\.LookupEnv\(\s*"([A-Z][A-Z0-9_]{2,})"\s*\)`),
	regexp.MustCompile(`\bos\.environ\[\s*['"]([A-Z][A-Z0-9_]{2,})['"]\s*\]`),
	regexp.MustCompile(`\bos\.environ\.get\(\s*['"]([A-Z][A-Z0-9_]{2,})['"]`),
	regexp.MustCompile(`\bgetenv\(\s*['"]([A-Z][A-Z0-9_]{2,})['"]`),
}

var envFileKeyRE = regexp.MustCompile(`(?m)^[ \t]*(?:export[ \t]+)?([A-Z][A-Z0-9_]*)[ \t]*=`)

// ambientVars are set by the operating system, the shell, or the CI provider
// itself. Reporting them would be noise.
var ambientVars = map[string]bool{
	"CI": true, "PATH": true, "HOME": true, "USERPROFILE": true,
	"HOMEDRIVE": true, "HOMEPATH": true, "TEMP": true, "TMP": true,
	"TMPDIR": true, "SHELL": true, "PWD": true, "OLDPWD": true,
	"LANG": true, "LC_ALL": true, "LC_CTYPE": true, "TZ": true,
	"NODE_ENV": true, "HOSTNAME": true, "COMPUTERNAME": true,
	"OS": true, "TERM": true, "TERM_PROGRAM": true, "SHLVL": true,
	"USER": true, "USERNAME": true, "LOGNAME": true,
	"GITHUB_ACTIONS": true, "GITHUB_WORKSPACE": true, "GITHUB_SHA": true,
	"GITHUB_REF": true, "GITHUB_TOKEN": true, "RUNNER_OS": true,
	"RUNNER_TEMP": true, "RUNNER_TOOL_CACHE": true,
	"SYSTEMROOT": true, "WINDIR": true, "COMSPEC": true, "PATHEXT": true,
}

// declaredEnv gathers every variable the repository itself defines: example
// files and workflow-level env blocks. The bool reports whether the repository
// declares any convention at all, because comparing against nothing would
// produce a finding for every variable in the project.
func declaredEnv(c *scan.Context) (map[string]bool, bool) {
	declared := map[string]bool{}
	hasConvention := false

	for _, p := range c.Tracked {
		base := strings.ToLower(path.Base(p))
		switch base {
		case ".env.example", ".env.sample", ".env.template", ".env.dist", ".env.defaults":
		default:
			continue
		}
		hasConvention = true
		data, err := c.ReadFile(p)
		if err != nil || data == nil {
			continue
		}
		for _, m := range envFileKeyRE.FindAllStringSubmatch(string(data), -1) {
			declared[m[1]] = true
		}
	}

	for _, w := range c.Workflows {
		for _, job := range w.Jobs {
			for k := range job.Env {
				declared[k] = true
				hasConvention = true
			}
			for _, step := range job.Steps {
				for k := range step.Env {
					declared[k] = true
					hasConvention = true
				}
			}
		}
	}

	return declared, hasConvention
}

func checkUndeclaredEnv(c *scan.Context) []report.Finding {
	declared, hasConvention := declaredEnv(c)
	if !hasConvention {
		return nil
	}

	type usage struct {
		file  string
		line  int
		names map[string]bool
	}
	byFile := map[string]*usage{}
	order := []string{}

	for _, p := range c.SourceFiles() {
		data, err := c.ReadFile(p)
		if err != nil || data == nil {
			continue
		}
		for _, re := range envRefREs {
			for _, m := range re.FindAllSubmatchIndex(data, -1) {
				if len(m) < 4 {
					continue
				}
				name := string(data[m[2]:m[3]])
				if ambientVars[name] || declared[name] {
					continue
				}
				u, ok := byFile[p]
				if !ok {
					u = &usage{file: p, line: lineAt(data, m[0]), names: map[string]bool{}}
					byFile[p] = u
					order = append(order, p)
				}
				u.names[name] = true
			}
		}
	}

	sort.Strings(order)

	var out []report.Finding
	for _, p := range order {
		u := byFile[p]
		names := make([]string, 0, len(u.names))
		for n := range u.names {
			names = append(names, n)
		}
		sort.Strings(names)

		shown := names
		suffix := ""
		if len(shown) > 5 {
			shown = shown[:5]
			suffix = fmt.Sprintf(" (+%d more)", len(names)-5)
		}

		out = append(out, report.New(
			"env-undeclared", report.SeverityLow,
			fmt.Sprintf("reads %d environment variable(s) nothing declares", len(names)),
			"If these are only set in your shell or in an untracked .env, CI starts without them and the failure looks unrelated to configuration.",
		).
			At(u.file, u.line).
			WithEvidence(strings.Join(shown, ", ")+suffix).
			WithFix("add them to .env.example with a safe default, and set them in the workflow env block"))
	}
	return out
}
