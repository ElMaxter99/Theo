package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

const projectRemote = "https://example.invalid/app.git"

// realEngine returns an engine over the real git, a fake Compose and an
// in-memory registry with the repository at main registered as "app".
func realEngine(t *testing.T, main string) (*Engine, *memStore, *fakeCompose) {
	t.Helper()
	store := &memStore{projects: []registry.Project{{Name: "app", Path: main}}}
	c := &fakeCompose{configs: map[string]*compose.Project{}}
	e := New(store, git.Git{}, c)
	e.dataDir = testutil.TempDir(t)
	return e, store, c
}

func cloneFixture(t *testing.T) (*Engine, *memStore, *fakeCompose, string) {
	t.Helper()
	main := testutil.NewRepoWithSubmodule(t, map[string]string{".gitignore": ".env.local\nconfig/\n"})
	testutil.Git(t, main, "remote", "add", "origin", projectRemote)
	e, store, c := realEngine(t, main)
	return e, store, c, main
}

func TestAddCloneWithSubmodules(t *testing.T) {
	e, store, c, _ := cloneFixture(t)
	ctx := context.Background()

	added, err := e.AddWorktree(ctx, "app", "feat/one", AddWorktreeOptions{NewBranch: true, Clone: true, Owner: "agent-7"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(e.dataDir, "worktrees", "app", "feat-one")
	if added.Kind != KindClone || added.Path != dir || added.Owner != "agent-7" {
		t.Fatalf("added = %+v, want a clone at %s owned by agent-7", added, dir)
	}
	// The submodule was initialized, and origin is the project's remote.
	if _, err := os.Stat(filepath.Join(dir, "libs", "shared", "lib.txt")); err != nil {
		t.Errorf("submodule not initialized: %v", err)
	}
	out, err := git.Git{}.RemoteURL(ctx, dir, "origin")
	if err != nil || out != projectRemote {
		t.Errorf("origin of the clone = %q (%v), want %q", out, err, projectRemote)
	}
	if rec := store.projects[0].Workspaces; len(rec) != 1 || rec[0].Kind != registry.KindClone || rec[0].Owner != "agent-7" {
		t.Errorf("registry records = %+v", rec)
	}

	// It is listed as a workspace, with kind and owner.
	list, err := e.Worktrees(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Kind != KindMain || list[1].Kind != KindClone || list[1].Branch != "feat/one" || list[1].Owner != "agent-7" {
		t.Errorf("Worktrees() = %+v", list)
	}
	branches, _ := e.Branches(ctx, "app")
	found := false
	for _, b := range branches {
		if b.Name == "feat/one" {
			found = b.Worktree == dir
		}
	}
	if !found {
		t.Errorf("Branches() does not show feat/one in the clone: %+v", branches)
	}

	// It resolves by branch, directory name and path, and has its own Compose name.
	c.configs[dir] = &compose.Project{Name: "app"}
	for _, sel := range []string{"feat/one", "feat-one", dir} {
		inst, err := e.Instance(ctx, "app", sel, nil)
		if err != nil {
			t.Fatalf("Instance(%q): %v", sel, err)
		}
		if inst.Compose != "app-feat-one" || inst.Kind != KindClone || inst.Owner != "agent-7" || inst.Main {
			t.Errorf("Instance(%q) = %+v", sel, inst)
		}
	}

	// A branch checked out in a workspace cannot be used twice.
	if _, err := e.AddWorktree(ctx, "app", "feat/one", AddWorktreeOptions{Clone: true, Path: filepath.Join(e.dataDir, "other")}); err == nil || !strings.Contains(err.Error(), "already checked out") {
		t.Errorf("second clone of the same branch: %v", err)
	}
	// A missing branch leaves nothing behind.
	bad := filepath.Join(e.dataDir, "bad")
	if _, err := e.AddWorktree(ctx, "app", "nope", AddWorktreeOptions{Clone: true, Path: bad}); err == nil {
		t.Errorf("clone of a branch that does not exist succeeded")
	}
	if _, err := os.Stat(bad); err == nil {
		t.Errorf("a failed clone left %s behind", bad)
	}
	if len(store.projects[0].Workspaces) != 1 {
		t.Errorf("a failed clone was registered: %+v", store.projects[0].Workspaces)
	}
}

func TestWorktreeWithSubmodulesSaysSo(t *testing.T) {
	e, _, _, _ := cloneFixture(t)
	added, err := e.AddWorktree(context.Background(), "app", "feat/two", AddWorktreeOptions{NewBranch: true})
	if err != nil {
		t.Fatal(err)
	}
	if added.Kind != KindWorktree || !strings.Contains(strings.Join(added.Notes, "\n"), "--clone") {
		t.Errorf("added = %+v, want a note that suggests --clone", added)
	}
}

func TestRemoveCloneProtections(t *testing.T) {
	e, store, c, _ := cloneFixture(t)
	ctx := context.Background()
	added, err := e.AddWorktree(ctx, "app", "feat/rm", AddWorktreeOptions{NewBranch: true, Clone: true})
	if err != nil {
		t.Fatal(err)
	}
	dir := added.Path

	// Containers block it.
	c.stacks = []compose.Stack{{Name: "app-feat-rm", Status: "running(1)", ConfigFiles: filepath.Join(dir, "compose.yaml")}}
	var inst *InstanceError
	if _, err := e.RemoveWorkspace(ctx, "app", "feat/rm", RemoveWorktreeOptions{}); !errors.As(err, &inst) {
		t.Fatalf("remove with containers: %v", err)
	}
	c.stacks = nil

	// Untracked files block it, and so do commits that exist only there.
	testutil.WriteFile(t, dir, "notes.txt", "mine\n")
	var changes *ChangesError
	if _, err := e.RemoveWorkspace(ctx, "app", "feat/rm", RemoveWorktreeOptions{}); !errors.As(err, &changes) {
		t.Fatalf("remove with untracked files: %v", err)
	}
	testutil.Git(t, dir, "add", "notes.txt")
	testutil.Git(t, dir, "commit", "-q", "-m", "work")
	_, err = e.RemoveWorkspace(ctx, "app", "feat/rm", RemoveWorktreeOptions{})
	if !errors.As(err, &changes) || !strings.Contains(strings.Join(changes.Changes, "\n"), "commits only in the clone: branch feat/rm") {
		t.Fatalf("remove with an unpushed commit: %v / %+v", err, changes)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("a refused removal deleted the clone")
	}
	// Discarding needs the confirmation.
	if _, err := e.RemoveWorkspace(ctx, "app", "feat/rm", RemoveWorktreeOptions{DiscardChanges: true, Confirm: "wrong"}); err == nil {
		t.Fatalf("wrong confirmation accepted")
	}
	res, err := e.RemoveWorkspace(ctx, "app", "feat/rm", RemoveWorktreeOptions{DiscardChanges: true, Confirm: filepath.Base(dir)})
	if err != nil || res.Kind != KindClone {
		t.Fatalf("confirmed remove: %+v, %v", res, err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("the clone directory still exists")
	}
	if len(store.projects[0].Workspaces) != 0 {
		t.Errorf("the record was kept: %+v", store.projects[0].Workspaces)
	}
}

func TestRemoveCleanCloneAndMissingClone(t *testing.T) {
	e, _, _, _ := cloneFixture(t)
	ctx := context.Background()
	// A branch created from the project's HEAD and not touched has nothing to lose.
	added, err := e.AddWorktree(ctx, "app", "feat/clean", AddWorktreeOptions{NewBranch: true, Clone: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.RemoveWorkspace(ctx, "app", "feat/clean", RemoveWorktreeOptions{}); err != nil {
		t.Fatalf("remove of a clean clone: %v", err)
	}
	if _, err := os.Stat(added.Path); err == nil {
		t.Errorf("clone still on disk")
	}

	// A clone whose directory vanished is forgotten, not an error.
	added, err = e.AddWorktree(ctx, "app", "feat/gone", AddWorktreeOptions{NewBranch: true, Clone: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(added.Path); err != nil {
		t.Fatal(err)
	}
	list, _ := e.Worktrees(ctx, "app")
	if len(list) != 2 || !list[1].Prunable {
		t.Errorf("Worktrees() = %+v, want the missing clone flagged", list)
	}
	res, err := e.RemoveWorkspace(ctx, "app", added.Path, RemoveWorktreeOptions{})
	if err != nil || !res.Forgot {
		t.Fatalf("remove of a missing clone: %+v, %v", res, err)
	}
	if list, _ := e.Worktrees(ctx, "app"); len(list) != 1 {
		t.Errorf("record not forgotten: %+v", list)
	}
}

func TestOwnerOfLinkedWorktreeAndMain(t *testing.T) {
	main, linked := testutil.NewRepo(t, nil)
	e, store, _ := realEngine(t, main)
	ctx := context.Background()

	wt, err := e.SetOwner(ctx, "app", linked, "  claude-a ")
	if err != nil || wt.Owner != "claude-a" || wt.Kind != KindWorktree {
		t.Fatalf("SetOwner = %+v, %v", wt, err)
	}
	if _, err := e.SetOwner(ctx, "app", "main", "human"); err != nil {
		t.Fatal(err)
	}
	list, _ := e.Worktrees(ctx, "app")
	if list[0].Owner != "human" || list[0].Kind != KindMain || list[1].Owner != "claude-a" {
		t.Errorf("Worktrees() = %+v", list)
	}
	if _, err := e.SetOwner(ctx, "app", linked, ""); err != nil {
		t.Fatal(err)
	}
	if got := store.projects[0].Workspaces; len(got) != 1 || got[0].Path != main {
		t.Errorf("clearing the owner should drop its record: %+v", got)
	}
	for _, bad := range []string{"", "  ", "a\x00b", strings.Repeat("x", 101)} {
		if _, err := ValidateOwner(bad); err == nil {
			t.Errorf("ValidateOwner(%q) accepted", bad)
		}
	}
	// Removing a worktree forgets its owner.
	if _, err := e.SetOwner(ctx, "app", linked, "claude-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RemoveWorkspace(ctx, "app", linked, RemoveWorktreeOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, w := range store.projects[0].Workspaces {
		if w.Path == linked {
			t.Errorf("owner record of a removed worktree kept")
		}
	}
}

func TestCleanCopyPattern(t *testing.T) {
	for _, ok := range []string{".env.local", "config/secrets.json", "./a/b.txt", "config/*.json"} {
		if _, err := CleanCopyPattern(ok); err != nil {
			t.Errorf("CleanCopyPattern(%q): %v", ok, err)
		}
	}
	bad := []string{"", "..", "../x", "a/../../x", "a/../b", "/etc/passwd", ".", ".git/config", ".git", `\x`}
	if runtime.GOOS == "windows" {
		bad = append(bad, `C:\x`)
	}
	for _, in := range bad {
		if c, err := CleanCopyPattern(in); err == nil {
			t.Errorf("CleanCopyPattern(%q) = %q, want an error", in, c)
		}
	}
}

func TestCopyOnAdd(t *testing.T) {
	main, _ := testutil.NewRepo(t, map[string]string{".gitignore": ".env.local\nconfig/\nlink.env\n", "tracked.txt": "t\n"})
	testutil.WriteFile(t, main, ".env.local", "SECRET=1\n")
	testutil.WriteFile(t, main, "config/secrets.json", "{}\n")
	testutil.WriteFile(t, main, "untracked.txt", "u\n")
	if err := os.Chmod(filepath.Join(main, ".env.local"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideDir := testutil.TempDir(t)
	testutil.WriteFile(t, outsideDir, "outside.env", "OUT=1\n")
	linkErr := os.Symlink(filepath.Join(outsideDir, "outside.env"), filepath.Join(main, "link.env"))

	e, store, _ := realEngine(t, main)
	ctx := context.Background()
	if _, err := e.SetCopyOnAdd("app", []string{"../x"}); err == nil {
		t.Errorf("SetCopyOnAdd accepted ..")
	}
	if _, err := e.SetCopyOnAdd("app", []string{".env.local", "config/secrets.json", "missing.env", "link.env", "tracked.txt", "config"}); err != nil {
		t.Fatal(err)
	}

	// Without --copy nothing is copied.
	plain, err := e.AddWorktree(ctx, "app", "feat/plain", AddWorktreeOptions{NewBranch: true})
	if err != nil || len(plain.Copied) != 0 {
		t.Fatalf("plain add copied: %+v, %v", plain, err)
	}
	if _, err := os.Stat(filepath.Join(plain.Path, ".env.local")); err == nil {
		t.Errorf(".env.local copied without --copy")
	}

	added, err := e.AddWorktree(ctx, "app", "feat/copy", AddWorktreeOptions{NewBranch: true, Copy: true, CopyFiles: []string{"untracked.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".env.local", "config/secrets.json", "untracked.txt"}
	if strings.Join(added.Copied, ",") != strings.Join(want, ",") {
		t.Errorf("Copied = %v, want %v", added.Copied, want)
	}
	reasons := map[string]string{}
	for _, n := range added.NotCopied {
		reasons[n.Path] = n.Reason
	}
	for _, p := range []string{"missing.env", "tracked.txt", "config"} {
		if reasons[p] == "" {
			t.Errorf("no reason for %s: %+v", p, added.NotCopied)
		}
	}
	if linkErr == nil && !strings.Contains(reasons["link.env"], "outside") {
		t.Errorf("symlink to outside not refused: %+v", added.NotCopied)
	}
	if _, err := os.Lstat(filepath.Join(added.Path, "link.env")); err == nil {
		t.Errorf("the symlink target was copied")
	}
	data, err := os.ReadFile(filepath.Join(added.Path, ".env.local"))
	if err != nil || string(data) != "SECRET=1\n" {
		t.Errorf("copied content = %q, %v", data, err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(filepath.Join(added.Path, ".env.local")); info == nil || info.Mode().Perm() != 0o600 {
			t.Errorf("permissions not kept: %v", info)
		}
	}
	notes := strings.Join(added.Notes, "\n")
	if !strings.Contains(notes, "secrets") || !strings.Contains(notes, "--env-file") {
		t.Errorf("notes lack the secrets warning and the by-reference alternative: %q", notes)
	}

	// Never overwrite: a destination that exists stays as it is.
	dst := t.TempDir()
	testutil.WriteFile(t, dst, ".env.local", "MINE=1\n")
	copied, skipped := e.copyUnversioned(ctx, main, dst, []string{".env.local"})
	if len(copied) != 0 || len(skipped) != 1 || !strings.Contains(skipped[0].Reason, "not overwritten") {
		t.Errorf("copyUnversioned over an existing file: %v %+v", copied, skipped)
	}
	if data, _ := os.ReadFile(filepath.Join(dst, ".env.local")); string(data) != "MINE=1\n" {
		t.Errorf("existing file overwritten: %q", data)
	}
	// A destination directory that is a symlink out of the workspace is not followed.
	away, dst2 := t.TempDir(), t.TempDir()
	if err := os.Symlink(away, filepath.Join(dst2, "config")); err == nil {
		copied, _ = e.copyUnversioned(ctx, main, dst2, []string{"config/secrets.json"})
		if entries, _ := os.ReadDir(away); len(copied) != 0 || len(entries) != 0 {
			t.Errorf("copied through a symlinked directory: %v", copied)
		}
	}

	// --copy with no list and no --copy-file is refused before creating anything.
	if _, err := e.SetCopyOnAdd("app", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AddWorktree(ctx, "app", "feat/none", AddWorktreeOptions{NewBranch: true, Copy: true}); err == nil || !strings.Contains(err.Error(), "no files to copy") {
		t.Errorf("--copy with an empty list: %v", err)
	}
	if _, err := e.AddWorktree(ctx, "app", "feat/esc", AddWorktreeOptions{NewBranch: true, CopyFiles: []string{"../secret"}}); err == nil {
		t.Errorf("--copy-file ../secret accepted")
	}
	if len(store.projects[0].CopyOnAdd) != 0 {
		t.Errorf("CopyOnAdd not cleared: %v", store.projects[0].CopyOnAdd)
	}
}

func TestCopyIntoClone(t *testing.T) {
	e, _, _, main := cloneFixture(t)
	testutil.WriteFile(t, main, ".env.local", "S=1\n")
	added, err := e.AddWorktree(context.Background(), "app", "feat/c", AddWorktreeOptions{NewBranch: true, Clone: true, CopyFiles: []string{".env.local"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(added.Copied) != 1 {
		t.Errorf("Copied = %v", added.Copied)
	}
	if _, err := os.Stat(filepath.Join(added.Path, ".env.local")); err != nil {
		t.Errorf("file missing in the clone: %v", err)
	}
}
