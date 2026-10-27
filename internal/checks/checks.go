// Package checks contains every rule workshere knows. A check is a pure
// function of the scanned repository, so it can be tested without git, CI, or
// any network access.
package checks

import (
	"github.com/sinnerk/workshere/internal/report"
	"github.com/sinnerk/workshere/internal/scan"
)

// Check is one rule.
type Check struct {
	Name        string
	Description string
	Run         func(*scan.Context) []report.Finding
}

// All returns the checks in a stable order.
func All() []Check {
	return []Check{
		{"env-tracked", "a real .env file is committed to git", checkEnvTracked},
		{"case-collision", "two tracked paths differ only by letter case", checkCaseCollision},
		{"case-mismatch", "an import's letter case does not match the file on disk", checkCaseMismatch},
		{"untracked-import", "an import resolves to a file git does not track", checkUntrackedImports},
		{"crlf-script", "a shebang script uses CRLF line endings", checkCRLFShebang},
		{"lockfile-drift", "package.json and its lockfile disagree", checkLockfileDrift},
		{"cache-without-lockfile", "CI caches a dependency manager that has no lockfile", checkCacheWithoutLockfile},
		{"missing-script", "CI runs a script that does not exist", checkMissingScript},
		{"runtime-mismatch", "CI pins a language version this machine does not run", checkRuntimeMismatch},
		{"dead-path-filter", "a workflow only triggers on paths that do not exist", checkDeadPathFilter},
		{"large-file", "a tracked file is near or over a hosting limit", checkLargeFiles},
		{"symlink", "a tracked symlink may not materialise on Windows", checkSymlinks},
		{"absolute-path", "a tracked source file contains a machine-specific path", checkAbsolutePaths},
		{"env-undeclared", "code reads an environment variable nothing declares", checkUndeclaredEnv},
	}
}

// Run executes every check and concatenates the findings.
func Run(ctx *scan.Context) []report.Finding {
	var out []report.Finding
	for _, c := range All() {
		out = append(out, c.Run(ctx)...)
	}
	return out
}
