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
)

// ProjectInfo is a registered project with all its settings (FR-004).
type ProjectInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// EnvFile is the env file shared by worktrees without their own .env;
	// empty means the main worktree's .env.
	EnvFile string `json:"env_file,omitempty"`
	// WorktreeDir is the directory set for new worktrees; empty means Theo's
	// own, which WorktreeDirEffective shows.
	WorktreeDir          string `json:"worktree_dir,omitempty"`
	WorktreeDirEffective string `json:"worktree_dir_effective,omitempty"`
	// ComposeFiles are the Compose files set for the project, relative to
	// each worktree; empty means: the ones Compose finds.
	ComposeFiles []string  `json:"compose_files,omitempty"`
	AddedAt      time.Time `json:"added_at"`
}

// ProjectInfo returns the project called name with all its settings.
func (e *Engine) ProjectInfo(name string) (ProjectInfo, error) {
	p, err := e.Project(name)
	if err != nil {
		return ProjectInfo{}, err
	}
	info := ProjectInfo{Name: p.Name, Path: p.Path, EnvFile: p.EnvFile, WorktreeDir: p.WorktreeDir,
		ComposeFiles: p.ComposeFiles, AddedAt: p.AddedAt}
	info.WorktreeDirEffective, _ = e.worktreeDir(p.Name, p.WorktreeDir)
	return info, nil
}

// ProjectRenamer is implemented by the stores that keep records keyed by the
// project name. RenameProject changes the name in all of them and returns
// how many records it changed.
type ProjectRenamer interface {
	RenameProject(oldName, newName string) (int, error)
}

// AddProjectRenamer makes `theo project rename` also migrate the records of
// r. The settings and ports stores take part without being added, when they
// implement ProjectRenamer.
func (e *Engine) AddProjectRenamer(r ProjectRenamer) { e.renamers = append(e.renamers, r) }

func (e *Engine) projectRenamers() []ProjectRenamer {
	out := append([]ProjectRenamer(nil), e.renamers...)
	for _, s := range []any{e.settings, e.portStore} {
		if r, ok := s.(ProjectRenamer); ok {
			out = append(out, r)
		}
	}
	return out
}

// RenameOptions are the options of RenameProject.
type RenameOptions struct {
	// Yes allows the rename although volumes kept under the old Compose
	// project names will not be used by the renamed instances.
	Yes bool
}

// RenameResult is the result of RenameProject.
type RenameResult struct {
	Old     string `json:"old"`
	New     string `json:"new"`
	Records int    `json:"records"` // records of Theo's stores that were changed
	// WorktreeDir is set when the project used Theo's default worktree
	// directory and it was pinned to the old one, so worktrees stay together.
	WorktreeDir string   `json:"worktree_dir,omitempty"`
	Notes       []string `json:"notes,omitempty"`
}

// RenameProject renames a project and migrates every record keyed by its
// name: the instance settings, the history, whatever other stores added with
// AddProjectRenamer keep, and the default worktree directory.
//
// The Compose project name of a linked worktree is the project name plus the
// worktree's directory name, so renaming changes it. Containers and networks
// are tied to the old name; the rename is refused while any exists, and so
// when the new names are taken. Volumes kept under the old names are not
// deleted or moved: the rename is refused unless Yes says it is fine to
// leave them. The main worktree's name comes from Compose and does not
// change.
func (e *Engine) RenameProject(ctx context.Context, oldName, newName string, opts RenameOptions) (*RenameResult, error) {
	p, err := e.Project(oldName)
	if err != nil {
		return nil, err
	}
	if err := validateName(newName); err != nil {
		return nil, err
	}
	if p.Name == newName {
		return nil, fmt.Errorf("the project is already called %q", newName)
	}
	projects, err := e.store.Load()
	if err != nil {
		return nil, err
	}
	for _, other := range projects {
		if !strings.EqualFold(other.Name, p.Name) && strings.EqualFold(other.Name, newName) {
			return nil, fmt.Errorf("a project named %q is already registered (%s); choose another name", other.Name, other.Path)
		}
	}

	// Compose project names that change.
	type change struct{ from, to, path string }
	var changes []change
	if list, err := e.git.Worktrees(ctx, p.Path); err == nil {
		for i, wt := range list {
			if i == 0 || wt.Bare {
				continue
			}
			from, to := instanceName(p.Name, "", wt, list), instanceName(newName, "", wt, list)
			if from != to {
				changes = append(changes, change{from, to, wt.Path})
			}
		}
	}
	result := &RenameResult{Old: p.Name, New: newName}
	if len(changes) > 0 {
		stacks, err := e.compose.Stacks(ctx)
		if err != nil {
			return nil, fmt.Errorf("cannot check whether the instances of %q have containers, which a rename would orphan (their Compose project names derive from the project name): %w", p.Name, err)
		}
		running := map[string]string{}
		for _, s := range stacks {
			running[s.Name] = s.Status
		}
		var busy, taken []string
		for _, c := range changes {
			if status, ok := running[c.from]; ok {
				busy = append(busy, fmt.Sprintf("%s (%s)", c.from, status))
			}
			if _, ok := running[c.to]; ok {
				taken = append(taken, c.to)
			}
		}
		if len(busy) > 0 {
			return nil, fmt.Errorf("cannot rename %q: Compose project(s) %s still have containers. Compose project names derive from the project name, so after the rename Theo would no longer manage them. Run `theo down` for each instance first", p.Name, strings.Join(busy, ", "))
		}
		if len(taken) > 0 {
			return nil, fmt.Errorf("cannot rename %q to %q: Compose project(s) %s already exist in Docker and the renamed instances would take them over", p.Name, newName, strings.Join(taken, ", "))
		}
		refs, err := e.RegisteredInstances(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range changes {
			for _, r := range refs {
				if !strings.EqualFold(r.Project, p.Name) && strings.EqualFold(r.Compose, c.to) {
					return nil, fmt.Errorf("cannot rename %q to %q: the worktree %s would use the Compose project name %s, which %s already uses", p.Name, newName, c.path, c.to, r)
				}
			}
		}
		var kept []string
		for _, c := range changes {
			if vols, err := e.compose.Volumes(ctx, c.from); err == nil {
				for _, v := range vols {
					kept = append(kept, v.Name)
				}
			}
		}
		if len(kept) > 0 && !opts.Yes {
			sort.Strings(kept)
			return nil, fmt.Errorf("cannot rename %q without --yes: volume(s) %s were kept by instances under the old Compose project names, and the renamed instances will start with new, empty volumes. The old ones are not deleted or moved; you can still use them with docker", p.Name, strings.Join(kept, ", "))
		}
		if len(kept) > 0 {
			result.Notes = append(result.Notes, fmt.Sprintf("Volumes kept under the old names are not used by the renamed instances: %s", strings.Join(kept, ", ")))
		}
	}

	// Worktrees created in Theo's default directory live under the old name.
	updated := p
	updated.Name = newName
	if p.WorktreeDir == "" && e.dataDir != "" && slug(p.Name) != slug(newName) {
		if old := filepath.Join(e.dataDir, "worktrees", slug(p.Name)); dirExists(old) {
			updated.WorktreeDir = old
			result.WorktreeDir = old
			result.Notes = append(result.Notes, fmt.Sprintf("New worktrees keep going to %s, where the existing ones are (`theo project set %s --default-worktree-dir` goes back to Theo's own directory).", old, newName))
		}
	}

	for _, r := range e.projectRenamers() {
		n, err := r.RenameProject(p.Name, newName)
		if err != nil {
			return nil, fmt.Errorf("renaming the records of %q: %w (the project is not renamed; run the command again)", p.Name, err)
		}
		result.Records += n
	}
	for i := range projects {
		if strings.EqualFold(projects[i].Name, p.Name) {
			projects[i] = updated
		}
	}
	if err := e.store.Save(projects); err != nil {
		return nil, err
	}
	return result, nil
}

// RenameProject on the in-memory settings, for engines without a data
// directory.
func (m *memSettings) RenameProject(oldName, newName string) (int, error) {
	n := 0
	for ws, cfg := range m.state {
		if strings.EqualFold(cfg.Project, oldName) {
			cfg.Project = newName
			m.state[ws] = cfg
			n++
		}
	}
	for i := range m.events {
		if strings.EqualFold(m.events[i].Project, oldName) {
			m.events[i].Project = newName
			n++
		}
	}
	return n, nil
}

// ProjectStack is a Compose project, running or stopped, that a project's
// worktrees own.
type ProjectStack struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Worktree string `json:"worktree"`
}

// ProjectResources is what keeps existing when a project is removed from
// the registry (FR-003), and Theo's own records about it.
type ProjectResources struct {
	Project        string         `json:"project"`
	Path           string         `json:"path"`
	Worktrees      []string       `json:"worktrees"`                 // directories that exist; the project's own is the first
	Stacks         []ProjectStack `json:"compose_projects"`          // with containers, running or stopped
	Volumes        []string       `json:"volumes"`                   // kept by Compose projects of its instances
	GeneratedFiles []string       `json:"generated_files,omitempty"` // generated by Theo, outside the project
	Records        RecordCounts   `json:"records"`
	// DockerError is set when Docker could not be asked: the containers and
	// volumes are then unknown.
	DockerError string `json:"docker_error,omitempty"`

	workspaces []string
}

// RecordCounts are Theo's own records about a project.
type RecordCounts struct {
	Settings int `json:"instance_settings"`
	Ports    int `json:"reserved_ports"`
	History  int `json:"history_events"`
}

// InspectProject lists what keeps existing if the project is removed: its
// worktrees on disk, Compose projects with containers, volumes kept, and the
// files and records Theo holds. It changes nothing.
func (e *Engine) InspectProject(ctx context.Context, name string) (*ProjectResources, error) {
	p, err := e.Project(name)
	if err != nil {
		return nil, err
	}
	res := &ProjectResources{Project: p.Name, Path: p.Path, Worktrees: []string{}, Stacks: []ProjectStack{}, Volumes: []string{}}
	settings, err := e.settings.Load()
	if err != nil {
		return nil, err
	}
	assigned, err := e.portStore.Load()
	if err != nil {
		return nil, err
	}
	events, err := e.settings.Events()
	if err != nil {
		return nil, err
	}

	// Compose project names per worktree path that Theo knows or can derive.
	names := map[string]map[string]bool{}
	addName := func(ws, n string) {
		if ws == "" || n == "" {
			return
		}
		if names[ws] == nil {
			names[ws] = map[string]bool{}
		}
		names[ws][n] = true
	}
	workspaces := map[string]bool{}
	list, gitErr := e.git.Worktrees(ctx, p.Path)
	if gitErr == nil {
		for i, wt := range list {
			if wt.Bare {
				continue
			}
			workspaces[wt.Path] = true
			if dirExists(wt.Path) && !wt.Prunable {
				res.Worktrees = append(res.Worktrees, wt.Path)
			}
			if i > 0 {
				addName(wt.Path, instanceName(p.Name, "", wt, list))
			}
		}
	} else if dirExists(p.Path) {
		res.Worktrees = append(res.Worktrees, p.Path)
	}
	for ws, cfg := range settings {
		if strings.EqualFold(cfg.Project, p.Name) {
			workspaces[ws] = true
		}
	}
	for _, ev := range events {
		if strings.EqualFold(ev.Project, p.Name) {
			workspaces[ev.Workspace] = true
			addName(ev.Workspace, ev.Compose)
			res.Records.History++
		}
	}
	for ws := range workspaces {
		if _, ok := settings[ws]; ok {
			res.Records.Settings++
		}
		if _, ok := assigned[ws]; ok {
			res.Records.Ports++
		}
	}

	if gitErr == nil {
		overview, err := e.Overview(ctx, p.Name)
		if err != nil {
			res.DockerError = err.Error()
		}
		for _, o := range overview {
			for _, s := range o.Stacks {
				res.Stacks = append(res.Stacks, ProjectStack{Name: s.Name, Status: s.Status, Worktree: o.Worktree.Path})
				addName(o.Worktree.Path, s.Name)
			}
		}
	} else if stacks, err := e.compose.Stacks(ctx); err != nil {
		res.DockerError = err.Error()
	} else {
		for _, s := range stacks { // no worktree list: match the names Theo recorded
			for ws, set := range names {
				if set[s.Name] {
					res.Stacks = append(res.Stacks, ProjectStack{Name: s.Name, Status: s.Status, Worktree: ws})
				}
			}
		}
	}

	volumes := map[string]bool{}
	seenName := map[string]bool{}
	for ws, set := range names {
		for n := range set {
			if e.dataDir != "" {
				for _, f := range e.instanceFilesOf(ws, n) {
					if fileExists(f) {
						res.GeneratedFiles = append(res.GeneratedFiles, f)
					}
				}
			}
			if seenName[n] || res.DockerError != "" {
				continue
			}
			seenName[n] = true
			vols, err := e.compose.Volumes(ctx, n)
			if err != nil {
				res.DockerError = err.Error()
				continue
			}
			for _, v := range vols {
				volumes[v.Name] = true
			}
		}
	}
	for v := range volumes {
		res.Volumes = append(res.Volumes, v)
	}
	sort.Strings(res.Volumes)
	sort.Strings(res.GeneratedFiles)
	sort.Slice(res.Stacks, func(i, j int) bool { return res.Stacks[i].Name < res.Stacks[j].Name })
	for ws := range workspaces {
		res.workspaces = append(res.workspaces, ws)
	}
	return res, nil
}

// RemoveProjectOptions are the options of RemoveProjectChecked.
type RemoveProjectOptions struct {
	// Yes allows removing a project whose instances have containers.
	Yes bool
	// ForgetRecords also drops Theo's own records about the project: the
	// instance settings, the reserved ports and the files Theo generated.
	// The history stays.
	ForgetRecords bool
}

// ContainersError is returned when a project cannot be removed because its
// instances have containers and the removal was not confirmed. Nothing was
// changed.
type ContainersError struct {
	Project string
	Stacks  []ProjectStack
}

func (c *ContainersError) Error() string {
	names := make([]string, len(c.Stacks))
	for i, s := range c.Stacks {
		names[i] = fmt.Sprintf("%s (%s)", s.Name, s.Status)
	}
	return fmt.Sprintf("project %q has Compose project(s) with containers: %s. Removing it from the registry does not stop them, but Theo will no longer manage them; stop them first with `theo down`, or confirm with --yes", c.Project, strings.Join(names, ", "))
}

// RemoveProjectChecked removes a project from the registry like
// RemoveProject, after InspectProject. It is refused while the project's
// instances have containers, unless opts.Yes. It never deletes worktrees,
// containers or volumes. It returns what InspectProject found.
func (e *Engine) RemoveProjectChecked(ctx context.Context, name string, opts RemoveProjectOptions) (*ProjectResources, error) {
	res, err := e.InspectProject(ctx, name)
	if err != nil {
		return nil, err
	}
	if len(res.Stacks) > 0 && !opts.Yes {
		return res, &ContainersError{Project: res.Project, Stacks: res.Stacks}
	}
	if opts.ForgetRecords {
		if err := e.forgetRecords(res); err != nil {
			return res, err
		}
	}
	if _, err := e.RemoveProject(name); err != nil {
		return res, err
	}
	return res, nil
}

// forgetRecords drops Theo's own records about a project: settings and
// reserved ports of its worktrees, and the files generated for them.
func (e *Engine) forgetRecords(res *ProjectResources) error {
	settings, err := e.settings.Load()
	if err != nil {
		return err
	}
	assigned, err := e.portStore.Load()
	if err != nil {
		return err
	}
	for _, ws := range res.workspaces {
		delete(settings, ws)
		delete(assigned, ws)
	}
	for _, f := range res.GeneratedFiles {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := e.settings.Save(settings); err != nil {
		return err
	}
	return e.portStore.Save(assigned)
}
