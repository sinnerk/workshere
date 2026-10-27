package checks

import (
	"bytes"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sinnerk/workshere/internal/report"
	"github.com/sinnerk/workshere/internal/scan"
)

// --- imports ---------------------------------------------------------------

var jsSpecRes = []*regexp.Regexp{
	regexp.MustCompile(`(?m)\bfrom[ \t]+['"]([^'"\n]+)['"]`),
	regexp.MustCompile(`(?m)\brequire[ \t]*\([ \t]*['"]([^'"\n]+)['"][ \t]*\)`),
	regexp.MustCompile(`(?m)\bimport[ \t]*\([ \t]*['"]([^'"\n]+)['"][ \t]*\)`),
	regexp.MustCompile(`(?m)^[ \t]*import[ \t]+['"]([^'"\n]+)['"]`),
}

var pyFromRE = regexp.MustCompile(`(?m)^[ \t]*from[ \t]+(\.+[A-Za-z0-9_.]*)[ \t]+import\b`)

var jsSuffixes = []string{"", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts", ".json"}
var jsIndexes = []string{"index.ts", "index.tsx", "index.js", "index.jsx", "index.mjs", "index.cjs"}

type resolvedImport struct {
	file   string
	line   int
	spec   string
	target string
	exact  bool
}

func lineAt(data []byte, offset int) int {
	if offset <= 0 {
		return 1
	}
	if offset > len(data) {
		offset = len(data)
	}
	return bytes.Count(data[:offset], []byte("\n")) + 1
}

// resolveJS resolves a relative JS/TS specifier to a tracked or untracked file.
// exact reports whether every path component matches the on-disk case.
func resolveJS(c *scan.Context, file, spec string) (actual string, exact, found bool) {
	target := path.Join(path.Dir(file), spec)
	if target == "." || target == ".." || strings.HasPrefix(target, "../") {
		return "", false, false
	}
	for _, suffix := range jsSuffixes {
		if got, ok := c.Resolver.Resolve(target + suffix); got != "" {
			return got, ok, true
		}
	}
	for _, idx := range jsIndexes {
		if got, ok := c.Resolver.Resolve(path.Join(target, idx)); got != "" {
			return got, ok, true
		}
	}
	return "", false, false
}

func resolvePy(c *scan.Context, file, module string) (string, bool) {
	level := 0
	for level < len(module) && module[level] == '.' {
		level++
	}
	rest := module[level:]
	if rest == "" {
		return "", false
	}

	base := path.Dir(file)
	for i := 1; i < level; i++ {
		base = path.Dir(base)
	}
	target := path.Join(base, strings.ReplaceAll(rest, ".", "/"))

	for _, candidate := range []string{target + ".py", path.Join(target, "__init__.py")} {
		if actual, exact := c.Resolver.Resolve(candidate); actual != "" {
			return actual, exact
		}
	}
	return "", false
}

// collectImports reads every source file once and resolves the relative imports
// it can see. Imports that cannot be resolved are ignored rather than guessed
// at: a generated or aliased module would otherwise produce false findings.
func collectImports(c *scan.Context) []resolvedImport {
	var out []resolvedImport

	for _, p := range c.SourceFiles() {
		lower := strings.ToLower(p)
		isJS := strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".jsx") ||
			strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".tsx") ||
			strings.HasSuffix(lower, ".mjs") || strings.HasSuffix(lower, ".cjs") ||
			strings.HasSuffix(lower, ".mts") || strings.HasSuffix(lower, ".cts") ||
			strings.HasSuffix(lower, ".vue") || strings.HasSuffix(lower, ".svelte")
		isPy := strings.HasSuffix(lower, ".py")
		if !isJS && !isPy {
			continue
		}

		data, err := c.ReadFile(p)
		if err != nil || data == nil {
			continue
		}

		if isJS {
			for _, re := range jsSpecRes {
				for _, m := range re.FindAllSubmatchIndex(data, -1) {
					if len(m) < 4 {
						continue
					}
					spec := string(data[m[2]:m[3]])
					if !strings.HasPrefix(spec, ".") {
						continue
					}
					target, exact, found := resolveJS(c, p, spec)
					if !found {
						continue
					}
					out = append(out, resolvedImport{
						file: p, line: lineAt(data, m[0]), spec: spec, target: target, exact: exact,
					})
				}
			}
		}

		if isPy {
			for _, m := range pyFromRE.FindAllSubmatchIndex(data, -1) {
				if len(m) < 4 {
					continue
				}
				module := string(data[m[2]:m[3]])
				target, exact := resolvePy(c, p, module)
				if target == "" {
					continue
				}
				out = append(out, resolvedImport{
					file: p, line: lineAt(data, m[0]), spec: module, target: target, exact: exact,
				})
			}
		}
	}

	return out
}

func checkCaseMismatch(c *scan.Context) []report.Finding {
	var out []report.Finding
	seen := map[string]bool{}

	for _, imp := range collectImports(c) {
		if imp.exact {
			continue
		}
		key := imp.file + "|" + imp.spec
		if seen[key] {
			continue
		}
		seen[key] = true

		fixTarget := imp.target
		if path.Ext(imp.spec) == "" {
			// Keep the author's style: an extensionless TypeScript import
			// should not gain an extension from the suggestion.
			fixTarget = strings.TrimSuffix(imp.target, path.Ext(imp.target))
		}

		out = append(out, report.New(
			"case-mismatch", report.SeverityHigh,
			fmt.Sprintf("imports %q, but the file on disk is %q", imp.spec, path.Base(imp.target)),
			"Windows and macOS ignore letter case when opening a file. The Linux runner does not, so this import resolves here and fails there.",
		).
			At(imp.file, imp.line).
			WithEvidence(imp.spec+"  ->  "+imp.target).
			WithFix(fmt.Sprintf("change the import to %q", importPathFor(imp.file, fixTarget))))
	}
	return out
}

// importPathFor rebuilds a relative specifier pointing at target.
func importPathFor(from, target string) string {
	rel, err := filepath.Rel(filepath.FromSlash(path.Dir(from)), filepath.FromSlash(target))
	if err != nil {
		return target
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, ".") {
		rel = "./" + rel
	}
	return rel
}

func checkUntrackedImports(c *scan.Context) []report.Finding {
	var out []report.Finding
	seen := map[string]bool{}

	for _, imp := range collectImports(c) {
		if c.TrackedSet[imp.target] {
			continue
		}
		if seen[imp.target] {
			continue
		}
		seen[imp.target] = true

		detail := "The file exists in this working copy but not in git. A fresh clone, and therefore CI, will not have it."
		if c.IgnoredSet[imp.target] {
			detail = "A .gitignore rule excludes this file, so CI will never receive it even though the import needs it."
		}

		out = append(out, report.New(
			"untracked-import", report.SeverityHigh,
			fmt.Sprintf("imports a file that is not in git: %s", imp.target),
			detail,
		).
			At(imp.file, imp.line).
			WithEvidence(imp.spec).
			WithFix("commit the file, or stop importing it"))
	}
	return out
}

// --- repository shape ------------------------------------------------------

func checkCaseCollision(c *scan.Context) []report.Finding {
	groups := map[string][]string{}
	for _, p := range c.Tracked {
		key := strings.ToLower(p)
		groups[key] = append(groups[key], p)
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []report.Finding
	for _, k := range keys {
		files := groups[k]
		if len(files) < 2 {
			continue
		}
		sort.Strings(files)
		out = append(out, report.New(
			"case-collision", report.SeverityHigh,
			fmt.Sprintf("%d tracked files differ only by letter case", len(files)),
			"Windows and macOS cannot hold both paths in one directory. Checking out this repository there silently loses one of them.",
		).
			At(files[0], 0).
			WithEvidence(strings.Join(files, "  <>  ")).
			WithFix("rename one of them and update its imports"))
	}
	return out
}

func checkEnvTracked(c *scan.Context) []report.Finding {
	secretNames := map[string]bool{
		".env": true, ".env.local": true, ".env.development": true,
		".env.production": true, ".env.test": true, ".env.staging": true,
	}
	var out []report.Finding
	for _, p := range c.Tracked {
		base := strings.ToLower(path.Base(p))
		if !secretNames[base] {
			continue
		}
		out = append(out, report.New(
			"env-tracked", report.SeverityHigh,
			base+" is committed to git",
			"Whatever is in this file is in the repository history, and CI will load it as if it were configuration.",
		).
			At(p, 0).
			WithFix("git rm --cached "+path.Base(p)+", add it to .gitignore, and commit an .env.example instead"))
	}
	return out
}

func checkCRLFShebang(c *scan.Context) []report.Finding {
	var out []report.Finding
	for _, p := range c.ScriptFiles() {
		data, err := c.ReadFile(p)
		if err != nil || len(data) < 2 || data[0] != '#' || data[1] != '!' {
			continue
		}
		nl := bytes.IndexByte(data, '\n')
		if nl <= 0 || data[nl-1] != '\r' {
			continue
		}
		out = append(out, report.New(
			"crlf-script", report.SeverityHigh,
			"shebang line ends with CRLF",
			"On Linux the interpreter name becomes \"bash\\r\" and the script fails with a confusing \"bad interpreter\" error.",
		).
			At(p, 1).
			WithEvidence(strings.TrimRight(string(data[:nl]), "\r")).
			WithFix("convert the file to LF, and add `* text=auto eol=lf` to .gitattributes"))
	}
	return out
}

func checkLargeFiles(c *scan.Context) []report.Finding {
	const warn, reject = 50 << 20, 100 << 20
	var out []report.Finding
	for _, p := range c.Tracked {
		size, ok := c.Size(p)
		if !ok {
			continue
		}
		switch {
		case size >= reject:
			out = append(out, report.New(
				"large-file", report.SeverityHigh,
				fmt.Sprintf("tracked file is %.1f MB", float64(size)/(1<<20)),
				"GitHub refuses files over 100 MB, so the push fails for everyone, not just CI.",
			).At(p, 0).WithFix("move it to a release asset or Git LFS"))
		case size >= warn:
			out = append(out, report.New(
				"large-file", report.SeverityMedium,
				fmt.Sprintf("tracked file is %.1f MB", float64(size)/(1<<20)),
				"GitHub warns above 50 MB and every clone pays for it forever.",
			).At(p, 0).WithFix("move it to a release asset or Git LFS"))
		}
	}
	return out
}

func checkSymlinks(c *scan.Context) []report.Finding {
	paths := make([]string, 0, len(c.Symlinks))
	for p := range c.Symlinks {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var out []report.Finding
	for _, p := range paths {
		out = append(out, report.New(
			"symlink", report.SeverityLow,
			"tracked symlink",
			"Windows checks it out as a plain text file unless core.symlinks is enabled, which changes what the build sees.",
		).At(p, 0).WithFix("vendor the target instead of linking to it"))
	}
	return out
}

var absolutePathRE = regexp.MustCompile(`(?i)(?:[a-z]:[\\/]{1,2}users[\\/][^\\/\s"']+|/users/[a-z0-9._-]+/|/home/[a-z0-9._-]+/)`)

// isTestFile reports whether a path is test code. Fixtures deliberately contain
// fake absolute paths and fake secrets, so judging them as production data is
// exactly the kind of false positive that gets a tool uninstalled.
func isTestFile(p string) bool {
	lower := strings.ToLower(p)
	base := path.Base(lower)
	if strings.HasSuffix(base, "_test.go") ||
		strings.HasSuffix(base, "_test.py") ||
		strings.HasSuffix(base, ".test.ts") || strings.HasSuffix(base, ".test.tsx") ||
		strings.HasSuffix(base, ".test.js") || strings.HasSuffix(base, ".test.jsx") ||
		strings.HasSuffix(base, ".spec.ts") || strings.HasSuffix(base, ".spec.tsx") ||
		strings.HasSuffix(base, ".spec.js") || strings.HasSuffix(base, ".spec.jsx") ||
		strings.HasPrefix(base, "test_") {
		return true
	}
	for _, part := range strings.Split(lower, "/") {
		switch part {
		case "test", "tests", "__tests__", "spec", "specs", "e2e", "__mocks__", "testdata":
			return true
		}
	}
	return false
}

func checkAbsolutePaths(c *scan.Context) []report.Finding {
	var out []report.Finding
	for _, p := range c.SourceFiles() {
		if isTestFile(p) {
			continue
		}
		data, err := c.ReadFile(p)
		if err != nil || data == nil {
			continue
		}
		locs := absolutePathRE.FindAllIndex(data, 3)
		if len(locs) == 0 {
			continue
		}
		line := lineAt(data, locs[0][0])
		match := string(data[locs[0][0]:locs[0][1]])
		out = append(out, report.New(
			"absolute-path", report.SeverityMedium,
			"machine-specific absolute path in a tracked file",
			"This path only exists on the machine that wrote it. CI runs as a different user in a different home directory.",
		).
			At(p, line).
			WithEvidence(match).
			WithFix("make it relative to the repository, or read it from an environment variable"))
	}
	return out
}
