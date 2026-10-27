package scan

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// Resolver answers the question git actually cares about: what is the exact
// spelling of this file on disk? os.Stat cannot answer it on Windows or macOS,
// because both fold case.
type Resolver struct {
	root string

	mu   sync.Mutex
	dirs map[string][]string
}

// NewResolver builds a resolver rooted at an absolute repository path.
func NewResolver(root string) *Resolver {
	return &Resolver{root: root, dirs: map[string][]string{}}
}

// names returns the exact directory entry names for a slash-relative directory.
// "" means the repository root.
func (r *Resolver) names(dir string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if got, ok := r.dirs[dir]; ok {
		return got
	}

	full := r.root
	if dir != "" {
		full = filepath.Join(r.root, filepath.FromSlash(dir))
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		r.dirs[dir] = nil
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	r.dirs[dir] = out
	return out
}

// Resolve returns the on-disk spelling of a slash-relative path and whether it
// matched case for case. A missing path returns ("", false).
func (r *Resolver) Resolve(rel string) (actual string, exact bool) {
	clean := path.Clean(filepath.ToSlash(rel))
	if clean == "." || clean == "" || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", false
	}

	parts := strings.Split(clean, "/")
	exact = true
	cur := ""
	built := make([]string, 0, len(parts))

	for _, want := range parts {
		if want == "." {
			continue
		}
		var hit string
		for _, name := range r.names(cur) {
			if name == want {
				hit = name
				break
			}
		}
		if hit == "" {
			exact = false
			for _, name := range r.names(cur) {
				if strings.EqualFold(name, want) {
					hit = name
					break
				}
			}
		}
		if hit == "" {
			return "", false
		}
		built = append(built, hit)
		if cur == "" {
			cur = hit
		} else {
			cur = cur + "/" + hit
		}
	}

	return strings.Join(built, "/"), exact
}

// Exists reports whether the path is present, in any case.
func (r *Resolver) Exists(rel string) bool {
	_, ok := r.Resolve(rel)
	return ok
}

// IsDir reports whether the path resolves to a directory.
func (r *Resolver) IsDir(rel string) bool {
	actual, _ := r.Resolve(rel)
	if actual == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(actual)))
	return err == nil && info.IsDir()
}
