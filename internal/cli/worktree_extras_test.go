package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// TestCopyOwnerAndJSON covers --copy, --owner and the JSON of worktrees and
// branches with real git and no Docker.
func TestCopyOwnerAndJSON(t *testing.T) {
	main, linked := testutil.NewRepo(t, map[string]string{".gitignore": ".env.local\nconfig/\n"})
	testutil.WriteFile(t, main, ".env.local", "SECRET=1\n")
	testutil.WriteFile(t, main, "config/secrets.json", "{}\n")
	t.Setenv(registry.EnvHome, t.TempDir())
	if _, stderr, code := run("project", "add", main, "--name", "t4copy"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}

	// --copy needs a list.
	if _, stderr, code := run("worktree", "add", "t4copy", "t4/none", "--new", "--copy"); code == ExitOK || !strings.Contains(stderr, "no files to copy") {
		t.Errorf("--copy without a list: code %d, %q", code, stderr)
	}
	if _, stderr, code := run("project", "set", "t4copy", "--copy-on-add", "../x"); code == ExitOK {
		t.Errorf("--copy-on-add ../x accepted: %q", stderr)
	}
	stdout, stderr, code := run("project", "set", "t4copy", "--copy-on-add", ".env.local", "--copy-on-add", "config/secrets.json")
	if code != ExitOK {
		t.Fatalf("project set: %s", stderr)
	}
	checkOutput(t, "project set", stdout, ".env.local, config/secrets.json")

	stdout, stderr, code = run("worktree", "add", "t4copy", "t4/copy", "--new", "--copy", "--owner", "agent-9")
	if code != ExitOK {
		t.Fatalf("worktree add --copy: %s\n%s", stdout, stderr)
	}
	for _, want := range []string{"Copied from the main worktree: .env.local, config/secrets.json", "Owner: agent-9", "secrets", "--env-file"} {
		checkOutput(t, "worktree add --copy", stdout, want)
	}
	home := os.Getenv(registry.EnvHome)
	dir := filepath.Join(home, "worktrees", "t4copy", "t4-copy")
	if data, err := os.ReadFile(filepath.Join(dir, ".env.local")); err != nil || string(data) != "SECRET=1\n" {
		t.Errorf("copied file: %q, %v", data, err)
	}

	// A one-off file, and a path that escapes.
	if _, stderr, code = run("worktree", "add", "t4copy", "t4/esc", "--new", "--copy-file", "../outside"); code == ExitOK {
		t.Errorf("--copy-file ../outside accepted")
	}
	stdout, _, code = run("worktree", "add", "t4copy", "t4/one", "--new", "--copy-file", ".env.local", "--copy-file", "nothing.txt", "--json")
	if code != ExitOK {
		t.Fatalf("add --copy-file --json: %s", stdout)
	}
	var added struct {
		Kind      string `json:"kind"`
		Copied    []string
		NotCopied []struct{ Path, Reason string } `json:"not_copied"`
	}
	if err := json.Unmarshal([]byte(stdout), &added); err != nil || added.Kind != "worktree" || len(added.Copied) != 1 || len(added.NotCopied) != 1 {
		t.Errorf("add --json = %s (%v)", stdout, err)
	}

	// Owners: table, filter, JSON and change.
	stdout, _, _ = run("worktrees", "t4copy")
	for _, want := range []string{"KIND", "OWNER", "agent-9", "main", "worktree"} {
		checkOutput(t, "worktrees", stdout, want)
	}
	stdout, _, _ = run("worktrees", "t4copy", "--owner", "agent-9")
	checkOutput(t, "worktrees --owner", stdout, "t4/copy")
	if strings.Contains(stdout, "t4/one") || strings.Contains(stdout, "feature/x") {
		t.Errorf("--owner did not filter:\n%s", stdout)
	}
	if _, stderr, code = run("worktree", "owner", "t4copy", "t4/copy", "agent-10"); code != ExitOK {
		t.Fatalf("worktree owner: %s", stderr)
	}
	if _, _, code = run("worktree", "owner", "t4copy", linked, "x", "y"); code == ExitOK {
		t.Errorf("worktree owner with extra arguments accepted")
	}
	stdout, _, _ = run("worktrees", "t4copy", "--json", "--owner", "agent-10")
	var list []map[string]any
	if err := json.Unmarshal([]byte(stdout), &list); err != nil || len(list) != 1 {
		t.Fatalf("worktrees --json = %s (%v)", stdout, err)
	}
	for _, k := range []string{"path", "branch", "head", "dirty", "kind", "owner"} {
		if _, ok := list[0][k]; !ok {
			t.Errorf("worktrees --json lacks %q: %v", k, list[0])
		}
	}
	if list[0]["kind"] != "worktree" || list[0]["owner"] != "agent-10" {
		t.Errorf("worktrees --json = %v", list[0])
	}
	if _, _, code = run("worktree", "owner", "t4copy", "t4/copy", "--clear"); code != ExitOK {
		t.Fatalf("worktree owner --clear failed")
	}
	stdout, _, _ = run("worktrees", "t4copy", "--json", "--owner", "agent-10")
	if strings.TrimSpace(stdout) != "[]" {
		t.Errorf("filter with no match must print [], got %q", stdout)
	}

	stdout, _, _ = run("branches", "t4copy", "--json")
	var branches []struct {
		Name     string `json:"name"`
		Worktree string `json:"worktree"`
	}
	if err := json.Unmarshal([]byte(stdout), &branches); err != nil || len(branches) < 3 {
		t.Fatalf("branches --json = %s (%v)", stdout, err)
	}

	// Clearing the list.
	if _, _, code = run("project", "set", "t4copy", "--no-copy-on-add"); code != ExitOK {
		t.Errorf("--no-copy-on-add failed")
	}
	if _, stderr, code = run("worktree", "add", "t4copy", "t4/after", "--new", "--copy"); code == ExitOK {
		t.Errorf("--copy after --no-copy-on-add should have nothing to copy: %q", stderr)
	}
}

// TestCloneCommand creates an independent clone of a project with a
// submodule (a local fixture; no network).
func TestCloneCommand(t *testing.T) {
	main := testutil.NewRepoWithSubmodule(t, map[string]string{".gitignore": ".env.local\n"})
	testutil.Git(t, main, "remote", "add", "origin", "https://example.invalid/t4.git")
	testutil.WriteFile(t, main, ".env.local", "S=1\n")
	t.Setenv(registry.EnvHome, t.TempDir())
	if _, stderr, code := run("project", "add", main, "--name", "t4sub"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}

	stdout, _, code := run("worktree", "add", "t4sub", "t4/lin", "--new")
	if code != ExitOK {
		t.Fatalf("worktree add on a project with submodules failed")
	}
	checkOutput(t, "worktree add", stdout, "submodules")
	checkOutput(t, "worktree add", stdout, "--clone")

	stdout, stderr, code := run("worktree", "add", "t4sub", "t4/clone", "--new", "--clone", "--owner", "t4-agent", "--copy-file", ".env.local")
	if code != ExitOK {
		t.Fatalf("worktree add --clone: %s\n%s", stdout, stderr)
	}
	dir := filepath.Join(os.Getenv(registry.EnvHome), "worktrees", "t4sub", "t4-clone")
	for _, want := range []string{"independent clone", dir, "https://example.invalid/t4.git", "submodules were initialized", "Copied from the main worktree: .env.local"} {
		checkOutput(t, "worktree add --clone", stdout, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "libs", "shared", "lib.txt")); err != nil {
		t.Errorf("submodule missing in the clone: %v", err)
	}

	stdout, _, _ = run("worktrees", "t4sub")
	for _, want := range []string{"t4/clone", "clone", "t4-agent", "t4/lin"} {
		checkOutput(t, "worktrees", stdout, want)
	}
	stdout, _, _ = run("worktrees", "t4sub", "--json", "--owner", "t4-agent")
	var list []struct {
		Path, Branch, Kind, Owner string
	}
	if err := json.Unmarshal([]byte(stdout), &list); err != nil || len(list) != 1 || list[0].Kind != "clone" || list[0].Path != dir {
		t.Fatalf("worktrees --json = %s (%v)", stdout, err)
	}
	stdout, _, _ = run("branches", "t4sub")
	checkOutput(t, "branches", stdout, dir)

	// A branch cannot be in a clone and a worktree at once.
	if _, stderr, code = run("worktree", "add", "t4sub", "t4/clone", "--clone", "--path", filepath.Join(t.TempDir(), "again")); code == ExitOK || !strings.Contains(stderr, "already checked out") {
		t.Errorf("same branch twice: code %d, %q", code, stderr)
	}
	// A project without a branch given still needs one.
	if _, _, code = run("worktree", "add", "t4sub", "--clone"); code == ExitOK {
		t.Errorf("worktree add --clone without a branch accepted")
	}
}

// TestCloneWorkspaceUp brings up a clone workspace with a real Docker daemon
// and removes it with the same protections as a worktree.
func TestCloneWorkspaceUp(t *testing.T) {
	testutil.RequireDaemon(t)
	main := testutil.NewRepoWithSubmodule(t, map[string]string{
		".gitignore": "server\nDockerfile\n",
		"compose.yaml": `services:
  web:
    build: .
    ports: ["8080"]
`,
	})
	testutil.WriteServerImage(t, main) // Dockerfile and server are ignored: they are copied
	t.Setenv(registry.EnvHome, t.TempDir())
	if _, stderr, code := run("project", "add", main, "--name", "t4clone"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	t.Cleanup(func() { run("down", "t4clone", "-w", "t4/up") })

	stdout, stderr, code := run("worktree", "add", "t4clone", "t4/up", "--new", "--clone", "--owner", "t4-agent", "--copy-file", "Dockerfile", "--copy-file", "server")
	if code != ExitOK {
		t.Fatalf("worktree add --clone: %s\n%s", stdout, stderr)
	}
	dir := filepath.Join(os.Getenv(registry.EnvHome), "worktrees", "t4clone", "t4-up")
	const composeName = "t4clone-t4-up"

	if stdout, stderr, code = run("up", "t4clone", "-w", "t4/up"); code != ExitOK {
		t.Fatalf("up: %s\n%s", stdout, stderr)
	}
	checkOutput(t, "up", stderr, "[clone]")
	checkOutput(t, "up", stderr, composeName)
	stdout, _, _ = run("ps", "t4clone")
	for _, want := range []string{composeName, "t4-agent", "OWNER", dir} {
		checkOutput(t, "ps", stdout, want)
	}
	stdout, _, _ = run("ps", "t4clone", "-w", "t4-up")
	checkOutput(t, "ps -w", stdout, "running")
	if got := testutil.Docker(t, "ps", "--filter", "label=com.docker.compose.project="+composeName, "--format", "{{.Names}}"); got == "" {
		t.Errorf("no container for the clone's Compose project")
	}
	stdout, _, _ = run("services", "t4clone", "-w", dir)
	checkOutput(t, "services", stdout, "web")
	stdout, stderr, code = run("reconcile", "t4clone")
	if code != ExitOK {
		t.Errorf("reconcile: code %d\n%s\n%s", code, stdout, stderr)
	}

	// Containers block the removal; so do the copied (untracked) files.
	if _, stderr, code = run("worktree", "remove", "t4clone", "t4/up"); code == ExitOK || !strings.Contains(stderr, "theo down") {
		t.Errorf("remove with containers: code %d, %q", code, stderr)
	}
	if _, stderr, code = run("down", "t4clone", "-w", "t4/up"); code != ExitOK {
		t.Fatalf("down: %s", stderr)
	}
	testutil.WriteFile(t, dir, "notes.txt", "mine\n")
	_, stderr, code = run("worktree", "remove", "t4clone", "t4/up")
	if code == ExitOK {
		t.Fatalf("remove of a clone with untracked files succeeded")
	}
	checkOutput(t, "remove", stderr, "--confirm t4-up")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("a refused removal deleted the clone")
	}
	stdout, stderr, code = run("worktree", "remove", "t4clone", "t4/up", "--discard-changes", "--confirm", "t4-up")
	if code != ExitOK {
		t.Fatalf("confirmed remove: %s\n%s", stdout, stderr)
	}
	checkOutput(t, "remove", stdout, "Removed clone")
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("clone directory still exists")
	}
	stdout, _, _ = run("worktrees", "t4clone")
	if strings.Contains(stdout, "t4/up") {
		t.Errorf("removed clone still listed:\n%s", stdout)
	}
}
