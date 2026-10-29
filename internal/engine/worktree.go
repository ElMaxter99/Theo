package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	list, err := e.worktreeList(ctx, p)
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
	// A clone has its own branches, which the project's repository does not
	// know until they are fetched from it.
	known := map[string]bool{}
	for _, b := range out {
		if !b.Remote {
			known[b.Name] = true
		}
	}
	for name, path := range checkedOut {
		if !known[name] {
			out = append(out, Branch{Name: name, Worktree: path})
		}
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

	// Clone creates an independent clone instead of a linked worktree, for
	// projects where git worktree is not enough (submodules).
	Clone bool
	// Owner labels the new workspace (an agent name, for example).
	Owner string
	// Copy copies the project's CopyOnAdd files from the main worktree;
	// CopyFiles adds one-off root-relative files. Existing files are never
	// overwritten.
	Copy      bool
	CopyFiles []string
}

// AddedWorktree is the result of AddWorktree.
type AddedWorktree struct {
	Path   string   `json:"path"`
	Branch string   `json:"branch"`
	Kind   string   `json:"kind"` // "worktree" or "clone"
	Owner  string   `json:"owner,omitempty"`
	Env    EnvFile  `json:"env_file"`
	Notes  []string `json:"notes,omitempty"` // things the user should know, such as missing .env files
	// Copied lists the root-relative files copied from the main worktree;
	// NotCopied the ones asked for that were not, with the reason.
	Copied    []string    `json:"copied,omitempty"`
	NotCopied []NotCopied `json:"not_copied,omitempty"`
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
	if opts.Owner != "" {
		if opts.Owner, err = ValidateOwner(opts.Owner); err != nil {
			return nil, err
		}
	}
	var patterns []string
	if opts.Copy {
		patterns = append(patterns, p.CopyOnAdd...)
	}
	for _, f := range opts.CopyFiles {
		c, err := CleanCopyPattern(f)
		if err != nil {
			return nil, fmt.Errorf("--copy-file: %w", err)
		}
		patterns = append(patterns, c)
	}
	if opts.Copy && len(patterns) == 0 {
		return nil, fmt.Errorf("no files to copy: list them with `theo project set %s --copy-on-add <file>`, or pass --copy-file <file>", p.Name)
	}
	list, err := e.worktreeList(ctx, p)
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
	added := &AddedWorktree{Path: path, Branch: branch, Kind: KindWorktree, Owner: opts.Owner}
	if opts.Clone {
		added.Kind = KindClone
		notes, err := e.createClone(ctx, p, path, branch, opts)
		if err != nil {
			return nil, err
		}
		added.Notes = append(added.Notes, notes...)
	} else {
		if err := e.git.AddWorktree(ctx, p.Path, path, branch, opts.NewBranch, opts.Base); err != nil {
			return nil, err
		}
		if opts.Owner != "" {
			if err := e.updateProject(p.Name, func(sp *registry.Project) error {
				putRecord(sp, registry.Workspace{Path: path, Kind: registry.KindWorktree, Owner: opts.Owner, CreatedAt: e.now().UTC().Truncate(time.Second)})
				return nil
			}); err != nil {
				added.Notes = append(added.Notes, "the worktree exists, but its owner could not be recorded: "+err.Error())
			}
		}
		if hasSubmodules(list[0].Path) {
			added.Notes = append(added.Notes, submoduleNote(p.Name))
		}
	}

	if len(patterns) > 0 {
		added.Copied, added.NotCopied = e.copyUnversioned(ctx, list[0].Path, path, patterns)
		if len(added.Copied) > 0 {
			added.Notes = append(added.Notes, fmt.Sprintf("Warning: %s may contain secrets, which now exist in two places. To share a file without duplicating it, use `theo project set %s --env-file <path>`: Theo then uses it by reference.", strings.Join(added.Copied, ", "), p.Name))
		}
	}

	p, err = e.Project(p.Name)
	if err != nil {
		return added, nil
	}
	list, err = e.worktreeList(ctx, p)
	if err != nil {
		return added, nil // the workspace exists; only the notes are lost
	}
	wt, err := selectWorktree(list, path)
	if err != nil {
		return added, nil
	}
	if env, _, err := resolveEnvFile(p, wt, list[0], nil); err == nil {
		added.Env = env
	}
	added.Notes = append(added.Notes, e.envNotes(ctx, p, list[0], wt, added.Env)...)
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

// RemovedWorkspace is the result of RemoveWorkspace.
type RemovedWorkspace struct {
	Worktree git.Worktree
	Kind     string // "worktree" or "clone"
	Forgot   bool   // a clone whose directory was already gone: only the record was removed
}

// RemoveWorktree removes a linked worktree or a clone; see RemoveWorkspace.
func (e *Engine) RemoveWorktree(ctx context.Context, project, selector string, opts RemoveWorktreeOptions) (git.Worktree, error) {
	r, err := e.RemoveWorkspace(ctx, project, selector, opts)
	return r.Worktree, err
}

// RemoveWorkspace removes a linked worktree or an independent clone. It
// refuses the main worktree, one that still has containers, and one with
// uncommitted or untracked files (and, for a clone, commits that exist
// nowhere else) unless the caller discards them explicitly with the
// directory's name as confirmation. The branch of a worktree and all volumes
// are always kept. A clone's directory is deleted with its Git history.
func (e *Engine) RemoveWorkspace(ctx context.Context, project, selector string, opts RemoveWorktreeOptions) (RemovedWorkspace, error) {
	p, err := e.Project(project)
	if err != nil {
		return RemovedWorkspace{}, err
	}
	list, err := e.worktreeList(ctx, p)
	if err != nil {
		return RemovedWorkspace{}, err
	}
	if selector == "" {
		return RemovedWorkspace{}, errors.New("say which worktree to remove: a branch, directory name or path")
	}
	wt, err := selectWorktree(list, selector)
	if err != nil {
		return RemovedWorkspace{}, fmt.Errorf("project %q: %w", p.Name, err)
	}
	res := RemovedWorkspace{Worktree: wt, Kind: workspaceKind(p, list, wt.Path)}
	if res.Kind == KindMain {
		return res, fmt.Errorf("%s is the main worktree of %q; Theo does not remove it", wt.Path, p.Name)
	}
	forget := func() error {
		return e.updateProject(p.Name, func(sp *registry.Project) error {
			if _, i := workspaceRecord(*sp, wt.Path); i >= 0 {
				sp.Workspaces = append(sp.Workspaces[:i:i], sp.Workspaces[i+1:]...)
			}
			return nil
		})
	}
	if wt.Prunable {
		if res.Kind == KindClone {
			res.Forgot = true
			return res, forget() // nothing on disk to lose
		}
		return res, fmt.Errorf("the directory of %s no longer exists; run `git worktree prune` in %s to forget it", wt.Path, p.Path)
	}

	overview, err := e.Overview(ctx, p.Name)
	if err != nil {
		return res, fmt.Errorf("cannot check whether %s has containers: %w", wt.Path, err)
	}
	for _, o := range overview {
		if samePath(o.Worktree.Path, wt.Path) && len(o.Stacks) > 0 {
			names := make([]string, len(o.Stacks))
			for i, s := range o.Stacks {
				names[i] = s.Name
			}
			return res, &InstanceError{Path: wt.Path, Stacks: names}
		}
	}

	changes, err := e.git.Changes(ctx, wt.Path)
	if err != nil {
		return res, err
	}
	if res.Kind == KindClone {
		unsaved, err := e.unsavedCommits(ctx, p, wt.Path)
		if err != nil {
			return res, err
		}
		changes = append(changes, unsaved...)
	}
	name := filepath.Base(wt.Path)
	force := false
	if len(changes) > 0 {
		if !opts.DiscardChanges || opts.Confirm != name {
			return res, &ChangesError{Path: wt.Path, Name: name, Changes: changes}
		}
		force = true
	}
	if res.Kind == KindClone {
		if err := removeAll(wt.Path); err != nil {
			return res, fmt.Errorf("cannot remove %s: %w", wt.Path, err)
		}
	} else if err := e.git.RemoveWorktree(ctx, p.Path, wt.Path, force); err != nil {
		return res, err
	}
	if err := forget(); err != nil {
		return res, fmt.Errorf("%s was removed, but its record could not be updated: %w", wt.Path, err)
	}
	return res, nil
}
