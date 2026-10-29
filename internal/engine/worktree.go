package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/registry"
)

// Branch is a branch of a project and the worktree where it is checked out.
type Branch struct {
	Name     string `json:"name"`
	Remote   bool   `json:"remote,omitempty"`
	Worktree string `json:"worktree,omitempty"` // path, when a worktree has it checked out
}

// Branches lists the local branches and the known remote branches of a
// project, and which of them already have a worktree. A remote branch has a
// worktree when a local branch of the same name has one.
func (e *Engine) Branches(ctx context.Context, project string) ([]Branch, error) {
	p, err := e.Project(project)
	if err != nil {
		return nil, err
	}
	list, err := e.git.Worktrees(ctx, p.Path)
	if err != nil {
		return nil, err
	}
	branches, err := e.git.Branches(ctx, p.Path)
	if err != nil {
		return nil, err
	}
	checkedOut := map[string]string{}
	for _, wt := range list {
		if wt.Branch != "" && !wt.Prunable {
			checkedOut[wt.Branch] = wt.Path
		}
	}
	out := make([]Branch, 0, len(branches))
	for _, b := range branches {
		name := b.Name
		if b.Remote {
			_, name, _ = strings.Cut(b.Name, "/")
		}
		out = append(out, Branch{Name: b.Name, Remote: b.Remote, Worktree: checkedOut[name]})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Remote != out[j].Remote {
			return !out[i].Remote
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// WorktreeDir returns the directory under which `theo worktree add` creates
// the worktrees of a project: the one set for the project, or a directory of
// Theo's own, <data>/worktrees/<project>.
func (e *Engine) WorktreeDir(project string) (string, error) {
	p, err := e.Project(project)
	if err != nil {
		return "", err
	}
	return e.worktreeDir(p.Name, p.WorktreeDir)
}

func (e *Engine) worktreeDir(name, configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	if e.dataDir == "" {
		return "", errors.New("no data directory for worktrees: pass --path")
	}
	return filepath.Join(e.dataDir, "worktrees", slug(name)), nil
}

// SetWorktreeDir sets where new worktrees of a project are created. An empty
// dir goes back to Theo's own directory. Existing worktrees do not move.
func (e *Engine) SetWorktreeDir(name, dir string) error {
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		dir = abs
	}
	projects, err := e.store.Load()
	if err != nil {
		return err
	}
	for i, p := range projects {
		if strings.EqualFold(p.Name, name) {
			projects[i].WorktreeDir = dir
			return e.store.Save(projects)
		}
	}
	return fmt.Errorf("project %q: %w (see `theo project list`)", name, ErrNotFound)
}

// AddWorktreeOptions are the options of AddWorktree.
type AddWorktreeOptions struct {
	NewBranch bool   // create Branch instead of using an existing one
	Base      string // with NewBranch: branch or commit to start from; empty means HEAD
	Path      string // where to create it; empty means <WorktreeDir>/<branch>
}

// AddedWorktree is the result of AddWorktree.
type AddedWorktree struct {
	Path   string   `json:"path"`
	Branch string   `json:"branch"`
	Env    EnvFile  `json:"env_file"`
	Notes  []string `json:"notes,omitempty"` // things the user should know, such as missing .env files
}

// AddWorktree creates a worktree for a branch. It refuses a branch that is
// already checked out in another worktree, and never forces anything. Files
// ignored by Git, such as .env, are not copied: the notes say which env file
// the new worktree will use and which ignored files it does not have.
func (e *Engine) AddWorktree(ctx context.Context, project, branch string, opts AddWorktreeOptions) (*AddedWorktree, error) {
	if strings.TrimSpace(branch) == "" {
		return nil, errors.New("a branch name is required")
	}
	p, err := e.Project(project)
	if err != nil {
		return nil, err
	}
	list, err := e.git.Worktrees(ctx, p.Path)
	if err != nil {
		return nil, err
	}
	for _, wt := range list {
		if wt.Branch == branch && !wt.Prunable {
			if opts.NewBranch {
				return nil, fmt.Errorf("branch %q already exists and is checked out in %s; drop --new to reuse it, or choose another name", branch, wt.Path)
			}
			return nil, fmt.Errorf("branch %q is already checked out in %s (git allows a branch in one worktree only). Use that worktree: `theo services %s -w %s`", branch, wt.Path, p.Name, branch)
		}
	}
	if opts.NewBranch {
		existing, err := e.git.Branches(ctx, p.Path)
		if err != nil {
			return nil, err
		}
		for _, b := range existing {
			if !b.Remote && b.Name == branch {
				return nil, fmt.Errorf("branch %q already exists; drop --new to create a worktree for it", branch)
			}
		}
	}

	path := opts.Path
	if path == "" {
		base, err := e.worktreeDir(p.Name, p.WorktreeDir)
		if err != nil {
			return nil, err
		}
		path = filepath.Join(base, slug(branch))
	}
	if path, err = filepath.Abs(path); err != nil {
		return nil, err
	}
	if inside := containing(list, path); inside != "" {
		return nil, fmt.Errorf("%s is inside the worktree %s; choose a directory outside the project's worktrees", path, inside)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := e.git.AddWorktree(ctx, p.Path, path, branch, opts.NewBranch, opts.Base); err != nil {
		return nil, err
	}

	added := &AddedWorktree{Path: path, Branch: branch}
	list, err = e.git.Worktrees(ctx, p.Path)
	if err != nil {
		return added, nil // the worktree exists; only the notes are lost
	}
	wt, err := selectWorktree(list, path)
	if err != nil {
		return added, nil
	}
	if env, _, err := resolveEnvFile(p, wt, list[0], nil); err == nil {
		added.Env = env
	}
	added.Notes = e.envNotes(ctx, p, list[0], wt, added.Env)
	return added, nil
}

// envNotes explains how a new worktree gets its env file (git does not copy
// ignored files).
func (e *Engine) envNotes(ctx context.Context, p registry.Project, main, wt git.Worktree, env EnvFile) []string {
	ignored, err := e.git.IgnoredFiles(ctx, main.Path, ".env*")
	if err != nil || len(ignored) == 0 {
		return nil
	}
	var missing []string
	for _, name := range ignored {
		if !fileExists(filepath.Join(wt.Path, name)) {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	note := fmt.Sprintf("Git does not copy ignored files: %s %s missing in the new worktree.", strings.Join(missing, ", "), plural(len(missing), "is", "are"))
	switch env.Source {
	case EnvFromFlag, EnvFromProject, EnvFromMain:
		note += fmt.Sprintf(" Theo will use %s by reference (%s); nothing is copied.", env.Path, env.Source)
	default:
		note += " Set one for the project with `theo project set " + p.Name + " --env-file <path>`, or pass --env-file."
	}
	return []string{note}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// containing returns the worktree of list that contains path, or "".
func containing(list []git.Worktree, path string) string {
	for _, wt := range list {
		if !wt.Bare && isInside(wt.Path, path) {
			return wt.Path
		}
	}
	return ""
}

// RemoveWorktreeOptions are the options of RemoveWorktree.
type RemoveWorktreeOptions struct {
	// DiscardChanges allows removing a worktree that has uncommitted or
	// untracked files, which are lost. It only works together with Confirm.
	DiscardChanges bool
	// Confirm must equal the worktree's directory name for DiscardChanges to
	// apply.
	Confirm string
}

// ChangesError is returned when a worktree cannot be removed because it has
// uncommitted or untracked files. Nothing was removed.
type ChangesError struct {
	Path    string
	Name    string   // directory name to pass as Confirm
	Changes []string // as `git status --porcelain` shows them
}

func (c *ChangesError) Error() string {
	return fmt.Sprintf("worktree %s has %d uncommitted or untracked file(s); nothing was removed", c.Path, len(c.Changes))
}

// InstanceError is returned when a worktree cannot be removed because it
// still has containers.
type InstanceError struct {
	Path   string
	Stacks []string
}

func (i *InstanceError) Error() string {
	return fmt.Sprintf("worktree %s still has Compose project(s) %s; stop and remove them first with `theo down`", i.Path, strings.Join(i.Stacks, ", "))
}

// RemoveWorktree removes a linked worktree. It refuses the main worktree,
// one that still has containers, and one with uncommitted or untracked files
// unless the caller discards them explicitly with the worktree's name as
// confirmation. The branch and the volumes are always kept.
func (e *Engine) RemoveWorktree(ctx context.Context, project, selector string, opts RemoveWorktreeOptions) (git.Worktree, error) {
	p, err := e.Project(project)
	if err != nil {
		return git.Worktree{}, err
	}
	list, err := e.git.Worktrees(ctx, p.Path)
	if err != nil {
		return git.Worktree{}, err
	}
	if selector == "" {
		return git.Worktree{}, errors.New("say which worktree to remove: a branch, directory name or path")
	}
	wt, err := selectWorktree(list, selector)
	if err != nil {
		return git.Worktree{}, fmt.Errorf("project %q: %w", p.Name, err)
	}
	if samePath(wt.Path, list[0].Path) {
		return wt, fmt.Errorf("%s is the main worktree of %q; Theo does not remove it", wt.Path, p.Name)
	}
	if set, ok, _ := e.SharedSet(p.Name); ok && samePath(set.From, wt.Path) {
		return wt, fmt.Errorf("%s defines the shared services (%s) of %q; stop sharing them first with `theo shared remove %s`", wt.Path, strings.Join(set.Services, ", "), p.Name, p.Name)
	}
	if wt.Prunable {
		return wt, fmt.Errorf("the directory of %s no longer exists; run `git worktree prune` in %s to forget it", wt.Path, p.Path)
	}

	overview, err := e.Overview(ctx, p.Name)
	if err != nil {
		return wt, fmt.Errorf("cannot check whether %s has containers: %w", wt.Path, err)
	}
	for _, o := range overview {
		if samePath(o.Worktree.Path, wt.Path) && len(o.Stacks) > 0 {
			names := make([]string, len(o.Stacks))
			for i, s := range o.Stacks {
				names[i] = s.Name
			}
			return wt, &InstanceError{Path: wt.Path, Stacks: names}
		}
	}

	changes, err := e.git.Changes(ctx, wt.Path)
	if err != nil {
		return wt, err
	}
	name := filepath.Base(wt.Path)
	force := false
	if len(changes) > 0 {
		if !opts.DiscardChanges || opts.Confirm != name {
			return wt, &ChangesError{Path: wt.Path, Name: name, Changes: changes}
		}
		force = true
	}
	if err := e.git.RemoveWorktree(ctx, p.Path, wt.Path, force); err != nil {
		return wt, err
	}
	return wt, nil
}
