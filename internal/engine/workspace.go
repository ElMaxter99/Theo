package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/registry"
)

// Kinds of workspace.
const (
	KindMain     = registry.KindMain
	KindWorktree = registry.KindWorktree
	KindClone    = registry.KindClone
)

// cloner is what the Git adapter offers for independent clones. It is
// optional so that the Git interface of the engine does not grow.
type cloner interface {
	CloneLocal(ctx context.Context, src, dst string) error
	RemoteURL(ctx context.Context, dir, name string) (string, error)
	SetRemoteURL(ctx context.Context, dir, name, url string) error
	Checkout(ctx context.Context, dir, branch string, newBranch bool, base string) error
	ResolvesTo(ctx context.Context, dir, rev string) bool
	SubmodulesInit(ctx context.Context, dir string) error
	UnsavedCommits(ctx context.Context, clone, origin string) ([]string, error)
}

// trackedChecker tells whether a file is tracked by Git.
type trackedChecker interface {
	IsTracked(ctx context.Context, dir, rel string) (bool, error)
}

// worktreeList returns every workspace of a project in the shape of Git's
// own list: the worktrees Git lists (the main one first), then the
// independent clones Theo recorded. A clone whose directory is gone is
// marked Prunable, like a worktree Git still lists but cannot find.
func (e *Engine) worktreeList(ctx context.Context, p registry.Project) ([]git.Worktree, error) {
	list, err := e.git.Worktrees(ctx, p.Path)
	if err != nil {
		return nil, err
	}
	for _, w := range p.Workspaces {
		if w.Kind != registry.KindClone {
			continue
		}
		entry := git.Worktree{Path: w.Path}
		if !dirExists(w.Path) {
			entry.Prunable = true
		} else if l, err := e.git.Worktrees(ctx, w.Path); err == nil && len(l) > 0 {
			entry = l[0]
			entry.Path = w.Path
		}
		list = append(list, entry)
	}
	return list, nil
}

func workspaceRecord(p registry.Project, wsPath string) (registry.Workspace, int) {
	for i, w := range p.Workspaces {
		if samePath(w.Path, wsPath) {
			return w, i
		}
	}
	return registry.Workspace{}, -1
}

// workspaceKind says what kind of workspace wsPath is in list.
func workspaceKind(p registry.Project, list []git.Worktree, wsPath string) string {
	if len(list) > 0 && samePath(list[0].Path, wsPath) {
		return KindMain
	}
	if w, i := workspaceRecord(p, wsPath); i >= 0 && w.Kind == registry.KindClone {
		return KindClone
	}
	return KindWorktree
}

func workspaceOwner(p registry.Project, wsPath string) string {
	w, _ := workspaceRecord(p, wsPath)
	return w.Owner
}

// updateProject applies fn to the stored project called name.
func (e *Engine) updateProject(name string, fn func(*registry.Project) error) error {
	projects, err := e.store.Load()
	if err != nil {
		return err
	}
	for i := range projects {
		if strings.EqualFold(projects[i].Name, name) {
			if err := fn(&projects[i]); err != nil {
				return err
			}
			return e.store.Save(projects)
		}
	}
	return fmt.Errorf("project %q: %w (see `theo project list`)", name, ErrNotFound)
}

// putRecord stores w, replacing the record for the same path. A record that
// says nothing (no owner, not a clone) is dropped.
func putRecord(p *registry.Project, w registry.Workspace) {
	_, i := workspaceRecord(*p, w.Path)
	keep := w.Kind == registry.KindClone || w.Owner != ""
	switch {
	case i >= 0 && keep:
		p.Workspaces[i] = w
	case i >= 0:
		p.Workspaces = append(p.Workspaces[:i:i], p.Workspaces[i+1:]...)
	case keep:
		p.Workspaces = append(p.Workspaces, w)
	}
}

// ValidateOwner checks an owner label: any text of up to 100 characters
// without control characters.
func ValidateOwner(owner string) (string, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return "", errors.New("the owner must not be empty (use --clear to remove it)")
	}
	if len([]rune(owner)) > 100 {
		return "", errors.New("the owner is too long (100 characters at most)")
	}
	for _, r := range owner {
		if unicode.IsControl(r) {
			return "", errors.New("the owner must not contain control characters")
		}
	}
	return owner, nil
}

// SetOwner sets (or, with an empty owner, clears) the owner label of a
// workspace, selected by branch, directory name or path.
func (e *Engine) SetOwner(ctx context.Context, project, selector, owner string) (Worktree, error) {
	p, err := e.Project(project)
	if err != nil {
		return Worktree{}, err
	}
	if owner != "" {
		if owner, err = ValidateOwner(owner); err != nil {
			return Worktree{}, err
		}
	}
	list, err := e.worktreeList(ctx, p)
	if err != nil {
		return Worktree{}, err
	}
	wt, err := selectWorktree(list, selector)
	if err != nil {
		return Worktree{}, fmt.Errorf("project %q: %w", p.Name, err)
	}
	kind := workspaceKind(p, list, wt.Path)
	err = e.updateProject(p.Name, func(sp *registry.Project) error {
		rec, _ := workspaceRecord(*sp, wt.Path)
		if rec.Path == "" {
			rec = registry.Workspace{Path: wt.Path, Kind: kind, CreatedAt: e.now().UTC().Truncate(time.Second)}
		}
		rec.Owner = owner
		putRecord(sp, rec)
		return nil
	})
	if err != nil {
		return Worktree{}, err
	}
	return Worktree{Worktree: wt, Kind: kind, Owner: owner}, nil
}

// SetCopyOnAdd sets the file patterns that `worktree add --copy` copies. An
// empty list clears them. Patterns are root-relative and are checked.
func (e *Engine) SetCopyOnAdd(name string, patterns []string) (registry.Project, error) {
	clean := make([]string, 0, len(patterns))
	for _, pat := range patterns {
		c, err := CleanCopyPattern(pat)
		if err != nil {
			return registry.Project{}, err
		}
		clean = append(clean, c)
	}
	var out registry.Project
	err := e.updateProject(name, func(p *registry.Project) error {
		p.CopyOnAdd = clean
		out = *p
		return nil
	})
	return out, err
}

// CleanCopyPattern checks a root-relative file pattern and returns it
// slash-separated and cleaned. Absolute paths and paths that leave the
// worktree (..) are refused, and so is anything inside .git.
func CleanCopyPattern(pat string) (string, error) {
	orig := pat
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return "", errors.New("an empty file pattern")
	}
	slashed := filepath.ToSlash(pat)
	if path.IsAbs(slashed) || filepath.IsAbs(pat) || filepath.VolumeName(pat) != "" || strings.HasPrefix(pat, `\`) {
		return "", fmt.Errorf("%q is an absolute path: patterns are relative to the project root", orig)
	}
	for _, part := range strings.Split(slashed, "/") {
		if part == ".." {
			return "", fmt.Errorf("%q leaves the project root (..)", orig)
		}
	}
	c := path.Clean(slashed)
	if c == "." || c == ".git" || strings.HasPrefix(c, ".git/") {
		return "", fmt.Errorf("%q is not a file that can be copied", orig)
	}
	if _, err := path.Match(c, "x"); err != nil {
		return "", fmt.Errorf("%q is not a valid pattern: %v", orig, err)
	}
	return c, nil
}

// NotCopied is a file that --copy did not copy, and why.
type NotCopied struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// copyUnversioned copies the files matching patterns from the main worktree
// src into the new workspace dst. It only copies regular files that Git does
// not track, never overwrites anything, and never follows a path out of
// either directory.
func (e *Engine) copyUnversioned(ctx context.Context, src, dst string, patterns []string) (copied []string, skipped []NotCopied) {
	realSrc, err := filepath.EvalSymlinks(src)
	if err != nil {
		realSrc = src
	}
	realDst, err := filepath.EvalSymlinks(dst)
	if err != nil {
		realDst = dst
	}
	tracker, _ := e.git.(trackedChecker)
	seen := map[string]bool{}
	skip := func(rel, reason string) { skipped = append(skipped, NotCopied{Path: rel, Reason: reason}) }

	for _, pat := range patterns {
		matches := []string{pat}
		if strings.ContainsAny(pat, "*?[") {
			matches, _ = fs.Glob(os.DirFS(src), pat)
			if len(matches) == 0 {
				skip(pat, "no file in the main worktree matches")
				continue
			}
		}
		for _, rel := range matches {
			if seen[rel] {
				continue
			}
			seen[rel] = true
			from := filepath.Join(src, filepath.FromSlash(rel))
			if _, err := os.Lstat(from); err != nil {
				skip(rel, "does not exist in the main worktree")
				continue
			}
			real, err := filepath.EvalSymlinks(from)
			if err != nil || !isInside(realSrc, real) {
				skip(rel, "is or goes through a symlink that points outside the worktree")
				continue
			}
			info, err := os.Stat(real)
			switch {
			case err != nil:
				skip(rel, err.Error())
				continue
			case info.IsDir():
				skip(rel, "is a directory (list files, not directories)")
				continue
			case !info.Mode().IsRegular():
				skip(rel, "is not a regular file")
				continue
			}
			if tracker != nil {
				if tracked, err := tracker.IsTracked(ctx, src, rel); err == nil && tracked {
					skip(rel, "is tracked by Git (the new workspace has its own checkout of it)")
					continue
				}
			}
			to := filepath.Join(dst, filepath.FromSlash(rel))
			if _, err := os.Lstat(to); err == nil {
				skip(rel, "already exists in the new workspace; not overwritten")
				continue
			}
			if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
				skip(rel, err.Error())
				continue
			}
			if realDir, err := filepath.EvalSymlinks(filepath.Dir(to)); err != nil || !isInside(realDst, realDir) {
				skip(rel, "its directory in the new workspace leads outside it")
				continue
			}
			if err := copyNew(real, to, info.Mode().Perm()); err != nil {
				if errors.Is(err, fs.ErrExist) {
					skip(rel, "already exists in the new workspace; not overwritten")
				} else {
					skip(rel, err.Error())
				}
				continue
			}
			copied = append(copied, rel)
		}
	}
	sort.Strings(copied)
	return copied, skipped
}

// copyNew copies from to the new file to, failing if to exists.
func copyNew(from, to string, perm os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(to)
		return err
	}
	return out.Close()
}

// createClone creates an independent clone of the project at dst on branch
// and records it. See AddWorktreeOptions.Clone.
func (e *Engine) createClone(ctx context.Context, p registry.Project, dst, branch string, opts AddWorktreeOptions) ([]string, error) {
	c, ok := e.git.(cloner)
	if !ok {
		return nil, errors.New("this Git adapter cannot create clones")
	}
	if entries, err := os.ReadDir(dst); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("%s exists and is not empty; choose another directory with --path", dst)
	}
	if err := c.CloneLocal(ctx, p.Path, dst); err != nil {
		os.RemoveAll(dst) // empty or created by the failed clone
		return nil, err
	}
	fail := func(err error) ([]string, error) {
		os.RemoveAll(dst) // nothing of the user's is in it yet
		return nil, err
	}

	var notes []string
	// Pushes must reach the project's own remote, not the local path.
	url, err := c.RemoteURL(ctx, p.Path, "origin")
	if err != nil {
		return fail(err)
	}
	if url != "" {
		if err := c.SetRemoteURL(ctx, dst, "origin", url); err != nil {
			return fail(err)
		}
		notes = append(notes, fmt.Sprintf("origin of the clone is %s, the project's own remote. Its remote-tracking branches still reflect the project's local branches until you run `git fetch` there.", url))
	} else {
		notes = append(notes, fmt.Sprintf("the project has no origin remote, so origin of the clone is still %s; add a remote there before pushing.", p.Path))
	}

	if opts.NewBranch {
		base := opts.Base
		if base != "" && !c.ResolvesTo(ctx, dst, base) && c.ResolvesTo(ctx, dst, "origin/"+base) {
			base = "origin/" + base
		}
		err = c.Checkout(ctx, dst, branch, true, base)
	} else {
		err = c.Checkout(ctx, dst, branch, false, "")
		if err != nil {
			err = fmt.Errorf("%w\n\nThe clone only knows the local branches of the project. Create the branch first, or use --new", err)
		}
	}
	if err != nil {
		return fail(err)
	}

	at := e.now().UTC().Truncate(time.Second)
	if err := e.updateProject(p.Name, func(sp *registry.Project) error {
		putRecord(sp, registry.Workspace{Path: dst, Kind: registry.KindClone, Owner: opts.Owner, CreatedAt: at})
		return nil
	}); err != nil {
		return fail(err)
	}

	if fileExists(filepath.Join(dst, ".gitmodules")) {
		if err := c.SubmodulesInit(ctx, dst); err != nil {
			return nil, fmt.Errorf("the clone was created at %s and registered, but initializing its submodules failed: %w\n\nFix the cause and run `git submodule update --init --recursive` in the clone, or remove it with `theo worktree remove %s %s --discard-changes --confirm %s`",
				dst, err, p.Name, branch, filepath.Base(dst))
		}
		notes = append(notes, "submodules were initialized (git submodule update --init --recursive).")
	}
	return notes, nil
}

func hasSubmodules(root string) bool {
	return fileExists(filepath.Join(root, ".gitmodules"))
}

// submoduleNote warns that git worktree only supports submodules
// experimentally.
func submoduleNote(project string) string {
	return fmt.Sprintf("this project has submodules (.gitmodules). Git only supports submodules in linked worktrees experimentally, so the submodules of the new worktree are empty and may misbehave. For a fully supported checkout use an independent clone: `theo worktree add %s <branch> --clone`.", project)
}

// removeAll deletes a directory tree, making read-only files writable when
// needed (Git object files are read-only on Windows).
func removeAll(dir string) error {
	err := os.RemoveAll(dir)
	if err == nil {
		return nil
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(dir)
}

// unsavedCommits lists the commits that exist only in a clone, which removing
// it would lose, as lines in the style of Changes.
func (e *Engine) unsavedCommits(ctx context.Context, p registry.Project, clone string) ([]string, error) {
	c, ok := e.git.(cloner)
	if !ok {
		return nil, nil
	}
	unsaved, err := c.UnsavedCommits(ctx, clone, p.Path)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(unsaved))
	for i, u := range unsaved {
		out[i] = "commits only in the clone: branch " + u
	}
	return out, nil
}
