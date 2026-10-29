// Package git is Theo's adapter for the git command-line tool. It only reads
// repository state: it never changes branches, files or worktrees.
package git

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ElMaxter99/Theo/internal/execx"
)

// Worktree is one entry of `git worktree list --porcelain`.
type Worktree struct {
	Path     string `json:"path"`
	Head     string `json:"head,omitempty"`
	Branch   string `json:"branch,omitempty"` // short name, empty when detached or bare
	Detached bool   `json:"detached,omitempty"`
	Bare     bool   `json:"bare,omitempty"`
	Locked   bool   `json:"locked,omitempty"`
	Prunable bool   `json:"prunable,omitempty"` // its directory no longer exists
}

// Git runs git commands.
type Git struct{}

// TopLevel returns the root directory of the working tree that contains dir.
func (Git) TopLevel(ctx context.Context, dir string) (string, error) {
	out, err := execx.Run(ctx, dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%s is not inside a Git repository: %w", dir, err)
	}
	return filepath.Clean(strings.TrimSpace(string(out))), nil
}

// Worktrees lists the worktrees of the repository that contains dir. The
// main worktree comes first.
func (Git) Worktrees(ctx context.Context, dir string) ([]Worktree, error) {
	out, err := execx.Run(ctx, dir, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return ParseWorktrees(out)
}

// Dirty reports whether the worktree at dir has uncommitted changes,
// including untracked files.
func (Git) Dirty(ctx context.Context, dir string) (bool, error) {
	out, err := execx.Run(ctx, dir, "git", "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}

// ParseWorktrees parses the output of `git worktree list --porcelain`.
func ParseWorktrees(out []byte) ([]Worktree, error) {
	var (
		list    []Worktree
		current *Worktree
	)
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			current = nil
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		if key == "worktree" {
			list = append(list, Worktree{Path: filepath.Clean(filepath.FromSlash(value))})
			current = &list[len(list)-1]
			continue
		}
		if current == nil {
			return nil, fmt.Errorf("unexpected line in git worktree output: %q", line)
		}
		switch key {
		case "HEAD":
			current.Head = value
		case "branch":
			current.Branch = strings.TrimPrefix(value, "refs/heads/")
		case "detached":
			current.Detached = true
		case "bare":
			current.Bare = true
		case "locked":
			current.Locked = true
		case "prunable":
			current.Prunable = true
		}
	}
	return list, sc.Err()
}
