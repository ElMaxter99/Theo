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

func t2ProjectRepo(t *testing.T, composeName string) (main, linked string) {
	t.Helper()
	main, linked = testutil.NewRepo(t, map[string]string{
		".gitignore": "server\nDockerfile\n",
		"compose.yaml": "name: " + composeName + `
services:
  web:
    build: .
`,
	})
	testutil.WriteServerImage(t, main)
	testutil.WriteServerImage(t, linked)
	return main, linked
}

// TestT2ProjectShowRenameRemove covers FR-003 and FR-004 with a real Docker
// daemon: show, rename (with its refusals) and remove (what keeps existing,
// --yes, --forget-records).
func TestT2ProjectShowRenameRemove(t *testing.T) {
	testutil.RequireDaemon(t)
	main, linked := t2ProjectRepo(t, "t2-proj")
	otherMain, _ := t2ProjectRepo(t, "t2-proj-other")
	home := t.TempDir()
	t.Setenv(registry.EnvHome, home)
	t2NoComposeFile(t)
	t2Cleanup(t, "t2-proj", "t2proj-app-feature", "t2renamed-app-feature")

	for _, args := range [][]string{{"project", "add", main, "--name", "t2proj"}, {"project", "add", otherMain, "--name", "t2other"}} {
		if _, stderr, code := run(args...); code != ExitOK {
			t.Fatalf("%v: %s", args, stderr)
		}
	}

	// show
	stdout, _, code := run("project", "show", "t2proj")
	if code != ExitOK {
		t.Fatal("project show failed")
	}
	for _, want := range []string{"t2proj", main, "Compose files", "Worktree dir", "Theo's own"} {
		checkOutput(t, "project show", stdout, want)
	}
	stdout, _, _ = run("project", "show", "t2proj", "--json")
	var info map[string]any
	if err := json.Unmarshal([]byte(stdout), &info); err != nil || info["name"] != "t2proj" || info["path"] != main || info["worktree_dir_effective"] == "" {
		t.Errorf("project show --json = %s (%v)", stdout, err)
	}
	if _, stderr, code := run("project", "show", "nope"); code != ExitError {
		t.Errorf("unknown project: code %d, %s", code, stderr)
	}

	// Some records keyed by the project name.
	if _, stderr, code := run("env", "set", "t2proj", "-w", "feature/x", "A=1"); code != ExitOK {
		t.Fatalf("env set: %s", stderr)
	}
	if _, stderr, code := run("up", "t2proj", "-w", "feature/x"); code != ExitOK {
		t.Fatalf("up: %s", stderr)
	}

	// rename refusals: taken name, and containers under the old names.
	if _, stderr, code := run("project", "rename", "t2proj", "t2other"); code != ExitError {
		t.Errorf("collision: code %d", code)
	} else {
		checkOutput(t, "collision", stderr, "already registered")
	}
	if _, stderr, code := run("project", "rename", "t2proj", "t2renamed"); code != ExitError {
		t.Fatalf("rename with containers: code %d", code)
	} else {
		checkOutput(t, "containers", stderr, "t2proj-app-feature")
		checkOutput(t, "containers", stderr, "derive from the project name")
	}
	if _, stderr, code := run("project", "rename", "t2proj"); code != ExitUsage {
		t.Errorf("rename without a new name: code %d, %s", code, stderr)
	}
	if _, stderr, code := run("down", "t2proj", "-w", "feature/x"); code != ExitOK {
		t.Fatalf("down: %s", stderr)
	}

	// rename: records follow, the new Compose name is used.
	stdout, stderr, code := run("project", "rename", "t2proj", "t2renamed")
	if code != ExitOK {
		t.Fatalf("rename: %s", stderr)
	}
	checkOutput(t, "rename", stdout, `Renamed project "t2proj" to "t2renamed"`)
	if _, _, code := run("project", "show", "t2proj"); code == ExitOK {
		t.Error("the old name still resolves")
	}
	stdout, _, _ = run("env", "list", "t2renamed", "-w", "feature/x")
	checkOutput(t, "env list", stdout, "A")
	stdout, _, _ = run("history", "t2renamed", "-w", "feature/x")
	checkOutput(t, "history", stdout, "up")
	if _, stderr, code := run("up", "t2renamed", "-w", "feature/x"); code != ExitOK {
		t.Fatalf("up after rename: %s", stderr)
	} else {
		checkOutput(t, "up after rename", stderr, `compose project "t2renamed-app-feature"`)
	}

	// remove: lists what stays, refused with containers, --yes goes on.
	stdout, stderr, code = run("project", "remove", "t2renamed")
	if code != ExitError {
		t.Fatalf("remove with containers: code %d\n%s", code, stdout)
	}
	for _, want := range []string{"worktrees on disk", main, linked, "t2renamed-app-feature", "Compose projects with containers"} {
		checkOutput(t, "remove list", stdout, want)
	}
	checkOutput(t, "remove refusal", stderr, "--yes")
	if _, _, code := run("project", "show", "t2renamed"); code != ExitOK {
		t.Fatal("a refused removal must keep the project")
	}
	run("down", "t2renamed", "-w", "feature/x")

	stdout, _, _ = run("project", "remove", "t2renamed", "--json")
	var res struct {
		Removed   bool `json:"removed"`
		Resources struct {
			Volumes []string `json:"volumes"`
			Records struct {
				Settings int `json:"instance_settings"`
			} `json:"records"`
		} `json:"resources"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil || !res.Removed || len(res.Resources.Volumes) != 0 || res.Resources.Records.Settings != 1 {
		t.Errorf("project remove --json = %s (%v)", stdout, err)
	}
	if _, err := os.Stat(main); err != nil {
		t.Error("remove deleted the worktree")
	}
	data, _ := os.ReadFile(filepath.Join(home, "instances.json"))
	if !strings.Contains(string(data), linked) {
		t.Errorf("without --forget-records the settings stay: %s", data)
	}

	// --forget-records drops Theo's records, not the history.
	if _, stderr, code := run("project", "add", main, "--name", "t2again"); code != ExitOK {
		t.Fatalf("add again: %s", stderr)
	}
	stdout, stderr, code = run("project", "remove", "t2again", "--forget-records")
	if code != ExitOK {
		t.Fatalf("remove --forget-records: %s", stderr)
	}
	checkOutput(t, "forget", stdout, "forgotten")
	data, _ = os.ReadFile(filepath.Join(home, "instances.json"))
	if strings.Contains(string(data), linked) {
		t.Errorf("settings remain after --forget-records: %s", data)
	}
	if _, err := os.Stat(filepath.Join(home, "history.jsonl")); err != nil {
		t.Errorf("the history must stay: %v", err)
	}
}

// TestT2DuplicateComposeNames covers FR-040 with the real tools: two
// projects whose main worktrees would both run as the same Compose project.
func TestT2DuplicateComposeNames(t *testing.T) {
	testutil.RequireDaemon(t)
	first, _ := t2ProjectRepo(t, "t2-dup")
	second, _ := t2ProjectRepo(t, "t2-dup")
	t.Setenv(registry.EnvHome, t.TempDir())
	t2NoComposeFile(t)
	t2Cleanup(t, "t2-dup")

	for _, a := range [][]string{{"project", "add", first, "--name", "t2dupa"}, {"project", "add", second, "--name", "t2dupb"}} {
		if _, stderr, code := run(a...); code != ExitOK {
			t.Fatalf("%v: %s", a, stderr)
		}
	}
	for _, name := range []string{"t2dupa", "t2dupb"} {
		_, stderr, code := run("up", name)
		if code != ExitError {
			t.Fatalf("up %s: code %d, want a refusal", name, code)
		}
		other := map[string]string{"t2dupa": "t2dupb", "t2dupb": "t2dupa"}[name]
		for _, want := range []string{`"t2-dup"`, `project "` + other + `"`, "project rename", "name:"} {
			checkOutput(t, "up "+name, stderr, want)
		}
	}
	if out := testutil.Docker(t, "ps", "--all", "--filter", "label=com.docker.compose.project=t2-dup", "--format", "{{.Names}}"); out != "" {
		t.Errorf("a refused up started containers: %s", out)
	}
	stdout, _, _ := run("reconcile")
	checkOutput(t, "reconcile", stdout, "duplicate compose name")
	checkOutput(t, "reconcile", stdout, "t2-dup")
	stdout, _, _ = run("reconcile", "t2dupa", "--json")
	checkOutput(t, "reconcile json", stdout, `"kind": "duplicate compose name"`)

	// Renaming does not fix a name that comes from the Compose file; changing
	// it does.
	if err := os.WriteFile(filepath.Join(second, "compose.yaml"), []byte("name: t2-dup-fixed\nservices:\n  web:\n    build: .\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if stdout, _, _ := run("reconcile"); strings.Contains(stdout, "duplicate compose name") {
		t.Errorf("fixed, but still reported:\n%s", stdout)
	}
	if _, stderr, code := run("up", "t2dupa"); code != ExitOK {
		t.Fatalf("up after the fix: %s", stderr)
	}
	run("down", "t2dupa")
}
