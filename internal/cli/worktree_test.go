package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// TestWorktreeAddAndRemove runs the worktree commands against a real
// repository. Removal needs `docker compose ls`, so it needs a daemon.
func TestWorktreeAddAndRemove(t *testing.T) {
	testutil.RequireDaemon(t)
	main, _ := testutil.NewRepo(t, map[string]string{
		".gitignore":   ".env\n",
		"compose.yaml": "services:\n  web:\n    image: scratch\n",
	})
	testutil.WriteFile(t, main, ".env", "A=1\n")
	home := t.TempDir()
	t.Setenv(registry.EnvHome, home)
	if _, stderr, code := run("project", "add", main, "--name", "app"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}

	stdout, stderr, code := run("branches", "app")
	if code != ExitOK {
		t.Fatalf("branches: %s", stderr)
	}
	checkOutput(t, "branches", stdout, "feature/x")

	// A new branch from main, in the project's worktree directory.
	stdout, stderr, code = run("worktree", "add", "app", "feature/new", "--new", "--from", "main")
	if code != ExitOK {
		t.Fatalf("worktree add: %s\n%s", stdout, stderr)
	}
	dir := filepath.Join(home, "worktrees", "app", "feature-new")
	checkOutput(t, "worktree add", stdout, dir)
	checkOutput(t, "worktree add", stdout, ".env")         // the missing ignored file is explained...
	checkOutput(t, "worktree add", stdout, "by reference") // ...and shared by reference
	if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
		t.Errorf(".env was copied into the new worktree")
	}

	// The same branch twice, and a branch used elsewhere, are refused.
	if _, stderr, code = run("worktree", "add", "app", "feature/new"); code == ExitOK || !strings.Contains(stderr, "already checked out") {
		t.Errorf("second add: code %d, stderr %q", code, stderr)
	}
	if _, stderr, code = run("worktree", "add", "app", "feature/x"); code == ExitOK || !strings.Contains(stderr, "already checked out") {
		t.Errorf("add of a branch used by the existing worktree: code %d, stderr %q", code, stderr)
	}

	stdout, _, _ = run("worktrees", "app")
	checkOutput(t, "worktrees", stdout, "feature/new")

	// Untracked files block the removal until they are discarded and confirmed.
	testutil.WriteFile(t, dir, "notes.txt", "mine\n")
	_, stderr, code = run("worktree", "remove", "app", "feature/new")
	if code == ExitOK {
		t.Fatalf("remove with untracked files succeeded")
	}
	checkOutput(t, "remove", stderr, "notes.txt")
	checkOutput(t, "remove", stderr, "--confirm feature-new")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the worktree was removed anyway")
	}
	if _, _, code = run("worktree", "remove", "app", "feature/new", "--discard-changes"); code == ExitOK {
		t.Errorf("--discard-changes without --confirm must be refused")
	}
	stdout, stderr, code = run("worktree", "remove", "app", "feature/new", "--discard-changes", "--confirm", "feature-new")
	if code != ExitOK {
		t.Fatalf("confirmed remove: %s\n%s", stdout, stderr)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("the worktree directory still exists")
	}
	stdout, _, _ = run("branches", "app")
	checkOutput(t, "branches after remove", stdout, "feature/new") // the branch is kept

	// The main worktree is never removed.
	if _, stderr, code = run("worktree", "remove", "app", "main"); code == ExitOK || !strings.Contains(stderr, "main worktree") {
		t.Errorf("remove main: code %d, stderr %q", code, stderr)
	}
}

func TestWorktreeAddUsesTheConfiguredDirectory(t *testing.T) {
	testutil.RequireGit(t)
	main, _ := testutil.NewRepo(t, nil)
	t.Setenv(registry.EnvHome, t.TempDir())
	run("project", "add", main, "--name", "app")
	target := filepath.Join(testutil.TempDir(t), "trees")
	if _, stderr, code := run("project", "set", "app", "--worktree-dir", target); code != ExitOK {
		t.Fatalf("project set: %s", stderr)
	}
	if _, stderr, code := run("worktree", "add", "app", "topic", "--new"); code != ExitOK {
		t.Fatalf("worktree add: %s", stderr)
	}
	if _, err := os.Stat(filepath.Join(target, "topic")); err != nil {
		t.Errorf("worktree not created in the configured directory: %v", err)
	}
}
