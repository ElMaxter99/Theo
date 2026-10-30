package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/registry"
)

// Instance states reported by Status.
const (
	StateNotCreated = "not created" // no containers
	StateRunning    = "running"     // every expected service runs
	StateStopped    = "stopped"     // containers exist and none runs
	StatePartial    = "partial"     // some run, some do not (or were never created)
	StateUnknown    = "unknown"     // Docker or the configuration could not be read
)

// KindBare is the kind Status reports for a bare repository; the others are
// KindMain, KindWorktree and KindClone (workspace.go).
const KindBare = "bare"

// StatusReport is the dashboard: every project, its worktrees and what runs
// in each. It is the data of `theo status --json`.
type StatusReport struct {
	Projects []ProjectStatus `json:"projects"`
	// DockerError is set when Docker could not be asked; the states are then
	// "unknown".
	DockerError string `json:"docker_error,omitempty"`
	// Warnings are differences that belong to no registered project, such as
	// settings left by a project that was removed.
	Warnings []StatusWarning `json:"warnings"`
}

// ProjectStatus is a registered project and its worktrees.
type ProjectStatus struct {
	Name      string           `json:"name"`
	Path      string           `json:"path"`
	Error     string           `json:"error,omitempty"` // the project could not be read
	Worktrees []WorktreeStatus `json:"worktrees"`
	Warnings  []StatusWarning  `json:"warnings"` // about the project, not one worktree
}

// WorktreeStatus is a worktree and its instance.
type WorktreeStatus struct {
	Path     string `json:"path"`
	Branch   string `json:"branch,omitempty"` // empty when detached or bare
	Head     string `json:"head,omitempty"`
	Kind     string `json:"kind"` // main, linked or bare
	Detached bool   `json:"detached,omitempty"`
	Dirty    bool   `json:"dirty"` // uncommitted or untracked files
	Locked   bool   `json:"locked,omitempty"`
	Missing  bool   `json:"missing,omitempty"` // Git lists it but its directory is gone
	// EnvFile is the env file the instance uses; absent when the worktree has
	// no instance.
	EnvFile *EnvFile `json:"env_file,omitempty"`
	// Instance and Settings are absent for a bare or missing worktree.
	Instance *InstanceStatus `json:"instance,omitempty"`
	Settings *SettingsStatus `json:"settings,omitempty"`
	Warnings []StatusWarning `json:"warnings"`
}

// InstanceStatus is the state of the Compose project of a worktree.
type InstanceStatus struct {
	ComposeProject string `json:"compose_project,omitempty"` // empty when the configuration cannot be read
	State          string `json:"state"`
	// InstanceState is State under the rules of InstanceState (running,
	// partial, unhealthy, stopped, absent or unknown), which `theo ps` and
	// the graphical interface share.
	InstanceState string `json:"instance_state"`
	// Counts are services by state.
	Counts   ServiceCounts   `json:"counts"`
	Services []ServiceStatus `json:"services"`
	Ports    []PortStatus    `json:"ports"`
}

// ServiceCounts counts the services of an instance by state. Total is the
// number of services listed: the ones without a profile and the ones that
// have containers.
type ServiceCounts struct {
	Running    int `json:"running"`
	Stopped    int `json:"stopped"`
	Completed  int `json:"completed"`   // exited with status 0, like a migration job
	NotCreated int `json:"not_created"` // expected, but no container exists
	Total      int `json:"total"`
}

// ServiceStatus is one service of an instance.
type ServiceStatus struct {
	Name       string   `json:"name"`
	State      string   `json:"state"` // running, stopped, completed, partial or not created
	Containers int      `json:"containers"`
	Running    int      `json:"running"`
	Health     string   `json:"health,omitempty"`
	Profiles   []string `json:"profiles,omitempty"`
}

// PortStatus is a host port a service publishes.
type PortStatus struct {
	Service       string `json:"service"`
	ContainerPort uint32 `json:"container_port"`
	Protocol      string `json:"protocol"`
	HostPort      int    `json:"host_port,omitempty"` // 0 when Docker picks it at start
	URL           string `json:"url,omitempty"`
	// Status is the one of `theo ports`: free, running, in use, kept, random...
	Status string `json:"status"`
}

// SettingsStatus summarizes what Theo keeps for the instance and what the
// project declares.
type SettingsStatus struct {
	EnvVars       int `json:"env_vars"`
	SecretEnvVars int `json:"secret_env_vars"`
	// NeutralizeContainerNames is true when the instance removes fixed
	// container_name settings; FixedContainerNames lists the services that
	// set one, with the name.
	NeutralizeContainerNames bool              `json:"neutralize_container_names"`
	FixedContainerNames      map[string]string `json:"fixed_container_names,omitempty"`
	// Profiles are the Compose profiles the project declares.
	Profiles []string `json:"profiles"`
}

// StatusWarning is something the user should look at.
type StatusWarning struct {
	// Kind is one of: port-conflict, foreign-stack, missing-env-file,
	// config-error, fixed-container-name, docker-unavailable, or a
	// `theo reconcile` finding kind ("worktree missing", "containers
	// removed", ...).
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// Status builds the dashboard of one project, or of every registered project
// when project is empty. It only reads: Git, Docker and Theo's own records.
// A project or worktree that cannot be read is reported in its entry, not as
// an error of the whole call.
func (e *Engine) Status(ctx context.Context, project string) (*StatusReport, error) {
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
	report := &StatusReport{Projects: []ProjectStatus{}, Warnings: []StatusWarning{}}

	drift, err := e.Reconcile(ctx, project)
	if err != nil {
		return nil, err
	}
	report.DockerError = drift.DockerError

	for _, p := range projects {
		report.Projects = append(report.Projects, e.projectStatus(ctx, p, drift, report.DockerError != ""))
	}
	for _, f := range drift.Findings {
		if f.Project == "" {
			report.Warnings = append(report.Warnings, findingWarning(f))
		}
	}
	return report, nil
}

func (e *Engine) projectStatus(ctx context.Context, p registry.Project, drift *Report, dockerDown bool) ProjectStatus {
	ps := ProjectStatus{Name: p.Name, Path: p.Path, Worktrees: []WorktreeStatus{}, Warnings: []StatusWarning{}}
	list, err := e.Worktrees(ctx, p.Name)
	if err != nil {
		ps.Error = firstLine(err.Error())
		for _, f := range drift.Findings {
			if strings.EqualFold(f.Project, p.Name) {
				ps.Warnings = append(ps.Warnings, findingWarning(f))
			}
		}
		return ps
	}
	for i, wt := range list {
		ws := WorktreeStatus{
			Path: wt.Path, Branch: wt.Branch, Head: wt.Head, Kind: KindWorktree, Detached: wt.Detached,
			Dirty: wt.Dirty, Locked: wt.Locked, Missing: wt.Prunable, Warnings: []StatusWarning{},
		}
		switch {
		case wt.Bare:
			ws.Kind = KindBare
		case i == 0:
			ws.Kind = KindMain
		}
		if !wt.Bare && !wt.Prunable {
			e.instanceStatus(ctx, p, &ws, dockerDown)
		}
		for _, f := range drift.Findings {
			if strings.EqualFold(f.Project, p.Name) && f.Worktree != "" && samePath(f.Worktree, wt.Path) {
				ws.Warnings = append(ws.Warnings, findingWarning(f))
			}
		}
		ps.Worktrees = append(ps.Worktrees, ws)
	}
	for _, f := range drift.Findings {
		if strings.EqualFold(f.Project, p.Name) && f.Worktree == "" {
			ps.Warnings = append(ps.Warnings, findingWarning(f))
		}
	}
	return ps
}

// instanceStatus fills the instance, settings and warnings of a worktree.
func (e *Engine) instanceStatus(ctx context.Context, p registry.Project, ws *WorktreeStatus, dockerDown bool) {
	warn := func(kind, msg, fix string) {
		ws.Warnings = append(ws.Warnings, StatusWarning{Kind: kind, Message: msg, Fix: fix})
	}
	ws.Instance = &InstanceStatus{State: StateUnknown, InstanceState: InstUnknown, Services: []ServiceStatus{}, Ports: []PortStatus{}}

	// A project env file that vanished is the one problem worth naming apart
	// from the Compose error it causes.
	if p.EnvFile != "" && !fileExists(p.EnvFile) && !fileExists(filepath.Join(ws.Path, ".env")) {
		warn("missing-env-file", fmt.Sprintf("the env file of project %q, %s, no longer exists", p.Name, p.EnvFile),
			fmt.Sprintf("theo project set %s --env-file <path>", p.Name))
	}

	inst, cfg, err := e.instance(ctx, p.Name, ws.Path, nil)
	if err != nil {
		kind, fix := "config-error", ""
		if strings.Contains(err.Error(), "No .env found") {
			kind = "missing-env-file"
			fix = fmt.Sprintf("theo project set %s --env-file <path>", p.Name)
		}
		if kind == "config-error" && hasKind(ws.Warnings, "missing-env-file") {
			return // already explained
		}
		warn(kind, firstLine(err.Error()), fix)
		return
	}
	ws.EnvFile = &inst.EnvFile
	ws.Instance.ComposeProject = inst.Compose

	settings, err := e.Settings(inst)
	if err == nil {
		ss := &SettingsStatus{
			EnvVars: len(settings.Env), NeutralizeContainerNames: settings.NeutralizeContainerNames,
			FixedContainerNames: inst.FixedContainerNames(), Profiles: declaredProfiles(cfg),
		}
		for _, v := range settings.Env {
			if v.Secret {
				ss.SecretEnvVars++
			}
		}
		if len(ss.FixedContainerNames) == 0 {
			ss.FixedContainerNames = nil
		}
		ws.Settings = ss
		if len(ss.FixedContainerNames) > 0 && !inst.Main && !settings.NeutralizeContainerNames {
			warn("fixed-container-name", fmt.Sprintf("%s set a fixed container_name, so another instance of this project cannot run at the same time", strings.Join(compose.SortedKeys(ss.FixedContainerNames), ", ")),
				fmt.Sprintf("theo instance set %s -w %s --neutralize-container-names", p.Name, statusSelector(ws)))
		}
	}

	if dockerDown {
		warn("docker-unavailable", "Docker could not be asked, so the state is unknown", "")
		return
	}
	containers, err := e.compose.PS(ctx, inst.target())
	if err != nil {
		warn("docker-unavailable", firstLine(err.Error()), "")
		return
	}
	ws.Instance.State, ws.Instance.Counts, ws.Instance.Services = summarize(cfg, containers)
	ws.Instance.InstanceState = InstanceState(containers, expectedServices(cfg), true)

	plan, err := e.PlanPorts(ctx, inst, nil, false)
	if err != nil {
		return
	}
	for _, m := range plan.Mappings {
		port := m.Effective
		if m.Running != 0 {
			port = m.Running
		}
		ws.Instance.Ports = append(ws.Instance.Ports, PortStatus{
			Service: m.Service, ContainerPort: m.Target, Protocol: m.Protocol, HostPort: port, URL: m.URL(), Status: m.Status,
		})
	}
	for _, m := range plan.Conflicts() {
		warn("port-conflict", fmt.Sprintf("host port %s (%s, container port %d/%s) is already in use", m.Configured, m.Service, m.Target, m.Protocol),
			fmt.Sprintf("free it, or start with `theo up %s -w %s --auto-ports`", p.Name, statusSelector(ws)))
	}
}

// summarize turns the containers of an instance into its state, the count
// of its services by state, and the list of services. A service is expected
// when it has no profile; services behind a profile appear only when they
// have containers.
func summarize(cfg *compose.Project, containers []compose.Container) (string, ServiceCounts, []ServiceStatus) {
	byService := map[string][]compose.Container{}
	for _, c := range containers {
		byService[c.Service] = append(byService[c.Service], c)
	}
	names := map[string]bool{}
	for name, s := range cfg.Services {
		if len(s.Profiles) == 0 {
			names[name] = true
		}
	}
	for name := range byService {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	var counts ServiceCounts
	services := make([]ServiceStatus, 0, len(sorted))
	for _, name := range sorted {
		cs := byService[name]
		ss := ServiceStatus{Name: name, Containers: len(cs), Profiles: cfg.Services[name].Profiles}
		completed := 0
		for _, c := range cs {
			switch {
			case c.State == "running":
				ss.Running++
				if c.Health != "" && c.Health != "healthy" {
					ss.Health = c.Health
				} else if ss.Health == "" {
					ss.Health = c.Health
				}
			case c.State == "exited" && c.ExitCode == 0:
				completed++
			}
		}
		switch {
		case len(cs) == 0:
			ss.State = StateNotCreated
			counts.NotCreated++
		case ss.Running == len(cs):
			ss.State = StateRunning
			counts.Running++
		case ss.Running > 0:
			ss.State = StatePartial
			counts.Stopped++
		case completed == len(cs):
			ss.State = "completed"
			counts.Completed++
		default:
			ss.State = StateStopped
			counts.Stopped++
		}
		services = append(services, ss)
		counts.Total++
	}

	state := StatePartial
	switch {
	case len(containers) == 0:
		state = StateNotCreated
	case counts.Running == 0 && !hasPartial(services):
		state = StateStopped
	case counts.Stopped == 0 && counts.NotCreated == 0 && !hasPartial(services):
		state = StateRunning
	}
	return state, counts, services
}

func hasPartial(services []ServiceStatus) bool {
	for _, s := range services {
		if s.State == StatePartial {
			return true
		}
	}
	return false
}

// declaredProfiles returns the Compose profiles the project declares.
func declaredProfiles(cfg *compose.Project) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range cfg.Services {
		for _, p := range s.Profiles {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

func findingWarning(f Finding) StatusWarning {
	kind := f.Kind
	if f.Kind == FindForeignStack {
		kind = "foreign-stack"
	}
	msg := f.Detail
	if f.Subject != "" && f.Worktree == "" {
		msg = f.Subject + ": " + msg
	}
	return StatusWarning{Kind: kind, Message: msg, Fix: f.Fix}
}

func hasKind(ws []StatusWarning, kind string) bool {
	for _, w := range ws {
		if w.Kind == kind {
			return true
		}
	}
	return false
}

// statusSelector is what to pass to -w for a worktree.
func statusSelector(ws *WorktreeStatus) string {
	if ws.Branch != "" {
		return ws.Branch
	}
	return ws.Path
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
