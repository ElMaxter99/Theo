package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// PruneOptions configures PruneMerged.
type PruneOptions struct {
	// Into is the branch (or commit) the worktrees' branches must be merged
	// into. Empty means the branch of the main worktree.
	Into string
	// Apply removes the worktrees that can go. Without it nothing changes.
	Apply bool
}

// PruneEntry is a worktree whose branch is merged.
type PruneEntry struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Owner  string `json:"owner,omitempty"`
	// Reason says why it stays; empty when it can be (or was) removed.
	Reason  string `json:"reason,omitempty"`
	Removed bool   `json:"removed"`
}

// PruneReport is the answer of PruneMerged.
type PruneReport struct {
	Project string `json:"project"`
	Into    string `json:"into"`
	Applied bool   `json:"applied"`
	// Merged lists the linked worktrees whose branch is merged into Into:
	// the ones that can go (or went) and the ones that stay, with why.
	Merged []PruneEntry `json:"merged"`
	// NotMerged counts the linked worktrees left alone because their branch
	// has commits that are not in Into.
	NotMerged int `json:"not_merged"`
}

// Removable lists the entries that can be removed (or were).
func (r *PruneReport) Removable() []PruneEntry {
	var out []PruneEntry
	for _, m := range r.Merged {
		if m.Reason == "" {
			out = append(out, m)
		}
	}
	return out
}

type mergeChecker interface {
	IsMerged(ctx context.Context, dir, branch, into string) (bool, error)
}

// PruneMerged finds the linked worktrees of a project whose branch is
// already merged into Into, and with Apply removes the ones with no
// containers and no uncommitted or untracked files, through the same checks
// as RemoveWorkspace. Branches, volumes and independent clones are never
// touched; a branch merged by squash or rebase does not count as merged.
func (e *Engine) PruneMerged(ctx context.Context, project string, opts PruneOptions) (*PruneReport, error) {
	p, err := e.Project(project)
	if err != nil {
		return nil, err
	}
	mc, ok := e.git.(mergeChecker)
	if !ok {
		return nil, errors.New("this Git adapter cannot tell merged branches")
	}
	list, err := e.worktreeList(ctx, p)
	if err != nil {
		return nil, err
	}
	into := opts.Into
	if into == "" {
		if len(list) == 0 || list[0].Branch == "" {
			return nil, fmt.Errorf("the main worktree of %q has no branch checked out; say which branch with --into", p.Name)
		}
		into = list[0].Branch
	}
	if r, ok := e.git.(interface {
		ResolvesTo(ctx context.Context, dir, rev string) bool
	}); ok && !r.ResolvesTo(ctx, p.Path, into) {
		return nil, fmt.Errorf("%s is not a branch or commit of %q", into, p.Name)
	}
	report := &PruneReport{Project: p.Name, Into: into, Applied: opts.Apply, Merged: []PruneEntry{}}

	var containers map[string]bool // worktree path -> has containers, read once
	hasContainers := func(path string) (bool, error) {
		if containers == nil {
			overview, err := e.Overview(ctx, p.Name)
			if err != nil {
				return false, err
			}
			containers = map[string]bool{}
			for _, o := range overview {
				containers[o.Worktree.Path] = len(o.Stacks) > 0
			}
		}
		for wt, has := range containers {
			if samePath(wt, path) {
				return has, nil
			}
		}
		return false, nil
	}

	for i, wt := range list {
		if i == 0 || wt.Bare || wt.Branch == "" || strings.EqualFold(wt.Branch, into) {
			continue
		}
		if workspaceKind(p, list, wt.Path) == KindClone {
			continue // its branch lives in its own repository
		}
		merged, err := mc.IsMerged(ctx, p.Path, wt.Branch, into)
		if err != nil {
			report.Merged = append(report.Merged, PruneEntry{Path: wt.Path, Branch: wt.Branch, Owner: workspaceOwner(p, wt.Path), Reason: "cannot tell: " + firstLine(err.Error())})
			continue
		}
		if !merged {
			report.NotMerged++
			continue
		}
		entry := PruneEntry{Path: wt.Path, Branch: wt.Branch, Owner: workspaceOwner(p, wt.Path)}
		switch {
		case wt.Prunable:
			entry.Reason = fmt.Sprintf("its directory is gone; `git worktree prune` in %s forgets it", p.Path)
		case wt.Locked:
			entry.Reason = "locked with git worktree lock"
		default:
			has, err := hasContainers(wt.Path)
			switch {
			case err != nil:
				entry.Reason = "cannot check its containers: " + firstLine(err.Error())
			case has:
				entry.Reason = fmt.Sprintf("its instance has containers; `theo down %s -w %s` first", p.Name, wt.Branch)
			default:
				changes, err := e.git.Changes(ctx, wt.Path)
				if err != nil {
					entry.Reason = "cannot check its files: " + firstLine(err.Error())
				} else if len(changes) > 0 {
					entry.Reason = fmt.Sprintf("%d uncommitted or untracked file(s)", len(changes))
				}
			}
		}
		if entry.Reason == "" && opts.Apply {
			if _, err := e.RemoveWorkspace(ctx, p.Name, wt.Path, RemoveWorktreeOptions{}); err != nil {
				entry.Reason = firstLine(err.Error())
			} else {
				entry.Removed = true
			}
		}
		report.Merged = append(report.Merged, entry)
	}
	return report, nil
}
