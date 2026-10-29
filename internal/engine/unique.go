package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// InstanceRef is a registered instance and the Compose project name it uses.
type InstanceRef struct {
	Project   string `json:"project"`
	Workspace string `json:"workspace"`
	Branch    string `json:"branch,omitempty"`
	Compose   string `json:"compose_project"`
}

func (r InstanceRef) String() string {
	where := r.Workspace
	if r.Branch != "" {
		where = r.Branch + " · " + r.Workspace
	}
	return fmt.Sprintf("project %q, %s", r.Project, where)
}

// RegisteredInstances lists the instances of every registered project (one
// per worktree that exists) with their Compose project names. A project
// whose repository cannot be read, or whose main worktree has no valid
// Compose configuration, contributes what can be known: linked worktrees get
// their name from the project name and directory, the main one only when
// Compose can read it.
func (e *Engine) RegisteredInstances(ctx context.Context) ([]InstanceRef, error) {
	return e.registeredInstances(ctx, nil)
}

// registeredInstances is RegisteredInstances; known, when given, is an
// instance already resolved, whose name is not asked from Compose again.
func (e *Engine) registeredInstances(ctx context.Context, known *Instance) ([]InstanceRef, error) {
	projects, err := e.Projects()
	if err != nil {
		return nil, err
	}
	var out []InstanceRef
	for _, p := range projects {
		list, err := e.git.Worktrees(ctx, p.Path)
		if err != nil {
			continue
		}
		for i, wt := range list {
			if wt.Bare || wt.Prunable {
				continue
			}
			ref := InstanceRef{Project: p.Name, Workspace: wt.Path, Branch: wt.Branch}
			if known != nil && samePath(wt.Path, known.Workspace) {
				ref.Compose = known.Compose
			} else if i == 0 {
				inst, _, err := e.resolveInstance(ctx, p.Name, wt.Path, InstanceOptions{})
				if err != nil {
					continue
				}
				ref.Compose = inst.Compose
			} else {
				ref.Compose = instanceName(p.Name, "", wt, list)
			}
			out = append(out, ref)
		}
	}
	return out, nil
}

// duplicateNames groups the instances that use the same Compose project
// name. Only names used by two or more are returned.
func duplicateNames(refs []InstanceRef) map[string][]InstanceRef {
	byName := map[string][]InstanceRef{}
	for _, r := range refs {
		key := strings.ToLower(r.Compose)
		byName[key] = append(byName[key], r)
	}
	for name, group := range byName {
		if len(group) < 2 {
			delete(byName, name)
		}
	}
	return byName
}

// CheckUniqueName fails when another registered instance, of this project or
// of any other, would use the same Compose project name as inst. Two
// instances with one name share containers, networks and volumes, and
// `up` of one would take over the other.
func (e *Engine) CheckUniqueName(ctx context.Context, inst *Instance) error {
	refs, err := e.registeredInstances(ctx, inst)
	if err != nil {
		return err
	}
	var clash []InstanceRef
	for _, r := range refs {
		if !samePath(r.Workspace, inst.Workspace) && strings.EqualFold(r.Compose, inst.Compose) {
			clash = append(clash, r)
		}
	}
	if len(clash) == 0 {
		return nil
	}
	list := make([]string, len(clash))
	for i, r := range clash {
		list[i] = r.String()
	}
	return fmt.Errorf("the Compose project name %q of %s is also used by %s. Two instances with the same name share their containers, networks and volumes, so nothing was started. "+
		"Make one name different: set `name:` in a Compose file or COMPOSE_PROJECT_NAME in the .env of the main worktree; for a linked worktree, use another directory name (`theo worktree add --path`) "+
		"or rename a project (`theo project rename`)",
		inst.Compose, InstanceRef{Project: inst.Project, Workspace: inst.Workspace, Branch: inst.Branch}, strings.Join(list, "; "))
}

// sortedGroups returns the names of duplicateNames in order.
func sortedGroups(groups map[string][]InstanceRef) []string {
	names := make([]string, 0, len(groups))
	for n := range groups {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
