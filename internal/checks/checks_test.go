package checks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sinnerk/workshere/internal/report"
	"github.com/sinnerk/workshere/internal/scan"
)

// newContext builds a scanned repository without git: every field the checks
// touch is set explicitly, so the tests describe exactly the situation they
// care about.
func newContext(t *testing.T, files map[string]string) *scan.Context {
	t.Helper()
	root := t.TempDir()

	tracked := make([]string, 0, len(files))
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		tracked = append(tracked, p)
	}

	c := &scan.Context{
		Root:          root,
		Resolver:      scan.NewResolver(root),
		Tracked:       tracked,
		TrackedSet:    map[string]bool{},
		UntrackedSet:  map[string]bool{},
		IgnoredSet:    map[string]bool{},
		Symlinks:      map[string]bool{},
		Status:        map[string]string{},
		LocalVersions: map[string]string{},
	}
	for _, p := range tracked {
		c.TrackedSet[p] = true
	}
	return c
}

func countCheck(findings []report.Finding, name string) int {
	n := 0
	for _, f := range findings {
		if f.Check == name {
			n++
		}
	}
	return n
}

func TestCaseMismatchOnRelativeImport(t *testing.T) {
	c := newContext(t, map[string]string{
		"src/api/utils.ts":    "export const x = 1\n",
		"src/api/handlers.ts": "import { x } from './Utils'\n",
	})

	got := checkCaseMismatch(c)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(got), got)
	}
	f := got[0]
	if f.Severity != report.SeverityHigh {
		t.Errorf("severity = %v, want high", f.Severity)
	}
	if f.Line != 1 {
		t.Errorf("line = %d, want 1", f.Line)
	}
	if !strings.Contains(f.Title, "utils.ts") {
		t.Errorf("title should name the real file: %q", f.Title)
	}
}

func TestCaseMismatchIgnoresExactImport(t *testing.T) {
	c := newContext(t, map[string]string{
		"src/utils.ts":    "export const x = 1\n",
		"src/handlers.ts": "import { x } from './utils'\n",
	})

	if got := checkCaseMismatch(c); len(got) != 0 {
		t.Fatalf("expected no findings, got %+v", got)
	}
}

func TestCaseMismatchOnDirectoryComponent(t *testing.T) {
	c := newContext(t, map[string]string{
		"src/Utils/helper.ts": "export const x = 1\n",
		"src/main.ts":         "import './utils/helper'\n",
	})

	got := checkCaseMismatch(c)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Fix, "Utils/helper") {
		t.Errorf("fix should point at the real path: %q", got[0].Fix)
	}
}

func TestUntrackedImport(t *testing.T) {
	c := newContext(t, map[string]string{
		"src/main.ts": "import './generated'\n",
	})
	// The file exists on disk but is not in the index.
	full := filepath.Join(c.Root, "src", "generated.ts")
	if err := os.WriteFile(full, []byte("export {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := checkUntrackedImports(c)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Title, "src/generated.ts") {
		t.Errorf("title = %q", got[0].Title)
	}
}

func TestIgnoredImportSaysSo(t *testing.T) {
	c := newContext(t, map[string]string{
		"src/main.ts": "import './generated'\n",
	})
	full := filepath.Join(c.Root, "src", "generated.ts")
	if err := os.WriteFile(full, []byte("export {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.IgnoredSet["src/generated.ts"] = true

	got := checkUntrackedImports(c)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %+v", got)
	}
	if !strings.Contains(got[0].Detail, ".gitignore") {
		t.Errorf("detail should mention .gitignore: %q", got[0].Detail)
	}
}

func TestPythonRelativeImportCase(t *testing.T) {
	c := newContext(t, map[string]string{
		"pkg/helpers.py": "x = 1\n",
		"pkg/main.py":    "from .Helpers import x\n",
	})

	got := checkCaseMismatch(c)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(got), got)
	}
}

func TestCRLFShebang(t *testing.T) {
	c := newContext(t, map[string]string{
		"scripts/build.sh": "#!/bin/sh\r\necho hi\r\n",
		"scripts/fine.sh":  "#!/bin/sh\necho hi\n",
	})

	got := checkCRLFShebang(c)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(got), got)
	}
	if got[0].File != "scripts/build.sh" {
		t.Errorf("file = %q", got[0].File)
	}
}

func TestEnvTracked(t *testing.T) {
	c := newContext(t, map[string]string{
		".env":         "SECRET=1\n",
		".env.example": "SECRET=\n",
	})

	got := checkEnvTracked(c)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %+v", got)
	}
	if got[0].File != ".env" {
		t.Errorf("file = %q", got[0].File)
	}
}

func TestAbsolutePath(t *testing.T) {
	c := newContext(t, map[string]string{
		"src/config.ts": `export const root = "C:\Users\alice\project"` + "\n",
		"src/clean.ts":  `export const root = "./project"` + "\n",
	})

	got := checkAbsolutePaths(c)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(got), got)
	}
	if got[0].File != "src/config.ts" {
		t.Errorf("file = %q", got[0].File)
	}
}

func TestCaseCollision(t *testing.T) {
	// Windows cannot create both files, so feed the check a tracked list.
	c := &scan.Context{Tracked: []string{"src/Utils.ts", "src/utils.ts"}}

	got := checkCaseCollision(c)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %+v", got)
	}
}

func TestRuntimeMismatch(t *testing.T) {
	c := newContext(t, map[string]string{
		".github/workflows/ci.yml": `on: push
jobs:
  test:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        node: [18]
    steps:
      - uses: actions/setup-node@v4
        with:
          node-version: ${{ matrix.node }}
`,
	})
	w, err := scan.ParseWorkflow(".github/workflows/ci.yml", []byte(`on: push
jobs:
  test:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        node: [18]
    steps:
      - uses: actions/setup-node@v4
        with:
          node-version: ${{ matrix.node }}
`))
	if err != nil {
		t.Fatal(err)
	}
	c.Workflows = []scan.Workflow{w}
	c.LocalVersions["node"] = "v22.3.0"

	got := checkRuntimeMismatch(c)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Title, "18") || !strings.Contains(got[0].Title, "22") {
		t.Errorf("title should name both versions: %q", got[0].Title)
	}
}

func TestRuntimeMatchIsQuiet(t *testing.T) {
	c := newContext(t, map[string]string{})
	w, err := scan.ParseWorkflow("ci.yml", []byte(`on: push
jobs:
  t:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with:
          node-version: "22"
`))
	if err != nil {
		t.Fatal(err)
	}
	c.Workflows = []scan.Workflow{w}
	c.LocalVersions["node"] = "v22.9.0"

	if got := checkRuntimeMismatch(c); len(got) != 0 {
		t.Fatalf("expected no findings, got %+v", got)
	}
}

func TestCacheWithoutLockfile(t *testing.T) {
	c := newContext(t, map[string]string{
		"package.json": "{}",
	})
	w, err := scan.ParseWorkflow(".github/workflows/ci.yml", []byte(`on: push
jobs:
  t:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with:
          node-version: 20
          cache: npm
`))
	if err != nil {
		t.Fatal(err)
	}
	c.Workflows = []scan.Workflow{w}

	got := checkCacheWithoutLockfile(c)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %+v", got)
	}
}

func TestMissingNpmScript(t *testing.T) {
	c := newContext(t, map[string]string{
		"package.json": `{"scripts":{"test":"vitest"}}`,
	})
	w, err := scan.ParseWorkflow(".github/workflows/ci.yml", []byte(`on: push
jobs:
  t:
    runs-on: ubuntu-latest
    steps:
      - run: npm run lint
`))
	if err != nil {
		t.Fatal(err)
	}
	c.Workflows = []scan.Workflow{w}

	got := checkMissingScript(c)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Title, "lint") {
		t.Errorf("title = %q", got[0].Title)
	}
}

func TestPresentNpmScriptIsQuiet(t *testing.T) {
	c := newContext(t, map[string]string{
		"package.json": `{"scripts":{"test":"vitest","lint":"eslint ."}}`,
	})
	w, _ := scan.ParseWorkflow("ci.yml", []byte(`on: push
jobs:
  t:
    runs-on: ubuntu-latest
    steps:
      - run: npm run lint
`))
	c.Workflows = []scan.Workflow{w}

	if got := checkMissingScript(c); len(got) != 0 {
		t.Fatalf("expected no findings, got %+v", got)
	}
}

func TestDeadPathFilter(t *testing.T) {
	c := newContext(t, map[string]string{
		"src/main.go":          "package main\n",
		"services/api/main.go": "package main\n",
	})
	w, err := scan.ParseWorkflow(".github/workflows/ci.yml", []byte(`on:
  push:
    paths:
      - "services/api"
      - "src/**"
jobs:
  t:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`))
	if err != nil {
		t.Fatal(err)
	}
	c.Workflows = []scan.Workflow{w}

	got := checkDeadPathFilter(c)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Title, "directory") {
		t.Errorf("title should explain the directory problem: %q", got[0].Title)
	}
	if !strings.Contains(got[0].Fix, "services/api/**") {
		t.Errorf("fix = %q", got[0].Fix)
	}
}

func TestDeadPathFilterOnMissingPath(t *testing.T) {
	c := newContext(t, map[string]string{
		"src/main.go": "package main\n",
	})
	w, err := scan.ParseWorkflow(".github/workflows/ci.yml", []byte(`on:
  push:
    paths:
      - "backend/main.go"
jobs:
  t:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`))
	if err != nil {
		t.Fatal(err)
	}
	c.Workflows = []scan.Workflow{w}

	got := checkDeadPathFilter(c)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Title, "does not exist") {
		t.Errorf("title = %q", got[0].Title)
	}
}

func TestLockfileDrift(t *testing.T) {
	c := newContext(t, map[string]string{
		"package.json":      `{"scripts":{}}`,
		"package-lock.json": `{}`,
	})
	c.Status["package.json"] = " M"

	got := checkLockfileDrift(c)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %+v", got)
	}
	if got[0].Severity != report.SeverityMedium {
		t.Errorf("severity = %v, want medium when CI does not use npm ci", got[0].Severity)
	}
}

func TestLockfileDriftEscalatesWithNpmCI(t *testing.T) {
	c := newContext(t, map[string]string{
		"package.json":      `{"scripts":{}}`,
		"package-lock.json": `{}`,
	})
	c.Status["package.json"] = " M"
	w, _ := scan.ParseWorkflow("ci.yml", []byte(`on: push
jobs:
  t:
    runs-on: ubuntu-latest
    steps:
      - run: npm ci
`))
	c.Workflows = []scan.Workflow{w}

	got := checkLockfileDrift(c)
	if len(got) != 1 || got[0].Severity != report.SeverityHigh {
		t.Fatalf("expected one high finding, got %+v", got)
	}
}

func TestUndeclaredEnv(t *testing.T) {
	c := newContext(t, map[string]string{
		".env.example": "DATABASE_URL=\n",
		"src/db.ts":    "const a = process.env.DATABASE_URL\nconst b = process.env.STRIPE_KEY\n",
	})

	got := checkUndeclaredEnv(c)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %+v", got)
	}
	if !strings.Contains(got[0].Evidence, "STRIPE_KEY") {
		t.Errorf("evidence = %q", got[0].Evidence)
	}
	if strings.Contains(got[0].Evidence, "DATABASE_URL") {
		t.Errorf("declared variable should not be reported: %q", got[0].Evidence)
	}
}

func TestUndeclaredEnvSkippedWithoutConvention(t *testing.T) {
	c := newContext(t, map[string]string{
		"src/db.ts": "const b = process.env.STRIPE_KEY\n",
	})

	if got := checkUndeclaredEnv(c); len(got) != 0 {
		t.Fatalf("expected no findings, got %+v", got)
	}
}

func TestLargeFile(t *testing.T) {
	c := newContext(t, map[string]string{"src/small.go": "package x\n"})
	big := filepath.Join(c.Root, "assets", "video.bin")
	if err := os.MkdirAll(filepath.Dir(big), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(51 << 20); err != nil {
		t.Fatal(err)
	}
	f.Close()
	c.Tracked = append(c.Tracked, "assets/video.bin")

	got := checkLargeFiles(c)
	if len(got) != 1 || got[0].Severity != report.SeverityMedium {
		t.Fatalf("expected one medium finding, got %+v", got)
	}
}
