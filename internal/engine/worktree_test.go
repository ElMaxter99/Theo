package engine

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/git"
)

func worktreeFixture(t *testing.T) (*Engine, *fakeGit, *fakeCompose) {
	t.Helper()
	e, _, g, c := newEngine()
	if _, err := e.AddProject(context.Background(), root, "", ""); err != nil {
		t.Fatal(err)
	}
	e.dataDir = t.TempDir()
	return e, g, c
}

func TestBranchesMarksWhichHaveAWorktree(t *testing.T) {
	e, g, _ := worktreeFixture(t)
	g.branches = []git.Branch{{Name: "main"}, {Name: "feature/login"}, {Name: "zeta"}, {Name: "origin/feature/login", Remote: true}, {Name: "origin/other", Remote: true}}
	list, err := e.Branches(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range list {
		got = append(got, b.Name+"="+b.Worktree)
	}
	want := []string{"feature/login=" + feature, "main=" + root, "zeta=", "origin/feature/login=" + feature, "origin/other="}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("branches = %v\nwant %v", got, want)
	}
}

func TestAddWorktreeUsesTheProjectDirectoryAndRefusesCheckedOutBranches(t *testing.T) {
	e, g, _ := worktreeFixture(t)
	ctx := context.Background()

	if _, err := e.AddWorktree(ctx, "app", "feature/login", AddWorktreeOptions{}); err == nil || !strings.Contains(err.Error(), feature) {
		t.Errorf("a branch checked out elsewhere must be refused, naming its worktree: %v", err)
	}
	g.branches = []git.Branch{{Name: "main"}, {Name: "taken"}}
	if _, err := e.AddWorktree(ctx, "app", "taken", AddWorktreeOptions{NewBranch: true}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("--new over an existing branch must be refused: %v", err)
	}
	if len(g.calls) != 0 {
		t.Fatalf("git was called: %v", g.calls)
	}

	added, err := e.AddWorktree(ctx, "app", "fix/Bug 1", AddWorktreeOptions{NewBranch: true, Base: "main"})
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(e.dataDir, "worktrees", "app", "fix-bug-1")
	if added.Path != wantPath {
		t.Errorf("path = %q, want %q", added.Path, wantPath)
	}
	if want := "add " + wantPath + " fix/Bug 1 new=true base=main"; len(g.calls) != 1 || g.calls[0] != want {
		t.Errorf("calls = %q, want %q", g.calls, want)
	}

	if _, err := e.AddWorktree(ctx, "app", "x", AddWorktreeOptions{Path: filepath.Join(feature, "sub")}); err == nil || !strings.Contains(err.Error(), "inside the worktree") {
		t.Errorf("a path inside another worktree must be refused: %v", err)
	}
}

func TestAddWorktreeExplainsMissingEnvFiles(t *testing.T) {
	e, g, _ := worktreeFixture(t)
	g.ignored = []string{".env"}
	added, err := e.AddWorktree(context.Background(), "app", "new", AddWorktreeOptions{Path: filepath.Join(t.TempDir(), "new")})
	if err != nil {
		t.Fatal(err)
	}
	// The fake main worktree has no .env on disk, so Theo has nothing to point at.
	if len(added.Notes) != 1 || !strings.Contains(added.Notes[0], "theo project set app --env-file") {
		t.Errorf("notes = %q", added.Notes)
	}
}

func TestSetWorktreeDir(t *testing.T) {
	e, _, _ := worktreeFixture(t)
	dir := abs("/elsewhere/trees")
	if err := e.SetWorktreeDir("app", dir); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.WorktreeDir("app"); got != dir {
		t.Errorf("dir = %q", got)
	}
	if err := e.SetWorktreeDir("app", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.WorktreeDir("app"); got != filepath.Join(e.dataDir, "worktrees", "app") {
		t.Errorf("default dir = %q", got)
	}
	if err := e.SetWorktreeDir("nope", dir); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestRemoveWorktreeProtections(t *testing.T) {
	e, g, c := worktreeFixture(t)
	ctx := context.Background()

	if _, err := e.RemoveWorktree(ctx, "app", "main", RemoveWorktreeOptions{}); err == nil || !strings.Contains(err.Error(), "main worktree") {
		t.Errorf("main worktree: %v", err)
	}
	if _, err := e.RemoveWorktree(ctx, "app", "old", RemoveWorktreeOptions{}); err == nil || !strings.Contains(err.Error(), "git worktree prune") {
		t.Errorf("missing directory: %v", err)
	}

	// Containers: blocked until they are gone.
	c.stacks = []compose.Stack{{Name: "app-feature", Status: "running(1)", ConfigFiles: filepath.Join(feature, "compose.yaml")}}
	var inst *InstanceError
	if _, err := e.RemoveWorktree(ctx, "app", "feature/login", RemoveWorktreeOptions{}); !errors.As(err, &inst) || inst.Stacks[0] != "app-feature" {
		t.Errorf("instances: %v", err)
	}
	c.stacks = nil

	// Uncommitted files: blocked, with the list, unless discarded and confirmed.
	g.changes = map[string][]string{feature: {" M a.go", "?? notes.txt"}}
	var ch *ChangesError
	if _, err := e.RemoveWorktree(ctx, "app", "feature/login", RemoveWorktreeOptions{}); !errors.As(err, &ch) || len(ch.Changes) != 2 || ch.Name != "app-feature" {
		t.Errorf("changes: %v", err)
	}
	if _, err := e.RemoveWorktree(ctx, "app", "feature/login", RemoveWorktreeOptions{DiscardChanges: true}); !errors.As(err, &ch) {
		t.Errorf("discarding without confirmation must be refused: %v", err)
	}
	if _, err := e.RemoveWorktree(ctx, "app", "feature/login", RemoveWorktreeOptions{DiscardChanges: true, Confirm: "wrong"}); !errors.As(err, &ch) {
		t.Errorf("a wrong confirmation must be refused: %v", err)
	}
	if len(g.calls) != 0 {
		t.Fatalf("nothing should have been removed: %v", g.calls)
	}

	if _, err := e.RemoveWorktree(ctx, "app", "feature/login", RemoveWorktreeOptions{DiscardChanges: true, Confirm: "app-feature"}); err != nil {
		t.Fatal(err)
	}
	if want := "remove " + feature + " force=true"; len(g.calls) != 1 || g.calls[0] != want {
		t.Errorf("calls = %q", g.calls)
	}

	// A clean worktree is removed without force.
	g.calls, g.changes = nil, nil
	if _, err := e.RemoveWorktree(ctx, "app", "feature/login", RemoveWorktreeOptions{}); err != nil {
		t.Fatal(err)
	}
	if want := "remove " + feature + " force=false"; len(g.calls) != 1 || g.calls[0] != want {
		t.Errorf("calls = %q", g.calls)
	}
}
