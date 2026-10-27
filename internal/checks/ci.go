package checks

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/sinnerk/workshere/internal/report"
	"github.com/sinnerk/workshere/internal/scan"
)

// --- runtime versions ------------------------------------------------------

func runtimeForAction(uses string) (string, []string) {
	u := strings.ToLower(strings.TrimSpace(uses))
	switch {
	case strings.HasPrefix(u, "actions/setup-node"):
		return "node", []string{"node-version", "node-version-file"}
	case strings.HasPrefix(u, "actions/setup-go"):
		return "go", []string{"go-version", "go-version-file"}
	case strings.HasPrefix(u, "actions/setup-python"):
		return "python", []string{"python-version", "python-version-file"}
	}
	return "", nil
}

var goDirectiveRE = regexp.MustCompile(`(?m)^go[ \t]+([0-9]+\.[0-9]+(?:\.[0-9]+)?)`)

func versionFromFile(c *scan.Context, rel string) string {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return ""
	}
	if path.IsAbs(rel) || strings.HasPrefix(rel, "../") {
		return ""
	}
	data, err := c.ReadFile(path.Clean(rel))
	if err != nil || data == nil {
		return ""
	}
	text := string(data)

	switch strings.ToLower(path.Base(rel)) {
	case "go.mod":
		if m := goDirectiveRE.FindStringSubmatch(text); len(m) == 2 {
			return m[1]
		}
	case ".tool-versions":
		for _, line := range strings.Split(text, "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && (f[0] == "nodejs" || f[0] == "golang" || f[0] == "python") {
				return f[1]
			}
		}
	default:
		// .nvmrc and .python-version are a single version on the first line.
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				return line
			}
		}
	}
	return ""
}

func leadingVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	var b strings.Builder
	for _, r := range v {
		if (r >= '0' && r <= '9') || r == '.' {
			b.WriteRune(r)
			continue
		}
		break
	}
	return strings.Trim(b.String(), ".")
}

// versionKey reduces a version to the part that actually changes behaviour.
// For Node that is the major; for Go and Python the major and minor.
func versionKey(runtime, raw string) string {
	v := leadingVersion(raw)
	if v == "" {
		return ""
	}
	parts := strings.Split(v, ".")
	if runtime == "node" {
		if parts[0] == "" {
			return ""
		}
		return parts[0]
	}
	if len(parts) < 2 || parts[1] == "" {
		return ""
	}
	return parts[0] + "." + parts[1]
}

func localVersion(c *scan.Context, runtime string) string {
	raw := c.LocalVersions[runtime]
	if raw == "" {
		return ""
	}
	fields := strings.Fields(raw)
	switch runtime {
	case "node":
		if len(fields) >= 1 {
			return strings.TrimPrefix(fields[0], "v")
		}
	case "go":
		for _, f := range fields {
			if strings.HasPrefix(f, "go") && len(f) > 2 {
				return strings.TrimPrefix(f, "go")
			}
		}
	case "python":
		if len(fields) >= 2 {
			return fields[1]
		}
	}
	return ""
}

func checkRuntimeMismatch(c *scan.Context) []report.Finding {
	type pin struct {
		runtime string
		spec    string
		file    string
	}

	var pins []pin
	for _, w := range c.Workflows {
		for _, job := range w.Jobs {
			for _, step := range job.Steps {
				runtime, keys := runtimeForAction(step.Uses)
				if runtime == "" {
					continue
				}
				for _, key := range keys {
					raw := step.With[key]
					if raw == "" {
						continue
					}
					if strings.HasSuffix(key, "-file") {
						raw = versionFromFile(c, raw)
						if raw == "" {
							continue
						}
					}
					for _, spec := range scan.ExpandSpec(raw, job.Matrix) {
						pins = append(pins, pin{runtime, spec, w.Path})
					}
				}
			}
		}
	}

	var out []report.Finding
	seen := map[string]bool{}
	for _, p := range pins {
		local := localVersion(c, p.runtime)
		if local == "" {
			continue
		}
		ciKey := versionKey(p.runtime, p.spec)
		localKey := versionKey(p.runtime, local)
		if ciKey == "" || localKey == "" || ciKey == localKey {
			continue
		}
		key := p.runtime + "|" + ciKey + "|" + localKey
		if seen[key] {
			continue
		}
		seen[key] = true

		out = append(out, report.New(
			"runtime-mismatch", report.SeverityMedium,
			fmt.Sprintf("CI runs %s %s; this machine runs %s", displayName(p.runtime), ciKey, local),
			"A dependency, a standard-library function or a compiler default that exists in one version may not exist in the other. This is the difference `act` cannot tell you about.",
		).
			At(p.file, 0).
			WithEvidence(fmt.Sprintf("workflow pins %s %q", p.runtime, p.spec)).
			WithFix(fmt.Sprintf("run the tests on %s %s locally before pushing, or pin CI to the version you develop on", displayName(p.runtime), ciKey)))
	}
	return out
}

func displayName(runtime string) string {
	switch runtime {
	case "node":
		return "Node"
	case "go":
		return "Go"
	case "python":
		return "Python"
	}
	return runtime
}

// --- workflows that cannot do what they look like -------------------------

func checkCacheWithoutLockfile(c *scan.Context) []report.Finding {
	lockfiles := map[string][]string{
		"npm":  {"package-lock.json", "npm-shrinkwrap.json"},
		"yarn": {"yarn.lock"},
		"pnpm": {"pnpm-lock.yaml"},
		"pip":  {"requirements.txt", "pyproject.toml", "poetry.lock", "Pipfile.lock"},
	}

	var out []report.Finding
	for _, w := range c.Workflows {
		for _, job := range w.Jobs {
			for _, step := range job.Steps {
				u := strings.ToLower(step.Uses)
				if !strings.HasPrefix(u, "actions/setup-node") && !strings.HasPrefix(u, "actions/setup-python") {
					continue
				}
				cache := strings.ToLower(strings.TrimSpace(step.With["cache"]))
				if cache == "" {
					continue
				}
				candidates, ok := lockfiles[cache]
				if !ok {
					continue
				}
				found := false
				for _, name := range candidates {
					if c.TrackedSet[name] {
						found = true
						break
					}
				}
				if found {
					continue
				}
				out = append(out, report.New(
					"cache-without-lockfile", report.SeverityHigh,
					fmt.Sprintf("CI caches %s, but no %s file is tracked", cache, cache),
					"setup-* fails the job when it cannot compute a cache key from a lockfile. This breaks on the first run, before any test executes.",
				).
					At(w.Path, 0).
					WithEvidence(fmt.Sprintf("cache: %s  (looked for %s)", cache, strings.Join(candidates, ", "))).
					WithFix("commit the lockfile, or remove the cache setting"))
			}
		}
	}
	return out
}

func checkMissingScript(c *scan.Context) []report.Finding {
	scripts := packageScripts(c)
	makeTargets := makefileTargets(c)

	runRE := regexp.MustCompile(`\b(npm|pnpm|yarn)[ \t]+run[ \t]+([A-Za-z0-9:_.-]+)`)
	testRE := regexp.MustCompile(`\b(npm|pnpm|yarn)[ \t]+test\b`)
	makeRE := regexp.MustCompile(`(?m)(?:^|&&|\|\|)[ \t]*make[ \t]+([A-Za-z0-9_.-]+)`)

	var out []report.Finding
	seen := map[string]bool{}

	for _, w := range c.Workflows {
		for _, job := range w.Jobs {
			for _, step := range job.Steps {
				for _, m := range runRE.FindAllStringSubmatch(step.Run, -1) {
					name := m[2]
					if _, ok := scripts[name]; ok || scripts == nil {
						continue
					}
					key := w.Path + "|" + name
					if seen[key] {
						continue
					}
					seen[key] = true
					out = append(out, missingScriptFinding(w.Path, name, m[0]))
				}
				if testRE.MatchString(step.Run) {
					if _, ok := scripts["test"]; !ok && scripts != nil {
						key := w.Path + "|test"
						if !seen[key] {
							seen[key] = true
							out = append(out, missingScriptFinding(w.Path, "test", "npm test"))
						}
					}
				}
				for _, m := range makeRE.FindAllStringSubmatch(step.Run, -1) {
					name := m[1]
					if makeTargets == nil {
						continue
					}
					if _, ok := makeTargets[name]; ok {
						continue
					}
					key := w.Path + "|make:" + name
					if seen[key] {
						continue
					}
					seen[key] = true
					out = append(out, report.New(
						"missing-script", report.SeverityHigh,
						fmt.Sprintf("CI runs `make %s`, but no such target exists", name),
						"The job fails on its first command, which means none of the tests it was supposed to protect ever run.",
					).At(w.Path, 0).WithEvidence(m[0]).WithFix(fmt.Sprintf("add the %q target to the Makefile, or correct the workflow", name)))
				}
			}
		}
	}
	return out
}

func missingScriptFinding(file, name, evidence string) report.Finding {
	return report.New(
		"missing-script", report.SeverityHigh,
		fmt.Sprintf("CI runs `npm run %s`, but package.json has no %q script", name, name),
		"The job fails on its first command, which means none of the tests it was supposed to protect ever run.",
	).At(file, 0).WithEvidence(evidence).WithFix(fmt.Sprintf("add the %q script to package.json, or correct the workflow", name))
}

func packageScripts(c *scan.Context) map[string]string {
	if !c.TrackedSet["package.json"] {
		return nil
	}
	data, err := c.ReadFile("package.json")
	if err != nil || data == nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		c.Warnings = append(c.Warnings, "could not parse package.json, so its scripts were not checked: "+err.Error())
		return nil
	}
	if pkg.Scripts == nil {
		return map[string]string{}
	}
	return pkg.Scripts
}

var makeTargetRE = regexp.MustCompile(`(?m)^([A-Za-z0-9_][A-Za-z0-9_.-]*)[ \t]*:([^=]|$)`)

func makefileTargets(c *scan.Context) map[string]string {
	for _, name := range []string{"Makefile", "makefile", "GNUmakefile"} {
		if !c.TrackedSet[name] {
			continue
		}
		data, err := c.ReadFile(name)
		if err != nil || data == nil {
			return nil
		}
		targets := map[string]string{}
		for _, m := range makeTargetRE.FindAllStringSubmatch(string(data), -1) {
			targets[m[1]] = name
		}
		return targets
	}
	return nil
}

func checkDeadPathFilter(c *scan.Context) []report.Finding {
	var out []report.Finding
	seen := map[string]bool{}

	for _, w := range c.Workflows {
		for _, t := range w.Triggers {
			for _, p := range t.Paths {
				if strings.ContainsAny(p, "*?[") || strings.HasPrefix(p, "!") {
					continue
				}
				clean := strings.TrimSuffix(strings.TrimPrefix(p, "./"), "/")
				if clean == "" || clean == "." {
					continue
				}
				isDir := c.Resolver.IsDir(clean)
				if c.Resolver.Exists(clean) && !isDir {
					continue
				}
				key := w.Path + "|" + p
				if seen[key] {
					continue
				}
				seen[key] = true

				title := fmt.Sprintf("workflow triggers on %q, which does not exist", p)
				detail := "The path filter can never match, so this workflow silently never runs for the changes it was written for."
				fix := "correct the path, or remove the filter"
				if isDir {
					title = fmt.Sprintf("workflow triggers on the directory %q, but paths filters match files", p)
					detail = "A paths filter is compared against changed file names. Naming a directory matches nothing, so changes inside it never start this workflow."
					fix = fmt.Sprintf("use %q to cover everything inside it", strings.TrimSuffix(p, "/")+"/**")
				}

				out = append(out, report.New(
					"dead-path-filter", report.SeverityMedium,
					title,
					detail,
				).At(w.Path, 0).WithEvidence("paths: "+p).WithFix(fix))
			}
		}
	}
	return out
}

// --- dependency manifests -------------------------------------------------

func checkLockfileDrift(c *scan.Context) []report.Finding {
	lockCandidates := []string{"package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml"}

	lock := ""
	for _, name := range lockCandidates {
		if c.TrackedSet[name] {
			lock = name
			break
		}
	}

	usesCI := workflowRuns(c, "npm ci") || workflowRuns(c, "npm ci --")

	if lock == "" {
		for _, name := range lockCandidates {
			if c.IgnoredSet[name] {
				return []report.Finding{report.New(
					"lockfile-drift", report.SeverityHigh,
					name+" is ignored by git",
					"Every install on every machine resolves a different dependency tree. CI cannot reproduce a build that was never recorded.",
				).At(".gitignore", 0).WithFix("remove it from .gitignore and commit it")}
			}
		}
		return nil
	}

	if !c.TrackedSet["package.json"] {
		return nil
	}

	pkgStatus := c.Status["package.json"]
	lockStatus := c.Status[lock]
	if pkgStatus == "" || lockStatus != "" {
		return nil
	}

	severity := report.SeverityMedium
	detail := "package.json has uncommitted changes and the lockfile does not. Whoever installs next, CI included, resolves dependencies differently from you."
	if usesCI {
		severity = report.SeverityHigh
		detail = "CI runs `npm ci`, which refuses to install when package.json and the lockfile disagree. The build stops before any test runs."
	}

	return []report.Finding{report.New(
		"lockfile-drift", severity,
		"package.json changed, "+lock+" did not",
		detail,
	).
		At("package.json", 0).
		WithEvidence("git status: " + strings.TrimSpace(pkgStatus) + " package.json").
		WithFix("run the package manager's install command and commit the updated lockfile")}
}

func workflowRuns(c *scan.Context, needle string) bool {
	for _, w := range c.Workflows {
		for _, job := range w.Jobs {
			for _, step := range job.Steps {
				if strings.Contains(step.Run, needle) {
					return true
				}
			}
		}
	}
	return false
}
