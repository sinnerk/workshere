// Package gitx is the thin slice of git workshere needs: the repository root,
// the list of tracked files, the symlink bit, and the working tree status.
package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotARepo means no .git directory was found walking upwards.
var ErrNotARepo = errors.New("not a git repository")

// Repo is an open repository.
type Repo struct {
	Root string
}

// Find walks up from start until it finds a .git entry.
func Find(start string) (*Repo, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return nil, err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return &Repo{Root: dir}, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, ErrNotARepo
		}
		dir = parent
	}
}

func (r *Repo) run(args ...string) ([]byte, error) {
	full := append([]string{"-C", r.Root}, args...)
	cmd := exec.Command("git", full...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return out, nil
}

func splitZ(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	parts := bytes.Split(b, []byte{0})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimRight(string(p), "\r\n")
		if s == "" {
			continue
		}
		out = append(out, filepath.ToSlash(s))
	}
	return out
}

// Tracked lists files in the index, relative to the root, slash separated.
func (r *Repo) Tracked() ([]string, error) {
	out, err := r.run("ls-files", "-z")
	if err != nil {
		return nil, err
	}
	return splitZ(out), nil
}

// Untracked lists files git would add, i.e. not ignored.
func (r *Repo) Untracked() ([]string, error) {
	out, err := r.run("ls-files", "-o", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	return splitZ(out), nil
}

// Ignored lists files excluded by .gitignore.
func (r *Repo) Ignored() ([]string, error) {
	out, err := r.run("ls-files", "-o", "-i", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	return splitZ(out), nil
}

// Symlinks lists tracked entries stored with mode 120000.
func (r *Repo) Symlinks() (map[string]bool, error) {
	out, err := r.run("ls-files", "-s", "-z")
	if err != nil {
		return nil, err
	}
	links := map[string]bool{}
	for _, entry := range splitZ(out) {
		// Format: "<mode> <object> <stage>\t<path>"
		tab := strings.IndexByte(entry, '\t')
		if tab < 0 {
			continue
		}
		meta, p := entry[:tab], entry[tab+1:]
		if strings.HasPrefix(meta, "120000 ") {
			links[p] = true
		}
	}
	return links, nil
}

// Status maps path to its two-letter porcelain code.
func (r *Repo) Status() (map[string]string, error) {
	out, err := r.run("status", "--porcelain=v1", "-z", "--untracked-files=no")
	if err != nil {
		return nil, err
	}
	status := map[string]string{}
	for _, entry := range splitZ(out) {
		if len(entry) < 4 {
			continue
		}
		code := entry[:2]
		p := strings.TrimSpace(entry[3:])
		// Renames arrive as "R  new\0old"; the old half was consumed by the split.
		status[p] = code
	}
	return status, nil
}

// AttributesExists reports whether .gitattributes is tracked or present.
func (r *Repo) AttributesExists() bool {
	if _, err := os.Stat(filepath.Join(r.Root, ".gitattributes")); err == nil {
		return true
	}
	return false
}
