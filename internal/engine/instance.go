package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/git"
)

// Instance is the Compose project that runs one worktree of a project.
// For now each worktree has exactly one instance.
type Instance struct {
	Project   string  `json:"project"`
	Workspace string  `json:"workspace"`
	Branch    string  `json:"branch,omitempty"`
	Compose   string  `json:"compose_project"`
	Main      bool    `json:"main"` // the project's main worktree
	EnvFile   EnvFile `json:"env_file"`

	worktree git.Worktree
	envFiles []string
	files    []string // Compose files, when Theo adds its own; empty otherwise
	// instanceEnv is the generated env file with the instance's variables,
	// read after the others; generated lists the files Theo generated.
	instanceEnv string
	neutralized bool // fixed container names are removed
	generated   []string
	config      *compose.Project // resolved configuration of the worktree
}

func (i *Instance) target() compose.Target {
	envFiles := i.envFiles
	if i.instanceEnv != "" {
		envFiles = append(append([]string(nil), envFiles...), i.instanceEnv)
	}
	return compose.Target{Dir: i.Workspace, Name: i.Compose, EnvFiles: envFiles, Files: i.files}
}

// instance resolves the instance of a worktree and its Compose
// configuration (see Services for how worktree and envFiles are used).
func (e *Engine) instance(ctx context.Context, project, worktree string, envFiles []string) (*Instance, *compose.Project, error) {
	p, err := e.Project(project)
	if err != nil {
		return nil, nil, err
	}
	list, err := e.git.Worktrees(ctx, p.Path)
	if err != nil {
		return nil, nil, err
	}
	wt, err := selectWorktree(list, worktree)
	if err != nil {
		return nil, nil, fmt.Errorf("project %q: %w", p.Name, err)
	}
	if wt.Bare {
		return nil, nil, fmt.Errorf("%s is a bare repository; choose a worktree with --worktree (see `theo worktrees %s`)", wt.Path, p.Name)
	}
	if wt.Prunable {
		return nil, nil, fmt.Errorf("worktree %s no longer exists on disk", wt.Path)
	}

	env, files, err := resolveEnvFile(p, wt, list[0], envFiles)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := e.compose.Config(ctx, wt.Path, files)
	if err != nil {
		if env.Source == EnvNone {
			return nil, nil, fmt.Errorf("%w\n\nNo .env found in this worktree or in the main worktree. Set one for the project with `theo project set %s --env-file <path>`, or pass --env-file.", err, p.Name)
		}
		return nil, nil, fmt.Errorf("%w\n\n(env file: %s, from the %s)", err, env.Path, env.Source)
	}

	inst := &Instance{
		Project:   p.Name,
		Workspace: wt.Path,
		Branch:    wt.Branch,
		Compose:   instanceName(p.Name, cfg.Name, wt, list),
		Main:      samePath(wt.Path, list[0].Path),
		EnvFile:   env,
		worktree:  wt,
		envFiles:  files,
		config:    cfg,
	}
	return inst, cfg, nil
}

// instanceName derives the Compose project name of a worktree's instance.
// The main worktree keeps the name Compose gives it (composeName, from
// `name:`, COMPOSE_PROJECT_NAME or the directory), so Theo sees the
// containers you started without it. A linked worktree gets the project name
// plus its directory name, and a short hash of its path when two worktrees
// share a directory name. So every worktree has its own containers, networks
// and volumes, even when they share the same .env.
func instanceName(project, composeName string, wt git.Worktree, list []git.Worktree) string {
	if len(list) == 0 || samePath(wt.Path, list[0].Path) {
		return composeName
	}
	dir := slug(filepath.Base(wt.Path))
	name := slug(project) + "-" + dir
	for _, other := range list {
		if !samePath(other.Path, wt.Path) && slug(filepath.Base(other.Path)) == dir {
			sum := sha256.Sum256([]byte(filepath.Clean(wt.Path)))
			return name + "-" + hex.EncodeToString(sum[:3])
		}
	}
	return name
}

// slug turns s into a valid Compose project name fragment: lowercase
// letters, digits, '-' and '_'.
func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		return "wt"
	}
	return out
}

// Instance returns the instance of a worktree without operating on it.
func (e *Engine) Instance(ctx context.Context, project, worktree string, envFiles []string) (*Instance, error) {
	inst, _, err := e.instance(ctx, project, worktree, envFiles)
	return inst, err
}

// Up creates and starts the containers of a worktree's instance.
func (e *Engine) Up(ctx context.Context, inst *Instance, opts compose.UpOptions, stdout, stderr io.Writer) error {
	if err := inst.CheckServices(opts.Services); err != nil {
		return err
	}
	err := e.compose.Up(ctx, inst.target(), opts, stdout, stderr)
	e.record(inst, "up", err)
	return err
}

// Down stops and removes the containers and networks of a worktree's
// instance. Named volumes and host files are always kept.
func (e *Engine) Down(ctx context.Context, inst *Instance, stdout, stderr io.Writer) error {
	err := e.compose.Down(ctx, inst.target(), stdout, stderr)
	e.record(inst, "down", err)
	return err
}

// Restart restarts the containers of a worktree's instance.
func (e *Engine) Restart(ctx context.Context, inst *Instance, stdout, stderr io.Writer) error {
	err := e.compose.Restart(ctx, inst.target(), stdout, stderr)
	e.record(inst, "restart", err)
	return err
}

// Logs writes the logs of a worktree's instance.
func (e *Engine) Logs(ctx context.Context, inst *Instance, opts compose.LogOptions, stdout, stderr io.Writer) error {
	return e.compose.Logs(ctx, inst.target(), opts, stdout, stderr)
}

// Containers returns the containers of a worktree's instance.
func (e *Engine) Containers(ctx context.Context, inst *Instance) ([]compose.Container, error) {
	return e.compose.PS(ctx, inst.target())
}

// WorktreeStacks pairs a worktree with the Compose projects Docker knows
// for it, whether Theo started them or not.
type WorktreeStacks struct {
	Worktree git.Worktree    `json:"worktree"`
	Stacks   []compose.Stack `json:"stacks"`
}

// Overview returns, for each worktree of a project, the Compose projects
// whose files live in it. A Compose project belongs to the innermost
// worktree that contains its first config file.
func (e *Engine) Overview(ctx context.Context, project string) ([]WorktreeStacks, error) {
	p, err := e.Project(project)
	if err != nil {
		return nil, err
	}
	list, err := e.git.Worktrees(ctx, p.Path)
	if err != nil {
		return nil, err
	}
	stacks, err := e.compose.Stacks(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]WorktreeStacks, len(list))
	for i, wt := range list {
		result[i].Worktree = wt
		result[i].Stacks = []compose.Stack{} // JSON: an empty list, not null
	}
	for _, s := range stacks {
		first, _, _ := strings.Cut(s.ConfigFiles, ",")
		if first == "" {
			continue
		}
		best := -1
		for i, wt := range list {
			if wt.Bare || !isInside(wt.Path, filepath.Dir(first)) {
				continue
			}
			if best < 0 || len(wt.Path) > len(list[best].Path) {
				best = i
			}
		}
		if best >= 0 {
			result[best].Stacks = append(result[best].Stacks, s)
		}
	}
	for i := range result {
		sort.Slice(result[i].Stacks, func(a, b int) bool { return result[i].Stacks[a].Name < result[i].Stacks[b].Name })
	}
	return result, nil
}

// Neutralized reports whether the instance removes fixed container names,
// after ApplyInstance.
func (i *Instance) Neutralized() bool { return i.neutralized }
