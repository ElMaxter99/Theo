package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/execx"
	"github.com/ElMaxter99/Theo/internal/instances"
	"github.com/ElMaxter99/Theo/internal/proxy"
	"github.com/ElMaxter99/Theo/internal/registry"
)

// Parameters shared by several tools.
var (
	pProject  = param{name: "project", kind: "string", description: "Name of a registered project (see list_projects).", required: true}
	pWorktree = param{name: "worktree", kind: "string", description: "Worktree: its branch, directory name or path (see list_worktrees). Empty means the project's main worktree."}
	pServices = param{name: "services", kind: "array", description: "Compose services to act on; empty means all of them."}
	pProfiles = param{name: "profiles", kind: "array", description: "Compose profiles to enable, in addition to the ones the instance always starts with."}
	pAutoPort = param{name: "auto_ports", kind: "boolean", description: "Use other host ports for the ones that are taken, and keep them for the next starts."}
)

// allTools returns every tool, in the order tools/list shows them.
func allTools() []*tool {
	return []*tool{
		{
			name: "list_projects", title: "List projects", readOnly: true,
			description: "List the projects registered in Theo: name, path of the main worktree and settings.",
			run:         runListProjects,
		},
		{
			name: "project_show", title: "Show a project", readOnly: true,
			description: "All the settings of a project: env file, Compose files, where new worktrees go, files copied on add.",
			params:      []param{pProject},
			run:         runProjectShow,
		},
		{
			name: "list_worktrees", title: "List worktrees", readOnly: true,
			description: "List the workspaces of a project (main worktree, linked worktrees and clones) with branch, kind, owner, changes and lifecycle state (in-use, unpushed, pushed, new, merged, inactive, kept, missing) with why.",
			params:      []param{pProject, {name: "owner", kind: "string", description: "Only the workspaces with this owner."}},
			run:         runListWorktrees,
		},
		{
			name: "list_branches", title: "List branches", readOnly: true,
			description: "List the local and remote branches of a project and which of them already have a worktree.",
			params:      []param{pProject},
			run:         runListBranches,
		},
		{
			name: "add_worktree", title: "Add a worktree",
			description: "Create a workspace for a branch: a linked Git worktree, or an independent clone with clone=true (for projects with submodules). " +
				"Never forces: a branch checked out elsewhere is refused. Nothing in the main worktree changes. Start it afterwards with up.",
			params: []param{pProject,
				{name: "branch", kind: "string", description: "Branch to check out (or to create with new=true).", required: true},
				{name: "new", kind: "boolean", description: "Create the branch."},
				{name: "from", kind: "string", description: "With new=true: branch or commit to start from (default: the current HEAD)."},
				{name: "path", kind: "string", description: "Directory for the workspace (default: the project's worktree directory)."},
				{name: "owner", kind: "string", description: "Owner label, such as an agent name. Default: the server's --owner, if any."},
				{name: "clone", kind: "boolean", description: "Create an independent clone instead of a linked worktree."},
				{name: "copy", kind: "boolean", description: "Copy the files the project lists for this (project set --copy-on-add) from the main worktree; existing files are never overwritten."},
				{name: "copy_files", kind: "array", description: "One-off root-relative files to copy from the main worktree."},
			},
			run: runAddWorktree,
		},
		{
			name: "remove_worktree", title: "Remove a worktree", destructive: true,
			description: "Remove a linked worktree or clone. The branch and the volumes are always kept. Refused while its instance has containers (call down first) " +
				"and while it has uncommitted or untracked files. Those files are lost with discard_changes=true plus confirm set to the worktree's directory name: " +
				"only do that when the user explicitly agreed.",
			params: []param{pProject,
				{name: "worktree", kind: "string", description: "Worktree to remove: branch, directory name or path.", required: true},
				{name: "discard_changes", kind: "boolean", description: "Remove it even with uncommitted or untracked files, which are lost. Needs confirm."},
				{name: "confirm", kind: "string", description: "With discard_changes: the worktree's directory name (the refusal says which)."},
			},
			run: runRemoveWorktree,
		},
		{
			name: "services", title: "Compose services", readOnly: true,
			description: "The services Docker Compose resolves for a worktree: image or build, ports, mounts, dependencies, networks and profiles, plus the env file and Compose files used.",
			params:      []param{pProject, pWorktree},
			run:         runServices,
		},
		{
			name: "ports", title: "Host ports", readOnly: true,
			description: "The host ports a worktree's instance publishes, whether they are free, and their URLs. With auto_ports, what up with auto_ports would assign (nothing is reserved).",
			params:      []param{pProject, pWorktree, pProfiles, {name: "auto_ports", kind: "boolean", description: "Show the ports up with auto_ports would use."}},
			run:         runPorts,
		},
		{
			name: "up", title: "Start an instance", idempotent: true,
			description: "Create and start the containers of a worktree's instance (its own Compose project, so worktrees never collide). " +
				"A host port that is taken only warns unless auto_ports is set. Returns the ports with their URLs and the end of Docker's output.",
			params: []param{pProject, pWorktree,
				{name: "build", kind: "boolean", description: "Build images before starting."},
				pAutoPort, pProfiles,
				{name: "services", kind: "array", description: "Start only these services and what they depend on."},
				{name: "force_recreate", kind: "boolean", description: "Recreate the containers even if nothing changed; volumes are kept."},
				{name: "start_shared", kind: "boolean", description: "Start the project's shared services this instance uses if they are stopped."},
			},
			run: runUp,
		},
		{
			name: "down", title: "Stop and remove an instance", destructive: true, idempotent: true,
			description: "Stop and remove the containers and networks of a worktree's instance. Volumes and their data are always kept.",
			params:      []param{pProject, pWorktree},
			run:         runDown,
		},
		{
			name: "restart", title: "Restart services",
			description: "Restart the containers of a worktree's instance, or only some services.",
			params:      []param{pProject, pWorktree, pServices},
			run:         runRestart,
		},
		{
			name: "stop", title: "Stop services", idempotent: true,
			description: "Stop services of a worktree's instance without removing anything; start starts them again.",
			params:      []param{pProject, pWorktree, pServices},
			run:         runStop,
		},
		{
			name: "start", title: "Start stopped services", idempotent: true,
			description: "Start containers of a worktree's instance that exist and are stopped (up creates them).",
			params:      []param{pProject, pWorktree, pServices},
			run:         runStart,
		},
		{
			name: "ps", title: "Containers", readOnly: true,
			description: "Without worktree: which worktrees of the project have containers. With worktree: the state of its instance and its containers.",
			params:      []param{pProject, {name: "worktree", kind: "string", description: "Worktree: branch, directory name or path. Empty lists every worktree."}},
			run:         runPS,
		},
		{
			name: "logs", title: "Logs", readOnly: true,
			description: fmt.Sprintf("The last lines of the logs of a worktree's instance (never follows). At most %d lines per container and %d KiB in all; the most recent are kept. Values of secret variables are masked.", maxLogTail, logsLimit>>10),
			params: []param{pProject, pWorktree, pServices,
				{name: "tail", kind: "integer", description: fmt.Sprintf("Lines per container (default %d, at most %d).", defaultLogTail, maxLogTail)},
			},
			run: runLogs,
		},
		{
			name: "status", title: "Status dashboard", readOnly: true,
			description: "Every project (or one) and its worktrees: branch, kind, uncommitted changes, instance state and services, ports with URLs, settings, and warnings.",
			params:      []param{{name: "project", kind: "string", description: "Only this project. Empty: all of them."}},
			run:         runStatus,
		},
		waitForTool(),
		{
			name: "proxy_routes", title: "Local domains", readOnly: true,
			description: "The names `theo proxy` serves each instance on (http://<service>.<instance>.<project>.localhost:7080, and <instance>.<project> for its web, app, api or frontend service), " +
				"with the host port behind each one and whether it runs. The names never change while the worktree exists, unlike host ports. proxy_running says whether a proxy is listening now; the user starts it with `theo proxy` or from the web interface.",
			params: []param{{name: "project", kind: "string", description: "Only this project. Empty: all of them."}},
			run:    runProxyRoutes,
		},
		{
			name: "env_list", title: "List instance variables", readOnly: true,
			description: "The variables set with Theo for a worktree's instance. Secret values are always masked here.",
			params:      []param{pProject, pWorktree},
			run:         runEnvList,
		},
		{
			name: "env_set", title: "Set an instance variable", idempotent: true,
			description: "Set a variable for this instance only; it applies at the next up. The project's files and .env are not changed. Use secret=true for passwords and tokens: they are masked everywhere.",
			params: []param{pProject, pWorktree,
				{name: "name", kind: "string", description: "Variable name.", required: true},
				{name: "value", kind: "string", description: "Value.", required: true},
				{name: "secret", kind: "boolean", description: "Mask the value wherever it is shown."},
				{name: "services", kind: "array", description: "Apply only to these services (default: every service)."},
			},
			run: runEnvSet,
		},
		{
			name: "env_unset", title: "Remove an instance variable", destructive: true, idempotent: true,
			description: "Remove a variable set with Theo for this instance; it applies at the next up.",
			params:      []param{pProject, pWorktree, {name: "name", kind: "string", description: "Variable name.", required: true}},
			run:         runEnvUnset,
		},
		{
			name: "instance_show", title: "Instance settings", readOnly: true,
			description: "The settings Theo keeps for a worktree's instance: Compose project name, env file, variable names, profiles, Compose files, fixed container names.",
			params:      []param{pProject, pWorktree},
			run:         runInstanceShow,
		},
		{
			name: "instance_config", title: "Resolved configuration", readOnly: true,
			description: fmt.Sprintf("The Compose configuration the instance would run with (docker compose config), with secret values masked. Cut at %d KiB.", configLimit>>10),
			params:      []param{pProject, pWorktree, pProfiles, {name: "auto_ports", kind: "boolean", description: "Include the ports auto_ports would assign (nothing is reserved)."}},
			run:         runInstanceConfig,
		},
		{
			name: "history", title: "Operation history", readOnly: true,
			description: "Operations Theo ran on the project's instances (up, down, restart...), newest first.",
			params: []param{pProject, {name: "worktree", kind: "string", description: "Only this worktree."},
				{name: "limit", kind: "integer", description: fmt.Sprintf("Number of entries (default %d, at most %d).", defaultHistory, maxHistory)}},
			run: runHistory,
		},
		{
			name: "reconcile", title: "Reconcile records", destructive: true, idempotent: true,
			description: "Compare what Theo recorded with Git and Docker and report differences: removed worktrees or containers, containers or volumes left behind. " +
				"Changes nothing unless apply=true, which only forgets Theo's own stale records (never containers, volumes or files).",
			params: []param{{name: "project", kind: "string", description: "Only this project. Empty: all of them."},
				{name: "apply", kind: "boolean", description: "Forget Theo's own stale records."}},
			readOnlyVariant: func() *tool {
				return &tool{
					name: "reconcile", title: "Reconcile records", readOnly: true,
					description: "Compare what Theo recorded with Git and Docker and report differences. Changes nothing.",
					params:      []param{{name: "project", kind: "string", description: "Only this project. Empty: all of them."}},
					run:         runReconcile,
				}
			},
			run: runReconcile,
		},
		{
			name: "volumes_list", title: "Volumes", readOnly: true,
			description: "The named volumes of a worktree's instance: in use, or kept with their data after down; external ones are listed and never touched. " +
				"Deleting, restoring or cloning volume data is not available here: it is left to the user, in the CLI or the web interface.",
			params: []param{pProject, pWorktree},
			run:    runVolumes,
		},
		{
			name: "explain", title: "Explain an operation", readOnly: true,
			description: "Dry run: the Git and Docker commands an operation would run, in order, with their directory. Commands that only read run; the ones that change anything are listed and not run, and Theo records nothing.",
			params: []param{
				{name: "operation", kind: "string", description: "Operation to explain.", required: true, enum: []string{"up", "down", "restart", "stop", "start"}},
				pProject, pWorktree,
				{name: "build", kind: "boolean", description: "up: build images before starting."},
				pAutoPort, pProfiles, pServices,
				{name: "start_shared", kind: "boolean", description: "up: start the shared services the instance uses."},
			},
			run: runExplain,
		},
		{
			name: "finish_worktree", title: "Finish your worktree", destructive: true,
			description: "Close the worktree you own once your work is done: its branch must be merged, or pushed with nothing left to push, and it must have no uncommitted files and no running instance (call down first). " +
				"The branch stays in the repository and on its remote; volumes are kept. Only the owner of the worktree can finish it.",
			params: []param{pProject,
				{name: "worktree", kind: "string", description: "Your worktree: branch, directory name or path.", required: true},
				{name: "owner", kind: "string", description: "Who you are. Default: the server's --owner."},
			},
			run: runFinishWorktree,
		},
		{
			name: "clean_worktrees", title: "Forgotten worktrees", readOnly: true,
			description: "List the worktrees of a project that are merged (also by squash or rebase) or whose folder is gone, which `theo worktree clean` can close, and the inactive ones. It changes nothing: closing them is the user's call.",
			params:      []param{pProject},
			run:         runCleanWorktrees,
		},
	}
}

// --- projects and worktrees ---

func runListProjects(_ context.Context, s *Server, _ args) (*result, error) {
	list, err := s.opts.Engine.Projects()
	if err != nil {
		return nil, err
	}
	names := make([]string, len(list))
	for i, p := range list {
		names[i] = p.Name
	}
	summary := "No projects registered. The user adds one with `theo project add <path>`."
	if len(list) > 0 {
		summary = fmt.Sprintf("%d project(s): %s", len(list), strings.Join(names, ", "))
	}
	return ok(summary, map[string]any{"projects": nonNil(list)}), nil
}

func runProjectShow(_ context.Context, s *Server, a args) (*result, error) {
	info, err := s.opts.Engine.ProjectInfo(a.str("project"))
	if err != nil {
		return nil, err
	}
	return ok(fmt.Sprintf("Project %s at %s.", info.Name, info.Path), info), nil
}

func runListWorktrees(ctx context.Context, s *Server, a args) (*result, error) {
	report, err := s.opts.Engine.WorktreeStates(ctx, a.str("project"), engine.StatesOptions{})
	if err != nil {
		return nil, err
	}
	list := report.Worktrees
	kept := []engine.WorktreeState{}
	owner := a.str("owner")
	for _, wt := range list {
		if owner == "" || wt.Owner == owner {
			kept = append(kept, wt)
		}
	}
	var lines []string
	for _, wt := range kept {
		ref := wt.Branch
		if ref == "" {
			ref = "(detached)"
		}
		state := "clean"
		if wt.Dirty {
			state = "uncommitted changes"
		}
		if wt.State != "" {
			state = wt.State + ": " + wt.Reason
		}
		line := fmt.Sprintf("- %s [%s, %s] %s", ref, wt.Kind, state, wt.Path)
		if wt.Owner != "" {
			line += " owner: " + wt.Owner
		}
		lines = append(lines, line)
	}
	summary := fmt.Sprintf("%d worktree(s) of %s", len(kept), a.str("project"))
	if owner != "" {
		summary += " owned by " + owner
	}
	if len(lines) > 0 {
		summary += ":\n" + strings.Join(lines, "\n")
	}
	return ok(summary, map[string]any{"worktrees": kept}), nil
}

func runListBranches(ctx context.Context, s *Server, a args) (*result, error) {
	list, err := s.opts.Engine.Branches(ctx, a.str("project"))
	if err != nil {
		return nil, err
	}
	free := 0
	for _, b := range list {
		if b.Worktree == "" {
			free++
		}
	}
	return ok(fmt.Sprintf("%d branch(es), %d without a worktree.", len(list), free), map[string]any{"branches": nonNil(list)}), nil
}

func runAddWorktree(ctx context.Context, s *Server, a args) (*result, error) {
	project := a.str("project")
	if a.str("from") != "" && !a.bool("new") {
		return nil, fmt.Errorf("%w: from only makes sense with new=true", errBadArgs)
	}
	owner := a.str("owner")
	if owner == "" {
		owner = s.opts.Owner
	}
	if owner != "" {
		var err error
		if owner, err = engine.ValidateOwner(owner); err != nil {
			return nil, fmt.Errorf("%w: %s", errBadArgs, err.Error())
		}
	}
	release, err := s.claim("project "+project, "worktree add")
	if err != nil {
		return nil, err
	}
	defer release()
	added, err := s.opts.Engine.AddWorktree(ctx, project, a.str("branch"), engine.AddWorktreeOptions{
		NewBranch: a.bool("new"), Base: a.str("from"), Path: a.str("path"),
		Clone: a.bool("clone"), Owner: owner, Copy: a.bool("copy"), CopyFiles: a.list("copy_files"),
	})
	if err != nil {
		return nil, err
	}
	what := "worktree"
	if added.Kind == engine.KindClone {
		what = "independent clone"
	}
	summary := fmt.Sprintf("Created %s for %s at %s.", what, added.Branch, added.Path)
	if added.Owner != "" {
		summary += " Owner: " + added.Owner + "."
	}
	for _, n := range added.Notes {
		summary += "\nNote: " + n
	}
	summary += fmt.Sprintf("\nStart it with up (project %q, worktree %q).", project, added.Branch)
	return ok(summary, added), nil
}

func runRemoveWorktree(ctx context.Context, s *Server, a args) (*result, error) {
	project, selector := a.str("project"), a.str("worktree")
	if a.str("confirm") != "" && !a.bool("discard_changes") {
		return nil, fmt.Errorf("%w: confirm only makes sense with discard_changes=true", errBadArgs)
	}
	release, err := s.claim("project "+project, "worktree remove")
	if err != nil {
		return nil, err
	}
	defer release()
	res, err := s.opts.Engine.RemoveWorkspace(ctx, project, selector, engine.RemoveWorktreeOptions{
		DiscardChanges: a.bool("discard_changes"), Confirm: a.str("confirm"),
	})
	var changes *engine.ChangesError
	if errors.As(err, &changes) {
		return nil, &changesError{changes}
	}
	if err != nil {
		return nil, err
	}
	wt := res.Worktree
	var summary string
	switch {
	case res.Forgot:
		summary = fmt.Sprintf("The clone %s was already gone; Theo forgot its record.", wt.Path)
	case res.Kind == engine.KindClone:
		summary = fmt.Sprintf("Removed clone %s with its Git history. Branches that were pushed are on the remote; any volumes were kept.", wt.Path)
	default:
		summary = fmt.Sprintf("Removed worktree %s. The branch %s and any volumes were kept.", wt.Path, wt.Branch)
	}
	return ok(summary, map[string]any{"path": wt.Path, "branch": wt.Branch, "kind": res.Kind, "forgot": res.Forgot}), nil
}

// changesError explains a refused removal the way the CLI does, in terms of
// the tool's arguments.
type changesError struct{ *engine.ChangesError }

func (c *changesError) Unwrap() error { return c.ChangesError }

func (c *changesError) Error() string {
	return fmt.Sprintf("%s\n\n  %s\n\nAsk the user to commit or move them. Only if the user agrees to lose them, call remove_worktree again with discard_changes=true and confirm=%q.",
		c.ChangesError.Error(), strings.Join(c.Changes, "\n  "), c.Name)
}

// --- instances: reading ---

// instance resolves a worktree's instance. Errors are masked with the
// instance's secrets when it could be resolved.
func (s *Server) instance(ctx context.Context, e *engine.Engine, a args) (*engine.Instance, error) {
	return e.InstanceFor(ctx, a.str("project"), a.str("worktree"), engine.InstanceOptions{})
}

// masker returns a function that masks the instance's secret values.
func masker(e *engine.Engine, inst *engine.Instance) func(string) string {
	cfg, err := e.Settings(inst)
	if err != nil {
		return func(s string) string { return s }
	}
	return func(s string) string { return engine.MaskSecrets(s, cfg) }
}

// maskedError is an error whose text had secrets masked.
type maskedError struct {
	msg string
	err error
}

func (m *maskedError) Error() string { return m.msg }
func (m *maskedError) Unwrap() error { return m.err }

func maskErr(mask func(string) string, err error) error {
	if err == nil {
		return nil
	}
	return &maskedError{mask(err.Error()), err}
}

func runServices(ctx context.Context, s *Server, a args) (*result, error) {
	res, err := s.opts.Engine.ServicesFor(ctx, a.str("project"), a.str("worktree"), engine.InstanceOptions{})
	if err != nil {
		return nil, err
	}
	names := make([]string, len(res.Services))
	for i, svc := range res.Services {
		names[i] = svc.Name
	}
	return ok(fmt.Sprintf("%d service(s) in %s: %s", len(names), res.Compose, strings.Join(names, ", ")), res), nil
}

// portView is a port mapping with its URL.
type portView struct {
	engine.PortMapping
	URL string `json:"url,omitempty"`
}

func portViews(plan *engine.PortPlan) []portView {
	out := []portView{}
	for _, m := range plan.Mappings {
		out = append(out, portView{m, m.URL()})
	}
	return out
}

func portLines(views []portView) string {
	var lines []string
	for _, v := range views {
		host := v.Running
		if host == 0 {
			host = v.Effective
		}
		line := fmt.Sprintf("- %s %d/%s -> host %s [%s]", v.Service, v.Target, v.Protocol, strconv.Itoa(host), v.Status)
		if v.URL != "" {
			line += " " + v.URL
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func runPorts(ctx context.Context, s *Server, a args) (*result, error) {
	e := s.opts.Engine
	inst, err := s.instance(ctx, e, a)
	if err != nil {
		return nil, err
	}
	plan, err := e.PlanPorts(ctx, inst, a.list("profiles"), a.bool("auto_ports"))
	if err != nil {
		return nil, err
	}
	views := portViews(plan)
	summary := fmt.Sprintf("%s publishes no port on the host.", inst.Compose)
	if len(views) > 0 {
		summary = fmt.Sprintf("Ports of %s:\n%s", inst.Compose, portLines(views))
		if n := len(plan.Conflicts()); n > 0 {
			summary += fmt.Sprintf("\n%d port(s) in use: up with auto_ports=true uses other ones.", n)
		}
	}
	links := s.proxyLinks(inst, views)
	summary += linksText(links)
	return ok(summary, struct {
		*engine.Instance
		Ports     []portView   `json:"ports"`
		ProxyURLs []proxy.Link `json:"proxy_urls,omitempty"`
		Overlay   string       `json:"overlay,omitempty"`
	}{inst, views, links, plan.Overlay}), nil
}

func runPS(ctx context.Context, s *Server, a args) (*result, error) {
	e := s.opts.Engine
	if a.str("worktree") == "" {
		overview, err := e.Overview(ctx, a.str("project"))
		if err != nil {
			return nil, err
		}
		var lines []string
		for _, o := range overview {
			for _, st := range o.Stacks {
				lines = append(lines, fmt.Sprintf("- %s: %s %s", o.Worktree.Path, st.Name, st.Status))
			}
		}
		summary := fmt.Sprintf("No worktree of %s has containers.", a.str("project"))
		if len(lines) > 0 {
			summary = "Compose projects with containers:\n" + strings.Join(lines, "\n")
		}
		return ok(summary, map[string]any{"worktrees": nonNil(overview)}), nil
	}
	inst, err := s.instance(ctx, e, a)
	if err != nil {
		return nil, err
	}
	containers, err := e.Containers(ctx, inst)
	if err != nil {
		return nil, err
	}
	if containers == nil {
		containers = []compose.Container{}
	}
	state := engine.InstanceState(containers, e.ExpectedServices(inst), true)
	var lines []string
	for _, c := range containers {
		lines = append(lines, fmt.Sprintf("- %s: %s %s", c.Service, c.State, c.Health))
	}
	summary := fmt.Sprintf("%s is %s.", inst.Compose, state)
	if len(lines) > 0 {
		summary += "\n" + strings.Join(lines, "\n")
	}
	return ok(summary, struct {
		*engine.Instance
		State      string              `json:"state"`
		Containers []compose.Container `json:"containers"`
	}{inst, state, containers}), nil
}

func runLogs(ctx context.Context, s *Server, a args) (*result, error) {
	e := s.opts.Engine
	tail := a.int("tail", defaultLogTail)
	if tail <= 0 || tail > maxLogTail {
		return nil, fmt.Errorf("%w: tail must be between 1 and %d", errBadArgs, maxLogTail)
	}
	inst, err := s.instance(ctx, e, a)
	if err != nil {
		return nil, err
	}
	if err := inst.CheckServices(a.list("services")); err != nil {
		return nil, err
	}
	mask := masker(e, inst)
	out, errs := newTailBuffer(logsLimit), newTailBuffer(outputLimit)
	err = e.Logs(ctx, inst, compose.LogOptions{Tail: tail, Services: a.list("services")}, out, errs)
	if err != nil {
		text, _ := errs.Text()
		return nil, maskErr(mask, joinOutput(err, text))
	}
	text, truncated := out.Text()
	text = mask(text)
	summary := fmt.Sprintf("Logs of %s (last %d line(s) per container", inst.Compose, tail)
	if truncated {
		summary += fmt.Sprintf(", cut to the last %d KiB", logsLimit>>10)
	}
	summary += "):\n" + text
	if strings.TrimSpace(text) == "" {
		summary = fmt.Sprintf("%s has no logs.", inst.Compose)
	}
	return &result{
		Content:           []content{{Type: "text", Text: summary}},
		StructuredContent: map[string]any{"compose_project": inst.Compose, "tail": tail, "truncated": truncated, "logs": text},
	}, nil
}

func runStatus(ctx context.Context, s *Server, a args) (*result, error) {
	report, err := s.opts.Engine.Status(ctx, a.str("project"))
	if err != nil {
		return nil, err
	}
	if port, running := s.proxyPort(); running {
		proxy.AddURLs(report, port)
	}
	var lines []string
	for _, p := range report.Projects {
		for _, wt := range p.Worktrees {
			state := "-"
			if wt.Instance != nil {
				state = wt.Instance.State
			}
			ref := wt.Branch
			if ref == "" {
				ref = wt.Path
			}
			line := fmt.Sprintf("- %s · %s: %s", p.Name, ref, state)
			if wt.Instance != nil && len(wt.Instance.ProxyURLs) > 0 {
				line += " (" + wt.Instance.ProxyURLs[0] + ")"
			}
			for _, w := range wt.Warnings {
				if w.Kind == engine.WarnComposeChanged {
					line += "; " + w.Message
				}
			}
			lines = append(lines, line)
		}
	}
	summary := "No projects registered."
	if len(lines) > 0 {
		summary = "Instances:\n" + strings.Join(lines, "\n")
	}
	if report.DockerError != "" {
		summary = "Docker could not be asked, so states are unknown.\n" + summary
	}
	return ok(summary, report), nil
}

func runEnvList(ctx context.Context, s *Server, a args) (*result, error) {
	e := s.opts.Engine
	inst, err := s.instance(ctx, e, a)
	if err != nil {
		return nil, err
	}
	cfg, err := e.Settings(inst)
	if err != nil {
		return nil, err
	}
	masked := map[string]instances.Var{}
	for k, v := range cfg.Env {
		if v.Secret {
			v.Value = engine.Mask // secrets never leave through MCP
		}
		masked[k] = v
	}
	summary := fmt.Sprintf("No variables set with theo for %s.", inst.Compose)
	if len(masked) > 0 {
		summary = fmt.Sprintf("%d variable(s) set with theo for %s: %s (secret values masked).", len(masked), inst.Compose, strings.Join(compose.SortedKeys(masked), ", "))
	}
	return ok(summary, masked), nil
}

func runInstanceShow(ctx context.Context, s *Server, a args) (*result, error) {
	e := s.opts.Engine
	inst, err := s.instance(ctx, e, a)
	if err != nil {
		return nil, err
	}
	cfg, err := e.Settings(inst)
	if err != nil {
		return nil, err
	}
	return ok(fmt.Sprintf("Instance %s of %s at %s.", inst.Compose, inst.Project, inst.Workspace), struct {
		*engine.Instance
		Neutralize     bool              `json:"neutralize_container_names"`
		ContainerNames map[string]string `json:"fixed_container_names,omitempty"`
		Variables      []string          `json:"variables,omitempty"`
	}{inst, cfg.NeutralizeContainerNames, inst.FixedContainerNames(), compose.SortedKeys(cfg.Env)}), nil
}

func runInstanceConfig(ctx context.Context, s *Server, a args) (*result, error) {
	e := s.opts.Engine
	inst, err := s.instance(ctx, e, a)
	if err != nil {
		return nil, err
	}
	text, err := e.PreviewConfig(ctx, inst, a.list("profiles"), a.bool("auto_ports")) // reserves nothing
	if err != nil {
		return nil, maskErr(masker(e, inst), err)
	}
	text, truncated := headLimit(text, configLimit)
	summary := fmt.Sprintf("Configuration of %s (secrets masked):\n%s", inst.Compose, text)
	if truncated {
		summary += fmt.Sprintf("\n[cut at %d KiB]", configLimit>>10)
	}
	return &result{
		Content: []content{{Type: "text", Text: summary}},
		StructuredContent: struct {
			*engine.Instance
			Config    string `json:"config"`
			Truncated bool   `json:"truncated"`
		}{inst, text, truncated},
	}, nil
}

func runHistory(ctx context.Context, s *Server, a args) (*result, error) {
	limit := a.int("limit", defaultHistory)
	if limit <= 0 || limit > maxHistory {
		return nil, fmt.Errorf("%w: limit must be between 1 and %d", errBadArgs, maxHistory)
	}
	events, err := s.opts.Engine.History(ctx, a.str("project"), a.str("worktree"), limit)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, ev := range events {
		lines = append(lines, fmt.Sprintf("- %s %s %s %s", ev.Time.UTC().Format("2006-01-02 15:04:05Z"), ev.Operation, ev.Result, ev.Compose))
	}
	summary := "No operations recorded yet."
	if len(lines) > 0 {
		summary = fmt.Sprintf("%d operation(s), newest first:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	return ok(summary, map[string]any{"events": nonNil(events)}), nil
}

func runReconcile(ctx context.Context, s *Server, a args) (*result, error) {
	e := s.opts.Engine
	apply := a.bool("apply")
	if apply {
		release, err := s.claim("reconcile", "reconcile")
		if err != nil {
			return nil, err
		}
		defer release()
	}
	report, err := e.Reconcile(ctx, a.str("project"))
	if err != nil {
		return nil, err
	}
	forgot := 0
	if apply {
		if forgot, err = e.ApplyReconcile(report); err != nil {
			return nil, err
		}
	}
	var lines []string
	forgettable := 0
	for _, f := range report.Findings {
		line := fmt.Sprintf("- %s: %s. %s", f.Kind, f.Subject, f.Detail)
		if f.Fix != "" && !(apply && f.Forgettable) {
			line += " Fix: " + f.Fix
		}
		lines = append(lines, line)
		if f.Forgettable {
			forgettable++
		}
	}
	summary := "Everything Theo recorded matches what exists."
	if len(lines) > 0 {
		summary = strings.Join(lines, "\n")
		switch {
		case apply:
			summary += fmt.Sprintf("\nForgot %d of Theo's own record(s). Nothing else was touched.", forgot)
		case forgettable > 0:
			summary += fmt.Sprintf("\n%d record(s) of Theo's own can be forgotten with apply=true. Nothing was changed.", forgettable)
		default:
			summary += "\nNothing was changed."
		}
	}
	if report.DockerError != "" {
		summary = "Docker could not be checked, so containers and volumes are missing from this report.\n" + summary
	}
	return ok(summary, struct {
		*engine.Report
		Forgotten int `json:"forgotten"`
	}{report, forgot}), nil
}

func runVolumes(ctx context.Context, s *Server, a args) (*result, error) {
	e := s.opts.Engine
	inst, err := s.instance(ctx, e, a)
	if err != nil {
		return nil, err
	}
	list, err := e.Volumes(ctx, inst)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, v := range list {
		lines = append(lines, fmt.Sprintf("- %s (%s): %s", v.Volume, v.Name, v.Status))
	}
	summary := fmt.Sprintf("%s has no volumes.", inst.Compose)
	if len(lines) > 0 {
		summary = fmt.Sprintf("Volumes of %s:\n%s", inst.Compose, strings.Join(lines, "\n"))
	}
	return ok(summary, map[string]any{"compose_project": inst.Compose, "volumes": nonNil(list)}), nil
}

// --- instances: changing ---

// mutate resolves the instance, claims it and runs fn with a buffer for
// Docker's output. The output (masked, cut to its end) goes with the error
// when fn fails.
func (s *Server) mutate(ctx context.Context, a args, op string, fn func(e *engine.Engine, inst *engine.Instance, out io.Writer) (*result, error)) (*result, error) {
	e := s.opts.Engine
	inst, err := s.instance(ctx, e, a)
	if err != nil {
		return nil, err
	}
	release, err := s.claim(inst.Compose, op)
	if err != nil {
		return nil, err
	}
	defer release()
	mask := masker(e, inst)
	out := newTailBuffer(outputLimit)
	var w io.Writer = out
	if pr := progressOf(ctx); pr != nil {
		pr.mask = mask
		pr.notify(fmt.Sprintf("%s %s", op, inst.Compose), true)
		w = io.MultiWriter(out, pr)
		defer pr.notify(fmt.Sprintf("%s %s: finished", op, inst.Compose), true)
	}
	res, err := fn(e, inst, w)
	text, truncated := out.Text()
	text = mask(text)
	if err != nil {
		return nil, maskErr(mask, joinOutput(err, text))
	}
	if sc, isMap := res.StructuredContent.(map[string]any); isMap {
		sc["output"] = text
		sc["output_truncated"] = truncated
		res = ok(res.Content[0].Text, sc)
	}
	return res, nil
}

// joinOutput adds the end of Docker's output to an error, when it says more.
func joinOutput(err error, output string) error {
	output = strings.TrimSpace(output)
	if output == "" || strings.Contains(err.Error(), output) {
		return err
	}
	lines := strings.Split(output, "\n")
	if len(lines) > 30 {
		lines = lines[len(lines)-30:]
	}
	return fmt.Errorf("%w\n\nDocker output (end):\n%s", err, strings.Join(lines, "\n"))
}

// upReport is what up returns besides Docker's output.
type upReport struct {
	warnings []string
	notes    []string
}

// doUp does what `theo up` does, in the same order.
func doUp(ctx context.Context, e *engine.Engine, inst *engine.Instance, a args, out io.Writer) (*upReport, error) {
	rep := &upReport{}
	auto := a.bool("auto_ports")
	profiles := a.list("profiles")
	if auto {
		if err := e.SetAutoPorts(inst, true); err != nil {
			return nil, err
		}
	}
	warnings, err := e.EnsureShared(ctx, inst, a.bool("start_shared"), out, out)
	if err != nil {
		return nil, err
	}
	rep.warnings = append(rep.warnings, warnings...)
	plan, err := e.ApplyInstance(ctx, inst, profiles, auto)
	if err != nil {
		return nil, err
	}
	for _, m := range plan.Mappings {
		if m.Status == engine.PortReassigned || m.Status == engine.PortKept {
			rep.notes = append(rep.notes, fmt.Sprintf("%s publishes %d/%s on host port %d instead of %s (%s)", m.Service, m.Target, m.Protocol, m.Effective, m.Configured, m.Status))
		}
	}
	for _, m := range plan.Conflicts() {
		rep.warnings = append(rep.warnings, fmt.Sprintf("host port %s (%s, container port %d/%s) is already in use; free it, or call up with auto_ports=true", m.Configured, m.Service, m.Target, m.Protocol))
	}
	if names := inst.FixedContainerNames(); len(names) > 0 && !inst.Main && !inst.Neutralized() {
		rep.warnings = append(rep.warnings, fmt.Sprintf("%s set a fixed container_name, so another instance of this project cannot run at the same time; `theo instance set %s -w %s --neutralize-container-names` removes it for this instance",
			strings.Join(compose.SortedKeys(names), ", "), inst.Project, selector(inst)))
	}
	if a.bool("build") {
		if shared, err := e.SharedImageBuilds(ctx, inst); err == nil && len(shared) > 0 {
			var parts []string
			for _, svc := range compose.SortedKeys(shared) {
				parts = append(parts, fmt.Sprintf("%s (%s)", svc, shared[svc]))
			}
			rep.warnings = append(rep.warnings, "services built into a fixed image name, which every worktree of this project shares: "+strings.Join(parts, ", "))
		}
	}
	err = e.Up(ctx, inst, compose.UpOptions{Build: a.bool("build"), Profiles: profiles, Services: a.list("services"), ForceRecreate: a.bool("force_recreate")}, out, out)
	return rep, err
}

func selector(inst *engine.Instance) string {
	if inst.Branch != "" {
		return inst.Branch
	}
	return inst.Workspace
}

func runUp(ctx context.Context, s *Server, a args) (*result, error) {
	return s.mutate(ctx, a, "up", func(e *engine.Engine, inst *engine.Instance, out io.Writer) (*result, error) {
		rep, err := doUp(ctx, e, inst, a, out)
		if err != nil {
			return nil, err
		}
		summary := fmt.Sprintf("Started %s (worktree %s).", inst.Compose, inst.Workspace)
		var views []portView
		if plan, err := e.PlanPorts(ctx, inst, a.list("profiles"), false); err == nil {
			views = portViews(plan)
			if len(views) > 0 {
				summary += "\nPorts:\n" + portLines(views)
			}
		}
		links := s.proxyLinks(inst, views)
		summary += linksText(links)
		for _, n := range rep.notes {
			summary += "\nNote: " + n
		}
		for _, w := range rep.warnings {
			summary += "\nWarning: " + w
		}
		state := ""
		if containers, err := e.Containers(ctx, inst); err == nil {
			state = engine.InstanceState(containers, e.ExpectedServices(inst), true)
		}
		return ok(summary, map[string]any{
			"project": inst.Project, "workspace": inst.Workspace, "branch": inst.Branch, "compose_project": inst.Compose,
			"state": state, "ports": nonNil(views), "proxy_urls": nonNil(links), "warnings": nonNil(rep.warnings), "notes": nonNil(rep.notes),
		}), nil
	})
}

func runDown(ctx context.Context, s *Server, a args) (*result, error) {
	return s.mutate(ctx, a, "down", func(e *engine.Engine, inst *engine.Instance, out io.Writer) (*result, error) {
		if err := e.Down(ctx, inst, out, out); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Stopped %s. Its volumes were kept.", inst.Compose),
			map[string]any{"project": inst.Project, "workspace": inst.Workspace, "compose_project": inst.Compose, "volumes_kept": true}), nil
	})
}

func servicesText(services []string) string {
	if len(services) == 0 {
		return "all services"
	}
	return strings.Join(services, ", ")
}

func runRestart(ctx context.Context, s *Server, a args) (*result, error) {
	return s.mutate(ctx, a, "restart", func(e *engine.Engine, inst *engine.Instance, out io.Writer) (*result, error) {
		if err := e.RestartServices(ctx, inst, a.list("services"), out, out); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Restarted %s of %s.", servicesText(a.list("services")), inst.Compose),
			map[string]any{"compose_project": inst.Compose, "services": nonNil(a.list("services"))}), nil
	})
}

func runStop(ctx context.Context, s *Server, a args) (*result, error) {
	return s.mutate(ctx, a, "stop", func(e *engine.Engine, inst *engine.Instance, out io.Writer) (*result, error) {
		if err := e.Stop(ctx, inst, a.list("services"), out, out); err != nil {
			return nil, err
		}
		return ok(fmt.Sprintf("Stopped %s of %s. The containers and volumes were kept; start starts them again.", servicesText(a.list("services")), inst.Compose),
			map[string]any{"compose_project": inst.Compose, "services": nonNil(a.list("services"))}), nil
	})
}

func runStart(ctx context.Context, s *Server, a args) (*result, error) {
	return s.mutate(ctx, a, "start", func(e *engine.Engine, inst *engine.Instance, out io.Writer) (*result, error) {
		if err := e.Start(ctx, inst, a.list("services"), out, out); err != nil {
			return nil, fmt.Errorf("%w\n(start only starts containers that exist; up creates them)", err)
		}
		return ok(fmt.Sprintf("Started %s of %s.", servicesText(a.list("services")), inst.Compose),
			map[string]any{"compose_project": inst.Compose, "services": nonNil(a.list("services"))}), nil
	})
}

func runEnvSet(ctx context.Context, s *Server, a args) (*result, error) {
	e := s.opts.Engine
	inst, err := s.instance(ctx, e, a)
	if err != nil {
		return nil, err
	}
	name := a.str("name")
	if err := e.SetEnv(inst, name, a.str("value"), a.bool("secret"), a.list("services")); err != nil {
		return nil, &maskedError{strings.ReplaceAll(err.Error(), a.str("value"), engine.Mask), err}
	}
	scope := "every service"
	if len(a.list("services")) > 0 {
		scope = strings.Join(a.list("services"), ", ")
	}
	return ok(fmt.Sprintf("Set %s for %s (%s). It applies the next time the instance starts; the project's files are not changed.", name, inst.Compose, scope),
		map[string]any{"name": name, "compose_project": inst.Compose, "secret": a.bool("secret"), "services": nonNil(a.list("services")), "applies_on": "next_up"}), nil
}

func runEnvUnset(ctx context.Context, s *Server, a args) (*result, error) {
	e := s.opts.Engine
	inst, err := s.instance(ctx, e, a)
	if err != nil {
		return nil, err
	}
	name := a.str("name")
	found, err := e.UnsetEnv(inst, name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%s has no variable %s set with theo", inst.Compose, name)
	}
	return ok(fmt.Sprintf("Removed %s from %s. It applies the next time the instance starts.", name, inst.Compose),
		map[string]any{"name": name, "compose_project": inst.Compose, "applies_on": "next_up"}), nil
}

// --- explain ---

var explainLine = regexp.MustCompile(`^(\d+)\. (read|change)\s+(.*?) \$ (.*)$`)

// explainedCommand is one command of an explained operation.
type explainedCommand struct {
	N       int    `json:"n"`
	Kind    string `json:"kind"` // "read" (it ran) or "change" (it did not)
	Dir     string `json:"dir"`
	Command string `json:"command"`
}

func runExplain(ctx context.Context, s *Server, a args) (*result, error) {
	// Explain mode is process-wide: nothing else may run meanwhile, or its
	// changes would be listed instead of made.
	s.explainMu.Lock()
	defer s.explainMu.Unlock()
	e, err := s.opts.Open()
	if err != nil {
		return nil, err
	}
	e.DiscardWrites() // Theo records nothing either

	var listing bytes.Buffer
	done := execx.Explain(&listing)
	opErr := func() error {
		inst, err := s.instance(ctx, e, a)
		if err != nil {
			return err
		}
		mask := masker(e, inst)
		services := a.list("services")
		switch a.str("operation") {
		case "up":
			_, err = doUp(ctx, e, inst, a, io.Discard)
		case "down":
			err = e.Down(ctx, inst, io.Discard, io.Discard)
		case "restart":
			err = e.RestartServices(ctx, inst, services, io.Discard, io.Discard)
		case "stop":
			err = e.Stop(ctx, inst, services, io.Discard, io.Discard)
		case "start":
			err = e.Start(ctx, inst, services, io.Discard, io.Discard)
		}
		return maskErr(mask, err)
	}()
	done()

	text := listing.String()
	commands := []explainedCommand{}
	changes := 0
	for _, line := range strings.Split(text, "\n") {
		m := explainLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		commands = append(commands, explainedCommand{N: n, Kind: m[2], Dir: m[3], Command: m[4]})
		if m[2] == "change" {
			changes++
		}
	}
	if opErr != nil {
		return nil, fmt.Errorf("%w\n\nCommands up to the error:\n%s", opErr, text)
	}
	summary := fmt.Sprintf("%s would run %d command(s), %d of which change something (not run). Nothing was changed.\n%s", a.str("operation"), len(commands), changes, text)
	return &result{
		Content:           []content{{Type: "text", Text: summary}},
		StructuredContent: map[string]any{"operation": a.str("operation"), "commands": commands, "changes": changes, "text": text},
	}, nil
}

// nonNil returns an empty list instead of a nil one, so that JSON shows [].
func nonNil[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

// --- local domains ---

// proxyPort is the port of the running `theo proxy`, if one runs.
func (s *Server) proxyPort() (int, bool) {
	if s.opts.ProxyPort != nil {
		return s.opts.ProxyPort()
	}
	dir, err := registry.DefaultDir()
	if err != nil {
		return 0, false
	}
	return proxy.RunningPort(dir)
}

// proxyHTTPSPort is the HTTPS port of the running proxy; 0 when it serves
// only HTTP (or under test).
func (s *Server) proxyHTTPSPort() int {
	if s.opts.ProxyPort != nil {
		return 0
	}
	dir, err := registry.DefaultDir()
	if err != nil {
		return 0
	}
	return proxy.RunningHTTPSPort(dir)
}

// proxyLinks are the proxy addresses of an instance while a proxy runs; nil
// otherwise.
func (s *Server) proxyLinks(inst *engine.Instance, views []portView) []proxy.Link {
	port, running := s.proxyPort()
	if !running || len(views) == 0 {
		return nil
	}
	statuses := make([]engine.PortStatus, 0, len(views))
	for _, v := range views {
		host := v.Running
		if host == 0 {
			host = v.Effective
		}
		statuses = append(statuses, engine.PortStatus{Service: v.Service, ContainerPort: v.Target, Protocol: v.Protocol, HostPort: host, URL: v.URL, Status: v.Status})
	}
	targets := proxy.InstanceTargets(proxy.Instance{
		Project: inst.Project, Worktree: inst.Workspace, Branch: inst.Branch, Main: inst.Main, ComposeProject: inst.Compose,
	}, statuses)
	return proxy.Links(targets, port, s.proxyHTTPSPort())[inst.Workspace]
}

func linksText(links []proxy.Link) string {
	if len(links) == 0 {
		return ""
	}
	var lines []string
	for _, l := range links {
		lines = append(lines, fmt.Sprintf("- %s %s", l.Service, l.URL))
	}
	return "\nLocal domains (theo proxy):\n" + strings.Join(lines, "\n")
}

func runProxyRoutes(ctx context.Context, s *Server, a args) (*result, error) {
	port, running := s.proxyPort()
	if !running {
		port = proxy.DefaultPort
	}
	targets, err := proxy.EngineSource(s.opts.Engine, a.str("project"))(ctx)
	if err != nil {
		return nil, err
	}
	routes := proxy.BuildTable(targets, port).Routes
	var lines []string
	for _, r := range routes {
		state := "stopped"
		if r.Running {
			state = "running"
		}
		lines = append(lines, fmt.Sprintf("- %s -> %s (%s · %s · %s, %s)", r.URL, r.Target, r.Project, r.Instance, r.Service, state))
	}
	summary := "No routes: no instance publishes a TCP port on the host."
	if len(lines) > 0 {
		summary = "Routes:\n" + strings.Join(lines, "\n")
	}
	if !running {
		summary = fmt.Sprintf("No proxy is running, so these names do not answer yet; the user starts it with `theo proxy` (port %d) or from the web interface.\n", port) + summary
	}
	return ok(summary, map[string]any{"port": port, "proxy_running": running, "routes": nonNil(routes)}), nil
}
