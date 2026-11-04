package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/registry"
)

// CleanOptions configures Clean.
type CleanOptions struct {
	// Down takes down the instance of a merged workspace (containers and
	// networks; volumes are always kept) so it can be closed.
	Down bool
	// DeleteBranches deletes the local branch of each closed worktree, after
	// checking again, right before, that it is still merged. Remote branches
	// are never touched.
	DeleteBranches bool
	// Images removes the images Compose built for each closed instance.
	Images bool
	// Apply closes what can be closed. Without it nothing changes.
	Apply bool
	// Output receives the output of `docker compose down`; nil discards it.
	Output io.Writer
}

// CleanEntry is one workspace in a CleanReport.
type CleanEntry struct {
	Path     string `json:"path"`
	Branch   string `json:"branch,omitempty"`
	Owner    string `json:"owner,omitempty"`
	State    string `json:"state"`
	MergedBy string `json:"merged_by,omitempty"`
	IdleDays int    `json:"idle_days"`
	Note     string `json:"note,omitempty"` // the keep note
	// Reason says why it stays; empty when it can be (or was) closed.
	Reason        string   `json:"reason,omitempty"`
	Closed        bool     `json:"closed"`
	WentDown      bool     `json:"went_down,omitempty"`
	BranchDeleted bool     `json:"branch_deleted,omitempty"`
	ImagesRemoved []string `json:"images_removed,omitempty"`
	// Problems are steps after closing that failed (the branch or an image).
	Problems []string `json:"problems,omitempty"`
}

// CleanReport is the answer of Clean.
type CleanReport struct {
	Project           string `json:"project"`
	Applied           bool   `json:"applied"`
	InactiveAfterDays int    `json:"inactive_after_days"`
	// Close lists the merged and missing workspaces: the ones that can be
	// (or were) closed, and the ones that stay, with why.
	Close []CleanEntry `json:"close"`
	// Inactive lists the inactive workspaces. Clean never closes them: park
	// them, keep them, or remove them one by one.
	Inactive []CleanEntry `json:"inactive"`
	// Kept lists the workspaces marked with `worktree keep`.
	Kept []CleanEntry `json:"kept"`
	// Leftovers are Docker resources and Theo records of workspaces that no
	// longer exist (from `theo reconcile`). With Apply, Theo's own records are
	// forgotten; containers and volumes are only listed, with how to remove them.
	Leftovers []Finding `json:"leftovers"`
	// Forgot counts the records of Leftovers that were forgotten.
	Forgot int      `json:"forgot,omitempty"`
	Notes  []string `json:"notes,omitempty"`
}

// Closable lists the entries that can be closed (or were).
func (r *CleanReport) Closable() []CleanEntry {
	var out []CleanEntry
	for _, c := range r.Close {
		if c.Reason == "" {
			out = append(out, c)
		}
	}
	return out
}

type imageRemover interface {
	ProjectImages(ctx context.Context, project string) ([]compose.Image, error)
	RemoveImage(ctx context.Context, id string) error
}

type branchDeleter interface {
	DeleteBranch(ctx context.Context, dir, branch string, force bool) error
}

type worktreePruner interface {
	PruneWorktrees(ctx context.Context, dir string) error
}

// Clean finds the workspaces of a project that are merged or whose directory
// is gone, and with Apply closes them: a linked worktree is removed with the
// same checks as RemoveWorkspace, a missing one is forgotten. It never closes
// the main worktree, a locked one, a kept one, one in use, one with changes,
// or a clone with commits that exist nowhere else. Inactive workspaces are
// only listed.
func (e *Engine) Clean(ctx context.Context, project string, opts CleanOptions) (*CleanReport, error) {
	p, err := e.Project(project)
	if err != nil {
		return nil, err
	}
	// A listing changes nothing, so the leftovers are looked up at the same
	// time as the states: on Windows each Docker call costs a second or more.
	type driftAnswer struct {
		report *Report
		err    error
	}
	var driftDone chan driftAnswer
	if !opts.Apply {
		driftDone = make(chan driftAnswer, 1)
		go func() { r, err := e.Reconcile(ctx, p.Name); driftDone <- driftAnswer{r, err} }()
	}
	states, err := e.WorktreeStates(ctx, p.Name, StatesOptions{})
	if err != nil {
		return nil, err
	}
	out := opts.Output
	if out == nil {
		out = io.Discard
	}
	report := &CleanReport{Project: p.Name, Applied: opts.Apply, InactiveAfterDays: states.InactiveAfterDays,
		Close: []CleanEntry{}, Inactive: []CleanEntry{}, Kept: []CleanEntry{}, Leftovers: []Finding{}, Notes: states.Notes}

	pruneGit := false
	for i, ws := range states.Worktrees {
		if i == 0 || ws.Bare {
			continue
		}
		entry := CleanEntry{Path: ws.Path, Branch: ws.Branch, Owner: ws.Owner, State: ws.State, MergedBy: ws.MergedBy, IdleDays: ws.IdleDays, Note: ws.KeepNote}
		switch ws.State {
		case LifeInactive:
			report.Inactive = append(report.Inactive, entry)
			continue
		case LifeKept:
			report.Kept = append(report.Kept, entry)
			continue
		case LifeInUse:
			// Merged but in use: say why it stays, or take it down when asked.
			if ws.MergedBy == "" || ws.Changes != 0 {
				continue
			}
			entry.State = LifeMerged
			switch {
			case !ws.Running:
				who := ws.Owner
				if who == "" {
					who = "its owner"
				}
				entry.Reason = fmt.Sprintf("in use (%s, %s ago)", who, agoText(e.now().Sub(ws.LastSeen)))
			case !opts.Down:
				entry.Reason = fmt.Sprintf("its instance is running; add --down to take it down, or `theo down %s -w %s`", p.Name, selectorOf(ws.Worktree.Worktree))
			}
			if entry.Reason != "" {
				report.Close = append(report.Close, entry)
				continue
			}
		case LifeMerged, LifeMissing:
		default:
			continue
		}
		switch {
		case ws.Locked:
			entry.Reason = "locked with git worktree lock"
		case ws.State == LifeMissing && ws.Kind != KindClone:
			pruneGit = true
		}
		if entry.Reason == "" && opts.Apply {
			e.closeOne(ctx, p, ws, &entry, opts, out)
		}
		report.Close = append(report.Close, entry)
	}
	if pruneGit && opts.Apply {
		if pr, ok := e.git.(worktreePruner); ok {
			if err := pr.PruneWorktrees(ctx, p.Path); err != nil {
				report.Notes = append(report.Notes, "git worktree prune failed: "+firstLine(err.Error()))
			} else {
				for i := range report.Close {
					if report.Close[i].State == LifeMissing && report.Close[i].Reason == "" {
						report.Close[i].Closed = true
					}
				}
				_ = e.forgetRecordsOf(p.Name, report.Close)
			}
		} else {
			for i := range report.Close {
				if report.Close[i].State == LifeMissing && report.Close[i].Reason == "" {
					report.Close[i].Reason = fmt.Sprintf("run `git worktree prune` in %s", p.Path)
				}
			}
		}
	}

	var drift *Report
	if driftDone != nil {
		d := <-driftDone
		drift, err = d.report, d.err
	} else {
		drift, err = e.Reconcile(ctx, p.Name)
	}
	if err == nil {
		var forget Report
		for _, f := range drift.Findings {
			switch f.Kind {
			case FindOrphanStack, FindOrphanVolumes, FindStaleSettings, FindStalePorts, FindStaleFiles:
				report.Leftovers = append(report.Leftovers, f)
				if f.Forgettable {
					forget.Findings = append(forget.Findings, f)
				}
			}
		}
		if opts.Apply && len(forget.Findings) > 0 {
			n, err := e.ApplyReconcile(&forget)
			report.Forgot = n
			if err != nil {
				report.Notes = append(report.Notes, "some leftover records could not be forgotten: "+firstLine(err.Error()))
			}
		}
	}
	return report, nil
}

func selectorOf(wt git.Worktree) string {
	if wt.Branch != "" {
		return wt.Branch
	}
	return filepath.Base(wt.Path)
}

// closeOne closes one merged or missing workspace and fills entry.
func (e *Engine) closeOne(ctx context.Context, p registry.Project, ws WorktreeState, entry *CleanEntry, opts CleanOptions, out io.Writer) {
	if ws.State == LifeMissing && ws.Kind != KindClone {
		return // forgotten with git worktree prune, all at once
	}
	composeName := ""
	if inst, err := e.Instance(ctx, p.Name, ws.Path, nil); err == nil {
		composeName = inst.Compose
		if ws.Running && opts.Down {
			if err := e.Down(ctx, inst, out, out); err != nil {
				entry.Reason = "cannot take its instance down: " + firstLine(err.Error())
				return
			}
			entry.WentDown = true
		}
	}
	if _, err := e.RemoveWorkspace(ctx, p.Name, ws.Path, RemoveWorktreeOptions{}); err != nil {
		entry.Reason = firstLine(err.Error())
		return
	}
	entry.Closed = true
	if opts.DeleteBranches && ws.Branch != "" && ws.Kind != KindClone {
		if err := e.deleteMergedBranch(ctx, p, ws); err != nil {
			entry.Problems = append(entry.Problems, "branch "+ws.Branch+" kept: "+firstLine(err.Error()))
		} else {
			entry.BranchDeleted = true
		}
	}
	if opts.Images && composeName != "" {
		if ir, ok := e.compose.(imageRemover); ok {
			images, err := ir.ProjectImages(ctx, composeName)
			if err != nil {
				entry.Problems = append(entry.Problems, "cannot list its images: "+firstLine(err.Error()))
			}
			for _, img := range images {
				if err := ir.RemoveImage(ctx, img.ID); err != nil {
					entry.Problems = append(entry.Problems, "image "+img.Name+" kept: "+firstLine(err.Error()))
				} else {
					entry.ImagesRemoved = append(entry.ImagesRemoved, img.Name)
				}
			}
		}
	}
}

// deleteMergedBranch deletes the local branch of a closed worktree after
// checking again that it is merged into its base: `git branch -d` when it is
// in the base, `-D` when it is merged by content (Git cannot see that).
func (e *Engine) deleteMergedBranch(ctx context.Context, p registry.Project, ws WorktreeState) error {
	bd, ok := e.git.(branchDeleter)
	lg, ok2 := e.git.(lifecycleGit)
	if !ok || !ok2 {
		return errors.New("this Git adapter cannot delete branches")
	}
	if ws.Base == "" {
		return errors.New("its base is unknown")
	}
	var bases []string
	for _, b := range []string{ws.Base, "origin/" + ws.Base} {
		if lg.Resolves(ctx, p.Path, b) {
			bases = append(bases, b)
		}
	}
	if len(bases) == 0 {
		return fmt.Errorf("its base %s no longer exists", ws.Base)
	}
	ahead, _, err := lg.AheadBehind(ctx, p.Path, bases[0], ws.Branch)
	if err != nil {
		return err
	}
	by, _ := e.mergedBy(ctx, lg, p.Path, bases, ws.Branch, ahead)
	switch by {
	case MergedAncestor:
		if err := bd.DeleteBranch(ctx, p.Path, ws.Branch, false); err != nil {
			// -d compares with HEAD of the main worktree or the upstream, which
			// may not be the base; the check above already proved it merged.
			return bd.DeleteBranch(ctx, p.Path, ws.Branch, true)
		}
		return nil
	case MergedContent:
		return bd.DeleteBranch(ctx, p.Path, ws.Branch, true)
	default:
		return errors.New("it is no longer merged into " + ws.Base)
	}
}

// forgetRecordsOf removes the registry records of closed entries.
func (e *Engine) forgetRecordsOf(project string, entries []CleanEntry) error {
	return e.updateProject(project, func(sp *registry.Project) error {
		for _, c := range entries {
			if !c.Closed {
				continue
			}
			if _, i := workspaceRecord(*sp, c.Path); i >= 0 {
				sp.Workspaces = append(sp.Workspaces[:i:i], sp.Workspaces[i+1:]...)
			}
		}
		return nil
	})
}

// KeepOptions configures Keep.
type KeepOptions struct {
	Until time.Time // zero: until cleared
	Note  string
	Clear bool
}

// Keep marks a workspace so it is never offered for cleaning (or, with
// Clear, removes the mark).
func (e *Engine) Keep(ctx context.Context, project, selector string, opts KeepOptions) (registry.Workspace, error) {
	note := strings.TrimSpace(opts.Note)
	if len([]rune(note)) > 200 {
		return registry.Workspace{}, errors.New("the note is too long (200 characters at most)")
	}
	for _, r := range note {
		if unicode.IsControl(r) {
			return registry.Workspace{}, errors.New("the note must not contain control characters")
		}
	}
	if !opts.Until.IsZero() && !opts.Until.After(e.now()) {
		return registry.Workspace{}, errors.New("--until must be a date in the future")
	}
	return e.updateRecord(ctx, project, selector, func(rec *registry.Workspace) {
		if opts.Clear {
			rec.Kept, rec.KeptUntil, rec.KeepNote = false, time.Time{}, ""
			return
		}
		rec.Kept, rec.KeptUntil, rec.KeepNote = true, opts.Until, note
	})
}

// updateRecord applies fn to the record of a non-main workspace and stores it.
func (e *Engine) updateRecord(ctx context.Context, project, selector string, fn func(*registry.Workspace)) (registry.Workspace, error) {
	p, err := e.Project(project)
	if err != nil {
		return registry.Workspace{}, err
	}
	list, err := e.worktreeList(ctx, p)
	if err != nil {
		return registry.Workspace{}, err
	}
	wt, err := selectWorktree(list, selector)
	if err != nil {
		return registry.Workspace{}, fmt.Errorf("project %q: %w", p.Name, err)
	}
	kind := workspaceKind(p, list, wt.Path)
	if kind == KindMain {
		return registry.Workspace{}, fmt.Errorf("%s is the main worktree of %q; it is never cleaned, so it needs no mark", wt.Path, p.Name)
	}
	var out registry.Workspace
	err = e.updateProject(p.Name, func(sp *registry.Project) error {
		rec, _ := workspaceRecord(*sp, wt.Path)
		if rec.Path == "" {
			rec = registry.Workspace{Path: wt.Path, Kind: kind}
		}
		fn(&rec)
		putRecord(sp, rec)
		out = rec
		return nil
	})
	return out, err
}

// Touch records that the owner of a workspace called Theo's MCP server about
// it now, so the workspace counts as in use for InUseWindow. It only touches
// a workspace owned by owner, and writes at most once a minute per workspace.
func (e *Engine) Touch(ctx context.Context, project, selector, owner string) error {
	p, err := e.Project(project)
	if err != nil {
		return err
	}
	now := e.now().UTC().Truncate(time.Second)
	list, err := e.gitWorktrees(ctx, p.Path)
	if err != nil {
		return err
	}
	all, err := e.worktreeList(ctx, p)
	if err != nil {
		all = list
	}
	wt, err := selectWorktree(all, selector)
	if err != nil || workspaceKind(p, all, wt.Path) == KindMain {
		return err
	}
	rec, i := workspaceRecord(p, wt.Path)
	if i < 0 || !strings.EqualFold(rec.Owner, strings.TrimSpace(owner)) || now.Sub(rec.LastSeen) < time.Minute {
		return nil
	}
	_, err = e.updateRecord(ctx, p.Name, wt.Path, func(rec *registry.Workspace) { rec.LastSeen = now })
	return err
}

// WorktreeBase returns the base of the branch of a workspace and whether it
// was recorded (otherwise it is the main worktree's branch).
func (e *Engine) WorktreeBase(ctx context.Context, project, selector string) (branch, base string, recorded bool, err error) {
	p, err := e.Project(project)
	if err != nil {
		return "", "", false, err
	}
	list, err := e.worktreeList(ctx, p)
	if err != nil {
		return "", "", false, err
	}
	wt, err := selectWorktree(list, selector)
	if err != nil {
		return "", "", false, fmt.Errorf("project %q: %w", p.Name, err)
	}
	if wt.Branch == "" {
		return "", "", false, fmt.Errorf("%s has no branch checked out (detached HEAD)", wt.Path)
	}
	repo := p.Path
	if workspaceKind(p, list, wt.Path) == KindClone {
		repo = wt.Path
	}
	if lg, ok := e.git.(lifecycleGit); ok {
		if refs, err := lg.LocalBranches(ctx, repo); err == nil && refs[wt.Branch].Base != "" {
			return wt.Branch, refs[wt.Branch].Base, true, nil
		}
	}
	return wt.Branch, list[0].Branch, false, nil
}

// SetWorktreeBase records the base of the branch of a workspace (an empty
// base removes the record, so the main worktree's branch is used).
func (e *Engine) SetWorktreeBase(ctx context.Context, project, selector, base string) (string, error) {
	p, err := e.Project(project)
	if err != nil {
		return "", err
	}
	list, err := e.worktreeList(ctx, p)
	if err != nil {
		return "", err
	}
	wt, err := selectWorktree(list, selector)
	if err != nil {
		return "", fmt.Errorf("project %q: %w", p.Name, err)
	}
	if wt.Branch == "" {
		return "", fmt.Errorf("%s has no branch checked out (detached HEAD)", wt.Path)
	}
	if base == wt.Branch {
		return "", errors.New("a branch cannot be its own base")
	}
	repo := p.Path
	if workspaceKind(p, list, wt.Path) == KindClone {
		repo = wt.Path
	}
	s, ok := e.git.(baseSetter)
	lg, ok2 := e.git.(lifecycleGit)
	if !ok || !ok2 {
		return "", errors.New("this Git adapter cannot record bases")
	}
	if base != "" && !lg.Resolves(ctx, repo, base) && !lg.Resolves(ctx, repo, "origin/"+base) {
		return "", fmt.Errorf("%s is not a branch of %q", base, p.Name)
	}
	return wt.Branch, s.SetBase(ctx, repo, wt.Branch, base)
}

// ParkOptions configures Park.
type ParkOptions struct {
	Note string
	// Confirm is the directory name, needed when the worktree has ignored
	// files (such as .env or node_modules) that removing it deletes.
	Confirm string
}

// ParkError is returned by Park when the worktree has ignored files and the
// directory name was not given as confirmation.
type ParkError struct {
	Path    string
	Name    string
	Ignored []string
}

func (p *ParkError) Error() string {
	shown := p.Ignored
	if len(shown) > 5 {
		shown = append(append([]string{}, shown[:5]...), fmt.Sprintf("and %d more", len(p.Ignored)-5))
	}
	return fmt.Sprintf("%s has files Git ignores, which parking deletes: %s. Repeat with --confirm %s to park it anyway", p.Path, strings.Join(shown, ", "), p.Name)
}

// Code is the error code of the API and MCP.
func (p *ParkError) Code() string { return "confirmation_required" }

type ignoredLister interface {
	IgnoredEntries(ctx context.Context, dir string) ([]string, error)
}

// ParkResult is the answer of Park.
type ParkResult struct {
	registry.Parked
	// WentDown is the Compose project of the instance Park took down, if any
	// (containers and networks; volumes are kept).
	WentDown string `json:"went_down,omitempty"`
	// Unpushed counts the commits of the branch that are on no remote.
	Unpushed int `json:"unpushed"`
}

// Park puts away a linked worktree whose work is unfinished: it takes its
// instance down (volumes are kept), removes the directory, keeps the branch,
// and records it so Resume brings it back. It refuses uncommitted or
// untracked changes (commit them first), the main worktree and clones.
func (e *Engine) Park(ctx context.Context, project, selector string, opts ParkOptions) (ParkResult, error) {
	p, err := e.Project(project)
	if err != nil {
		return ParkResult{}, err
	}
	list, err := e.worktreeList(ctx, p)
	if err != nil {
		return ParkResult{}, err
	}
	wt, err := selectWorktree(list, selector)
	if err != nil {
		return ParkResult{}, fmt.Errorf("project %q: %w", p.Name, err)
	}
	switch workspaceKind(p, list, wt.Path) {
	case KindMain:
		return ParkResult{}, fmt.Errorf("%s is the main worktree of %q; it cannot be parked", wt.Path, p.Name)
	case KindClone:
		return ParkResult{}, fmt.Errorf("%s is an independent clone; only linked worktrees can be parked (their branch stays in the project)", wt.Path)
	}
	if wt.Branch == "" {
		return ParkResult{}, fmt.Errorf("%s has no branch checked out (detached HEAD), so nothing would bring it back; create a branch there first", wt.Path)
	}
	if wt.Prunable {
		return ParkResult{}, fmt.Errorf("the directory of %s no longer exists; `theo worktree clean %s` forgets it", wt.Path, p.Name)
	}
	if wt.Locked {
		return ParkResult{}, fmt.Errorf("%s is locked with git worktree lock", wt.Path)
	}
	changes, err := e.git.Changes(ctx, wt.Path)
	if err != nil {
		return ParkResult{}, err
	}
	if len(changes) > 0 {
		return ParkResult{}, fmt.Errorf("%s has %d uncommitted or untracked file(s); commit them (a work-in-progress commit is fine) and park it again, so nothing is lost", wt.Path, len(changes))
	}
	name := filepath.Base(wt.Path)
	if il, ok := e.git.(ignoredLister); ok {
		ignored, err := il.IgnoredEntries(ctx, wt.Path)
		if err != nil {
			return ParkResult{}, err
		}
		if len(ignored) > 0 && opts.Confirm != name {
			return ParkResult{}, &ParkError{Path: wt.Path, Name: name, Ignored: ignored}
		}
	}
	note := strings.TrimSpace(opts.Note)
	if len([]rune(note)) > 200 {
		return ParkResult{}, errors.New("the note is too long (200 characters at most)")
	}
	_, base, _, _ := e.WorktreeBase(ctx, p.Name, wt.Path)
	rec, _ := workspaceRecord(p, wt.Path)
	parked := registry.Parked{Branch: wt.Branch, Path: wt.Path, Base: base, Owner: rec.Owner, Note: note, ParkedAt: e.now().UTC().Truncate(time.Second)}

	res := ParkResult{Parked: parked}
	if lg, ok := e.git.(lifecycleGit); ok {
		in := stateInput{p: p, wt: Worktree{Worktree: wt, Kind: KindWorktree}, rec: rec, mainBranch: list[0].Branch, now: e.now(), threshold: InactiveAfter(p)}
		in.mainRefs, _ = lg.LocalBranches(ctx, p.Path)
		l, _ := e.lifecycleOf(ctx, lg, in)
		res.Unpushed = l.Unpushed
	}
	if overview, err := e.Overview(ctx, p.Name); err == nil {
		for _, o := range overview {
			if samePath(o.Worktree.Path, wt.Path) && len(o.Stacks) > 0 {
				inst, err := e.Instance(ctx, p.Name, wt.Path, nil)
				if err != nil {
					return ParkResult{}, fmt.Errorf("its instance has containers and cannot be taken down: %w", err)
				}
				if err := e.Down(ctx, inst, io.Discard, io.Discard); err != nil {
					return ParkResult{}, fmt.Errorf("cannot take its instance down: %w", err)
				}
				res.WentDown = inst.Compose
			}
		}
	}

	// RemoveWorkspace checks the containers and the shared services again.
	// Ignored files do not stop git worktree remove.
	if _, err := e.RemoveWorkspace(ctx, p.Name, wt.Path, RemoveWorktreeOptions{}); err != nil {
		return res, err
	}
	if dirExists(wt.Path) {
		_ = os.RemoveAll(wt.Path) // what Git left: ignored files, already confirmed
	}
	err = e.updateProject(p.Name, func(sp *registry.Project) error {
		for i, pk := range sp.Parked {
			if pk.Branch == parked.Branch {
				sp.Parked = append(sp.Parked[:i:i], sp.Parked[i+1:]...)
				break
			}
		}
		sp.Parked = append(sp.Parked, parked)
		return nil
	})
	if err != nil {
		return res, fmt.Errorf("%s was removed and branch %s kept, but it could not be recorded as parked: %w. `theo worktree add %s %s` brings it back", wt.Path, wt.Branch, err, p.Name, wt.Branch)
	}
	return res, nil
}

// ResumeOptions configures Resume.
type ResumeOptions struct {
	Copy bool // copy the project's CopyOnAdd files, like `worktree add --copy`
}

// Resume brings back a parked worktree: it creates it again for its branch,
// in the same directory when that is free, with the same owner and note.
func (e *Engine) Resume(ctx context.Context, project, branch string, opts ResumeOptions) (*AddedWorktree, error) {
	p, err := e.Project(project)
	if err != nil {
		return nil, err
	}
	var parked registry.Parked
	found := false
	for _, pk := range p.Parked {
		if pk.Branch == branch {
			parked, found = pk, true
		}
	}
	if !found {
		names := make([]string, len(p.Parked))
		for i, pk := range p.Parked {
			names[i] = pk.Branch
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("project %q has no parked worktrees", p.Name)
		}
		return nil, fmt.Errorf("%s is not parked in %q; parked: %s", branch, p.Name, strings.Join(names, ", "))
	}
	path := parked.Path
	if entries, err := os.ReadDir(path); err == nil && len(entries) > 0 {
		path = "" // taken: let AddWorktree choose
	}
	added, err := e.AddWorktree(ctx, p.Name, branch, AddWorktreeOptions{Path: path, Owner: parked.Owner, Copy: opts.Copy && len(p.CopyOnAdd) > 0})
	if err != nil {
		return nil, err
	}
	if err := e.updateProject(p.Name, func(sp *registry.Project) error {
		for i, pk := range sp.Parked {
			if pk.Branch == branch {
				sp.Parked = append(sp.Parked[:i:i], sp.Parked[i+1:]...)
				break
			}
		}
		if parked.Note != "" {
			if rec, i := workspaceRecord(*sp, added.Path); i >= 0 {
				rec.KeepNote = parked.Note
				putRecord(sp, rec)
			}
		}
		return nil
	}); err != nil {
		added.Notes = append(added.Notes, "the worktree is back, but it is still listed as parked: "+err.Error())
	}
	return added, nil
}

// FinishOptions configures FinishWorktree.
type FinishOptions struct {
	Owner string // who asks: must be the owner of the workspace
}

// FinishWorktree lets an agent close the workspace it owns once its work is
// done: only when the caller is its owner, it is merged or pushed, and it
// has no changes and no running instance. A pushed branch stays in the
// repository and on its remote.
func (e *Engine) FinishWorktree(ctx context.Context, project, selector string, opts FinishOptions) (WorktreeState, error) {
	if strings.TrimSpace(opts.Owner) == "" {
		return WorktreeState{}, errors.New("say who you are (owner): only the owner of a worktree can finish it")
	}
	states, err := e.WorktreeStates(ctx, project, StatesOptions{})
	if err != nil {
		return WorktreeState{}, err
	}
	all := make([]git.Worktree, len(states.Worktrees))
	for i, w := range states.Worktrees {
		all[i] = w.Worktree.Worktree
	}
	wt, err := selectWorktree(all, selector)
	if err != nil {
		return WorktreeState{}, fmt.Errorf("project %q: %w", states.Project, err)
	}
	var ws WorktreeState
	for i, w := range states.Worktrees {
		if samePath(w.Path, wt.Path) {
			if i == 0 {
				return w, errors.New("the main worktree is never finished")
			}
			ws = w
		}
	}
	if !strings.EqualFold(ws.Owner, strings.TrimSpace(opts.Owner)) {
		if ws.Owner == "" {
			return ws, fmt.Errorf("%s has no owner; only a worktree created with an owner can be finished by its agent", ws.Path)
		}
		return ws, fmt.Errorf("%s belongs to %s, not to %s", ws.Path, ws.Owner, opts.Owner)
	}
	if ws.Running {
		return ws, fmt.Errorf("its instance is running; stop it first with `down`")
	}
	if ws.Changes != 0 {
		return ws, fmt.Errorf("%s has %d uncommitted or untracked file(s); commit and push them first", ws.Path, ws.Changes)
	}
	if ws.State == LifeKept {
		return ws, fmt.Errorf("%s is kept (`theo worktree keep`); clear the mark first", ws.Path)
	}
	// The call itself made it in use, so decide on the facts, not the state.
	pushed := ws.Upstream != "" && !ws.UpstreamGone && ws.Unpushed == 0
	if ws.MergedBy == "" && !pushed {
		return ws, fmt.Errorf("%s is %s (%s); push its branch first, so no work is lost", ws.Path, ws.State, ws.Reason)
	}
	if _, err := e.RemoveWorkspace(ctx, states.Project, ws.Path, RemoveWorktreeOptions{}); err != nil {
		return ws, err
	}
	return ws, nil
}
