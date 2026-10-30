package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/instances"
	"github.com/ElMaxter99/Theo/internal/registry"
)

// Shared services (FR-090 to FR-093), following the recommendation of Q-023:
// a project declares which services it shares, one dedicated Compose
// project (<project>-shared) runs them from the Compose files of one
// worktree, and instances that choose to use them stop running their own
// copy and join a Docker network that Theo manages. Nothing is shared unless
// the user says so.

const (
	sharedNetworkKey = "theo_shared" // key of the shared network in the generated Compose files
	sharedProfile    = "theo-shared" // profile that is never enabled: the service does not start
)

func sharedProjectName(project string) string { return slug(project) + "-shared" }
func sharedNetworkName(project string) string { return "theo-" + slug(project) + "-shared" }

// SharedSet returns the shared services of a project.
func (e *Engine) SharedSet(project string) (instances.SharedSet, bool, error) {
	p, err := e.Project(project)
	if err != nil {
		return instances.SharedSet{}, false, err
	}
	all, err := e.settings.LoadShared()
	if err != nil {
		return instances.SharedSet{}, false, err
	}
	set, ok := all[strings.ToLower(p.Name)]
	return set, ok, nil
}

func (e *Engine) saveSharedSet(project string, set *instances.SharedSet) error {
	unlock, err := e.locked()
	if err != nil {
		return err
	}
	defer unlock()
	all, err := e.settings.LoadShared()
	if err != nil {
		return err
	}
	if set == nil {
		delete(all, strings.ToLower(project))
	} else {
		all[strings.ToLower(project)] = *set
	}
	return e.settings.SaveShared(all)
}

// SharedAdd declares services of a project as shared. The first time it
// also chooses the worktree whose Compose files define them (from; empty
// means the main worktree). Declaring does not start anything.
func (e *Engine) SharedAdd(ctx context.Context, project string, services []string, from string, publish bool) (instances.SharedSet, error) {
	p, err := e.Project(project)
	if err != nil {
		return instances.SharedSet{}, err
	}
	if len(services) == 0 {
		return instances.SharedSet{}, errors.New("say which services to share")
	}
	set, exists, err := e.SharedSet(p.Name)
	if err != nil {
		return set, err
	}
	selector := from
	if exists && from == "" {
		selector = set.From
	}
	inst, _, err := e.instance(ctx, p.Name, selector, nil)
	if err != nil {
		return set, err
	}
	if exists && !samePath(set.From, inst.Workspace) {
		return set, fmt.Errorf("the shared services of %q are defined from %s; remove them (`theo shared remove`) before choosing another worktree", p.Name, set.From)
	}
	for _, s := range services {
		if _, ok := inst.config.Services[s]; !ok {
			return set, fmt.Errorf("%s has no service %q (services: %s)", inst.Workspace, s, strings.Join(compose.SortedKeys(inst.config.Services), ", "))
		}
	}
	if !exists {
		set = instances.SharedSet{From: inst.Workspace, Network: sharedNetworkName(p.Name)}
	}
	set.Services = mergeNames(set.Services, services)
	if publish {
		set.Publish = true
	}
	return set, e.saveSharedSet(p.Name, &set)
}

func mergeNames(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range append(append([]string(nil), a...), b...) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// sharedInstance is the instance that runs the shared services: the
// worktree they are defined in, under the project's shared Compose project
// name. It has no settings of its own.
func (e *Engine) sharedInstance(ctx context.Context, project string) (*Instance, instances.SharedSet, error) {
	set, ok, err := e.SharedSet(project)
	if err != nil {
		return nil, set, err
	}
	if !ok {
		return nil, set, fmt.Errorf("project %q shares no services; declare them with `theo shared add %s <service>...`", project, project)
	}
	inst, _, err := e.instance(ctx, project, set.From, nil)
	if err != nil {
		return nil, set, fmt.Errorf("the worktree that defines the shared services (%s): %w", set.From, err)
	}
	inst.Compose = sharedProjectName(inst.Project)
	inst.Main = false
	return inst, set, nil
}

// SharedUp creates the shared network if needed and starts the shared
// services (and what they depend on) in their own Compose project. Host
// ports are not published unless the set was declared with publish.
func (e *Engine) SharedUp(ctx context.Context, project string, stdout, stderr io.Writer) error {
	inst, set, err := e.sharedInstance(ctx, project)
	if err != nil {
		return err
	}
	if _, err := e.compose.EnsureNetwork(ctx, set.Network); err != nil {
		return err
	}
	overlay := compose.Overlay{ExternalNetworks: map[string]string{sharedNetworkKey: set.Network}}
	var profiles []string
	for _, name := range set.Services {
		svc, ok := inst.config.Services[name]
		if !ok {
			return fmt.Errorf("%s no longer defines the shared service %q", set.From, name)
		}
		overlay.Networks = ensureMap(overlay.Networks)
		overlay.Networks[name] = networksFor(svc)
		if len(svc.Ports) > 0 && !set.Publish {
			overlay.Ports = ensurePorts(overlay.Ports)
			overlay.Ports[name] = []compose.Port{}
		}
		if svc.ContainerName != "" {
			// A fixed name would collide with the same service of a
			// worktree that still runs its own copy.
			overlay.ResetContainerName = append(overlay.ResetContainerName, name)
		}
		profiles = mergeNames(profiles, svc.Profiles)
	}
	if _, err := e.writeOverlay(ctx, inst, overlay, nil, instances.Config{}); err != nil {
		return err
	}
	err = e.compose.Up(ctx, inst.target(), compose.UpOptions{Services: set.Services, Profiles: profiles}, stdout, stderr)
	e.record(inst, "shared up", err)
	return err
}

func ensureMap(m map[string][]string) map[string][]string {
	if m == nil {
		return map[string][]string{}
	}
	return m
}

func ensurePorts(m map[string][]compose.Port) map[string][]compose.Port {
	if m == nil {
		return map[string][]compose.Port{}
	}
	return m
}

// networksFor lists the networks a service must name to join the shared one
// without leaving the ones it has: a service with the default network keeps
// it explicitly.
func networksFor(svc compose.Service) []string {
	if _, ok := svc.Networks["default"]; ok {
		return []string{"default", sharedNetworkKey}
	}
	return []string{sharedNetworkKey}
}

// SharedConsumer is an instance that uses shared services.
type SharedConsumer struct {
	Workspace string   `json:"workspace"`
	Branch    string   `json:"branch,omitempty"`
	Compose   string   `json:"compose_project,omitempty"` // empty if Theo never started it
	Services  []string `json:"services"`
	Active    bool     `json:"active"` // it has running containers
}

// SharedConsumers lists the instances of a project linked to its shared
// services.
func (e *Engine) SharedConsumers(ctx context.Context, project string) ([]SharedConsumer, error) {
	p, err := e.Project(project)
	if err != nil {
		return nil, err
	}
	settings, err := e.settings.Load()
	if err != nil {
		return nil, err
	}
	events, err := e.settings.Events()
	if err != nil {
		return nil, err
	}
	composeOf := map[string]string{}
	for _, ev := range events {
		if ev.Compose != "" && !strings.HasPrefix(ev.Operation, "shared ") {
			composeOf[ev.Workspace] = ev.Compose
		}
	}
	branchOf := map[string]string{}
	if list, err := e.git.Worktrees(ctx, p.Path); err == nil {
		for _, wt := range list {
			branchOf[wt.Path] = wt.Branch
		}
	}
	running := map[string]bool{}
	if stacks, err := e.compose.Stacks(ctx); err == nil {
		for _, s := range stacks {
			if strings.Contains(s.Status, "running") {
				running[s.Name] = true
			}
		}
	}
	var out []SharedConsumer
	for ws, cfg := range settings {
		if !strings.EqualFold(cfg.Project, p.Name) || len(cfg.Shared) == 0 {
			continue
		}
		name := composeOf[ws]
		out = append(out, SharedConsumer{Workspace: ws, Branch: branchOf[ws], Compose: name, Services: cfg.Shared, Active: name != "" && running[name]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Workspace < out[j].Workspace })
	return out, nil
}

// SharedInUseError is returned when shared services cannot be stopped,
// removed or purged because instances still use them. Nothing was changed.
type SharedInUseError struct {
	Project   string
	Action    string
	Consumers []SharedConsumer
	Confirm   string // what --confirm must say, when a confirmation can override
}

// Code is the stable code of the error, for interfaces that translate it.
func (s *SharedInUseError) Code() string {
	if s.Confirm != "" {
		return "confirmation_required"
	}
	return "shared_in_use"
}

func (s *SharedInUseError) Error() string {
	var lines []string
	for _, c := range s.Consumers {
		state := "linked"
		if c.Active {
			state = "running"
		}
		who := c.Workspace
		if c.Branch != "" {
			who = c.Branch + " · " + c.Workspace
		}
		lines = append(lines, fmt.Sprintf("  %s (%s): %s", who, state, strings.Join(c.Services, ", ")))
	}
	msg := fmt.Sprintf("the shared services of %q are used by %d instance(s); nothing was changed:\n%s", s.Project, len(s.Consumers), strings.Join(lines, "\n"))
	if s.Confirm != "" {
		msg += fmt.Sprintf("\n\nTo %s anyway, pass --confirm %s. Those instances will lose the services.", s.Action, s.Confirm)
	} else {
		msg += "\n\nStop using them first with `theo shared unuse`."
	}
	return msg
}

// SharedDown stops and removes the containers and networks of the shared
// services; their volumes are kept. While running instances use them it
// refuses, unless confirm equals the shared Compose project name.
func (e *Engine) SharedDown(ctx context.Context, project, confirm string, stdout, stderr io.Writer) error {
	inst, _, err := e.sharedInstance(ctx, project)
	if err != nil {
		return err
	}
	consumers, err := e.SharedConsumers(ctx, project)
	if err != nil {
		return err
	}
	var active []SharedConsumer
	for _, c := range consumers {
		if c.Active {
			active = append(active, c)
		}
	}
	if len(active) > 0 && confirm != inst.Compose {
		return &SharedInUseError{Project: inst.Project, Action: "stop them", Consumers: active, Confirm: inst.Compose}
	}
	err = e.compose.Down(ctx, inst.target(), stdout, stderr)
	e.record(inst, "shared down", err)
	return err
}

// SharedRemove stops declaring services as shared (all of them when none is
// given). It refuses while instances are linked to them or the shared
// containers exist, and never touches volumes. When the last service goes,
// the network Theo created goes too.
func (e *Engine) SharedRemove(ctx context.Context, project string, services []string) error {
	inst, set, err := e.sharedInstance(ctx, project)
	if err != nil {
		return err
	}
	removing := set.Services
	if len(services) > 0 {
		for _, s := range services {
			if !contains(set.Services, s) {
				return fmt.Errorf("%q is not shared in project %q", s, project)
			}
		}
		removing = services
	}
	consumers, err := e.SharedConsumers(ctx, project)
	if err != nil {
		return err
	}
	var linked []SharedConsumer
	for _, c := range consumers {
		for _, s := range c.Services {
			if contains(removing, s) {
				linked = append(linked, c)
				break
			}
		}
	}
	if len(linked) > 0 {
		return &SharedInUseError{Project: inst.Project, Action: "remove them", Consumers: linked}
	}
	if cs, err := e.compose.PS(ctx, inst.target()); err == nil && len(cs) > 0 {
		return fmt.Errorf("the shared services still have %d container(s); stop them first with `theo shared down %s`", len(cs), project)
	}

	var rest []string
	for _, s := range set.Services {
		if !contains(removing, s) {
			rest = append(rest, s)
		}
	}
	if len(rest) > 0 {
		set.Services = rest
		return e.saveSharedSet(inst.Project, &set)
	}
	if err := e.saveSharedSet(inst.Project, nil); err != nil {
		return err
	}
	_ = e.compose.RemoveNetwork(ctx, set.Network) // fails while something is attached: then it stays
	return nil
}

// SharedPurge deletes the data (named volumes) of the shared services. Like
// PurgeVolumes it needs the shared containers gone and --confirm with the
// shared Compose project name; it also lists the instances linked to them.
func (e *Engine) SharedPurge(ctx context.Context, project, confirm string) ([]VolumeInfo, error) {
	inst, _, err := e.sharedInstance(ctx, project)
	if err != nil {
		return nil, err
	}
	return e.PurgeVolumes(ctx, inst, confirm)
}

// SharedUse makes an instance use shared services instead of running its own
// copy. join names the instance's services that join the shared network
// besides the ones that depended on a shared service. It takes effect at the
// next `up`.
func (e *Engine) SharedUse(inst *Instance, services, join []string) error {
	set, ok, err := e.SharedSet(inst.Project)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("project %q shares no services; declare them with `theo shared add`", inst.Project)
	}
	if len(services) == 0 {
		return errors.New("say which shared services to use")
	}
	for _, s := range services {
		if !contains(set.Services, s) {
			return fmt.Errorf("%q is not shared in project %q (shared: %s)", s, inst.Project, strings.Join(set.Services, ", "))
		}
		if _, ok := inst.config.Services[s]; !ok {
			return fmt.Errorf("%s has no service %q to replace", inst.Workspace, s)
		}
	}
	for _, j := range join {
		if _, ok := inst.config.Services[j]; !ok {
			return fmt.Errorf("%s has no service %q to connect", inst.Workspace, j)
		}
	}
	return e.updateSettings(inst, func(c *instances.Config) error {
		c.Shared = mergeNames(c.Shared, services)
		c.Join = mergeNames(c.Join, join)
		return nil
	})
}

// SharedUnuse makes an instance run its own copy of the given services again
// (all of them when none is given).
func (e *Engine) SharedUnuse(inst *Instance, services []string) error {
	return e.updateSettings(inst, func(c *instances.Config) error {
		if len(services) == 0 {
			c.Shared, c.Join = nil, nil
			return nil
		}
		var rest []string
		for _, s := range c.Shared {
			if !contains(services, s) {
				rest = append(rest, s)
			}
		}
		c.Shared = rest
		if len(rest) == 0 {
			c.Join = nil
		}
		return nil
	})
}

// addSharedConsumer adds to the overlay what an instance needs to use the
// shared services: its own copy does not start (a profile that is never
// enabled), the services that depended on it lose only that dependency, and
// they join the shared network.
func (e *Engine) addSharedConsumer(inst *Instance, settings instances.Config, overlay *compose.Overlay) error {
	if len(settings.Shared) == 0 {
		return nil
	}
	set, ok, err := e.SharedSet(inst.Project)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("this instance uses shared services, but project %q shares none now; run `theo shared unuse %s -w %s` or declare them again", inst.Project, inst.Project, worktreeSelector(inst))
	}
	shared := map[string]bool{}
	for _, s := range settings.Shared {
		if !contains(set.Services, s) {
			return fmt.Errorf("%q is no longer shared in project %q; run `theo shared unuse %s %s -w %s`", s, inst.Project, inst.Project, s, worktreeSelector(inst))
		}
		if _, ok := inst.config.Services[s]; !ok {
			return fmt.Errorf("this worktree has no service %q to replace with the shared one", s)
		}
		shared[s] = true
	}

	overlay.ExternalNetworks = map[string]string{sharedNetworkKey: set.Network}
	overlay.Profiles = map[string][]string{}
	overlay.DependsOn = map[string]map[string]json.RawMessage{}
	overlay.Networks = ensureMap(overlay.Networks)
	join := map[string]bool{}
	for _, j := range settings.Join {
		join[j] = true
	}
	for name, svc := range inst.config.Services {
		if shared[name] {
			overlay.Profiles[name] = []string{sharedProfile}
			continue
		}
		rest := map[string]json.RawMessage{}
		dependent := false
		for dep, raw := range svc.DependsOn {
			if shared[dep] {
				dependent = true
			} else {
				rest[dep] = raw
			}
		}
		if dependent {
			overlay.DependsOn[name] = rest
			join[name] = true
		}
	}
	for name := range join {
		svc, ok := inst.config.Services[name]
		if !ok || shared[name] {
			continue
		}
		overlay.Networks[name] = networksFor(svc)
	}
	return nil
}

func worktreeSelector(inst *Instance) string {
	if inst.Branch != "" {
		return inst.Branch
	}
	return inst.Workspace
}

// EnsureShared makes sure the shared services an instance uses are running
// before it starts. When they are not it refuses, unless start is set, in
// which case it starts them. It returns warnings, such as the shared
// service running another image than the instance's own definition.
func (e *Engine) EnsureShared(ctx context.Context, inst *Instance, start bool, stdout, stderr io.Writer) ([]string, error) {
	settings, err := e.Settings(inst)
	if err != nil || len(settings.Shared) == 0 {
		return nil, err
	}
	provider, set, err := e.sharedInstance(ctx, inst.Project)
	if err != nil {
		return nil, err
	}
	containers, err := e.compose.PS(ctx, provider.target())
	if err != nil {
		return nil, err
	}
	up := map[string]bool{}
	for _, c := range containers {
		if c.State == "running" {
			up[c.Service] = true
		}
	}
	var down []string
	for _, s := range settings.Shared {
		if !up[s] {
			down = append(down, s)
		}
	}
	if len(down) > 0 {
		if !start {
			return nil, fmt.Errorf("the shared service(s) %s are not running. Start them with `theo shared up %s`, or pass --start-shared", strings.Join(down, ", "), inst.Project)
		}
		if err := e.SharedUp(ctx, inst.Project, stdout, stderr); err != nil {
			return nil, err
		}
	}
	if _, err := e.compose.EnsureNetwork(ctx, set.Network); err != nil {
		return nil, err
	}

	var warnings []string
	for _, s := range settings.Shared {
		mine, theirs := inst.config.Services[s].Image, provider.config.Services[s].Image
		if mine != "" && theirs != "" && mine != theirs {
			warnings = append(warnings, fmt.Sprintf("this worktree defines %s with image %s, but the shared one runs %s", s, mine, theirs))
		}
	}
	return warnings, nil
}

// SharedService is a shared service and its state.
type SharedService struct {
	Name  string `json:"name"`
	Image string `json:"image,omitempty"`
	State string `json:"state"` // running, exited, ... or "not created"
}

// SharedStatus describes the shared services of a project.
type SharedStatus struct {
	Project   string           `json:"project"`
	Compose   string           `json:"compose_project"`
	From      string           `json:"from"`
	Network   string           `json:"network"`
	Publish   bool             `json:"publish_ports"`
	Services  []SharedService  `json:"services"`
	Consumers []SharedConsumer `json:"consumers"`
	Warnings  []string         `json:"warnings,omitempty"`
}

// SharedStatus returns the state of a project's shared services and who
// uses them.
func (e *Engine) SharedStatus(ctx context.Context, project string) (*SharedStatus, error) {
	inst, set, err := e.sharedInstance(ctx, project)
	if err != nil {
		return nil, err
	}
	st := &SharedStatus{Project: inst.Project, Compose: inst.Compose, From: set.From, Network: set.Network, Publish: set.Publish}
	state := map[string]string{}
	if cs, err := e.compose.PS(ctx, inst.target()); err == nil {
		for _, c := range cs {
			state[c.Service] = c.State
		}
	} else {
		st.Warnings = append(st.Warnings, "Docker could not be asked for the state: "+firstLineOf(err.Error()))
	}
	for _, name := range set.Services {
		s := SharedService{Name: name, Image: inst.config.Services[name].Image, State: "not created"}
		if v, ok := state[name]; ok {
			s.State = v
		}
		st.Services = append(st.Services, s)
	}
	if st.Consumers, err = e.SharedConsumers(ctx, project); err != nil {
		return nil, err
	}
	return st, nil
}

func firstLineOf(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// sharedFindings reports shared services whose defining worktree is gone and
// instances linked to services that are no longer shared (FR-101).
func (e *Engine) sharedFindings(projects []registry.Project, settings instances.State, add func(Finding)) {
	all, err := e.settings.LoadShared()
	if err != nil {
		return
	}
	for _, p := range projects {
		set, ok := all[strings.ToLower(p.Name)]
		if !ok {
			continue
		}
		if !dirExists(set.From) {
			add(Finding{Kind: FindSharedBroken, Project: p.Name, Subject: set.From,
				Detail: fmt.Sprintf("the worktree that defines the shared services (%s) no longer exists, so `theo shared up` cannot run", strings.Join(set.Services, ", ")),
				Fix:    fmt.Sprintf("recreate the worktree, or `theo shared remove %s` (volumes are kept)", p.Name)})
		}
		for ws, cfg := range settings {
			if !strings.EqualFold(cfg.Project, p.Name) {
				continue
			}
			for _, s := range cfg.Shared {
				if !contains(set.Services, s) {
					add(Finding{Kind: FindSharedBroken, Project: p.Name, Subject: ws,
						Detail: fmt.Sprintf("this instance uses %q, which the project no longer shares", s),
						Fix:    fmt.Sprintf("`theo shared unuse %s -w %s %s`, or share it again with `theo shared add`", p.Name, ws, s)})
				}
			}
		}
	}
	for _, p := range projects {
		if _, ok := all[strings.ToLower(p.Name)]; ok {
			continue
		}
		for ws, cfg := range settings {
			if strings.EqualFold(cfg.Project, p.Name) && len(cfg.Shared) > 0 {
				add(Finding{Kind: FindSharedBroken, Project: p.Name, Subject: ws,
					Detail: fmt.Sprintf("this instance uses shared services (%s), but the project shares none now", strings.Join(cfg.Shared, ", ")),
					Fix:    fmt.Sprintf("`theo shared unuse %s -w %s`", p.Name, ws)})
			}
		}
	}
}
