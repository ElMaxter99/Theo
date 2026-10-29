package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/instances"
	"github.com/ElMaxter99/Theo/internal/registry"
)

// InstanceOptions select how an instance is resolved. The zero value is
// what a plain command does.
type InstanceOptions struct {
	// EnvFiles replace the usual env file choice (see Services).
	EnvFiles []string
	// ComposeFiles, relative to the worktree root and in the order Compose
	// merges them, replace the Compose files for this call only.
	ComposeFiles []string
}

// Sources of the Compose files of an instance.
const (
	FilesFromFlag    = "--compose-file"
	FilesFromProject = "project setting"
	FilesFromEnv     = "COMPOSE_FILE"
)

// InstanceFor resolves the instance of a worktree like Instance, with
// options. See SelectedComposeFiles for how the Compose files are chosen.
func (e *Engine) InstanceFor(ctx context.Context, project, worktree string, opts InstanceOptions) (*Instance, error) {
	inst, _, err := e.resolveInstance(ctx, project, worktree, opts)
	return inst, err
}

// ServicesFor is Services with options.
func (e *Engine) ServicesFor(ctx context.Context, project, worktree string, opts InstanceOptions) (*Services, error) {
	return e.services(ctx, project, worktree, opts)
}

type fileSelection struct {
	files  []string // absolute; empty means: let Compose find them
	source string
}

// selectComposeFiles chooses the Compose files of a worktree, in order: the
// flag (one call only), the project's setting, COMPOSE_FILE from the process
// environment or from the env files; otherwise none, so Compose finds them
// by its own rules. Every file must exist in the worktree.
func (e *Engine) selectComposeFiles(p registry.Project, wt git.Worktree, env EnvFile, envFiles, flag []string) (fileSelection, error) {
	var sel fileSelection
	var rel []string
	switch {
	case len(flag) > 0:
		rel, sel.source = flag, FilesFromFlag
	case len(p.ComposeFiles) > 0:
		rel, sel.source = p.ComposeFiles, FilesFromProject
	}
	if rel != nil {
		clean, err := cleanComposeFiles(rel)
		if err != nil {
			return sel, fmt.Errorf("%s: %w", sel.source, err)
		}
		sel.files = compose.ResolvePaths(wt.Path, clean)
	} else {
		items, source := composeFileFromEnv(env, envFiles)
		if len(items) == 0 {
			return fileSelection{}, nil
		}
		sel.source = source
		sel.files = compose.ResolvePaths(wt.Path, items)
	}
	for _, f := range sel.files {
		if !isRegularFile(f) {
			name := f
			if rel, err := filepath.Rel(wt.Path, f); err == nil && !strings.HasPrefix(rel, "..") {
				name = rel
			}
			hint := ""
			switch sel.source {
			case FilesFromProject:
				hint = fmt.Sprintf("; change the setting with `theo project set %s --compose-file <file>` or go back to Compose's own choice with --default-compose-files", p.Name)
			case FilesFromEnv:
				hint = "; fix COMPOSE_FILE, or choose the files with `theo project set " + p.Name + " --compose-file <file>`"
			}
			return fileSelection{}, fmt.Errorf("Compose file %s (from %s) does not exist in worktree %s%s", name, sel.source, wt.Path, hint)
		}
	}
	return sel, nil
}

// composeFileFromEnv reads COMPOSE_FILE from the process environment, which
// wins, or else from the env files, the last one that sets it winning. The
// separator is COMPOSE_PATH_SEPARATOR, looked up the same way.
func composeFileFromEnv(env EnvFile, envFiles []string) (items []string, source string) {
	lookup := envFiles
	if lookup == nil && env.Source == EnvFromWorktree {
		lookup = []string{env.Path}
	}
	value := os.Getenv("COMPOSE_FILE")
	source = FilesFromEnv + " (environment)"
	if value == "" {
		for _, f := range lookup {
			if v, ok := compose.EnvFileValue([]string{f}, "COMPOSE_FILE"); ok {
				value, source = v, FilesFromEnv+" (in "+f+")"
			}
		}
	}
	if value == "" {
		return nil, ""
	}
	sep := os.Getenv("COMPOSE_PATH_SEPARATOR")
	if sep == "" {
		sep, _ = compose.EnvFileValue(lookup, "COMPOSE_PATH_SEPARATOR")
	}
	return compose.SplitComposeFile(value, sep), source
}

// configWithFiles reads the configuration of dir with the chosen Compose
// files. The adapter takes the files through an optional method, so fakes
// that only know Config keep working.
func (e *Engine) configWithFiles(ctx context.Context, dir string, envFiles, files []string) (*compose.Project, error) {
	if len(files) > 0 {
		if c, ok := e.compose.(interface {
			ConfigFiles(ctx context.Context, dir string, envFiles, files []string) (*compose.Project, error)
		}); ok {
			return c.ConfigFiles(ctx, dir, envFiles, files)
		}
	}
	return e.compose.Config(ctx, dir, envFiles)
}

// cleanComposeFiles checks paths that are relative to a worktree root and
// returns them normalized, with slashes. They must be non-empty, relative,
// stay inside the worktree and not repeat.
func cleanComposeFiles(files []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(files))
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" {
			return nil, fmt.Errorf("a Compose file path is empty")
		}
		native := filepath.FromSlash(f)
		if filepath.IsAbs(native) || strings.HasPrefix(f, "/") || strings.HasPrefix(f, `\`) || filepath.VolumeName(native) != "" {
			return nil, fmt.Errorf("%q is not relative: use paths relative to the worktree root, so the same setting works in every worktree", f)
		}
		c := filepath.Clean(native)
		if c == "." || c == ".." || strings.HasPrefix(c, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%q is outside the worktree: Compose files must be inside it", f)
		}
		c = filepath.ToSlash(c)
		if seen[c] {
			return nil, fmt.Errorf("%q is listed twice", f)
		}
		seen[c] = true
		out = append(out, c)
	}
	return out, nil
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// SetComposeFiles sets the Compose files of a project: paths relative to the
// root of each worktree, in the order Compose merges them (like repeated -f
// flags). Every file must exist in the project's main worktree. An empty
// list goes back to Compose's own choice (compose.yaml and its override).
func (e *Engine) SetComposeFiles(ctx context.Context, name string, files []string) (registry.Project, error) {
	p, err := e.Project(name)
	if err != nil {
		return registry.Project{}, err
	}
	var clean []string
	if len(files) > 0 {
		if clean, err = cleanComposeFiles(files); err != nil {
			return registry.Project{}, err
		}
		list, err := e.git.Worktrees(ctx, p.Path)
		if err != nil {
			return registry.Project{}, err
		}
		if len(list) == 0 || list[0].Bare {
			return registry.Project{}, fmt.Errorf("project %q has no main worktree to check the files in", p.Name)
		}
		for _, f := range clean {
			if !isRegularFile(filepath.Join(list[0].Path, filepath.FromSlash(f))) {
				return registry.Project{}, fmt.Errorf("Compose file %s does not exist in worktree %s (the main worktree of %q)", f, list[0].Path, p.Name)
			}
		}
	}
	unlock, err := e.locked()
	if err != nil {
		return registry.Project{}, err
	}
	defer unlock()
	projects, err := e.store.Load()
	if err != nil {
		return registry.Project{}, err
	}
	for i := range projects {
		if strings.EqualFold(projects[i].Name, p.Name) {
			projects[i].ComposeFiles = clean
			if err := e.store.Save(projects); err != nil {
				return registry.Project{}, err
			}
			return projects[i], nil
		}
	}
	return registry.Project{}, fmt.Errorf("project %q: %w (see `theo project list`)", name, ErrNotFound)
}

// mergeProfiles returns the profiles of a followed by the ones of b that a
// does not have.
func mergeProfiles(a, b []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, list := range [][]string{a, b} {
		for _, p := range list {
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// KnownProfiles returns the profiles that the services of the instance
// declare.
func (inst *Instance) KnownProfiles() []string {
	set := map[string]bool{}
	for _, s := range inst.config.Services {
		for _, p := range s.Profiles {
			set[p] = true
		}
	}
	return compose.SortedKeys(set)
}

// SetProfiles sets the Compose profiles the instance always starts with, in
// addition to the ones given to `up`. Each must be declared by a service.
// An empty list clears them.
func (e *Engine) SetProfiles(inst *Instance, profiles []string) error {
	profiles = mergeProfiles(profiles, nil)
	known := map[string]bool{}
	for _, p := range inst.KnownProfiles() {
		known[p] = true
	}
	for _, p := range profiles {
		if !known[p] {
			if len(known) == 0 {
				return fmt.Errorf("no service of the project declares profiles, so %q cannot be enabled", p)
			}
			return fmt.Errorf("no service declares the profile %q (profiles: %s)", p, strings.Join(inst.KnownProfiles(), ", "))
		}
	}
	if err := e.updateSettings(inst, func(c *instances.Config) error {
		c.Profiles = profiles
		return nil
	}); err != nil {
		return err
	}
	inst.Profiles = profiles
	return nil
}
