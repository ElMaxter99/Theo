package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ElMaxter99/Theo/internal/agents"
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
	// Reason says why it stays; empty when it can be (or was) parked.
	Reason string `json:"reason,omitempty"`
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
	states, err := e.WorktreeStates(ctx, p.Name, StatesOptions{})
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
		if i == 0 || ws.Bare || ws.State != LifeInactive {
			continue
		}
		ent := ParkInactiveEntry{Path: ws.Path, Branch: ws.Branch, IdleDays: ws.IdleDays, Changes: ws.Changes, Unpushed: ws.Unpushed, UpstreamGone: ws.UpstreamGone}
		switch {
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
				}
			}
		}
		report.Entries = append(report.Entries, ent)
	}
	return report, nil
}
