package scan

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/sinnerk/workshere/internal/gitx"
)

// Context is the repository as workshere sees it: what git tracks, what it
// ignores, what the working tree looks like, and what CI is configured to run.
type Context struct {
	Root     string
	Resolver *Resolver

	Tracked      []string
	TrackedSet   map[string]bool
	Untracked    []string
	UntrackedSet map[string]bool
	IgnoredSet   map[string]bool
	Symlinks     map[string]bool
	Status       map[string]string

	Workflows        []Workflow
	LocalVersions    map[string]string
	HasGitattributes bool

	Warnings []string
}

// skipDirs are never worth scanning: generated, vendored, or fixture data.
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true,
	".git": true, "testdata": true, "fixtures": true, "third_party": true,
	".venv": true, "venv": true, "__pycache__": true, "target": true,
	".next": true, ".nuxt": true, "coverage": true, ".cache": true,
}

var sourceExts = map[string]bool{
	".js": true, ".jsx": true, ".ts": true, ".tsx": true,
	".mjs": true, ".cjs": true, ".mts": true, ".cts": true,
	".vue": true, ".svelte": true, ".py": true, ".go": true,
	".rs": true, ".java": true, ".kt": true, ".cs": true,
	".rb": true, ".php": true, ".sh": true, ".bash": true,
	".zsh": true, ".ps1": true, ".yml": true, ".yaml": true,
	".json": true, ".toml": true, ".ini": true, ".cfg": true,
	".conf": true, ".env": true,
}

// MaxSourceBytes caps how much of a single file is read for heuristics.
const MaxSourceBytes = 512 * 1024

// Load inspects the repository that contains dir.
func Load(dir string) (*Context, error) {
	repo, err := gitx.Find(dir)
	if err != nil {
		return nil, err
	}

	c := &Context{
		Root:          repo.Root,
		Resolver:      NewResolver(repo.Root),
		TrackedSet:    map[string]bool{},
		UntrackedSet:  map[string]bool{},
		IgnoredSet:    map[string]bool{},
		Symlinks:      map[string]bool{},
		Status:        map[string]string{},
		LocalVersions: map[string]string{},
	}

	tracked, err := repo.Tracked()
	if err != nil {
		return nil, err
	}
	c.Tracked = tracked
	for _, p := range tracked {
		c.TrackedSet[p] = true
	}

	if untracked, err := repo.Untracked(); err != nil {
		c.Warnings = append(c.Warnings, "could not list untracked files: "+err.Error())
	} else {
		c.Untracked = untracked
		for _, p := range untracked {
			c.UntrackedSet[p] = true
		}
	}

	if ignored, err := repo.Ignored(); err != nil {
		c.Warnings = append(c.Warnings, "could not list ignored files: "+err.Error())
	} else {
		for _, p := range ignored {
			c.IgnoredSet[p] = true
		}
	}

	if links, err := repo.Symlinks(); err != nil {
		c.Warnings = append(c.Warnings, "could not read index modes: "+err.Error())
	} else {
		c.Symlinks = links
	}

	if status, err := repo.Status(); err != nil {
		c.Warnings = append(c.Warnings, "could not read working tree status: "+err.Error())
	} else {
		c.Status = status
	}

	c.HasGitattributes = repo.AttributesExists()
	c.Workflows = c.loadWorkflows()
	c.LocalVersions = detectLocalVersions()

	return c, nil
}

// ReadFile reads a tracked file relative to the root.
func (c *Context) ReadFile(rel string) ([]byte, error) {
	full := filepath.Join(c.Root, filepath.FromSlash(rel))
	info, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxSourceBytes {
		return nil, nil
	}
	return os.ReadFile(full)
}

// Size returns the on-disk size of a tracked file.
func (c *Context) Size(rel string) (int64, bool) {
	info, err := os.Stat(filepath.Join(c.Root, filepath.FromSlash(rel)))
	if err != nil {
		return 0, false
	}
	return info.Size(), true
}

// SourceFiles lists tracked text files worth heuristically scanning.
func (c *Context) SourceFiles() []string {
	out := make([]string, 0, len(c.Tracked))
	for _, p := range c.Tracked {
		if skippedPath(p) {
			continue
		}
		if !sourceExts[strings.ToLower(path.Ext(p))] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// ScriptFiles lists tracked files that could carry a shebang.
func (c *Context) ScriptFiles() []string {
	out := make([]string, 0, 32)
	for _, p := range c.Tracked {
		if skippedPath(p) {
			continue
		}
		ext := strings.ToLower(path.Ext(p))
		base := path.Base(p)
		if ext == "" || ext == ".sh" || ext == ".bash" || ext == ".zsh" ||
			ext == ".py" || ext == ".pl" || ext == ".rb" {
			out = append(out, p)
			continue
		}
		if strings.HasPrefix(base, "Dockerfile") || base == "Makefile" {
			continue
		}
	}
	return out
}

func skippedPath(p string) bool {
	parts := strings.Split(p, "/")
	for _, d := range parts[:max(0, len(parts)-1)] {
		if skipDirs[d] {
			return true
		}
	}
	return false
}

func (c *Context) loadWorkflows() []Workflow {
	var out []Workflow
	for _, p := range c.Tracked {
		lower := strings.ToLower(p)
		if !strings.HasPrefix(lower, ".github/workflows/") {
			continue
		}
		if !strings.HasSuffix(lower, ".yml") && !strings.HasSuffix(lower, ".yaml") {
			continue
		}
		data, err := c.ReadFile(p)
		if err != nil {
			c.Warnings = append(c.Warnings, "could not read "+p+": "+err.Error())
			continue
		}
		if data == nil {
			c.Warnings = append(c.Warnings, p+" is too large to inspect")
			continue
		}
		w, err := ParseWorkflow(p, data)
		if err != nil {
			c.Warnings = append(c.Warnings, "could not parse "+p+": "+err.Error())
			continue
		}
		out = append(out, w)
	}
	return out
}

// detectLocalVersions asks the machine what it actually runs. A missing tool is
// not an error; the checks simply skip that language.
func detectLocalVersions() map[string]string {
	versions := map[string]string{}
	if v := capture("node", "--version"); v != "" {
		versions["node"] = v
	}
	if v := capture("go", "version"); v != "" {
		versions["go"] = v
	}
	for _, name := range []string{"python", "python3"} {
		if v := capture(name, "--version"); v != "" {
			versions["python"] = v
			break
		}
	}
	return versions
}

func capture(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
