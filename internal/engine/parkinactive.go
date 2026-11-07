package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ElMaxter99/Theo/internal/agents"
	"github.com/ElMaxter99/Theo/internal/registry"
)

// DefaultParkMaxChanges is how many uncommitted files ParkInactive saves in a
// work-in-progress commit at most; a worktree with more is left for a person.
const DefaultParkMaxChanges = 50

// ParkInactiveOptions configures ParkInactive.
type ParkInactiveOptions struct {
	// SaveChanges commits the uncommitted and untracked files of a worktree
	// (a local work-in-progress commit, never pushed) so it can be parked.
	// Without it a worktree with changes is skipped.
	SaveChanges bool
	// MaxChanges is the most files SaveChanges commits in one worktree
	// (DefaultParkMaxChanges when zero).
	MaxChanges int
	// IncludeIgnored parks worktrees that have ignored files (.env,
	// node_modules...), which removing the folder deletes. Without it they
	// are skipped.
	IncludeIgnored bool
	// Only limits it to these workspaces (by path or branch name); empty means every
	// inactive one. A path that is no longer inactive stays, with a reason.
	Only []string
	// DeleteBranches also deletes the local branch of each parked worktree,
	// but only when everything in it is on its remote: no uncommitted files,
	// no WIP commit, no commit missing from the upstream. The check is made
	// again right before deleting. Remote branches are never touched.
	DeleteBranches bool
	// Size measures the folder of each workspace (slow on big worktrees).
	Size bool
	// Apply parks. Without it nothing changes.
	Apply bool
}

// ParkInactiveEntry is one inactive workspace in a ParkInactiveReport.
type ParkInactiveEntry struct {
	Path         string `json:"path"`
	Branch       string `json:"branch,omitempty"`
	IdleDays     int    `json:"idle_days"`
	Changes      int    `json:"changes"`
	Unpushed     int    `json:"unpushed"`
	UpstreamGone bool   `json:"upstream_gone,omitempty"`
	// Ignored counts the ignored files and folders parking would delete.
	Ignored int `json:"ignored,omitempty"`
	// Safe is true when everything it has is on its remote and nothing is
	// uncommitted: removing it loses nothing even without its branch.
	Safe bool `json:"safe,omitempty"`
	// BranchDeletable is true when DeleteBranches would delete its branch.
	BranchDeletable bool  `json:"branch_deletable,omitempty"`
	SizeBytes       int64 `json:"size_bytes,omitempty"`
	// Reason says why it stays; empty when it can be (or was) parked.
	Reason string `json:"reason,omitempty"`
	// Blocked is true when the reason is not something an option fixes (a
	// clone, a lock, a detached HEAD, a live agent).
	Blocked       bool     `json:"blocked,omitempty"`
	BranchDeleted bool     `json:"branch_deleted,omitempty"`
	Problems      []string `json:"problems,omitempty"`
	// WillSave is true when its changes go into a work-in-progress commit.
	WillSave bool `json:"will_save,omitempty"`
	Saved    bool `json:"saved,omitempty"`
	Parked   bool `json:"parked"`
}

// ParkInactiveReport is the answer of ParkInactive.
type ParkInactiveReport struct {
	Project string `json:"project"`
	Applied bool   `json:"applied"`
	// Entries lists every inactive workspace, parked or not, with why.
	Entries []ParkInactiveEntry `json:"entries"`
	// Summary counts the entries, for a quick look.
	Summary ParkInactiveSummary `json:"summary"`
	Notes   []string            `json:"notes,omitempty"`
}

// Parkable counts the entries that can be (or were) parked.
func (r *ParkInactiveReport) Parkable() int {
	n := 0
	for _, e := range r.Entries {
		if e.Reason == "" {
			n++
		}
	}
	return n
}

// ParkedCount counts the entries that were parked.
func (r *ParkInactiveReport) ParkedCount() int {
	n := 0
	for _, e := range r.Entries {
		if e.Parked {
			n++
		}
	}
	return n
}

// ParkInactiveSummary counts the entries of a ParkInactiveReport.
type ParkInactiveSummary struct {
	Total int `json:"total"`
	// Parkable can be parked with the options asked for.
	Parkable int `json:"parkable"`
	// Safe have everything on their remote and nothing uncommitted.
	Safe int `json:"safe"`
	// WithChanges have uncommitted or untracked files.
	WithChanges int `json:"with_changes"`
	// WithUnpushed have commits that are on no remote.
	WithUnpushed int `json:"with_unpushed"`
	// UpstreamGone have lost their remote branch (usually a merged or closed PR).
	UpstreamGone int `json:"upstream_gone"`
	// Blocked cannot be parked by any option (clones, locked, detached, agent).
	Blocked int `json:"blocked"`
	// SizeBytes adds up the folders of the parkable ones (only with Size).
	SizeBytes int64 `json:"size_bytes,omitempty"`
}

func summarizeParkInactive(entries []ParkInactiveEntry) ParkInactiveSummary {
	var s ParkInactiveSummary
	s.Total = len(entries)
	for _, e := range entries {
		if e.Reason == "" || e.Parked {
			s.Parkable++
			s.SizeBytes += e.SizeBytes
		}
		if e.Safe {
			s.Safe++
		}
		if e.Changes > 0 {
			s.WithChanges++
		}
		if e.Unpushed > 0 {
			s.WithUnpushed++
		}
		if e.UpstreamGone {
			s.UpstreamGone++
		}
		if e.Blocked {
			s.Blocked++
		}
	}
	return s
}

type allCommitter interface {
	CommitAll(ctx context.Context, dir, msg string) error
}

// ParkInactive parks every inactive worktree of a project in one go. Nothing
// is lost: each branch stays (with its commits, and with a work-in-progress
// commit of the uncommitted files when SaveChanges is set), volumes are kept,
// and `theo worktree resume` brings any of them back. Worktrees that are kept,
// have an agent alive, are clones or locked, have changes without SaveChanges
// or too many of them, or have ignored files without IncludeIgnored, stay,
// each with its reason.
func (e *Engine) ParkInactive(ctx context.Context, project string, opts ParkInactiveOptions) (*ParkInactiveReport, error) {
	p, err := e.Project(project)
	if err != nil {
		return nil, err
	}
	states, err := e.WorktreeStates(ctx, p.Name, StatesOptions{Size: opts.Size})
	if err != nil {
		return nil, err
	}
	maxChanges := opts.MaxChanges
	if maxChanges <= 0 {
		maxChanges = DefaultParkMaxChanges
	}
	report := &ParkInactiveReport{Project: p.Name, Applied: opts.Apply, Entries: []ParkInactiveEntry{}, Notes: states.Notes}
	committer, canCommit := e.git.(allCommitter)
	for i, ws := range states.Worktrees {
		if i == 0 || ws.Bare {
			continue
		}
		if len(opts.Only) > 0 && !containsPath(opts.Only, ws.Path) && !containsBranch(opts.Only, ws.Branch) {
			continue
		}
		if len(opts.Only) == 0 && ws.State != LifeInactive {
			continue
		}
		pushedAll := ws.Changes == 0 && ws.Upstream != "" && !ws.UpstreamGone && ws.Unpushed == 0
		ent := ParkInactiveEntry{Path: ws.Path, Branch: ws.Branch, IdleDays: ws.IdleDays, Changes: max(ws.Changes, 0), Unpushed: ws.Unpushed,
			UpstreamGone: ws.UpstreamGone, Safe: pushedAll, BranchDeletable: pushedAll && ws.Branch != "", SizeBytes: ws.SizeBytes}
		switch {
		case ws.State != LifeInactive:
			ent.Reason = "it is no longer inactive (" + ws.State + ")"
			ent.Blocked = true
			ent.BranchDeletable = false
		case ws.Kind == KindClone || ws.Branch == "" || ws.Locked:
			ent.Blocked = true
		case ws.Agent != nil && (ws.Agent.State == agents.Working || ws.Agent.State == agents.Waiting):
			ent.Blocked = true
		}
		switch {
		case ent.Reason != "":
		case ws.Kind == KindClone:
			ent.Reason = "an independent clone: only linked worktrees can be parked"
		case ws.Branch == "":
			ent.Reason = "no branch checked out (detached HEAD), so nothing would bring it back"
		case ws.Locked:
			ent.Reason = "locked with git worktree lock"
		case ws.Agent != nil && (ws.Agent.State == agents.Working || ws.Agent.State == agents.Waiting):
			ent.Reason = "an agent is alive here (" + ws.Agent.State + ")"
		case ws.Changes < 0:
			ent.Reason = "its files could not be read"
		case ws.Changes > 0 && !opts.SaveChanges:
			ent.Reason = fmt.Sprintf("%d uncommitted file(s); add --save-changes to keep them in a work-in-progress commit", ws.Changes)
		case ws.Changes > maxChanges:
			ent.Reason = fmt.Sprintf("%d uncommitted files (more than %d): review them by hand", ws.Changes, maxChanges)
		case ws.Changes > 0 && !canCommit:
			ent.Reason = "this Git adapter cannot save changes"
		}
		if ent.Reason == "" {
			if il, ok := e.git.(ignoredLister); ok {
				ignored, err := il.IgnoredEntries(ctx, ws.Path)
				if err != nil {
					ent.Reason = "cannot read its ignored files: " + firstLine(err.Error())
				}
				ent.Ignored = len(ignored)
				if ent.Reason == "" && len(ignored) > 0 && !opts.IncludeIgnored {
					shown := ignored
					if len(shown) > 3 {
						shown = append(append([]string{}, shown[:3]...), fmt.Sprintf("and %d more", len(ignored)-3))
					}
					ent.Reason = "has files Git ignores that parking deletes (" + strings.Join(shown, ", ") + "); add --include-ignored"
				}
			}
		}
		ent.WillSave = ent.Reason == "" && ws.Changes > 0
		if ent.Reason == "" && opts.Apply {
			if ent.WillSave {
				msg := "WIP: saved by Theo before parking " + filepath.Base(ws.Path)
				if err := committer.CommitAll(ctx, ws.Path, msg); err != nil {
					ent.Reason = "cannot save its changes (nothing was removed): " + firstLine(err.Error())
				} else {
					ent.Saved = true
				}
			}
			if ent.Reason == "" {
				note := "parked in bulk after " + fmt.Sprint(ws.IdleDays) + " idle days"
				if _, err := e.Park(ctx, p.Name, ws.Path, ParkOptions{Note: note, Confirm: filepath.Base(ws.Path)}); err != nil {
					ent.Reason = firstLine(err.Error())
				} else {
					ent.Parked = true
					if opts.DeleteBranches {
						if err := e.deletePushedBranch(ctx, p, ws, ent.Saved); err != nil {
							ent.Problems = append(ent.Problems, "branch "+ws.Branch+" kept: "+firstLine(err.Error()))
						} else {
							ent.BranchDeleted = true
						}
					}
				}
			}
		}
		report.Entries = append(report.Entries, ent)
	}
	report.Summary = summarizeParkInactive(report.Entries)
	return report, nil
}

// deletePushedBranch deletes the local branch of a worktree that was just
// parked, after checking again that nothing would be lost: no WIP commit was
// made, the branch tracks a remote branch that still exists, and every commit
// of the branch is on it. The remote branch and the remote-tracking ref stay,
// so `theo worktree add` can bring the branch back. The parked record is
// forgotten, because there is no local branch left to resume.
func (e *Engine) deletePushedBranch(ctx context.Context, p registry.Project, ws WorktreeState, savedWIP bool) error {
	bd, ok := e.git.(branchDeleter)
	lg, ok2 := e.git.(lifecycleGit)
	if !ok || !ok2 {
		return errors.New("this Git adapter cannot delete branches")
	}
	if savedWIP {
		return errors.New("it has a work-in-progress commit that is on no remote")
	}
	refs, err := lg.LocalBranches(ctx, p.Path)
	if err != nil {
		return err
	}
	ref, found := refs[ws.Branch]
	if !found {
		return errors.New("it is not a local branch")
	}
	if ref.Upstream == "" || ref.Gone || !lg.Resolves(ctx, p.Path, ref.Upstream) {
		return errors.New("it has no remote branch to fall back on")
	}
	ahead, _, err := lg.AheadBehind(ctx, p.Path, ref.Upstream, ws.Branch)
	if err != nil {
		return err
	}
	if ahead != 0 {
		return fmt.Errorf("%d commit(s) are on no remote", ahead)
	}
	if err := bd.DeleteBranch(ctx, p.Path, ws.Branch, true); err != nil {
		return err
	}
	return e.updateProject(p.Name, func(sp *registry.Project) error {
		for i, pk := range sp.Parked {
			if pk.Branch == ws.Branch {
				sp.Parked = append(sp.Parked[:i:i], sp.Parked[i+1:]...)
				break
			}
		}
		return nil
	})
}

func containsPath(list []string, path string) bool {
	for _, p := range list {
		if samePath(p, path) {
			return true
		}
	}
	return false
}

func containsBranch(list []string, branch string) bool {
	if branch == "" {
		return false
	}
	for _, b := range list {
		if b == branch {
			return true
		}
	}
	return false
}
