package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ElMaxter99/Theo/internal/execx"
	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/registry"
)

// OriginTheo is the origin of the workspaces Theo created (registry.Workspace.Origin).
const OriginTheo = "theo"

// Lifecycle states of a workspace (ADR-0016), in the order they are checked:
// a workspace gets the first one that applies. The main worktree has none.
const (
	LifeMissing  = "missing"  // Git lists it but its directory is gone
	LifeInUse    = "in-use"   // its instance runs, or its owner called MCP recently
	LifeKept     = "kept"     // marked with `theo worktree keep`
	LifeMerged   = "merged"   // no changes, and its branch is in the base
	LifeInactive = "inactive" // not merged, and no activity for longer than the threshold
	LifeUnpushed = "unpushed" // changes, or commits that are on no remote
	LifeNew      = "new"      // no commits of its own yet
	LifePushed   = "pushed"   // everything is on its remote and nothing changed
)

// LifecycleStates lists the states in the order they are checked.
var LifecycleStates = []string{LifeMissing, LifeInUse, LifeKept, LifeMerged, LifeInactive, LifeUnpushed, LifeNew, LifePushed}

// DefaultInactiveDays is the inactivity threshold of a project that sets none.
const DefaultInactiveDays = 7

// MaxInactiveDays is the largest threshold `project set --inactive-after` accepts.
const MaxInactiveDays = 90

// InUseWindow is how recent an MCP call of the owner must be for the
// workspace to count as in use.
const InUseWindow = 30 * time.Minute

// How a branch counts as merged (Lifecycle.MergedBy).
const (
	MergedAncestor = "ancestor" // every commit of the branch is in the base
	MergedContent  = "content"  // merging it would change nothing (squash or rebase merge)
)

// Where the activity date of a workspace comes from (Lifecycle.ActivitySource).
const (
	ActivityCommit   = "commit"
	ActivityCreated  = "created"
	ActivityInstance = "instance"
	ActivityMCP      = "mcp"
	ActivityFiles    = "files"
)

// Lifecycle is what Theo knows about where a workspace is in its life.
type Lifecycle struct {
	State string `json:"state,omitempty"` // empty for the main worktree
	// ActivityAt is the most recent activity, and ActivitySource where it
	// comes from (commit, created, instance, mcp or files).
	ActivityAt     time.Time `json:"activity_at,omitzero"`
	ActivitySource string    `json:"activity_source,omitempty"`
	// IdleDays is how many whole days ago ActivityAt was.
	IdleDays int `json:"idle_days"`
	// Base is the branch the workspace's branch is compared with: the one
	// recorded when it was created (BaseRecorded) or the branch of the main
	// worktree.
	Base         string `json:"base,omitempty"`
	BaseRecorded bool   `json:"base_recorded,omitempty"`
	// Ahead counts the commits of the branch that the base lacks; Behind the
	// commits of the base that the branch lacks. -1 when the base is unknown.
	Ahead  int `json:"ahead"`
	Behind int `json:"behind"`
	// Upstream is the remote branch it tracks; UpstreamGone is true when that
	// branch was deleted (usually after its pull request was merged).
	Upstream     string `json:"upstream,omitempty"`
	UpstreamGone bool   `json:"upstream_gone,omitempty"`
	// Unpushed counts the commits that are not on Upstream (all of its own
	// commits when there is none).
	Unpushed int `json:"unpushed"`
	// MergedBy is "ancestor" or "content" when the branch is merged into the base.
	MergedBy string `json:"merged_by,omitempty"`
	// Changes counts uncommitted and untracked files.
	Changes int `json:"changes"`
	// Running is true when its instance has running containers.
	Running bool `json:"running,omitempty"`
	// Origin is "theo" when Theo created it.
	Origin    string    `json:"origin,omitempty"`
	CreatedAt time.Time `json:"created_at,omitzero"`
	LastSeen  time.Time `json:"last_seen,omitzero"`
	Kept      bool      `json:"kept,omitempty"`
	KeptUntil time.Time `json:"kept_until,omitzero"`
	KeepNote  string    `json:"keep_note,omitempty"`
	// SizeBytes is the size of its directory; only with StatesOptions.Size.
	SizeBytes int64 `json:"size_bytes,omitempty"`
	// Reason says, in one line, why it has this state.
	Reason string `json:"reason,omitempty"`
	// Problem is set when part of the state could not be read.
	Problem string `json:"problem,omitempty"`
}

// WorktreeState is a workspace with its lifecycle.
type WorktreeState struct {
	Worktree
	Lifecycle
}

// StatesOptions configures WorktreeStates.
type StatesOptions struct {
	Size bool // also measure the size of each directory (slow on big worktrees)
}

// StatesReport is the answer of WorktreeStates.
type StatesReport struct {
	Project string `json:"project"`
	// InactiveAfterDays is the threshold of the project.
	InactiveAfterDays int             `json:"inactive_after_days"`
	Worktrees         []WorktreeState `json:"worktrees"`
	Parked            []ParkedEntry   `json:"parked"`
	// Notes are things that limit the answer, such as Docker being off (so
	// in-use only counts MCP calls) or a Git too old to see squash merges.
	Notes []string `json:"notes,omitempty"`
}

// ParkedEntry is a parked worktree.
type ParkedEntry struct {
	registry.Parked
	// BranchGone is true when the branch no longer exists, so it cannot be resumed.
	BranchGone bool `json:"branch_gone,omitempty"`
}

// Count returns how many workspaces are in state.
func (r *StatesReport) Count(state string) int {
	n := 0
	for _, w := range r.Worktrees {
		if w.State == state {
			n++
		}
	}
	return n
}

// lifecycleGit is what the Git adapter offers to tell the lifecycle of a
// workspace. It is optional so that the Git interface of the engine (and its
// fakes) does not grow; without it the states are limited to what the
// engine knows by itself.
type lifecycleGit interface {
	LocalBranches(ctx context.Context, dir string) (map[string]git.BranchRef, error)
	Resolves(ctx context.Context, dir, rev string) bool
	AheadBehind(ctx context.Context, dir, base, branch string) (ahead, behind int, err error)
	LastCommitTime(ctx context.Context, dir, base, branch string) (time.Time, bool, error)
	BranchMoves(ctx context.Context, dir, branch string) int
	ContentMerged(ctx context.Context, dir, base, branch string) (bool, error)
	Commit(ctx context.Context, dir, rev string) (string, error)
}

// baseSetter records the base of a new branch.
type baseSetter interface {
	SetBase(ctx context.Context, dir, branch, base string) error
}

// recordBase records the base of a branch Theo just created: the one asked
// for, or the branch checked out in the main worktree. It is best effort:
// without it the base is the main worktree's branch anyway.
func (e *Engine) recordBase(ctx context.Context, repo, branch, base, mainBranch string) {
	s, ok := e.git.(baseSetter)
	if !ok {
		return
	}
	if base == "" {
		base = mainBranch
	}
	if base == "" || base == branch {
		return
	}
	_ = s.SetBase(ctx, repo, branch, base)
}

// InactiveAfter returns the inactivity threshold of a project in days.
func InactiveAfter(p registry.Project) int {
	if p.InactiveAfterDays <= 0 {
		return DefaultInactiveDays
	}
	return p.InactiveAfterDays
}

// SetInactiveAfter sets the inactivity threshold of a project: 1 to 90
// days, or 0 for the default.
func (e *Engine) SetInactiveAfter(name string, days int) (registry.Project, error) {
	if days < 0 || days > MaxInactiveDays {
		return registry.Project{}, fmt.Errorf("the inactivity threshold must be between 1 and %d days (0 restores the default, %d)", MaxInactiveDays, DefaultInactiveDays)
	}
	var out registry.Project
	err := e.updateProject(name, func(p *registry.Project) error {
		p.InactiveAfterDays = days
		out = *p
		return nil
	})
	return out, err
}

// ParseDays parses a number of days written as "7", "7d" or "7 days".
func ParseDays(s string) (int, error) {
	t := strings.TrimSpace(strings.ToLower(s))
	for _, suf := range []string{" days", " day", "days", "day", "d"} {
		if strings.HasSuffix(t, suf) {
			t = strings.TrimSpace(strings.TrimSuffix(t, suf))
			break
		}
	}
	var n int
	if _, err := fmt.Sscanf(t, "%d", &n); err != nil || fmt.Sprint(n) != t {
		return 0, fmt.Errorf("%q is not a number of days (for example 7d)", s)
	}
	return n, nil
}

// WorktreeStates returns every workspace of a project with its lifecycle,
// the main worktree first, and the parked worktrees. It only reads: Git,
// Docker (to tell which instances run; when Docker is off that part is
// skipped and said in Notes) and Theo's own records.
func (e *Engine) WorktreeStates(ctx context.Context, project string, opts StatesOptions) (*StatesReport, error) {
	p, err := e.Project(project)
	if err != nil {
		return nil, err
	}
	list, err := e.Worktrees(ctx, p.Name)
	if err != nil {
		return nil, err
	}
	now := e.now()
	threshold := InactiveAfter(p)
	report := &StatesReport{Project: p.Name, InactiveAfterDays: threshold, Worktrees: make([]WorktreeState, len(list)), Parked: []ParkedEntry{}}

	running := map[int]bool{}
	if overview, err := e.Overview(ctx, p.Name); err != nil {
		report.Notes = append(report.Notes, "Docker could not be asked, so in-use only counts recent calls of agents through MCP")
	} else {
		for i, o := range overview {
			for _, s := range o.Stacks {
				if strings.Contains(strings.ToLower(s.Status), "running") {
					running[i] = true
				}
			}
		}
	}
	lastEvent := map[string]time.Time{}
	if events, err := e.settings.Events(); err == nil {
		for _, ev := range events {
			if ev.Project != "" && !strings.EqualFold(ev.Project, p.Name) {
				continue
			}
			for _, wt := range list {
				if samePath(ev.Workspace, wt.Path) && ev.Time.After(lastEvent[wt.Path]) {
					lastEvent[wt.Path] = ev.Time
				}
			}
		}
	}

	lg, _ := e.git.(lifecycleGit)
	if lg == nil {
		report.Notes = append(report.Notes, "this Git adapter cannot compare branches, so merged, pushed and new are not told apart")
	}
	mainBranch := ""
	if len(list) > 0 {
		mainBranch = list[0].Branch
	}
	var mainRefs map[string]git.BranchRef
	if lg != nil {
		if mainRefs, err = lg.LocalBranches(ctx, p.Path); err != nil {
			report.Notes = append(report.Notes, "cannot list the branches: "+firstLine(err.Error()))
		}
	}

	var (
		mu          sync.Mutex
		wg          sync.WaitGroup
		noMergeTree bool
	)
	par := dirtyParallelism
	if execx.Explaining() {
		par = 1
	}
	sem := make(chan struct{}, par)
	for i, wt := range list {
		report.Worktrees[i].Worktree = wt
		if i == 0 || wt.Bare {
			continue
		}
		rec, _ := workspaceRecord(p, wt.Path)
		ws := &report.Worktrees[i]
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			in := stateInput{
				p: p, wt: wt, rec: rec, mainBranch: mainBranch, mainRefs: mainRefs,
				running: running[i], lastEvent: lastEvent[wt.Path], now: now, threshold: threshold, size: opts.Size,
			}
			l, unsupported := e.lifecycleOf(ctx, lg, in)
			ws.Lifecycle = l
			if unsupported {
				mu.Lock()
				noMergeTree = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if noMergeTree {
		report.Notes = append(report.Notes, git.ErrMergeTreeUnsupported.Error()+"; branches merged that way show as inactive, unpushed or pushed")
	}
	e.merges().save()

	for _, pk := range p.Parked {
		entry := ParkedEntry{Parked: pk}
		if lg != nil && mainRefs != nil {
			_, ok := mainRefs[pk.Branch]
			entry.BranchGone = !ok
		}
		report.Parked = append(report.Parked, entry)
	}
	return report, nil
}

type stateInput struct {
	p          registry.Project
	wt         Worktree
	rec        registry.Workspace
	mainBranch string
	mainRefs   map[string]git.BranchRef
	running    bool
	lastEvent  time.Time
	now        time.Time
	threshold  int
	size       bool
}

// lifecycleOf reads the facts of one workspace and classifies it. The second
// result is true when Git could not check merges by content.
func (e *Engine) lifecycleOf(ctx context.Context, lg lifecycleGit, in stateInput) (Lifecycle, bool) {
	wt := in.wt
	l := Lifecycle{
		Ahead: -1, Behind: -1, Origin: in.rec.Origin, CreatedAt: in.rec.CreatedAt, LastSeen: in.rec.LastSeen,
		Kept: in.rec.Kept, KeptUntil: in.rec.KeptUntil, KeepNote: in.rec.KeepNote, Running: in.running,
	}
	f := lifeFacts{
		Missing: wt.Prunable, Running: in.running, LastSeen: in.rec.LastSeen,
		Kept: in.rec.Kept, KeptUntil: in.rec.KeptUntil, Ahead: -1,
	}
	activity := func(t time.Time, src string) {
		if !t.IsZero() && t.After(l.ActivityAt) {
			l.ActivityAt, l.ActivitySource = t, src
		}
	}
	created := in.rec.CreatedAt
	if c, ok := git.WorktreeCreated(wt.Path); ok && (created.IsZero() || c.Before(created)) {
		created = c
	}
	if l.CreatedAt.IsZero() {
		l.CreatedAt = created
	}
	activity(created, ActivityCreated)
	activity(in.lastEvent, ActivityInstance)
	activity(in.rec.LastSeen, ActivityMCP)

	unsupported := false
	if !wt.Prunable {
		changes, err := e.git.Changes(ctx, wt.Path)
		if err != nil {
			l.Problem = "cannot read its files: " + firstLine(err.Error())
			f.Changes = -1
		} else {
			l.Changes, f.Changes = len(changes), len(changes)
			activity(changesTime(wt.Path, changes), ActivityFiles)
		}
		if in.size {
			l.SizeBytes = dirSize(wt.Path)
		}
		if lg != nil {
			unsupported = e.branchFacts(ctx, lg, in, &l, &f, activity)
		}
	}
	f.Activity = l.ActivityAt
	if !l.ActivityAt.IsZero() {
		l.IdleDays = int(in.now.Sub(l.ActivityAt) / (24 * time.Hour))
		if l.IdleDays < 0 {
			l.IdleDays = 0
		}
	}
	l.State, l.Reason = classify(f, in.now, in.threshold)
	return l, unsupported
}

// branchFacts compares the branch of a workspace with its base and its
// upstream.
func (e *Engine) branchFacts(ctx context.Context, lg lifecycleGit, in stateInput, l *Lifecycle, f *lifeFacts, activity func(time.Time, string)) (unsupported bool) {
	wt := in.wt
	repo, refs := in.p.Path, in.mainRefs
	if wt.Kind == KindClone {
		repo = wt.Path
		refs, _ = lg.LocalBranches(ctx, repo)
	}
	rev := wt.Branch
	if rev == "" {
		rev = wt.Head // detached
	}
	if rev == "" {
		return false
	}
	ref, isBranch := refs[wt.Branch]
	isBranch = isBranch && wt.Branch != ""
	l.Base = in.mainBranch
	if isBranch && ref.Base != "" {
		l.Base, l.BaseRecorded = ref.Base, true
	}
	if isBranch {
		l.Upstream, l.UpstreamGone = ref.Upstream, ref.Gone
	}
	var bases []string
	if l.Base != "" {
		if lg.Resolves(ctx, repo, l.Base) {
			bases = append(bases, l.Base)
		}
		if !strings.HasPrefix(l.Base, "origin/") && lg.Resolves(ctx, repo, "origin/"+l.Base) {
			bases = append(bases, "origin/"+l.Base)
		}
	}
	if len(bases) == 0 {
		if l.Base == "" {
			l.Problem = "no base to compare with: the main worktree has no branch; set one with `theo worktree base`"
		} else {
			l.Problem = fmt.Sprintf("its base %s does not exist; set another with `theo worktree base`", l.Base)
		}
	} else {
		ahead, behind, err := lg.AheadBehind(ctx, repo, bases[0], rev)
		if err != nil {
			l.Problem = "cannot compare it with " + bases[0] + ": " + firstLine(err.Error())
		} else {
			l.Ahead, l.Behind, f.Ahead = ahead, behind, ahead
			if ahead > 0 {
				if t, ok, err := lg.LastCommitTime(ctx, repo, bases[0], rev); err == nil && ok {
					activity(t, ActivityCommit)
				}
			}
			f.MergedBy, unsupported = e.mergedBy(ctx, lg, repo, bases, rev, ahead)
			if f.MergedBy == MergedAncestor && ahead == 0 && isBranch && (ref.Upstream == "" || ref.Gone) && lg.BranchMoves(ctx, repo, wt.Branch) <= 1 {
				// Created and never moved: it is new, not merged.
				f.MergedBy, f.NotStarted = "", true
			}
			if ahead == 0 && f.MergedBy == "" {
				f.NotStarted = true
			}
			l.MergedBy = f.MergedBy
		}
	}
	switch {
	case l.Upstream != "" && !l.UpstreamGone:
		f.HasUpstream = true
		if a, _, err := lg.AheadBehind(ctx, repo, l.Upstream, rev); err == nil {
			l.Unpushed, f.Unpushed = a, a
		}
	case l.Ahead > 0:
		l.Unpushed = l.Ahead
	}
	return unsupported
}

// mergedBy tells whether rev is merged into any of bases: by ancestry (ahead
// is its count against bases[0]) or by content.
func (e *Engine) mergedBy(ctx context.Context, lg lifecycleGit, repo string, bases []string, rev string, ahead int) (string, bool) {
	if ahead == 0 {
		return MergedAncestor, false
	}
	for _, b := range bases[1:] {
		if a, _, err := lg.AheadBehind(ctx, repo, b, rev); err == nil && a == 0 {
			return MergedAncestor, false
		}
	}
	tip, err := lg.Commit(ctx, repo, rev)
	if err != nil {
		return "", false
	}
	for _, b := range bases {
		bc, err := lg.Commit(ctx, repo, b)
		if err != nil {
			continue
		}
		key := bc + ":" + tip
		if v, ok := e.merges().get(key); ok {
			if v {
				return MergedContent, false
			}
			continue
		}
		ok, err := lg.ContentMerged(ctx, repo, b, rev)
		if errors.Is(err, git.ErrMergeTreeUnsupported) {
			return "", true
		}
		if err != nil {
			continue
		}
		e.merges().put(key, ok)
		if ok {
			return MergedContent, false
		}
	}
	return "", false
}

// lifeFacts are the facts classify decides on.
type lifeFacts struct {
	Missing    bool
	Running    bool
	LastSeen   time.Time
	Kept       bool
	KeptUntil  time.Time
	Changes    int // -1 when unknown
	Ahead      int // -1 when unknown
	MergedBy   string
	NotStarted bool // no commits of its own, and never had any
	Unpushed   int
	// HasUpstream is true when it tracks a remote branch that still exists.
	HasUpstream bool
	Activity    time.Time
}

// classify gives the first state of ADR-0016 that applies, and why.
func classify(f lifeFacts, now time.Time, thresholdDays int) (string, string) {
	if thresholdDays <= 0 {
		thresholdDays = DefaultInactiveDays
	}
	idle := now.Sub(f.Activity)
	switch {
	case f.Missing:
		return LifeMissing, "its directory no longer exists"
	case f.Running:
		return LifeInUse, "its instance is running"
	case !f.LastSeen.IsZero() && now.Sub(f.LastSeen) < InUseWindow:
		return LifeInUse, "its owner used Theo's MCP server " + agoText(now.Sub(f.LastSeen)) + " ago"
	case f.Kept && (f.KeptUntil.IsZero() || now.Before(f.KeptUntil)):
		if f.KeptUntil.IsZero() {
			return LifeKept, "kept with `theo worktree keep`"
		}
		return LifeKept, "kept until " + f.KeptUntil.Local().Format("2006-01-02")
	case f.Changes == 0 && f.MergedBy == MergedAncestor:
		return LifeMerged, "its branch is in the base"
	case f.Changes == 0 && f.MergedBy == MergedContent:
		return LifeMerged, "its changes are in the base (squash or rebase merge)"
	case f.MergedBy == "" && !f.Activity.IsZero() && idle > time.Duration(thresholdDays)*24*time.Hour:
		return LifeInactive, fmt.Sprintf("no activity for %d days (threshold %d)", int(idle/(24*time.Hour)), thresholdDays)
	case f.Changes != 0:
		if f.Changes < 0 {
			return LifeUnpushed, "its files could not be read"
		}
		return LifeUnpushed, fmt.Sprintf("%d uncommitted or untracked file(s)", f.Changes)
	case f.Unpushed > 0 && f.HasUpstream:
		return LifeUnpushed, fmt.Sprintf("%d commit(s) not pushed", f.Unpushed)
	case f.Ahead > 0 && !f.HasUpstream:
		return LifeUnpushed, fmt.Sprintf("%d commit(s) on no remote branch", f.Ahead)
	case f.NotStarted || f.Ahead == 0:
		return LifeNew, "no commits of its own yet"
	case f.Ahead < 0 && !f.HasUpstream:
		return LifeUnpushed, "its base is unknown and it tracks no remote branch"
	default:
		return LifePushed, "everything is pushed"
	}
}

func agoText(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}
	return fmt.Sprintf("%d min", int(d/time.Minute))
}

// changesTime is the newest modification time among the changed files of a
// worktree (`git status --porcelain` lines). It looks at 200 files at most.
func changesTime(root string, changes []string) time.Time {
	var newest time.Time
	for i, c := range changes {
		if i >= 200 {
			break
		}
		if len(c) < 4 {
			continue
		}
		st, err := os.Stat(filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(c[3:], "/"))))
		if err == nil && st.ModTime().After(newest) {
			newest = st.ModTime()
		}
	}
	return newest
}

// dirSize adds up the sizes of the files under dir, without following links.
func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// mergeCache remembers, per pair of commits, whether a branch is merged into
// a base by content: the answer never changes for the same two commits, and
// asking Git costs a merge per worktree.
type mergeCache struct {
	mu    sync.Mutex
	path  string // empty: in memory only
	m     map[string]bool
	dirty bool
}

const mergeCacheFile = "merge-cache.json"
const mergeCacheMax = 5000

func (e *Engine) merges() *mergeCache {
	e.mergesOnce.Do(func() {
		c := &mergeCache{m: map[string]bool{}}
		if e.dataDir != "" {
			c.path = filepath.Join(e.dataDir, mergeCacheFile)
			if data, err := os.ReadFile(c.path); err == nil {
				_ = json.Unmarshal(data, &c.m)
				if c.m == nil {
					c.m = map[string]bool{}
				}
			}
		}
		e.mergeCache = c
	})
	return e.mergeCache
}

func (c *mergeCache) get(key string) (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[key]
	return v, ok
}

func (c *mergeCache) put(key string, v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= mergeCacheMax {
		c.m = map[string]bool{}
	}
	c.m[key] = v
	c.dirty = true
}

// save writes the cache when it changed. It is best effort: a lost cache
// only costs time.
func (c *mergeCache) save() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty || c.path == "" || execx.Explaining() {
		return
	}
	data, err := json.Marshal(c.m)
	if err != nil {
		return
	}
	tmp := c.path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil && os.Rename(tmp, c.path) == nil {
		c.dirty = false
	}
}
