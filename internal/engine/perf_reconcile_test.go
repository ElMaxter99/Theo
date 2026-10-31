package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/registry"
)

// dirGit lists one main worktree for any directory and counts the lists.
type dirGit struct {
	*fakeGit
	mu    sync.Mutex
	lists int
}

func (g *dirGit) Worktrees(_ context.Context, dir string) ([]git.Worktree, error) {
	g.mu.Lock()
	g.lists++
	g.mu.Unlock()
	return []git.Worktree{{Path: dir, Branch: "main"}}, nil
}

func TestReconcileAsksGitOncePerProjectUnderAMemo(t *testing.T) {
	store := &memStore{}
	for _, n := range []string{"one", "two", "three"} {
		store.projects = append(store.projects, registry.Project{Name: n, Path: abs("/src/" + n)})
	}
	g := &dirGit{fakeGit: &fakeGit{}}
	c := &slowCompose{fakeCompose: &fakeCompose{configs: map[string]*compose.Project{}}, delay: time.Millisecond}
	e := New(store, g, c)

	run := func(ctx context.Context) int {
		g.mu.Lock()
		g.lists = 0
		g.mu.Unlock()
		if _, err := e.Reconcile(ctx, ""); err != nil {
			t.Fatal(err)
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.lists
	}
	plain := run(context.Background())
	memo := run(WithRequestMemo(context.Background()))
	if memo != 3 {
		t.Errorf("under a memo Reconcile lists the worktrees of each of 3 projects once: %d calls", memo)
	}
	if plain <= memo {
		t.Errorf("without a memo it asks more than once per project (%d calls); the memo saves nothing", plain)
	}
}
