package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/execx"
)

// BaseKey is the Git configuration key, per branch, that records the branch
// a branch was created from. It is the key `gh pr create` reads as the
// default base of a pull request (branch.<name>.gh-merge-base).
const BaseKey = "gh-merge-base"

// BranchRef is what the lifecycle of a workspace needs to know about one
// local branch.
type BranchRef struct {
	Name string
	// Upstream is the short name of the branch it tracks ("origin/x"); empty
	// when it tracks none.
	Upstream string
	// Gone is true when the upstream is configured but no longer exists,
	// as after a pull request is merged and its branch deleted.
	Gone bool
	// Base is the recorded base (branch.<name>.gh-merge-base); empty when
	// none is recorded.
	Base string
	// UpstreamAhead is how many commits the branch has that its upstream
	// has not, read from the same command; valid when TrackRead is true.
	UpstreamAhead int
	TrackRead     bool
}

// LocalBranches lists the local branches of the repository that contains dir
// with their upstream and recorded base, in two Git commands.
func (Git) LocalBranches(ctx context.Context, dir string) (map[string]BranchRef, error) {
	out, err := execx.Run(ctx, dir, "git", "for-each-ref",
		"--format=%(refname:short)%00%(upstream:short)%00%(upstream:track)", "refs/heads")
	if err != nil {
		return nil, err
	}
	refs := map[string]BranchRef{}
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.Split(line, "\x00")
		if len(parts) != 3 || parts[0] == "" {
			continue
		}
		r := BranchRef{Name: parts[0], Upstream: parts[1], Gone: strings.Contains(parts[2], "gone")}
		if r.Upstream != "" && !r.Gone {
			r.UpstreamAhead, r.TrackRead = parseTrackAhead(parts[2])
		}
		refs[parts[0]] = r
	}
	// --get-regexp exits with 1 when nothing matches: that is not an error.
	cfg, _ := execx.Run(ctx, dir, "git", "config", "--get-regexp", `^branch\..*\.`+BaseKey+`$`)
	for name, base := range ParseBases(cfg) {
		if r, ok := refs[name]; ok {
			r.Base = base
			refs[name] = r
		}
	}
	return refs, nil
}

// parseTrackAhead reads the ahead count of %(upstream:track): "" when the
// branch and its upstream are equal, "[ahead 2]", "[behind 1]" or
// "[ahead 2, behind 1]". It reports false for anything else.
func parseTrackAhead(track string) (int, bool) {
	t := strings.TrimSpace(track)
	if t == "" {
		return 0, true
	}
	if !strings.HasPrefix(t, "[") || !strings.HasSuffix(t, "]") {
		return 0, false
	}
	ahead := 0
	for _, part := range strings.Split(strings.Trim(t, "[]"), ",") {
		f := strings.Fields(part)
		if len(f) != 2 {
			return 0, false
		}
		n, err := strconv.Atoi(f[1])
		if err != nil {
			return 0, false
		}
		switch f[0] {
		case "ahead":
			ahead = n
		case "behind":
		default:
			return 0, false
		}
	}
	return ahead, true
}

// AheadBehindAll compares every local branch with base in one Git command
// (%(ahead-behind:...), Git 2.41 or later): for each branch, the commits it
// has that base has not, and the commits base has that it has not. An older
// Git answers an error, and callers fall back to AheadBehind.
func (Git) AheadBehindAll(ctx context.Context, dir, base string) (map[string][2]int, error) {
	if err := checkRef("base", base); err != nil {
		return nil, err
	}
	out, err := execx.Run(ctx, dir, "git", "for-each-ref", "--format=%(refname:short)%00%(ahead-behind:"+base+")", "refs/heads")
	if err != nil {
		return nil, err
	}
	counts := map[string][2]int{}
	for _, line := range strings.Split(string(out), "\n") {
		name, ab, ok := strings.Cut(line, "\x00")
		if !ok || name == "" {
			continue
		}
		ahead, behind, err := parseCounts([]byte(ab))
		if err != nil {
			return nil, err
		}
		counts[name] = [2]int{ahead, behind}
	}
	return counts, nil
}

// ParseBases parses `git config --get-regexp '^branch\..*\.gh-merge-base$'`.
func ParseBases(out []byte) map[string]string {
	bases := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || !strings.HasPrefix(key, "branch.") || !strings.HasSuffix(key, "."+BaseKey) {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "branch."), "."+BaseKey)
		if name != "" && value != "" {
			bases[name] = strings.TrimSpace(value)
		}
	}
	return bases
}

// SetBase records base as the base of branch (branch.<branch>.gh-merge-base).
// An empty base removes the record.
func (Git) SetBase(ctx context.Context, dir, branch, base string) error {
	if err := checkRef("branch", branch); err != nil {
		return err
	}
	if err := checkRef("base", base); err != nil {
		return err
	}
	key := "branch." + branch + "." + BaseKey
	if base == "" {
		_, err := execx.RunChange(ctx, dir, "git", "config", "--unset", key)
		var exit *execx.ExitError
		if errors.As(err, &exit) && exit.Code == 5 { // nothing to unset
			return nil
		}
		return err
	}
	_, err := execx.RunChange(ctx, dir, "git", "config", key, base)
	return err
}

// Resolves reports whether rev names a commit of the repository at dir.
func (Git) Resolves(ctx context.Context, dir, rev string) bool {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return false
	}
	_, err := execx.Run(ctx, dir, "git", "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	return err == nil
}

// IsLocalBranch reports whether name is a local branch of the repository.
func (Git) IsLocalBranch(ctx context.Context, dir, name string) bool {
	if name == "" || strings.HasPrefix(name, "-") {
		return false
	}
	_, err := execx.Run(ctx, dir, "git", "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// CurrentBranch returns the branch checked out in the worktree at dir, or ""
// when HEAD is detached.
func (Git) CurrentBranch(ctx context.Context, dir string) string {
	out, err := execx.Run(ctx, dir, "git", "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// AheadBehind counts the commits of branch that base lacks (ahead) and the
// commits of base that branch lacks (behind).
func (Git) AheadBehind(ctx context.Context, dir, base, branch string) (ahead, behind int, err error) {
	if err := checkRef("base", base); err != nil {
		return 0, 0, err
	}
	if err := checkRef("branch", branch); err != nil {
		return 0, 0, err
	}
	out, err := execx.Run(ctx, dir, "git", "rev-list", "--left-right", "--count", base+"..."+branch)
	if err != nil {
		return 0, 0, err
	}
	// The left side of base...branch is base-only, the right side branch-only.
	left, right, err := parseCounts(out)
	return right, left, err
}

func parseCounts(out []byte) (left, right int, err error) {
	f := strings.Fields(string(out))
	if len(f) != 2 {
		return 0, 0, fmt.Errorf("unexpected output of git rev-list --count: %q", strings.TrimSpace(string(out)))
	}
	if left, err = strconv.Atoi(f[0]); err != nil {
		return 0, 0, err
	}
	right, err = strconv.Atoi(f[1])
	return left, right, err
}

// LastCommitTime returns the committer date of the newest commit of branch
// that base lacks; ok is false when there is none. With an empty base it is
// the date of the tip of branch.
func (Git) LastCommitTime(ctx context.Context, dir, base, branch string) (t time.Time, ok bool, err error) {
	if err := checkRef("branch", branch); err != nil {
		return time.Time{}, false, err
	}
	rng := branch
	if base != "" {
		if err := checkRef("base", base); err != nil {
			return time.Time{}, false, err
		}
		rng = base + ".." + branch
	}
	out, err := execx.Run(ctx, dir, "git", "log", "-1", "--format=%ct", rng, "--")
	if err != nil {
		return time.Time{}, false, err
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return time.Time{}, false, nil
	}
	sec, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, false, err
	}
	return time.Unix(sec, 0), true, nil
}

// BranchMoves counts the entries of the reflog of a local branch: 1 for a
// branch that has not moved since it was created. It returns 0 when Git
// keeps no reflog for it.
func (Git) BranchMoves(ctx context.Context, dir, branch string) int {
	if checkRef("branch", branch) != nil {
		return 0
	}
	// The reflog is a file with one line per move; reading it saves a Git
	// process per worktree. A .git that is not a directory (a linked
	// worktree, a submodule) or a missing file falls back to Git.
	if fi, err := os.Stat(filepath.Join(dir, ".git")); err == nil && fi.IsDir() {
		if data, err := os.ReadFile(filepath.Join(dir, ".git", "logs", "refs", "heads", filepath.FromSlash(branch))); err == nil {
			n := 0
			for _, line := range strings.Split(string(data), "\n") {
				if strings.TrimSpace(line) != "" {
					n++
				}
			}
			return n
		}
	}
	out, err := execx.Run(ctx, dir, "git", "reflog", "show", "--format=%H", "refs/heads/"+branch, "--")
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// ErrMergeTreeUnsupported is returned by ContentMerged when Git is older than
// 2.38 and has no `git merge-tree --write-tree`.
var ErrMergeTreeUnsupported = errors.New("this Git has no `git merge-tree --write-tree` (Git 2.38 or later is needed to recognize squash merges)")

// ContentMerged reports whether merging branch into base would change
// nothing: the result of the merge is exactly the tree of base. That is the
// case after a squash or rebase merge of branch, whose commits differ from
// the ones base got. A merge with conflicts is not merged. Nothing is
// written to the repository besides objects Git may cache.
func (Git) ContentMerged(ctx context.Context, dir, base, branch string) (bool, error) {
	if err := checkRef("base", base); err != nil {
		return false, err
	}
	if err := checkRef("branch", branch); err != nil {
		return false, err
	}
	baseTree, err := execx.Run(ctx, dir, "git", "rev-parse", "--verify", "--quiet", base+"^{tree}")
	if err != nil {
		return false, fmt.Errorf("%s is not a branch or commit of this repository", base)
	}
	out, err := execx.Run(ctx, dir, "git", "merge-tree", "--write-tree", "--no-messages", base, branch)
	if err != nil {
		var exit *execx.ExitError
		if errors.As(err, &exit) && exit.Code == 1 {
			return false, nil // conflicts
		}
		msg := err.Error()
		if strings.Contains(msg, "usage:") || strings.Contains(msg, "unknown option") {
			return false, ErrMergeTreeUnsupported
		}
		return false, err
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(first) == strings.TrimSpace(string(baseTree)), nil
}

// Commit returns the commit rev points to.
func (Git) Commit(ctx context.Context, dir, rev string) (string, error) {
	if err := checkRef("revision", rev); err != nil {
		return "", err
	}
	out, err := execx.Run(ctx, dir, "git", "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%s is not a branch or commit of this repository", rev)
	}
	return strings.TrimSpace(string(out)), nil
}

// DeleteBranch deletes a local branch. Without force Git refuses a branch
// that is not merged into its upstream or HEAD; with force it deletes it
// anyway: only call it after checking the branch is merged.
func (Git) DeleteBranch(ctx context.Context, dir, branch string, force bool) error {
	if err := checkRef("branch", branch); err != nil {
		return err
	}
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := execx.RunChange(ctx, dir, "git", "branch", flag, "--", branch)
	return err
}

// IgnoredEntries lists the ignored files and directories of the worktree at
// dir, as `git status --ignored` reports them: a directory that is ignored
// as a whole is one entry ending in "/".
func (Git) IgnoredEntries(ctx context.Context, dir string) ([]string, error) {
	out, err := execx.Run(ctx, dir, "git", "status", "--porcelain", "-z", "--ignored=traditional", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	var list []string
	for _, f := range strings.Split(string(out), "\x00") {
		if strings.HasPrefix(f, "!! ") {
			list = append(list, strings.TrimPrefix(f, "!! "))
		}
	}
	return list, nil
}

// PruneWorktrees forgets the worktrees whose directory is gone
// (`git worktree prune`).
func (Git) PruneWorktrees(ctx context.Context, dir string) error {
	_, err := execx.RunChange(ctx, dir, "git", "worktree", "prune")
	return err
}

// RepairWorktrees runs `git worktree repair` in dir, which fixes the links
// between the main worktree and its linked worktrees after one moved.
func (Git) RepairWorktrees(ctx context.Context, dir string) error {
	_, err := execx.RunChange(ctx, dir, "git", "worktree", "repair")
	return err
}

// Fetch runs `git fetch` for one remote (default origin) in dir. It is the
// only command of this package that uses the network, and only on request.
func (Git) Fetch(ctx context.Context, dir, remote string) error {
	if remote == "" {
		remote = "origin"
	}
	if err := checkRef("remote", remote); err != nil {
		return err
	}
	_, err := execx.RunChange(ctx, dir, "git", "fetch", "--prune", remote)
	return err
}

// WorktreeCreated returns when the linked worktree at path was created: the
// modification time of the commondir file Git writes in the worktree's
// administrative directory when it creates it, and does not touch later. It
// reads the .git file of the worktree and writes nothing. ok is false when
// it cannot tell (a main worktree, or a clone).
func WorktreeCreated(path string) (time.Time, bool) {
	data, err := os.ReadFile(filepath.Join(path, ".git"))
	if err != nil {
		return time.Time{}, false // a directory (main worktree or clone) or nothing
	}
	line := strings.TrimSpace(string(data))
	gitdir, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return time.Time{}, false
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(path, gitdir)
	}
	st, err := os.Stat(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return time.Time{}, false
	}
	return st.ModTime(), true
}

// CommitAll stages every change of the worktree at dir (tracked and untracked,
// never the files Git ignores) and commits it with msg, without running hooks
// or signing, so a branch keeps unfinished work before its folder is removed.
// It does nothing when there is nothing to commit. Only call it when the
// person asked for it.
func (Git) CommitAll(ctx context.Context, dir, msg string) error {
	if _, err := execx.RunChange(ctx, dir, "git", "add", "-A"); err != nil {
		return err
	}
	_, err := execx.RunChange(ctx, dir, "git", "-c", "commit.gpgsign=false", "commit", "--no-verify", "-q", "-m", msg)
	return err
}

// SwitchNewBranch creates branch name at the current commit of the worktree at
// dir and checks it out (`git switch -c`), keeping every file as it is. It is
// how a detached HEAD gets a branch that holds its commits.
func (Git) SwitchNewBranch(ctx context.Context, dir, name string) error {
	if err := checkRef("branch", name); err != nil {
		return err
	}
	_, err := execx.RunChange(ctx, dir, "git", "switch", "-c", name)
	return err
}
