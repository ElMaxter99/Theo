package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// TestProjectRemoveDeleteWorktrees runs `project remove --delete-worktrees`
// against a real repository with a linked worktree, a worktree in Theo's
// directory and a clone. Checking for containers needs a daemon.
func TestProjectRemoveDeleteWorktrees(t *testing.T) {
	testutil.RequireDaemon(t)
	main, linked := testutil.NewRepo(t, map[string]string{
		"compose.yaml": "name: theo-cli-delwt\nservices:\n  web:\n    image: scratch\n",
	})
	home := t.TempDir()
	t.Setenv(registry.EnvHome, home)
	if _, stderr, code := run("project", "add", main, "--name", "delwt"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	for _, args := range [][]string{
		{"worktree", "add", "delwt", "feature/new", "--new"},
		{"worktree", "add", "delwt", "feature/c", "--new", "--clone"},
	} {
		if stdout, stderr, code := run(args...); code != ExitOK {
			t.Fatalf("%v: %s\n%s", args, stdout, stderr)
		}
	}
	own := filepath.Join(home, "worktrees", "delwt")
	wt, clone := filepath.Join(own, "feature-new"), filepath.Join(own, "feature-c")
	testutil.WriteFile(t, wt, "notes.txt", "mine\n")
	all := []string{linked, wt, clone}
	exist := func(when string) {
		t.Helper()
		for _, d := range append([]string{main}, all...) {
			if _, err := os.Stat(d); err != nil {
				t.Fatalf("%s: %s is gone", when, d)
			}
		}
	}

	if _, _, code := run("project", "remove", "delwt", "--discard-changes"); code != ExitUsage {
		t.Errorf("--discard-changes without --delete-worktrees: code %d, want %d", code, ExitUsage)
	}

	// Without --confirm: the listing says what would be deleted, nothing is.
	stdout, stderr, code := run("project", "remove", "delwt", "--delete-worktrees")
	if code == ExitOK {
		t.Fatalf("removed without --confirm: %s", stdout)
	}
	checkOutput(t, "stderr", stderr, "--confirm delwt")
	checkOutput(t, "stdout", stdout, "would be deleted")
	for _, d := range all {
		checkOutput(t, "stdout", stdout, d)
	}
	checkOutput(t, "stdout", stdout, "(clone, feature/c)")
	exist("without --confirm")

	// A wrong name is refused too.
	if _, stderr, code = run("project", "remove", "delwt", "--delete-worktrees", "--confirm", "feature-new"); code == ExitOK || !strings.Contains(stderr, "does not match") {
		t.Errorf("wrong --confirm: code %d, stderr %q", code, stderr)
	}
	exist("wrong --confirm")

	// Uncommitted files block everything until discarded.
	stdout, stderr, code = run("project", "remove", "delwt", "--delete-worktrees", "--confirm", "delwt")
	if code == ExitOK {
		t.Fatalf("removed a worktree with untracked files: %s", stdout)
	}
	checkOutput(t, "stderr", stderr, "--discard-changes")
	checkOutput(t, "stdout", stdout, "?? notes.txt")
	exist("with untracked files")
	if _, _, code := run("project", "show", "delwt"); code != ExitOK {
		t.Fatal("a refused removal must keep the project")
	}

	stdout, stderr, code = run("project", "remove", "delwt", "--delete-worktrees", "--confirm", "delwt", "--discard-changes", "--json")
	if code != ExitOK {
		t.Fatalf("remove --delete-worktrees: %s\n%s", stdout, stderr)
	}
	var out struct {
		Removed   bool `json:"removed"`
		Resources struct {
			Deleted []string `json:"deleted"`
		} `json:"resources"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil || !out.Removed || len(out.Resources.Deleted) != 3 {
		t.Fatalf("json = %s (%v)", stdout, err)
	}
	for _, d := range all {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("%s still exists (%v)", d, err)
		}
	}
	if _, err := os.Stat(own); !os.IsNotExist(err) {
		t.Errorf("Theo's empty worktree directory %s remains", own)
	}
	if _, err := os.Stat(filepath.Join(main, "compose.yaml")); err != nil {
		t.Errorf("the main worktree was touched: %v", err)
	}
	// Branches are kept, and Git has forgotten the worktrees.
	branches, err := exec.Command("git", "-C", main, "branch", "--list", "feature/*").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"feature/x", "feature/new"} {
		checkOutput(t, "branches", string(branches), b)
	}
	list, _ := exec.Command("git", "-C", main, "worktree", "list").Output()
	if strings.Count(strings.TrimSpace(string(list)), "\n") != 0 {
		t.Errorf("git still lists worktrees:\n%s", list)
	}
}

// noDocker points the docker CLI at a socket that does not exist, so that
// Theo cannot ask Docker whatever the machine has.
func noDocker(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "no-docker.sock"))
	t.Setenv("DOCKER_CONTEXT", "")
}

// TestProjectRemoveDeleteWorktreesNeedsDocker: when Docker cannot say
// whether instances have containers, nothing is deleted or unregistered,
// and the listing still shows what would have been deleted.
func TestProjectRemoveDeleteWorktreesNeedsDocker(t *testing.T) {
	main, linked := testutil.NewRepo(t, map[string]string{
		"compose.yaml": "name: theo-cli-delwt-nodocker\nservices:\n  web:\n    image: scratch\n",
	})
	t.Setenv(registry.EnvHome, t.TempDir())
	noDocker(t)
	if _, stderr, code := run("project", "add", main, "--name", "nodocker"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	stdout, stderr, code := run("project", "remove", "nodocker", "--delete-worktrees", "--confirm", "nodocker", "--discard-changes")
	if code == ExitOK {
		t.Fatalf("removed without Docker: %s", stdout)
	}
	checkOutput(t, "stderr", stderr, "no worktree was deleted")
	checkOutput(t, "stdout", stdout, linked+" (worktree, feature/x) clean")
	if _, err := os.Stat(linked); err != nil {
		t.Errorf("the worktree was deleted: %v", err)
	}
	if _, _, code := run("project", "show", "nodocker"); code != ExitOK {
		t.Error("a refused removal must keep the project")
	}
}

// TestProjectRemoveHintsAtDeleteWorktrees: without the flag nothing is
// deleted, and the listing mentions the flag when worktrees remain. It does
// not need Docker: without the flag a removal goes on when Docker is away.
func TestProjectRemoveHintsAtDeleteWorktrees(t *testing.T) {
	main, linked := testutil.NewRepo(t, map[string]string{
		"compose.yaml": "name: theo-cli-delwt-hint\nservices:\n  web:\n    image: scratch\n",
	})
	t.Setenv(registry.EnvHome, t.TempDir())
	noDocker(t)
	if _, stderr, code := run("project", "add", main, "--name", "hint"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	stdout, stderr, code := run("project", "remove", "hint")
	if code != ExitOK {
		t.Fatalf("project remove: %s", stderr)
	}
	checkOutput(t, "stdout", stdout, "--delete-worktrees --confirm hint")
	if _, err := os.Stat(linked); err != nil {
		t.Errorf("the worktree was deleted without --delete-worktrees: %v", err)
	}
}
