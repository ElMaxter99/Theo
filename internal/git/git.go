// Package git is Theo's adapter for the git command-line tool. It reads
// repository state, and creates and removes worktrees on request. It never
// changes branches or files of a worktree, and never passes --force unless
// the caller asks for it explicitly.
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

// Branch is a local or remote-tracking branch.
type Branch struct {
	Name   string `json:"name"`             // short name; for remote branches, "origin/feature"
	Remote bool   `json:"remote,omitempty"` // a remote-tracking branch
}

// Branches lists the local branches and the known remote-tracking branches
// of the repository that contains dir. Remote HEAD aliases are left out.
func (Git) Branches(ctx context.Context, dir string) ([]Branch, error) {
	out, err := execx.Run(ctx, dir, "git", "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	var list []Branch
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "refs/heads/"):
			list = append(list, Branch{Name: strings.TrimPrefix(line, "refs/heads/")})
		case strings.HasPrefix(line, "refs/remotes/") && !strings.HasSuffix(line, "/HEAD"):
			list = append(list, Branch{Name: strings.TrimPrefix(line, "refs/remotes/"), Remote: true})
		}
	}
	return list, nil
}

// AddWorktree creates a worktree at path. With newBranch, branch is created
// from base (or from the current HEAD of the repository when base is empty);
// otherwise branch must already exist, or be the name of a single remote
// branch, which Git then tracks. Git itself refuses a branch that is already
// checked out elsewhere: there is no --force.
func (Git) AddWorktree(ctx context.Context, dir, path, branch string, newBranch bool, base string) error {
	args := []string{"worktree", "add"}
	if newBranch {
		args = append(args, "-b", branch, path)
		if base != "" {
			args = append(args, base)
		}
	} else {
		args = append(args, path, branch)
	}
	_, err := execx.RunChange(ctx, dir, "git", args...)
	return err
}

// RemoveWorktree removes the worktree at path. Without force, Git refuses
// when the worktree has uncommitted or untracked files, or is locked. With
// force it removes it anyway: only call it after an explicit confirmation.
// The branch is never deleted.
func (Git) RemoveWorktree(ctx context.Context, dir, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	_, err := execx.RunChange(ctx, dir, "git", append(args, "--", path)...)
	return err
}

// Changes lists the uncommitted and untracked files of the worktree at dir,
// as `git status --porcelain` reports them: a two-letter status, a space and
// the path. Ignored files are not included.
func (Git) Changes(ctx context.Context, dir string) ([]string, error) {
	out, err := execx.Run(ctx, dir, "git", "status", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	return ParseChanges(out), nil
}

// ParseChanges parses the output of `git status --porcelain -z`.
func ParseChanges(out []byte) []string {
	var list []string
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		list = append(list, f)
		if f[0] == 'R' || f[0] == 'C' { // renames and copies are followed by the original path
			i++
		}
	}
	return list
}

// IgnoredFiles lists the files in the root of the worktree at dir that match
// pattern (for example ".env*") and are ignored by Git, so a new worktree
// does not get them.
func (Git) IgnoredFiles(ctx context.Context, dir, pattern string) ([]string, error) {
	out, err := execx.Run(ctx, dir, "git", "ls-files", "--others", "--ignored", "--exclude-standard", "--", ":(glob)"+pattern)
	if err != nil {
		return nil, err
	}
	var list []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			list = append(list, line)
		}
	}
	return list, nil
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
