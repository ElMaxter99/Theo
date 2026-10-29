package git

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ElMaxter99/Theo/internal/testutil"
)

func TestParseWorktrees(t *testing.T) {
	out := "worktree /repo\r\nbare\r\n\r\n" +
		"worktree /repo/main\nHEAD 1111111111111111111111111111111111111111\nbranch refs/heads/main\n\n" +
		"worktree /repo/detached\nHEAD 2222222222222222222222222222222222222222\ndetached\n\n" +
		"worktree /repo/gone\nHEAD 3333333333333333333333333333333333333333\nbranch refs/heads/feature/a\nlocked reason here\nprunable gitdir file points to non-existent location\n\n"

	got, err := ParseWorktrees([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := []Worktree{
		{Path: filepath.FromSlash("/repo"), Bare: true},
		{Path: filepath.FromSlash("/repo/main"), Head: "1111111111111111111111111111111111111111", Branch: "main"},
		{Path: filepath.FromSlash("/repo/detached"), Head: "2222222222222222222222222222222222222222", Detached: true},
		{Path: filepath.FromSlash("/repo/gone"), Head: "3333333333333333333333333333333333333333", Branch: "feature/a", Locked: true, Prunable: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseWorktrees() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestParseWorktreesRejectsGarbage(t *testing.T) {
	if _, err := ParseWorktrees([]byte("HEAD abc\n")); err == nil {
		t.Error("expected an error for output without a worktree line")
	}
}

func TestGitAgainstRealRepository(t *testing.T) {
	main, linked := testutil.NewRepo(t, nil)
	ctx := context.Background()
	g := Git{}

	top, err := g.TopLevel(ctx, filepath.Join(linked))
	if err != nil {
		t.Fatal(err)
	}
	if top != linked {
		t.Errorf("TopLevel() = %q, want %q", top, linked)
	}

	list, err := g.Worktrees(ctx, linked)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Path != main || list[0].Branch != "main" || list[1].Path != linked || list[1].Branch != "feature/x" {
		t.Errorf("Worktrees() = %+v", list)
	}

	if dirty, err := g.Dirty(ctx, linked); err != nil || dirty {
		t.Errorf("Dirty() on a clean worktree = %v, %v", dirty, err)
	}
	testutil.WriteFile(t, linked, "new.txt", "x")
	if dirty, err := g.Dirty(ctx, linked); err != nil || !dirty {
		t.Errorf("Dirty() with an untracked file = %v, %v", dirty, err)
	}

	if _, err := g.TopLevel(ctx, testutil.TempDir(t)); err == nil {
		t.Error("TopLevel() outside a repository should fail")
	}
}
