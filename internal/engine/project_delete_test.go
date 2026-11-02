package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/compose"
)

func TestRemoveProjectDeleteWorktreesRefusedWithContainersEvenWithYes(t *testing.T) {
	f := removeFixture(t)
	f.c.stacks = []compose.Stack{{Name: "app-app-feature", Status: "exited(1)", ConfigFiles: filepath.Join(f.linked, "compose.yaml")}}
	res, err := f.e.RemoveProjectChecked(context.Background(), "app", RemoveProjectOptions{Yes: true, DeleteWorktrees: true, Confirm: "app", DiscardChanges: true})
	var busy *ContainersError
	if !errors.As(err, &busy) || !busy.DeleteWorktrees {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "even with --yes") || !strings.Contains(err.Error(), "app-app-feature") {
		t.Errorf("message = %v", err)
	}
	if res == nil || len(res.ToDelete) != 1 || res.ToDelete[0].Path != f.linked || len(res.Deleted) != 0 {
		t.Errorf("res = %+v", res)
	}
	if len(f.g.calls) != 0 {
		t.Errorf("git calls = %v", f.g.calls)
	}
	if _, err := f.e.Project("app"); err != nil {
		t.Error("a refused removal must keep the project")
	}
}

func TestRemoveProjectDeleteWorktreesNeedsTheProjectName(t *testing.T) {
	f := removeFixture(t)
	for _, confirm := range []string{"", "App", "app-app-feature"} {
		res, err := f.e.RemoveProjectChecked(context.Background(), "app", RemoveProjectOptions{DeleteWorktrees: true, Confirm: confirm})
		var conf *ConfirmError
		if !errors.As(err, &conf) || !strings.Contains(err.Error(), "--confirm app") {
			t.Fatalf("confirm %q: err = %v", confirm, err)
		}
		if res == nil || len(res.ToDelete) != 1 {
			t.Errorf("confirm %q: res = %+v", confirm, res)
		}
	}
	if len(f.g.calls) != 0 {
		t.Errorf("git calls = %v", f.g.calls)
	}
	if _, err := f.e.Project("app"); err != nil {
		t.Error("a refused removal must keep the project")
	}
}

func TestRemoveProjectDeleteWorktreesRefusesChangesUnlessDiscarded(t *testing.T) {
	f := removeFixture(t)
	f.g.changes = map[string][]string{f.linked: {"?? notes.txt"}}
	ctx := context.Background()

	res, err := f.e.RemoveProjectChecked(ctx, "app", RemoveProjectOptions{DeleteWorktrees: true, Confirm: "app"})
	var conf *ConfirmError
	if !errors.As(err, &conf) || !strings.Contains(err.Error(), "--discard-changes") || !strings.Contains(err.Error(), f.linked) {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(res.ToDelete[0].Changes, []string{"?? notes.txt"}) {
		t.Errorf("changes = %+v", res.ToDelete)
	}
	if len(f.g.calls) != 0 {
		t.Fatalf("git calls = %v", f.g.calls)
	}

	res, err = f.e.RemoveProjectChecked(ctx, "app", RemoveProjectOptions{DeleteWorktrees: true, Confirm: "app", DiscardChanges: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"remove " + f.linked + " force=true"}; !reflect.DeepEqual(f.g.calls, want) {
		t.Errorf("git calls = %v, want %v", f.g.calls, want)
	}
	if !reflect.DeepEqual(res.Deleted, []string{f.linked}) {
		t.Errorf("deleted = %v", res.Deleted)
	}
}

func TestRemoveProjectDeleteWorktreesKeepsMainVolumesAndRecords(t *testing.T) {
	f := removeFixture(t)
	ctx := context.Background()
	own := filepath.Join(f.e.dataDir, "worktrees", "app") // Theo's own directory, empty
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := f.e.RemoveProjectChecked(ctx, "app", RemoveProjectOptions{DeleteWorktrees: true, Confirm: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"remove " + f.linked + " force=false"}; !reflect.DeepEqual(f.g.calls, want) {
		t.Errorf("git calls = %v, want %v (the main worktree is never removed)", f.g.calls, want)
	}
	if !dirExists(f.main) {
		t.Error("the main worktree was deleted")
	}
	if dirExists(own) {
		t.Error("Theo's empty worktree directory for the project should be gone")
	}
	if len(f.c.removed) != 0 {
		t.Errorf("volumes removed: %v", f.c.removed)
	}
	if _, err := f.e.Project("app"); !errors.Is(err, ErrNotFound) {
		t.Error("the project should be unregistered")
	}
	// Without ForgetRecords, Theo's records and generated files stay.
	state, _ := f.e.settings.Load()
	if len(state) != 1 {
		t.Errorf("records changed without ForgetRecords: %v", state)
	}
	for _, g := range res.GeneratedFiles {
		if !fileExists(g) {
			t.Errorf("%s was deleted", g)
		}
	}
}

func TestRemoveProjectDeleteWorktreesNeedsDocker(t *testing.T) {
	f := removeFixture(t)
	f.e.compose = &stacksFail{f.c}
	_, err := f.e.RemoveProjectChecked(context.Background(), "app", RemoveProjectOptions{DeleteWorktrees: true, Confirm: "app"})
	if err == nil || !strings.Contains(err.Error(), "no worktree was deleted") {
		t.Fatalf("err = %v", err)
	}
	if len(f.g.calls) != 0 {
		t.Errorf("git calls = %v", f.g.calls)
	}
}
