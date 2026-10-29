// Package engine is Theo's local engine. It holds the domain logic and
// exposes the operations that every interface (the CLI today, a graphical
// interface later) builds on. It must not depend on any interface.
package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/registry"
)

// Version is the engine version. It is overridden at build time with
// -ldflags "-X github.com/ElMaxter99/Theo/internal/engine.Version=...".
var Version = "0.0.0-dev"

// ErrNotFound is returned when a project or worktree does not exist.
var ErrNotFound = errors.New("not found")

// Store persists the registered projects.
type Store interface {
	Load() ([]registry.Project, error)
	Save([]registry.Project) error
}

// Git reads the state of Git repositories.
type Git interface {
	TopLevel(ctx context.Context, dir string) (string, error)
	Worktrees(ctx context.Context, dir string) ([]git.Worktree, error)
	Dirty(ctx context.Context, dir string) (bool, error)
}

// Compose reads the resolved configuration of Compose projects.
type Compose interface {
	Config(ctx context.Context, dir string) (*compose.Project, error)
}

// Engine is the entry point to Theo's operations.
type Engine struct {
	store   Store
	git     Git
	compose Compose
	now     func() time.Time
}

// New returns an engine that uses the given store and adapters.
func New(store Store, g Git, c Compose) *Engine {
	return &Engine{store: store, git: g, compose: c, now: time.Now}
}

// Open returns an engine that uses the registry in Theo's default data
// directory and the real git and docker compose tools.
func Open() (*Engine, error) {
	dir, err := registry.DefaultDir()
	if err != nil {
		return nil, err
	}
	return New(registry.Open(dir), git.Git{}, compose.Compose{}), nil
}

// AddProject registers the Git repository that contains path. When path is
// inside a linked worktree, the repository's main worktree is registered.
// The name defaults to the main worktree's directory name. Nothing in the
// repository is modified.
func (e *Engine) AddProject(ctx context.Context, path, name string) (registry.Project, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return registry.Project{}, err
	}
	if _, err := e.git.TopLevel(ctx, abs); err != nil {
		return registry.Project{}, err
	}
	worktrees, err := e.git.Worktrees(ctx, abs)
	if err != nil {
		return registry.Project{}, err
	}
	if len(worktrees) == 0 {
		return registry.Project{}, fmt.Errorf("git reported no worktrees for %s", abs)
	}
	root := worktrees[0].Path

	if name == "" {
		name = filepath.Base(root)
	}
	if err := validateName(name); err != nil {
		return registry.Project{}, err
	}

	projects, err := e.store.Load()
	if err != nil {
		return registry.Project{}, err
	}
	for _, p := range projects {
		if samePath(p.Path, root) {
			return registry.Project{}, fmt.Errorf("%s is already registered as %q", root, p.Name)
		}
		if strings.EqualFold(p.Name, name) {
			return registry.Project{}, fmt.Errorf("a project named %q is already registered (%s); choose another name with --name", p.Name, p.Path)
		}
	}

	p := registry.Project{Name: name, Path: root, AddedAt: e.now().UTC().Truncate(time.Second)}
	if err := e.store.Save(append(projects, p)); err != nil {
		return registry.Project{}, err
	}
	return p, nil
}

// Projects returns the registered projects.
func (e *Engine) Projects() ([]registry.Project, error) {
	return e.store.Load()
}

// Project returns the registered project called name.
func (e *Engine) Project(name string) (registry.Project, error) {
	projects, err := e.store.Load()
	if err != nil {
		return registry.Project{}, err
	}
	for _, p := range projects {
		if strings.EqualFold(p.Name, name) {
			return p, nil
		}
	}
	return registry.Project{}, fmt.Errorf("project %q: %w (see `theo project list`)", name, ErrNotFound)
}

// RemoveProject removes a project from the registry. Its files, containers
// and volumes are left untouched.
func (e *Engine) RemoveProject(name string) (registry.Project, error) {
	projects, err := e.store.Load()
	if err != nil {
		return registry.Project{}, err
	}
	for i, p := range projects {
		if strings.EqualFold(p.Name, name) {
			rest := append(projects[:i:i], projects[i+1:]...)
			if err := e.store.Save(rest); err != nil {
				return registry.Project{}, err
			}
			return p, nil
		}
	}
	return registry.Project{}, fmt.Errorf("project %q: %w (see `theo project list`)", name, ErrNotFound)
}

// Worktree is a worktree of a project and whether it has uncommitted changes.
type Worktree struct {
	git.Worktree
	Dirty bool `json:"dirty"`
}

// Worktrees lists the worktrees of a project, the main one first.
func (e *Engine) Worktrees(ctx context.Context, project string) ([]Worktree, error) {
	p, err := e.Project(project)
	if err != nil {
		return nil, err
	}
	list, err := e.git.Worktrees(ctx, p.Path)
	if err != nil {
		return nil, err
	}
	result := make([]Worktree, len(list))
	for i, wt := range list {
		result[i].Worktree = wt
		if wt.Bare || wt.Prunable {
			continue
		}
		if result[i].Dirty, err = e.git.Dirty(ctx, wt.Path); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Services is the resolved Compose configuration of one workspace.
type Services struct {
	Project   string    `json:"project"`
	Workspace string    `json:"workspace"`
	Branch    string    `json:"branch,omitempty"`
	Compose   string    `json:"compose_project"`
	Services  []Service `json:"services"`
}

// Service is a service of the resolved Compose configuration.
type Service struct {
	Name      string         `json:"name"`
	Image     string         `json:"image,omitempty"`
	Build     string         `json:"build,omitempty"` // build context
	Ports     []compose.Port `json:"ports,omitempty"`
	Mounts    []Mount        `json:"mounts,omitempty"`
	DependsOn []string       `json:"depends_on,omitempty"`
	Networks  []string       `json:"networks,omitempty"`
	Profiles  []string       `json:"profiles,omitempty"`
}

// Mount is a volume, bind mount or tmpfs of a service. OutsideWorkspace
// marks bind mounts whose host path is outside the workspace directory.
type Mount struct {
	compose.Volume
	OutsideWorkspace bool `json:"outside_workspace,omitempty"`
}

// Services returns the services of a project as docker compose resolves
// them. worktree selects the workspace by branch name, directory name or
// path; empty means the project's main worktree.
func (e *Engine) Services(ctx context.Context, project, worktree string) (*Services, error) {
	p, err := e.Project(project)
	if err != nil {
		return nil, err
	}
	list, err := e.git.Worktrees(ctx, p.Path)
	if err != nil {
		return nil, err
	}
	wt, err := selectWorktree(list, worktree)
	if err != nil {
		return nil, fmt.Errorf("project %q: %w", p.Name, err)
	}
	if wt.Bare {
		return nil, fmt.Errorf("%s is a bare repository; choose a worktree with --worktree (see `theo worktrees %s`)", wt.Path, p.Name)
	}
	if wt.Prunable {
		return nil, fmt.Errorf("worktree %s no longer exists on disk", wt.Path)
	}

	cfg, err := e.compose.Config(ctx, wt.Path)
	if err != nil {
		return nil, err
	}

	result := &Services{Project: p.Name, Workspace: wt.Path, Branch: wt.Branch, Compose: cfg.Name}
	for _, name := range compose.SortedKeys(cfg.Services) {
		s := cfg.Services[name]
		svc := Service{
			Name:      name,
			Image:     s.Image,
			Ports:     s.Ports,
			DependsOn: compose.SortedKeys(s.DependsOn),
			Networks:  compose.SortedKeys(s.Networks),
			Profiles:  s.Profiles,
		}
		if s.Build != nil {
			svc.Build = s.Build.Context
		}
		for _, v := range s.Volumes {
			m := Mount{Volume: v}
			if v.Type == "bind" && !isInside(wt.Path, v.Source) {
				m.OutsideWorkspace = true
			}
			svc.Mounts = append(svc.Mounts, m)
		}
		result.Services = append(result.Services, svc)
	}
	return result, nil
}

func selectWorktree(list []git.Worktree, selector string) (git.Worktree, error) {
	if len(list) == 0 {
		return git.Worktree{}, fmt.Errorf("git reported no worktrees")
	}
	if selector == "" {
		return list[0], nil
	}
	if abs, err := filepath.Abs(selector); err == nil {
		candidates := []string{abs}
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			candidates = append(candidates, real)
		}
		for _, wt := range list {
			for _, c := range candidates {
				if samePath(wt.Path, c) {
					return wt, nil
				}
			}
		}
	}
	for _, wt := range list {
		if wt.Branch == selector {
			return wt, nil
		}
	}
	var matches []git.Worktree
	for _, wt := range list {
		if filepath.Base(wt.Path) == selector {
			matches = append(matches, wt)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return git.Worktree{}, fmt.Errorf("worktree %q: %w (see `theo worktrees`)", selector, ErrNotFound)
	default:
		return git.Worktree{}, fmt.Errorf("worktree %q is ambiguous: several worktrees have that directory name; use the branch or the full path", selector)
	}
}

func validateName(name string) error {
	if name == "" || strings.ContainsAny(name, " \t\r\n/\\") {
		return fmt.Errorf("invalid project name %q: it must not be empty or contain spaces or slashes", name)
	}
	return nil
}

// samePath compares two cleaned absolute paths, ignoring case on Windows.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// isInside reports whether path is base or a path below it.
func isInside(base, path string) bool {
	if runtime.GOOS == "windows" {
		base, path = strings.ToLower(base), strings.ToLower(path)
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
