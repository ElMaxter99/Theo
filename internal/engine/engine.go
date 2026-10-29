// Package engine is Theo's local engine. It holds the domain logic and
// exposes the operations that every interface (the CLI today, a graphical
// interface later) builds on. It must not depend on any interface.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/instances"
	"github.com/ElMaxter99/Theo/internal/ports"
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
	Branches(ctx context.Context, dir string) ([]git.Branch, error)
	AddWorktree(ctx context.Context, dir, path, branch string, newBranch bool, base string) error
	RemoveWorktree(ctx context.Context, dir, path string, force bool) error
	Changes(ctx context.Context, dir string) ([]string, error)
	IgnoredFiles(ctx context.Context, dir, pattern string) ([]string, error)
}

// Compose reads and operates Compose projects.
type Compose interface {
	Config(ctx context.Context, dir string, envFiles []string) (*compose.Project, error)
	Up(ctx context.Context, t compose.Target, opts compose.UpOptions, stdout, stderr io.Writer) error
	Down(ctx context.Context, t compose.Target, stdout, stderr io.Writer) error
	Restart(ctx context.Context, t compose.Target, stdout, stderr io.Writer) error
	Logs(ctx context.Context, t compose.Target, opts compose.LogOptions, stdout, stderr io.Writer) error
	PS(ctx context.Context, t compose.Target) ([]compose.Container, error)
	Stacks(ctx context.Context) ([]compose.Stack, error)
	ConfigFor(ctx context.Context, t compose.Target) (*compose.Project, error)
	ConfigText(ctx context.Context, t compose.Target) ([]byte, error)
	Volumes(ctx context.Context, project string) ([]compose.DockerVolume, error)
	VolumeUsers(ctx context.Context, volume string) ([]string, error)
	RemoveVolume(ctx context.Context, volume string) error
	EnsureNetwork(ctx context.Context, name string) (bool, error)
	RemoveNetwork(ctx context.Context, name string) error
}

// Engine is the entry point to Theo's operations.
type Engine struct {
	store   Store
	git     Git
	compose Compose
	now     func() time.Time

	portStore PortStore
	settings  SettingsStore
	portFree  func(hostIP string, port int, proto string) bool
	dataDir   string // where generated files are kept; empty if none
	baseFiles func(dir string) []string
}

// New returns an engine that uses the given store and adapters.
func New(store Store, g Git, c Compose) *Engine {
	return &Engine{store: store, git: g, compose: c, now: time.Now, portStore: &memPorts{}, settings: &memSettings{}, portFree: ports.Free, baseFiles: compose.DefaultFiles}
}

// memPorts keeps port assignments in memory, for engines that are not given
// a place to persist them.
type memPorts struct{ state ports.State }

func (m *memPorts) Load() (ports.State, error) {
	out := ports.State{}
	for k, v := range m.state {
		out[k] = v
	}
	return out, nil
}

func (m *memPorts) Save(s ports.State) error { m.state = s; return nil }

// Open returns an engine that uses the registry in Theo's default data
// directory and the real git and docker compose tools.
func Open() (*Engine, error) {
	dir, err := registry.DefaultDir()
	if err != nil {
		return nil, err
	}
	e := New(registry.Open(dir), git.Git{}, compose.Compose{})
	e.portStore = ports.Open(dir)
	e.settings = instances.Open(dir)
	e.dataDir = dir
	return e, nil
}

// AddProject registers the Git repository that contains path. When path is
// inside a linked worktree, the repository's main worktree is registered.
// The name defaults to the main worktree's directory name. Nothing in the
// repository is modified.
//
// envFile, if not empty, is the env file shared by worktrees that have no
// .env of their own; see SetEnvFile.
func (e *Engine) AddProject(ctx context.Context, path, name, envFile string) (registry.Project, error) {
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

	if envFile, err = checkEnvFile(envFile); err != nil {
		return registry.Project{}, err
	}
	p := registry.Project{Name: name, Path: root, EnvFile: envFile, AddedAt: e.now().UTC().Truncate(time.Second)}
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

// SetEnvFile sets the env file that worktrees without their own .env use.
// An empty path clears it, so they fall back to the main worktree's .env.
func (e *Engine) SetEnvFile(name, envFile string) (registry.Project, error) {
	envFile, err := checkEnvFile(envFile)
	if err != nil {
		return registry.Project{}, err
	}
	projects, err := e.store.Load()
	if err != nil {
		return registry.Project{}, err
	}
	for i, p := range projects {
		if strings.EqualFold(p.Name, name) {
			projects[i].EnvFile = envFile
			if err := e.store.Save(projects); err != nil {
				return registry.Project{}, err
			}
			return projects[i], nil
		}
	}
	return registry.Project{}, fmt.Errorf("project %q: %w (see `theo project list`)", name, ErrNotFound)
}

// checkEnvFile returns the absolute path of an existing env file, or an
// empty string for an empty path.
func checkEnvFile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if !fileExists(abs) {
		return "", fmt.Errorf("env file %s does not exist", abs)
	}
	return abs, nil
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
	// Kind is "main", "worktree" (linked) or "clone" (independent clone).
	Kind string `json:"kind"`
	// Owner is the label set with --owner or `theo worktree owner`, if any.
	Owner string `json:"owner,omitempty"`
}

// Worktrees lists the worktrees of a project, the main one first.
func (e *Engine) Worktrees(ctx context.Context, project string) ([]Worktree, error) {
	p, err := e.Project(project)
	if err != nil {
		return nil, err
	}
	list, err := e.worktreeList(ctx, p)
	if err != nil {
		return nil, err
	}
	result := make([]Worktree, len(list))
	for i, wt := range list {
		result[i].Worktree = wt
		result[i].Kind, result[i].Owner = workspaceKind(p, list, wt.Path), workspaceOwner(p, wt.Path)
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
	EnvFile   EnvFile   `json:"env_file"`
	Services  []Service `json:"services"`
}

// EnvFile is the env file docker compose used and where it came from.
type EnvFile struct {
	Path   string `json:"path,omitempty"` // empty when there is none
	Source string `json:"source"`         // flag, worktree, project, main worktree or none
}

// Sources of the env file of a worktree.
const (
	EnvFromFlag     = "flag"
	EnvFromWorktree = "worktree"
	EnvFromProject  = "project"
	EnvFromMain     = "main worktree"
	EnvNone         = "none"
)

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
//
// The env file is, in order: envFiles when given; the worktree's own .env;
// the project's env file (SetEnvFile); the main worktree's .env. So linked
// worktrees, which Git creates without untracked files, share the main
// worktree's .env unless they have their own.
func (e *Engine) Services(ctx context.Context, project, worktree string, envFiles []string) (*Services, error) {
	inst, cfg, err := e.instance(ctx, project, worktree, envFiles)
	if err != nil {
		return nil, err
	}
	wt := inst.worktree
	result := &Services{Project: inst.Project, Workspace: inst.Workspace, Branch: inst.Branch, Compose: inst.Compose, EnvFile: inst.EnvFile}
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

// resolveEnvFile chooses the env file for a worktree (see Services) and
// returns it with the files to pass to docker compose: nil means Compose's
// default, the worktree's own .env.
func resolveEnvFile(p registry.Project, wt, main git.Worktree, flagFiles []string) (EnvFile, []string, error) {
	if len(flagFiles) > 0 {
		files := make([]string, len(flagFiles))
		for i, f := range flagFiles {
			abs, err := checkEnvFile(f)
			if err != nil {
				return EnvFile{}, nil, err
			}
			files[i] = abs
		}
		return EnvFile{Path: strings.Join(files, ", "), Source: EnvFromFlag}, files, nil
	}
	if own := filepath.Join(wt.Path, ".env"); fileExists(own) {
		return EnvFile{Path: own, Source: EnvFromWorktree}, nil, nil
	}
	if p.EnvFile != "" {
		if !fileExists(p.EnvFile) {
			return EnvFile{}, nil, fmt.Errorf("the env file of project %q, %s, no longer exists; change it with `theo project set %s --env-file <path>`", p.Name, p.EnvFile, p.Name)
		}
		return EnvFile{Path: p.EnvFile, Source: EnvFromProject}, []string{p.EnvFile}, nil
	}
	if shared := filepath.Join(main.Path, ".env"); fileExists(shared) {
		return EnvFile{Path: shared, Source: EnvFromMain}, []string{shared}, nil
	}
	return EnvFile{Source: EnvNone}, nil, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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
