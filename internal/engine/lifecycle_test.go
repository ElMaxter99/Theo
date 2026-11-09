package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

func TestClassify(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-time.Hour)
	old := now.Add(-10 * 24 * time.Hour)
	cases := []struct {
		name string
		f    lifeFacts
		want string
	}{
		{"missing wins", lifeFacts{Missing: true, Running: true}, LifeMissing},
		{"running", lifeFacts{Running: true, MergedBy: MergedAncestor, Activity: old}, LifeInUse},
		{"owner seen recently", lifeFacts{LastSeen: now.Add(-10 * time.Minute), Activity: old, Ahead: 2}, LifeInUse},
		{"owner seen long ago", lifeFacts{LastSeen: now.Add(-time.Hour), Activity: recent, Ahead: 2}, LifeUnpushed},
		{"kept forever", lifeFacts{Kept: true, MergedBy: MergedAncestor}, LifeKept},
		{"keep expired", lifeFacts{Kept: true, KeptUntil: now.Add(-time.Hour), MergedBy: MergedContent}, LifeMerged},
		{"merged", lifeFacts{MergedBy: MergedAncestor, Activity: old}, LifeMerged},
		{"merged by content", lifeFacts{MergedBy: MergedContent, Ahead: 3, Activity: old}, LifeMerged},
		{"merged with changes is not merged", lifeFacts{MergedBy: MergedAncestor, Changes: 1, Activity: recent}, LifeUnpushed},
		{"inactive with commits", lifeFacts{Ahead: 2, Activity: old}, LifeInactive},
		{"inactive without commits", lifeFacts{Ahead: 0, NotStarted: true, Activity: old}, LifeInactive},
		{"changes", lifeFacts{Changes: 3, Activity: recent}, LifeUnpushed},
		{"unpushed commits", lifeFacts{Ahead: 2, Unpushed: 1, HasUpstream: true, Activity: recent}, LifeUnpushed},
		{"commits without upstream", lifeFacts{Ahead: 2, Activity: recent}, LifeUnpushed},
		{"new", lifeFacts{Ahead: 0, NotStarted: true, Activity: recent}, LifeNew},
		{"pushed", lifeFacts{Ahead: 2, HasUpstream: true, Activity: recent}, LifePushed},
	}
	for _, c := range cases {
		if got, why := classify(c.f, now, 7); got != c.want {
			t.Errorf("%s: classify() = %s (%s), want %s", c.name, got, why, c.want)
		}
	}
}

func TestParseDays(t *testing.T) {
	for in, want := range map[string]int{"7": 7, "7d": 7, "14 days": 14, " 3D ": 3} {
		if got, err := ParseDays(in); err != nil || got != want {
			t.Errorf("ParseDays(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "x", "7w", "1.5"} {
		if _, err := ParseDays(in); err == nil {
			t.Errorf("ParseDays(%q) should fail", in)
		}
	}
}

// lifecycleFixture builds a project "app" with worktrees in every state Git
// can tell: merged normally, merged by squash, merged by rebase, new, with
// commits on no remote, with changes. It returns the engine and the path of
// each worktree by branch.
func lifecycleFixture(t *testing.T) (*Engine, *memStore, string, map[string]string) {
	t.Helper()
	main, _ := testutil.NewRepo(t, nil)
	testutil.Git(t, main, "worktree", "remove", "--force", filepath.Join(filepath.Dir(main), "app-feature"))
	testutil.Git(t, main, "branch", "-D", "feature/x")
	e, store, _ := realEngine(t, main)
	ctx := context.Background()
	paths := map[string]string{}
	add := func(branch string) string {
		added, err := e.AddWorktree(ctx, "app", branch, AddWorktreeOptions{NewBranch: true})
		if err != nil {
			t.Fatal(err)
		}
		paths[branch] = added.Path
		return added.Path
	}
	commit := func(dir, file, content string) {
		testutil.WriteFile(t, dir, file, content)
		testutil.Git(t, dir, "add", "-A")
		testutil.Git(t, dir, "commit", "-q", "-m", "change "+file)
	}

	merged := add("merged")
	commit(merged, "merged.txt", "m\n")
	testutil.Git(t, main, "merge", "-q", "--no-ff", "-m", "merge", "merged")

	squash := add("squash")
	commit(squash, "s1.txt", "1\n")
	commit(squash, "s2.txt", "2\n")
	testutil.Git(t, main, "merge", "-q", "--squash", "squash")
	testutil.Git(t, main, "commit", "-q", "-m", "squash")

	rebased := add("rebased")
	commit(rebased, "r.txt", "r\n")
	commit(main, "before.txt", "b\n") // so the cherry-pick is a new commit
	testutil.Git(t, main, "cherry-pick", "rebased")

	// main moves on after the merges, as it does.
	commit(main, "later.txt", "later\n")

	add("fresh")

	work := add("work")
	commit(work, "w.txt", "w\n")

	dirty := add("dirty")
	testutil.WriteFile(t, dirty, "draft.txt", "draft\n")
	return e, store, main, paths
}

func statesByBranch(t *testing.T, e *Engine) (map[string]WorktreeState, *StatesReport) {
	t.Helper()
	r, err := e.WorktreeStates(context.Background(), "app", StatesOptions{Size: true})
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]WorktreeState{}
	for _, w := range r.Worktrees {
		m[w.Branch] = w
	}
	return m, r
}

func TestWorktreeStatesWithRealGit(t *testing.T) {
	e, store, _, _ := lifecycleFixture(t)
	got, report := statesByBranch(t, e)
	want := map[string]string{
		"main": "", "merged": LifeMerged, "squash": LifeMerged, "rebased": LifeMerged,
		"fresh": LifeNew, "work": LifeUnpushed, "dirty": LifeUnpushed,
	}
	for b, st := range want {
		if got[b].State != st {
			t.Errorf("%s: state = %q (%s, problem %q), want %q", b, got[b].State, got[b].Reason, got[b].Problem, st)
		}
	}
	if got["merged"].MergedBy != MergedAncestor || got["squash"].MergedBy != MergedContent || got["rebased"].MergedBy != MergedContent {
		t.Errorf("merged by = %q, %q, %q", got["merged"].MergedBy, got["squash"].MergedBy, got["rebased"].MergedBy)
	}
	if w := got["work"]; w.Ahead != 1 || w.Unpushed != 1 || w.Base != "main" || !w.BaseRecorded || w.ActivitySource == "" {
		t.Errorf("work = %+v", w.Lifecycle)
	}
	if got["dirty"].Changes != 1 || got["dirty"].SizeBytes == 0 {
		t.Errorf("dirty = %+v", got["dirty"].Lifecycle)
	}
	if got["work"].Origin != OriginTheo || got["work"].CreatedAt.IsZero() {
		t.Errorf("work origin = %q, created %v", got["work"].Origin, got["work"].CreatedAt)
	}
	if report.InactiveAfterDays != DefaultInactiveDays || len(report.Notes) != 0 {
		t.Errorf("report = days %d, notes %v", report.InactiveAfterDays, report.Notes)
	}
	// Every workspace Theo creates is recorded, owner or not.
	if n := len(store.projects[0].Workspaces); n != 6 {
		t.Errorf("records = %d, want 6", n)
	}
	// The cache of content merges is kept for the next call.
	if _, err := os.Stat(filepath.Join(e.dataDir, mergeCacheFile)); err != nil {
		t.Errorf("merge cache not written: %v", err)
	}

	// Ten days later the unmerged ones are inactive, the merged ones still merged.
	e.now = func() time.Time { return time.Now().Add(10 * 24 * time.Hour) }
	got, _ = statesByBranch(t, e)
	for b, st := range map[string]string{"merged": LifeMerged, "squash": LifeMerged, "fresh": LifeInactive, "work": LifeInactive, "dirty": LifeInactive} {
		if got[b].State != st {
			t.Errorf("10 days later, %s = %q, want %q", b, got[b].State, st)
		}
	}
	if got["work"].IdleDays < 9 {
		t.Errorf("idle days = %d", got["work"].IdleDays)
	}
	// A longer threshold brings them back.
	if _, err := e.SetInactiveAfter("app", 30); err != nil {
		t.Fatal(err)
	}
	got, _ = statesByBranch(t, e)
	if got["work"].State != LifeUnpushed {
		t.Errorf("with 30 days, work = %q", got["work"].State)
	}
	if _, err := e.SetInactiveAfter("app", 91); err == nil {
		t.Error("91 days should be refused")
	}
}

func TestNewBranchPushedWithoutCommitsIsNotMerged(t *testing.T) {
	e, _, main, paths := lifecycleFixture(t)
	// A branch that moved (it was reset) and has no commits of its own is merged.
	testutil.Git(t, paths["fresh"], "commit", "-q", "--allow-empty", "-m", "tmp")
	testutil.Git(t, paths["fresh"], "reset", "-q", "--hard", "HEAD~1")
	got, _ := statesByBranch(t, e)
	if got["fresh"].State != LifeMerged {
		t.Errorf("fresh after moving = %q (%s)", got["fresh"].State, got["fresh"].Reason)
	}
	_ = main
}

func TestKeepAndHeartbeat(t *testing.T) {
	e, _, _, _ := lifecycleFixture(t)
	ctx := context.Background()
	if _, err := e.Keep(ctx, "app", "merged", KeepOptions{Note: "demo on Friday"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Keep(ctx, "app", "main", KeepOptions{}); err == nil {
		t.Error("keeping the main worktree should fail")
	}
	if _, err := e.Keep(ctx, "app", "squash", KeepOptions{Until: time.Now().Add(-time.Hour)}); err == nil {
		t.Error("a date in the past should fail")
	}
	if err := e.Touch(ctx, "app", "work", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := statesByBranch(t, e)
	if got["merged"].State != LifeKept || got["merged"].KeepNote != "demo on Friday" {
		t.Errorf("merged = %q %q", got["merged"].State, got["merged"].KeepNote)
	}
	if got["work"].State != LifeInUse || got["work"].LastSeen.IsZero() {
		t.Errorf("work after Touch = %q", got["work"].State)
	}
	if _, err := e.Keep(ctx, "app", "merged", KeepOptions{Clear: true}); err != nil {
		t.Fatal(err)
	}
	got, _ = statesByBranch(t, e)
	if got["merged"].State != LifeMerged {
		t.Errorf("merged after clearing = %q", got["merged"].State)
	}
}

func TestCleanClosesMergedAndDeletesBranches(t *testing.T) {
	e, store, main, paths := lifecycleFixture(t)
	ctx := context.Background()
	if _, err := e.Keep(ctx, "app", "rebased", KeepOptions{}); err != nil {
		t.Fatal(err)
	}
	// A worktree whose directory vanished.
	gone, err := e.AddWorktree(ctx, "app", "gone", AddWorktreeOptions{NewBranch: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(gone.Path); err != nil {
		t.Fatal(err)
	}

	dry, err := e.Clean(ctx, "app", CleanOptions{DeleteBranches: true})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(dry.Closable()); n != 3 || dry.Applied {
		t.Fatalf("dry run closable = %+v", dry.Close)
	}
	if len(dry.Kept) != 1 || dry.Kept[0].Branch != "rebased" {
		t.Errorf("kept = %+v", dry.Kept)
	}
	if _, err := os.Stat(paths["merged"]); err != nil {
		t.Fatalf("a dry run removed something: %v", err)
	}

	r, err := e.Clean(ctx, "app", CleanOptions{DeleteBranches: true, Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range r.Close {
		if !c.Closed || c.Reason != "" {
			t.Errorf("%s not closed: %s %v", c.Branch, c.Reason, c.Problems)
		}
		if c.State == LifeMerged && !c.BranchDeleted {
			t.Errorf("branch %s not deleted: %v", c.Branch, c.Problems)
		}
	}
	for _, b := range []string{"merged", "squash"} {
		if _, err := os.Stat(paths[b]); !os.IsNotExist(err) {
			t.Errorf("%s still exists", paths[b])
		}
		if (git.Git{}).IsLocalBranch(ctx, main, b) {
			t.Errorf("branch %s still exists", b)
		}
	}
	// What was not merged is untouched, branch and directory.
	for _, b := range []string{"rebased", "work", "dirty", "fresh"} {
		if _, err := os.Stat(paths[b]); err != nil {
			t.Errorf("%s was removed: %v", b, err)
		}
	}
	list, _ := e.Worktrees(ctx, "app")
	for _, w := range list {
		if w.Branch == "gone" {
			t.Error("the missing worktree is still listed")
		}
	}
	for _, rec := range store.projects[0].Workspaces {
		if rec.Path == paths["merged"] || rec.Path == gone.Path {
			t.Errorf("record of a closed workspace kept: %+v", rec)
		}
	}
}

func TestCleanKeepsABranchThatIsNoLongerMerged(t *testing.T) {
	e, _, main, paths := lifecycleFixture(t)
	ctx := context.Background()
	states, _ := statesByBranch(t, e)
	ws := states["squash"]
	// Between listing and deleting, someone commits to the branch.
	testutil.WriteFile(t, paths["squash"], "late.txt", "late\n")
	testutil.Git(t, paths["squash"], "add", "-A")
	testutil.Git(t, paths["squash"], "commit", "-q", "-m", "late")
	p, _ := e.Project("app")
	if err := e.deleteMergedBranch(ctx, p, ws); err == nil {
		t.Error("deleteMergedBranch should refuse a branch that is no longer merged")
	}
	if !(git.Git{}).IsLocalBranch(ctx, main, "squash") {
		t.Error("the branch was deleted")
	}
}

func TestParkAndResume(t *testing.T) {
	e, store, main, paths := lifecycleFixture(t)
	ctx := context.Background()
	if _, err := e.Park(ctx, "app", "dirty", ParkOptions{}); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("parking with changes: %v", err)
	}
	testutil.WriteFile(t, main, ".gitignore", ".env\n")
	testutil.Git(t, main, "add", ".gitignore")
	testutil.Git(t, main, "commit", "-q", "-m", "ignore")
	testutil.Git(t, paths["work"], "merge", "-q", "main")
	testutil.WriteFile(t, paths["work"], ".env", "SECRET=1\n")
	var pe *ParkError
	if _, err := e.Park(ctx, "app", "work", ParkOptions{}); !errors.As(err, &pe) || pe.Name != filepath.Base(paths["work"]) {
		t.Fatalf("parking with ignored files: %v", err)
	}
	parked, err := e.Park(ctx, "app", "work", ParkOptions{Note: "waiting for the API", Confirm: filepath.Base(paths["work"])})
	if err != nil {
		t.Fatal(err)
	}
	if parked.Branch != "work" || parked.Base != "main" || parked.Note != "waiting for the API" || parked.Unpushed != 2 { // its commit and the merge
		t.Errorf("parked = %+v", parked)
	}
	if _, err := os.Stat(paths["work"]); !os.IsNotExist(err) {
		t.Errorf("the directory of a parked worktree is still there: %v", err)
	}
	if !(git.Git{}).IsLocalBranch(ctx, main, "work") {
		t.Fatal("parking deleted the branch")
	}
	_, report := statesByBranch(t, e)
	if len(report.Parked) != 1 || report.Parked[0].BranchGone {
		t.Errorf("parked list = %+v", report.Parked)
	}

	added, err := e.Resume(ctx, "app", "work", ResumeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if added.Path != paths["work"] {
		t.Errorf("resumed at %s, want %s", added.Path, paths["work"])
	}
	if len(store.projects[0].Parked) != 0 {
		t.Errorf("still parked: %+v", store.projects[0].Parked)
	}
	if _, err := e.Resume(ctx, "app", "work", ResumeOptions{}); err == nil {
		t.Error("resuming twice should fail")
	}
}

func TestFinishWorktree(t *testing.T) {
	main, _ := testutil.NewRepo(t, nil)
	e, _, _ := realEngine(t, main)
	ctx := context.Background()
	added, err := e.AddWorktree(ctx, "app", "agent/task", AddWorktreeOptions{NewBranch: true, Owner: "agent-1"})
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, added.Path, "x.txt", "x\n")
	testutil.Git(t, added.Path, "add", "-A")
	testutil.Git(t, added.Path, "commit", "-q", "-m", "x")
	if _, err := e.FinishWorktree(ctx, "app", "agent/task", FinishOptions{Owner: "agent-2"}); err == nil {
		t.Error("another owner should not finish it")
	}
	if _, err := e.FinishWorktree(ctx, "app", "agent/task", FinishOptions{Owner: "agent-1"}); err == nil || !strings.Contains(err.Error(), "push") {
		t.Errorf("finishing unpushed work: %v", err)
	}
	// Push it to a bare remote.
	remote := filepath.Join(testutil.TempDir(t), "remote.git")
	testutil.Git(t, main, "init", "-q", "--bare", remote)
	testutil.Git(t, main, "remote", "add", "origin", remote)
	testutil.Git(t, added.Path, "push", "-q", "-u", "origin", "agent/task")
	if err := e.Touch(ctx, "app", "agent/task", "agent-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.FinishWorktree(ctx, "app", "agent/task", FinishOptions{Owner: "agent-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(added.Path); !os.IsNotExist(err) {
		t.Errorf("worktree still there: %v", err)
	}
	if !(git.Git{}).IsLocalBranch(ctx, main, "agent/task") {
		t.Error("finishing deleted the branch")
	}
}

func TestContentMergedNeedsNewGit(t *testing.T) {
	testutil.RequireGit(t)
	out, err := exec.Command("git", "merge-tree", "-h").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "write-tree") {
		t.Skip("this Git has no merge-tree --write-tree")
	}
	main, linked := testutil.NewRepo(t, nil)
	testutil.WriteFile(t, linked, "a.txt", "a\n")
	testutil.Git(t, linked, "add", "-A")
	testutil.Git(t, linked, "commit", "-q", "-m", "a")
	ok, err := git.Git{}.ContentMerged(context.Background(), main, "main", "feature/x")
	if err != nil || ok {
		t.Errorf("ContentMerged before merging = %v, %v", ok, err)
	}
	testutil.Git(t, main, "merge", "-q", "--squash", "feature/x")
	testutil.Git(t, main, "commit", "-q", "-m", "squash")
	ok, err = git.Git{}.ContentMerged(context.Background(), main, "main", "feature/x")
	if err != nil || !ok {
		t.Errorf("ContentMerged after a squash = %v, %v", ok, err)
	}
}

func TestParkInactiveInBulk(t *testing.T) {
	for _, kv := range [][2]string{{"GIT_AUTHOR_NAME", "Theo Test"}, {"GIT_AUTHOR_EMAIL", "t@example.invalid"}, {"GIT_COMMITTER_NAME", "Theo Test"}, {"GIT_COMMITTER_EMAIL", "t@example.invalid"}} {
		t.Setenv(kv[0], kv[1])
	}
	e, store, main, paths := lifecycleFixture(t)
	ctx := context.Background()
	e.now = func() time.Time { return time.Now().Add(10 * 24 * time.Hour) }

	// Without --save-changes the worktree with changes stays, and a dry run touches nothing.
	dry, err := e.ParkInactive(ctx, "app", ParkInactiveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]ParkInactiveEntry{}
	for _, en := range dry.Entries {
		by[en.Branch] = en
	}
	if len(by) != 3 || by["fresh"].Reason != "" || by["work"].Reason != "" || !strings.Contains(by["dirty"].Reason, "--save-changes") {
		t.Fatalf("dry run = %+v", dry.Entries)
	}
	if dry.Parkable() != 2 || dry.ParkedCount() != 0 {
		t.Errorf("parkable %d, parked %d", dry.Parkable(), dry.ParkedCount())
	}
	for _, b := range []string{"fresh", "work", "dirty"} {
		if _, err := os.Stat(paths[b]); err != nil {
			t.Fatalf("a dry run removed %s: %v", b, err)
		}
	}

	// Too many files for the cap: it stays.
	testutil.WriteFile(t, paths["dirty"], "second.txt", "2\n")
	small, _ := e.ParkInactive(ctx, "app", ParkInactiveOptions{SaveChanges: true, MaxChanges: 1})
	for _, en := range small.Entries {
		if en.Branch == "dirty" && !strings.Contains(en.Reason, "more than 1") {
			t.Errorf("dirty with 2 files and cap 1: %+v", en)
		}
	}

	var ticks []ParkProgress
	r, err := e.ParkInactive(ctx, "app", ParkInactiveOptions{SaveChanges: true, Apply: true, Progress: func(p ParkProgress) { ticks = append(ticks, p) }})
	if err != nil {
		t.Fatal(err)
	}
	// Two ticks per worktree (starting, finished) and a last one at the end.
	if len(ticks) != 7 || ticks[0].Total != 3 || ticks[0].Done != 0 || ticks[0].Current == "" || ticks[1].Entry == nil || ticks[1].Done != 1 || ticks[6].Done != 3 {
		t.Errorf("progress = %+v", ticks)
	}
	if r.ParkedCount() != 3 {
		t.Fatalf("parked %d: %+v", r.ParkedCount(), r.Entries)
	}
	for _, b := range []string{"fresh", "work", "dirty"} {
		if _, err := os.Stat(paths[b]); !os.IsNotExist(err) {
			t.Errorf("%s still exists", paths[b])
		}
		if !(git.Git{}).IsLocalBranch(ctx, main, b) {
			t.Errorf("branch %s was deleted", b)
		}
	}
	// The uncommitted files are in the branch.
	showOut, _ := exec.Command("git", "-C", main, "show", "--stat", "--format=%s", "dirty").CombinedOutput()
	if out := string(showOut); !strings.Contains(out, "draft.txt") || !strings.Contains(out, "second.txt") || !strings.Contains(out, "WIP") {
		t.Errorf("dirty tip = %s", out)
	}
	if len(store.projects[0].Parked) != 3 {
		t.Errorf("parked records = %d", len(store.projects[0].Parked))
	}
	// Unmerged worktrees that were not inactive, and the merged ones, are untouched.
	for _, b := range []string{"merged", "squash", "rebased"} {
		if _, err := os.Stat(paths[b]); err != nil {
			t.Errorf("%s was removed: %v", b, err)
		}
	}
}

func TestCleanShowsMergedWorktreesWithChanges(t *testing.T) {
	e, _, _, paths := lifecycleFixture(t)
	testutil.WriteFile(t, paths["merged"], "notes.txt", "stray\n")
	r, err := e.Clean(context.Background(), "app", CleanOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range r.Close {
		if c.Branch == "merged" {
			found = true
			if c.Closed || !strings.Contains(c.Reason, "uncommitted") || c.Changes != 1 {
				t.Errorf("merged with changes = %+v", c)
			}
		}
	}
	if !found {
		t.Error("a merged worktree with changes is not listed")
	}
	if _, err := os.Stat(paths["merged"]); err != nil {
		t.Errorf("it was removed: %v", err)
	}
}

func TestParkInactiveSelectionSummaryAndBranchDeletion(t *testing.T) {
	for _, kv := range [][2]string{{"GIT_AUTHOR_NAME", "Theo Test"}, {"GIT_AUTHOR_EMAIL", "t@example.invalid"}, {"GIT_COMMITTER_NAME", "Theo Test"}, {"GIT_COMMITTER_EMAIL", "t@example.invalid"}} {
		t.Setenv(kv[0], kv[1])
	}
	e, store, main, paths := lifecycleFixture(t)
	ctx := context.Background()
	remote := filepath.Join(t.TempDir(), "remote.git")
	testutil.Git(t, filepath.Dir(remote), "init", "-q", "--bare", remote)
	testutil.Git(t, main, "remote", "add", "origin", remote)
	// "work" and "fresh" are fully on the remote; "dirty" is not.
	testutil.WriteFile(t, paths["fresh"], "f.txt", "f\n")
	testutil.Git(t, paths["fresh"], "add", "-A")
	testutil.Git(t, paths["fresh"], "commit", "-q", "-m", "fresh work")
	testutil.Git(t, main, "push", "-q", "-u", "origin", "work", "fresh")
	e.now = func() time.Time { return time.Now().Add(10 * 24 * time.Hour) }

	dry, err := e.ParkInactive(ctx, "app", ParkInactiveOptions{SaveChanges: true})
	if err != nil {
		t.Fatal(err)
	}
	s := dry.Summary
	if s.Total != 3 || s.Parkable != 3 || s.Safe != 2 || s.WithChanges != 1 || s.WithUnpushed != 0 || s.Blocked != 0 {
		t.Errorf("summary = %+v", s)
	}
	by := map[string]ParkInactiveEntry{}
	for _, en := range dry.Entries {
		by[en.Branch] = en
	}
	if !by["work"].Safe || !by["work"].BranchDeletable || by["dirty"].Safe || by["dirty"].BranchDeletable {
		t.Errorf("entries = %+v", dry.Entries)
	}

	// Only the selected ones are touched: "work" and "dirty", not "fresh".
	r, err := e.ParkInactive(ctx, "app", ParkInactiveOptions{
		SaveChanges: true, DeleteBranches: true, Apply: true,
		Only: []string{paths["work"], paths["dirty"], paths["merged"]},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ParkInactiveEntry{}
	for _, en := range r.Entries {
		got[en.Branch] = en
	}
	if len(r.Entries) != 3 {
		t.Fatalf("entries = %+v", r.Entries)
	}
	if w := got["work"]; !w.Parked || !w.BranchDeleted {
		t.Errorf("work = %+v", w)
	}
	if d := got["dirty"]; !d.Parked || d.BranchDeleted || !d.Saved {
		t.Errorf("dirty = %+v", d)
	}
	// A merged worktree that is named is parked too (the redesigned cleanup lists them with the others).
	if m := got["merged"]; !m.Parked || m.BranchDeleted {
		t.Errorf("merged (named) = %+v", m)
	}
	if (git.Git{}).IsLocalBranch(ctx, main, "work") {
		t.Error("the pushed branch was not deleted")
	}
	if !(git.Git{}).IsLocalBranch(ctx, main, "dirty") {
		t.Error("a branch with a WIP commit was deleted")
	}
	if _, err := os.Stat(paths["fresh"]); err != nil {
		t.Errorf("an unselected worktree was removed: %v", err)
	}
	// Its remote-tracking ref stays, so the branch can be recreated; no stale parked record is left.
	if !(git.Git{}).IsLocalBranch(ctx, main, "fresh") {
		t.Error("fresh lost its branch")
	}
	for _, pk := range store.projects[0].Parked {
		if pk.Branch == "work" {
			t.Errorf("parked record of a deleted branch kept: %+v", pk)
		}
	}
}

func TestParkInactiveIgnoredFoldersAndDetachedHead(t *testing.T) {
	for _, kv := range [][2]string{{"GIT_AUTHOR_NAME", "Theo Test"}, {"GIT_AUTHOR_EMAIL", "t@example.invalid"}, {"GIT_COMMITTER_NAME", "Theo Test"}, {"GIT_COMMITTER_EMAIL", "t@example.invalid"}} {
		t.Setenv(kv[0], kv[1])
	}
	e, _, main, paths := lifecycleFixture(t)
	ctx := context.Background()
	testutil.WriteFile(t, main, ".gitignore", "node_modules/\nlogs/\ncerts/\n")
	testutil.Git(t, main, "add", ".gitignore")
	testutil.Git(t, main, "commit", "-q", "-m", "ignore")
	testutil.Git(t, paths["work"], "merge", "-q", "main")
	testutil.Git(t, paths["fresh"], "merge", "-q", "main")
	testutil.WriteFile(t, paths["fresh"], "f.txt", "f\n")
	testutil.Git(t, paths["fresh"], "add", "f.txt")
	testutil.Git(t, paths["fresh"], "commit", "-q", "-m", "fresh work")
	testutil.WriteFile(t, paths["work"], "node_modules/x/index.js", "x\n")
	testutil.WriteFile(t, paths["work"], "logs/a.log", "log\n")
	testutil.WriteFile(t, paths["fresh"], "certs/key.pem", "KEY\n")
	// A worktree whose HEAD is detached, with a commit that is on no branch.
	testutil.Git(t, paths["dirty"], "checkout", "-q", "--detach")
	testutil.WriteFile(t, paths["dirty"], "d.txt", "d\n")
	testutil.Git(t, paths["dirty"], "add", "d.txt")
	testutil.Git(t, paths["dirty"], "commit", "-q", "-m", "only here")
	e.now = func() time.Time { return time.Now().Add(10 * 24 * time.Hour) }

	// Rebuildable folders (node_modules, logs) do not stop a park; certs/ does; a detached HEAD stays without the option.
	dry, err := e.ParkInactive(ctx, "app", ParkInactiveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]ParkInactiveEntry{}
	for _, en := range dry.Entries {
		k := en.Branch
		if k == "" {
			k = filepath.Base(en.Path)
		}
		by[k] = en
	}
	if by["work"].Reason != "" {
		t.Errorf("work: %+v", by["work"])
	}
	if f := by["fresh"]; f.ReasonCode != "ignored" || len(f.IgnoredNames) != 1 {
		t.Errorf("fresh: %+v", f)
	}
	d := by[filepath.Base(paths["dirty"])]
	if d.ReasonCode != "no-branch" || d.Blocked {
		t.Errorf("detached without the option: %+v", d)
	}

	r, err := e.ParkInactive(ctx, "app", ParkInactiveOptions{RescueDetached: true, SaveChanges: true, Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range r.Entries {
		if filepath.Base(en.Path) == filepath.Base(paths["dirty"]) && (!en.Parked || en.RescueBranch == "") {
			t.Errorf("detached not parked: %+v", en)
		}
	}
	if _, err := os.Stat(paths["dirty"]); !os.IsNotExist(err) {
		t.Error("the detached worktree is still there")
	}
	rescue := "theo-rescue/" + filepath.Base(paths["dirty"])
	if !(git.Git{}).IsLocalBranch(ctx, main, rescue) {
		t.Errorf("rescue branch %s missing", rescue)
	}
	if _, err := os.Stat(paths["fresh"]); err != nil {
		t.Errorf("fresh (certs/) was removed: %v", err)
	}
}

// A merged worktree with an uncommitted file is parked, saving the file, when it is named; and Clean only closes
// the ones named in Only.
func TestParkInactiveTakesNamedMergedWorktreeWithChanges(t *testing.T) {
	for _, kv := range [][2]string{{"GIT_AUTHOR_NAME", "Theo Test"}, {"GIT_AUTHOR_EMAIL", "t@example.invalid"}, {"GIT_COMMITTER_NAME", "Theo Test"}, {"GIT_COMMITTER_EMAIL", "t@example.invalid"}} {
		t.Setenv(kv[0], kv[1])
	}
	e, _, _, paths := lifecycleFixture(t)
	ctx := context.Background()
	testutil.WriteFile(t, paths["merged"], "notes.txt", "stray\n")

	// Not named: it is not part of the inactive ones.
	r, err := e.ParkInactive(ctx, "app", ParkInactiveOptions{SaveChanges: true, Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range r.Entries {
		if en.Branch == "merged" {
			t.Fatalf("merged worktree handled without being named: %+v", en)
		}
	}
	// Named: it is saved in a commit and parked.
	r, err = e.ParkInactive(ctx, "app", ParkInactiveOptions{SaveChanges: true, Only: []string{"merged"}, Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Entries) != 1 || !r.Entries[0].Parked || !r.Entries[0].Saved {
		t.Fatalf("named merged worktree = %+v", r.Entries)
	}
	if _, err := os.Stat(paths["merged"]); !os.IsNotExist(err) {
		t.Errorf("its folder is still there: %v", err)
	}
}

func TestCleanOnlyClosesTheNamedOnes(t *testing.T) {
	e, _, _, paths := lifecycleFixture(t)
	r, err := e.Clean(context.Background(), "app", CleanOptions{Apply: true, Only: []string{"no-such-branch"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range r.Close {
		if c.Closed {
			t.Errorf("%s was closed although it was not named", c.Branch)
		}
	}
	if _, err := os.Stat(paths["merged"]); err != nil {
		t.Errorf("merged worktree removed: %v", err)
	}
}
