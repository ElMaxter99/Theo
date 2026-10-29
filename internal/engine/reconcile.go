package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/instances"
	"github.com/ElMaxter99/Theo/internal/registry"
)

// Finding kinds.
const (
	FindProjectMissing  = "project missing"      // the registered path is gone or is not a Git repository
	FindWorktreeMissing = "worktree missing"     // Git lists a worktree whose directory is gone
	FindForeignStack    = "started outside Theo" // a Compose project in a worktree, named differently from the instance Theo would use
	FindStopped         = "containers removed"   // Theo's last operation was `up` but the instance has no containers now
	FindOrphanStack     = "orphan containers"    // containers of an instance whose worktree no longer exists
	FindOrphanVolumes   = "orphan volumes"       // volumes of an instance whose worktree no longer exists
	FindStaleSettings   = "stale settings"       // Theo's settings for a worktree that no longer exists
	FindStalePorts      = "stale ports"          // ports reserved for a worktree that no longer exists
	FindStaleFiles      = "stale files"          // files Theo generated for a worktree that no longer exists
)

// Finding is a difference between what Theo recorded and what exists.
type Finding struct {
	Kind    string `json:"kind"`
	Project string `json:"project,omitempty"`
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
	Fix     string `json:"fix,omitempty"` // what to do about it
	// Forgettable marks Theo's own records, which `reconcile --apply` can
	// forget. Containers, volumes and worktrees are never touched.
	Forgettable bool `json:"forgettable,omitempty"`

	workspace string // for stale records: the worktree path
	path      string // for stale files: the file
}

// Report is the result of Reconcile.
type Report struct {
	Findings []Finding `json:"findings"`
	// DockerError is set when Docker could not be asked; the findings that
	// need it are missing.
	DockerError string `json:"docker_error,omitempty"`
}

// Reconcile compares what Theo recorded (registered projects, instance
// settings, assigned ports, generated files, history) with what exists in
// Git, on disk and in Docker, and reports every difference. It changes
// nothing. With an empty project it covers all registered projects and the
// records that belong to none. A Docker that cannot be reached is reported,
// not an error.
func (e *Engine) Reconcile(ctx context.Context, project string) (*Report, error) {
	projects, err := e.Projects()
	if err != nil {
		return nil, err
	}
	if project != "" {
		p, err := e.Project(project)
		if err != nil {
			return nil, err
		}
		projects = []registry.Project{p}
	}
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

	// What Theo knows about each worktree path it has handled: the project
	// it belongs to and the Compose project name it used.
	type known struct{ project, compose string }
	seen := map[string]known{}
	for _, ev := range events {
		seen[ev.Workspace] = known{project: ev.Project, compose: ev.Compose}
	}
	for ws, cfg := range settings {
		if k := seen[ws]; cfg.Project != "" {
			seen[ws] = known{project: cfg.Project, compose: k.compose}
		}
	}

	report := &Report{}
	add := func(f Finding) { report.Findings = append(report.Findings, f) }

	stacks, stacksErr := e.compose.Stacks(ctx)
	if stacksErr != nil {
		report.DockerError = stacksErr.Error()
	}
	stackNames := map[string]string{} // name -> status
	for _, s := range stacks {
		stackNames[s.Name] = s.Status
	}

	live := map[string]bool{} // worktree paths that exist, of every project in scope
	for _, p := range projects {
		list, err := e.git.Worktrees(ctx, p.Path)
		if err != nil || !dirExists(p.Path) {
			add(Finding{Kind: FindProjectMissing, Project: p.Name, Subject: p.Path,
				Detail: "the path no longer exists or is not a Git repository",
				Fix:    fmt.Sprintf("restore it, or run `theo project remove %s` (files, containers and volumes are not touched)", p.Name)})
			continue
		}
		for _, wt := range list {
			if wt.Bare {
				continue
			}
			if wt.Prunable || !dirExists(wt.Path) {
				add(Finding{Kind: FindWorktreeMissing, Project: p.Name, Subject: wt.Path,
					Detail: "Git still lists this worktree but its directory is gone",
					Fix:    "run `git worktree prune` in " + p.Path})
				continue
			}
			live[filepath.Clean(wt.Path)] = true
			e.checkWorktreeStacks(p, wt, list, stacks, stacksErr == nil, seen[wt.Path].compose, stackNames, add)
		}
	}

	// Records of worktrees that no longer exist, in scope.
	inScope := map[string]bool{}
	for _, p := range projects {
		inScope[strings.ToLower(p.Name)] = true
	}
	workspaces := map[string]bool{}
	for ws := range settings {
		workspaces[ws] = true
	}
	for ws := range assigned {
		workspaces[ws] = true
	}
	for ws := range seen {
		workspaces[ws] = true
	}
	var gone []string
	for ws := range workspaces {
		if live[filepath.Clean(ws)] || dirExists(ws) {
			continue
		}
		owner := strings.ToLower(seen[ws].project)
		if project != "" && !inScope[owner] {
			continue
		}
		gone = append(gone, ws)
	}
	sort.Strings(gone)
	for _, ws := range gone {
		k := seen[ws]
		owner := k.project
		subject := ws
		if cfg, ok := settings[ws]; ok {
			add(Finding{Kind: FindStaleSettings, Project: owner, Subject: subject, workspace: ws, Forgettable: true,
				Detail: fmt.Sprintf("settings for a worktree that no longer exists (%s)", describeSettings(cfg)),
				Fix:    "`theo reconcile --apply` forgets them"})
		}
		if a, ok := assigned[ws]; ok {
			add(Finding{Kind: FindStalePorts, Project: owner, Subject: subject, workspace: ws, Forgettable: true,
				Detail: fmt.Sprintf("%d host port(s) still reserved, which keeps other instances from using them", len(a)),
				Fix:    "`theo reconcile --apply` releases them"})
		}
		if k.compose != "" && e.dataDir != "" {
			for _, f := range e.instanceFilesOf(ws, k.compose) {
				if fileExists(f) {
					add(Finding{Kind: FindStaleFiles, Project: owner, Subject: f, workspace: ws, path: f, Forgettable: true,
						Detail: "generated file of a worktree that no longer exists", Fix: "`theo reconcile --apply` deletes it"})
				}
			}
		}
		if k.compose == "" || stacksErr != nil {
			continue
		}
		if status, ok := stackNames[k.compose]; ok {
			add(Finding{Kind: FindOrphanStack, Project: owner, Subject: k.compose,
				Detail: fmt.Sprintf("Compose project %s (%s) still has containers, but its worktree %s is gone", k.compose, status, ws),
				Fix:    fmt.Sprintf("docker compose -p %s down", k.compose)})
		}
		if vols, err := e.compose.Volumes(ctx, k.compose); err == nil && len(vols) > 0 {
			names := make([]string, len(vols))
			for i, v := range vols {
				names[i] = v.Name
			}
			add(Finding{Kind: FindOrphanVolumes, Project: owner, Subject: k.compose,
				Detail: fmt.Sprintf("%d volume(s) of %s kept with their data: %s", len(vols), k.compose, strings.Join(names, ", ")),
				Fix:    "check them, then `docker volume rm " + strings.Join(names, " ") + "` if the data is not needed. Theo never deletes them on its own"})
		}
	}
	for _, check := range reconcileChecks {
		check(ctx, e, project, add)
	}
	return report, nil
}

// checkWorktreeStacks compares the Compose projects Docker has for a
// worktree with the instance Theo would use for it.
func (e *Engine) checkWorktreeStacks(p registry.Project, wt git.Worktree, list []git.Worktree, stacks []compose.Stack, dockerOK bool, lastCompose string, names map[string]string, add func(Finding)) {
	if !dockerOK {
		return
	}
	isMain := len(list) > 0 && samePath(wt.Path, list[0].Path)
	if !isMain {
		want := instanceName(p.Name, "", wt, list)
		for _, s := range stacks {
			first, _, _ := strings.Cut(s.ConfigFiles, ",")
			if first == "" || !samePath(filepath.Dir(first), wt.Path) || s.Name == want {
				continue
			}
			add(Finding{Kind: FindForeignStack, Project: p.Name, Subject: s.Name,
				Detail: fmt.Sprintf("Compose project %s (%s) runs from %s, but Theo uses the name %s for this worktree, so `theo ps` and `theo down` do not manage it", s.Name, s.Status, wt.Path, want),
				Fix:    fmt.Sprintf("docker compose -p %s down, then `theo up %s -w %s`", s.Name, p.Name, wt.Branch)})
		}
	}
	if lastCompose != "" {
		if _, running := names[lastCompose]; !running {
			if last := e.lastOperation(wt.Path); last == "up" {
				add(Finding{Kind: FindStopped, Project: p.Name, Subject: lastCompose,
					Detail: fmt.Sprintf("the last Theo operation on %s was `up`, but %s has no containers now: they were removed outside Theo", wt.Path, lastCompose),
					Fix:    fmt.Sprintf("`theo up %s -w %s` starts it again; its volumes were kept", p.Name, wt.Branch)})
			}
		}
	}
}

// lastOperation returns the last successful operation recorded for a worktree.
func (e *Engine) lastOperation(workspace string) string {
	events, err := e.settings.Events()
	if err != nil {
		return ""
	}
	last := ""
	for _, ev := range events {
		if samePath(ev.Workspace, workspace) && ev.Result == "ok" && (ev.Operation == "up" || ev.Operation == "down") {
			last = ev.Operation
		}
	}
	return last
}

// instanceFilesOf returns the generated files of a worktree path and
// Compose project name (see instanceFile).
func (e *Engine) instanceFilesOf(workspace, composeName string) []string {
	inst := &Instance{Workspace: workspace, Compose: composeName}
	return []string{e.instanceFile(inst, ".yaml"), e.instanceFile(inst, ".env")}
}

func describeSettings(cfg instances.Config) string {
	var parts []string
	if n := len(cfg.Env); n > 0 {
		parts = append(parts, fmt.Sprintf("%d variable(s)", n))
	}
	if cfg.NeutralizeContainerNames {
		parts = append(parts, "container names neutralized")
	}
	if len(parts) == 0 {
		return "empty"
	}
	return strings.Join(parts, ", ")
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ApplyReconcile forgets the records of a Reconcile report that are Theo's
// own: settings and port reservations of worktrees that no longer exist, and
// the files generated for them. It never touches worktrees, containers,
// volumes or any file of the user. It returns how many records it forgot.
func (e *Engine) ApplyReconcile(report *Report) (int, error) {
	settings, err := e.settings.Load()
	if err != nil {
		return 0, err
	}
	assigned, err := e.portStore.Load()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, f := range report.Findings {
		if !f.Forgettable {
			continue
		}
		switch f.Kind {
		case FindStaleSettings:
			delete(settings, f.workspace)
		case FindStalePorts:
			delete(assigned, f.workspace)
		case FindStaleFiles:
			if err := os.Remove(f.path); err != nil && !os.IsNotExist(err) {
				return n, err
			}
		}
		n++
	}
	if n == 0 {
		return 0, nil
	}
	if err := e.settings.Save(settings); err != nil {
		return 0, err
	}
	if err := e.portStore.Save(assigned); err != nil {
		return 0, err
	}
	return n, nil
}
