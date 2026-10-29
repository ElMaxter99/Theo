package git

import (
	"context"
	"fmt"
	"strings"

	"github.com/ElMaxter99/Theo/internal/execx"
)

// CloneLocal clones the repository at src into dst without checking out any
// files, using Git's local transport. dst must not exist or be empty.
func (Git) CloneLocal(ctx context.Context, src, dst string) error {
	_, err := execx.Run(ctx, "", "git", "clone", "--local", "--no-checkout", "--", src, dst)
	return err
}

// RemoteURL returns the URL of the remote called name in the repository at
// dir, or "" when there is no such remote.
func (Git) RemoteURL(ctx context.Context, dir, name string) (string, error) {
	out, err := execx.Run(ctx, dir, "git", "remote")
	if err != nil {
		return "", err
	}
	found := false
	for _, r := range strings.Fields(string(out)) {
		if r == name {
			found = true
		}
	}
	if !found {
		return "", nil
	}
	out, err = execx.Run(ctx, dir, "git", "remote", "get-url", name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// SetRemoteURL points the existing remote called name at url.
func (Git) SetRemoteURL(ctx context.Context, dir, name, url string) error {
	_, err := execx.Run(ctx, dir, "git", "remote", "set-url", name, url)
	return err
}

// Checkout checks out branch in the repository at dir. With newBranch the
// branch is created from base (the current HEAD when base is empty).
// Otherwise branch must exist, locally or as a single remote-tracking branch.
func (Git) Checkout(ctx context.Context, dir, branch string, newBranch bool, base string) error {
	args := []string{"checkout", "-q"}
	if newBranch {
		args = append(args, "-b", branch)
		if base != "" {
			args = append(args, base)
		}
	} else {
		args = append(args, branch, "--")
	}
	_, err := execx.Run(ctx, dir, "git", args...)
	return err
}

// ResolvesTo reports whether rev names a commit in the repository at dir.
func (Git) ResolvesTo(ctx context.Context, dir, rev string) bool {
	_, err := execx.Run(ctx, dir, "git", "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	return err == nil
}

// SubmodulesInit runs `git submodule update --init --recursive` in dir.
func (Git) SubmodulesInit(ctx context.Context, dir string) error {
	_, err := execx.Run(ctx, dir, "git", "submodule", "update", "--init", "--recursive")
	return err
}

// IsTracked reports whether the file rel (a slash-separated path relative to
// dir) is tracked by Git in the repository at dir.
func (Git) IsTracked(ctx context.Context, dir, rel string) (bool, error) {
	out, err := execx.Run(ctx, dir, "git", "ls-files", "--", ":(literal)"+rel)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// UnsavedCommits lists the local branches of the clone at clone whose tip is
// not contained in any branch, remote-tracking branch or tag of the clone's
// remote-tracking refs or of the repository at origin. Removing the clone
// would lose those commits. Each entry reads "<branch> (<short sha>)".
func (Git) UnsavedCommits(ctx context.Context, clone, origin string) ([]string, error) {
	out, err := execx.Run(ctx, clone, "git", "for-each-ref", "--format=%(refname:short) %(objectname)", "refs/heads")
	if err != nil {
		return nil, err
	}
	var unsaved []string
	for _, line := range strings.Split(string(out), "\n") {
		name, sha, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if containedIn(ctx, clone, sha, "refs/remotes") || containedIn(ctx, origin, sha, "refs/heads", "refs/remotes", "refs/tags") {
			continue
		}
		unsaved = append(unsaved, fmt.Sprintf("%s (%s)", name, sha[:min(7, len(sha))]))
	}
	return unsaved, nil
}

// containedIn reports whether some ref under prefixes in dir contains the
// commit. A commit the repository does not know is not contained.
func containedIn(ctx context.Context, dir, sha string, prefixes ...string) bool {
	args := append([]string{"for-each-ref", "--count=1", "--format=%(refname)", "--contains", sha}, prefixes...)
	out, err := execx.Run(ctx, dir, "git", args...)
	return err == nil && strings.TrimSpace(string(out)) != ""
}
