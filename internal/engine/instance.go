package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
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
	Kind      string  `json:"kind"` // "main", "worktree" or "clone"
	Owner     string  `json:"owner,omitempty"`
	EnvFile   EnvFile `json:"env_file"`
	// ComposeFiles are the Compose files the instance uses when they were
	// chosen rather than found by Compose (see InstanceOptions), and where
	// the choice came from. Profiles are the Compose profiles the instance
	// always starts with (see SetProfiles).
	ComposeFiles       []string `json:"compose_files,omitempty"`
	ComposeFilesSource string   `json:"compose_files_source,omitempty"`
	Profiles           []string `json:"profiles,omitempty"`

	worktree git.Worktree
	envFiles []string
	selected []string // chosen Compose files, absolute; empty means: let Compose find them
	files    []string // Compose files passed to Compose: selected, plus Theo's own when it adds any
	// instanceEnv is the generated env file with the instance's variables,
	// read after the others; generated lists the files Theo generated.
	instanceEnv string
	neutralized bool // fixed container names are removed
	siblings    int  // other worktrees of the project, which run their own instances
	generated   []string
	config      *compose.Project // resolved configuration of the worktree
}

func (i *Instance) target() compose.Target {
	envFiles := withInstanceEnv(i.Workspace, i.envFiles, i.instanceEnv)
	return compose.Target{Dir: i.Workspace, Name: i.Compose, EnvFiles: envFiles, Files: i.files}
}

// withInstanceEnv adds the generated env file, read last, to the env files of
// the instance. Any --env-file makes Compose skip the .env of the project
// directory, so when the instance relies on that .env it is named too, first:
// the generated file must add to it, not replace it.
func withInstanceEnv(workspace string, envFiles []string, instanceEnv string) []string {
	if instanceEnv == "" {
		return envFiles
	}
	out := append([]string(nil), envFiles...)
	if len(out) == 0 {
		if own := filepath.Join(workspace, ".env"); fileExists(own) {
			out = append(out, own)
		}
	}
	return append(out, instanceEnv)
}

// instance resolves the instance of a worktree and its Compose
// configuration (see Services for how worktree and envFiles are used).
func (e *Engine) instance(ctx context.Context, project, worktree string, envFiles []string) (*Instance, *compose.Project, error) {
	return e.resolveInstance(ctx, project, worktree, InstanceOptions{EnvFiles: envFiles})
}

func (e *Engine) resolveInstance(ctx context.Context, project, worktree string, opts InstanceOptions) (*Instance, *compose.Project, error) {
	p, err := e.Project(project)
	if err != nil {
		return nil, nil, err
	}
	list, err := e.worktreeList(ctx, p)
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

	env, files, err := resolveEnvFile(p, wt, list[0], opts.EnvFiles)
	if err != nil {
		return nil, nil, err
	}
	sel, err := e.selectComposeFiles(p, wt, env, files, opts.ComposeFiles)
	if err != nil {
		return nil, nil, err
	}
	// Compose reads the default .env from the directory of the first
	// Compose file, so name the worktree's own .env when that is elsewhere.
	if len(sel.files) > 0 && files == nil && env.Source == EnvFromWorktree && !samePath(filepath.Dir(sel.files[0]), wt.Path) {
		files = []string{env.Path}
	}
	cfg, err := e.configWithFiles(ctx, wt.Path, files, sel.files)
	if err != nil {
		if len(sel.files) > 0 {
			err = fmt.Errorf("%w\n\n(compose files, from %s: %s)", err, sel.source, strings.Join(sel.files, ", "))
		}
		if rel := missingWorktreeEnvFile(err.Error(), wt.Path); rel != "" && !samePath(wt.Path, list[0].Path) && fileExists(filepath.Join(list[0].Path, rel)) {
			return nil, nil, fmt.Errorf("%w\n\nA service declares `env_file: %s`, which Git does not track, so this worktree does not have it (the main worktree does). "+
				"Copy it into the worktree, or let Theo copy it from now on: `theo project set %s --copy-on-add %s`, then add the worktree with `--copy`.", err, rel, p.Name, rel)
		}
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
		Kind:      workspaceKind(p, list, wt.Path),
		Owner:     workspaceOwner(p, wt.Path),
		EnvFile:   env,
		worktree:  wt,
		envFiles:  files,
		selected:  sel.files,
		files:     sel.files,
		config:    cfg,
		siblings:  len(list) - 1,
	}
	if len(sel.files) > 0 {
		inst.ComposeFiles, inst.ComposeFilesSource = sel.files, sel.source
	}
	if state, err := e.settings.Load(); err == nil {
		inst.Profiles = state[wt.Path].Profiles
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
	if err := e.CheckUniqueName(ctx, inst); err != nil {
		return err
	}
	if err := inst.CheckServices(opts.Services); err != nil {
		return err
	}
	opts.Profiles = mergeProfiles(inst.Profiles, opts.Profiles)
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
	Kind     string          `json:"kind"` // "main", "worktree" or "clone"
	Owner    string          `json:"owner,omitempty"`
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
	list, err := e.worktreeList(ctx, p)
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
		result[i].Kind, result[i].Owner = workspaceKind(p, list, wt.Path), workspaceOwner(p, wt.Path)
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

// missingWorktreeEnvFile returns the path, relative to the worktree, of the
// file a Compose error says is missing ("env file <path> not found") when
// that file is supposed to live inside the worktree; "" otherwise.
func missingWorktreeEnvFile(msg, worktree string) string {
	m := missingEnvFileRe.FindStringSubmatch(msg)
	if m == nil {
		return ""
	}
	rel, err := filepath.Rel(worktree, filepath.Clean(m[1]))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel)
}

var missingEnvFileRe = regexp.MustCompile(`env file (\S+) not found`)
