package engine

import (
	"context"
	"fmt"
	"strings"
)

// FindDuplicateName is reported when two registered instances would use the
// same Compose project name (FR-040).
const FindDuplicateName = "duplicate compose name"

// reconcileChecks are extra checks that Reconcile runs after its own. Each
// reports findings through add; project is the project in scope, or empty.
var reconcileChecks []func(ctx context.Context, e *Engine, project string, add func(Finding))

func init() {
	reconcileChecks = append(reconcileChecks, checkDuplicateNames)
}

// checkDuplicateNames reports every Compose project name that two or more
// registered instances share. With a project in scope, only the names that
// involve one of its instances.
func checkDuplicateNames(ctx context.Context, e *Engine, project string, add func(Finding)) {
	refs, err := e.RegisteredInstances(ctx)
	if err != nil {
		return
	}
	groups := duplicateNames(refs)
	for _, name := range sortedGroups(groups) {
		group := groups[name]
		involved := project == ""
		list := make([]string, len(group))
		for i, r := range group {
			list[i] = r.String()
			if strings.EqualFold(r.Project, project) {
				involved = true
			}
		}
		if !involved {
			continue
		}
		owner := group[0].Project
		if project != "" {
			owner = project
		}
		add(Finding{Kind: FindDuplicateName, Project: owner, Subject: group[0].Compose,
			Detail: fmt.Sprintf("%d instances would use the Compose project name %s, so they would share containers, networks and volumes: %s", len(group), group[0].Compose, strings.Join(list, "; ")),
			Fix:    "make one name different: `name:` in a Compose file or COMPOSE_PROJECT_NAME in the .env of the main worktree, another directory name for a linked worktree, or `theo project rename`. `theo up` refuses these instances until then"})
	}
}
