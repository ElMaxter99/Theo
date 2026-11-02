package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// open, the compose-changed warning and stop --all against a real Docker.
func TestOpenComposeChangedAndStopAll(t *testing.T) {
	testutil.RequireDaemon(t)
	port := testutil.FreePort(t)
	file := func(cmd string) string {
		return fmt.Sprintf(`services:
  api:
    image: alpine:3
    command: ["sleep", "%s"]
    stop_signal: SIGKILL
    ports: ["%d:8080"]
`, cmd, port)
	}
	main, _ := testutil.NewRepo(t, map[string]string{"compose.yaml": file("3600")})
	t.Setenv(registry.EnvHome, t.TempDir())
	const project = "v02x"
	if _, stderr, code := run("project", "add", main, "--name", project); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	t.Cleanup(func() { run("down", project) })

	if _, stderr, code := run("open", project, "--print"); code != ExitError || !strings.Contains(stderr, "theo up v02x") {
		t.Errorf("open before up: %d %s", code, stderr)
	}
	if _, stderr, code := run("up", project); code != ExitOK {
		t.Fatalf("up: %s", stderr)
	}

	// open: the host port without a proxy, the local domain with one.
	stdout, stderr, code := run("open", project, "--print")
	if want := fmt.Sprintf("http://localhost:%d\n", port); code != ExitOK || stdout != want {
		t.Errorf("open --print = %d %q %s, want %q", code, stdout, stderr, want)
	}
	orig := proxyPortForCLI
	t.Cleanup(func() { proxyPortForCLI = orig })
	proxyPortForCLI = func() (int, bool) { return 7080, true }
	if stdout, _, _ := run("open", project, "--print"); stdout != "http://main.v02x.localhost:7080\n" {
		t.Errorf("open with a proxy = %q", stdout)
	}
	if stdout, _, _ := run("open", project, "api", "--print"); stdout != "http://api.main.v02x.localhost:7080\n" {
		t.Errorf("open api with a proxy = %q", stdout)
	}
	if _, stderr, code := run("open", project, "nope", "--print"); code == ExitOK || !strings.Contains(stderr, "nope") {
		t.Errorf("open of an unknown service: %d %s", code, stderr)
	}
	proxyPortForCLI = func() (int, bool) { return 0, false }
	var opened string
	origOpen := openURL
	t.Cleanup(func() { openURL = origOpen })
	openURL = func(u string) error { opened = u; return nil }
	if _, stderr, code := run("open", project); code != ExitOK || opened != fmt.Sprintf("http://localhost:%d", port) {
		t.Errorf("open = %d %q %s", code, opened, stderr)
	}

	// compose-changed: none right after up, one after an edit.
	warnings := func() []engine.StatusWarning {
		t.Helper()
		stdout, stderr, code := run("status", project, "--json")
		if code != ExitOK {
			t.Fatalf("status: %s", stderr)
		}
		var r engine.StatusReport
		if err := json.Unmarshal([]byte(stdout), &r); err != nil {
			t.Fatal(err)
		}
		return r.Projects[0].Worktrees[0].Warnings
	}
	has := func(ws []engine.StatusWarning) bool {
		for _, w := range ws {
			if w.Kind == engine.WarnComposeChanged {
				return true
			}
		}
		return false
	}
	if ws := warnings(); has(ws) {
		t.Errorf("compose-changed right after up: %+v", ws)
	}
	if err := os.WriteFile(filepath.Join(main, "compose.yaml"), []byte(file("3601")), 0o644); err != nil {
		t.Fatal(err)
	}
	if ws := warnings(); !has(ws) {
		t.Errorf("no compose-changed after editing compose.yaml: %+v", ws)
	}
	stdout, _, _ = run("status", project)
	if !strings.Contains(stdout, "Compose configuration changed") || !strings.Contains(stdout, "theo up v02x -w main") {
		t.Errorf("status text:\n%s", stdout)
	}
	if _, stderr, code := run("up", project); code != ExitOK {
		t.Fatalf("up again: %s", stderr)
	}
	if ws := warnings(); has(ws) {
		t.Errorf("compose-changed after up with the new file: %+v", ws)
	}

	// stop --all stops what runs and keeps it.
	stdout, stderr, code = run("stop", "--all")
	if code != ExitOK || !strings.Contains(stdout, "Stopped 1 instance(s)") {
		t.Fatalf("stop --all = %d %s %s", code, stdout, stderr)
	}
	if got := testutil.Docker(t, "ps", "-a", "--filter", "label=com.docker.compose.project=app", "--format", "{{.State}}"); !strings.Contains(got, "exited") {
		t.Errorf("containers after stop --all: %q", got)
	}
	if stdout, _, _ := run("stop", "--all", project); !strings.Contains(stdout, "No instance is running") {
		t.Errorf("second stop --all: %s", stdout)
	}
	if _, _, code := run("stop", "--all", project, "-w", "main"); code != ExitUsage {
		t.Errorf("stop --all -w: exit %d, want usage", code)
	}
}

func TestWorktreePruneMerged(t *testing.T) {
	// Prune keeps a worktree whose containers it cannot check, so the
	// removable cases need a reachable Docker daemon.
	testutil.RequireDaemon(t)
	main, linked := testutil.NewRepo(t, nil)
	t.Setenv(registry.EnvHome, t.TempDir())
	if _, stderr, code := run("project", "add", main, "--name", "prn"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	// feature/x was branched from main and has no commit of its own: merged.
	// wip has a commit main does not have: not merged.
	wip := filepath.Join(filepath.Dir(main), "app-wip")
	testutil.Git(t, main, "worktree", "add", "-q", "-b", "wip", wip)
	testutil.WriteFile(t, wip, "wip.txt", "work\n")
	testutil.Git(t, wip, "add", "-A")
	testutil.Git(t, wip, "commit", "-q", "-m", "wip")

	if _, _, code := run("worktree", "prune", "prn"); code != ExitUsage {
		t.Errorf("prune without --merged: exit %d", code)
	}
	stdout, stderr, code := run("worktree", "prune", "prn", "--merged")
	if code != ExitOK || !strings.Contains(stdout, "feature/x") || !strings.Contains(stdout, "can be removed") || strings.Contains(stdout, "wip") {
		t.Fatalf("dry run = %d\n%s%s", code, stdout, stderr)
	}
	checkOutput(t, "dry run", stdout, "--confirm prn")
	if _, err := os.Stat(linked); err != nil {
		t.Fatalf("the dry run removed %s", linked)
	}

	// Uncommitted files keep a merged worktree.
	testutil.WriteFile(t, linked, "notes.txt", "draft\n")
	stdout, _, _ = run("worktree", "prune", "prn", "--merged", "--json")
	var rep engine.PruneReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("--json: %v\n%s", err, stdout)
	}
	if rep.Into != "main" || rep.NotMerged != 1 || len(rep.Merged) != 1 || !strings.Contains(rep.Merged[0].Reason, "uncommitted") {
		t.Errorf("with a dirty worktree: %+v", rep)
	}
	os.Remove(filepath.Join(linked, "notes.txt"))

	if _, stderr, code := run("worktree", "prune", "prn", "--merged", "--confirm", "other"); code != ExitError || !strings.Contains(stderr, "nothing was removed") {
		t.Errorf("wrong --confirm: %d %s", code, stderr)
	}
	stdout, stderr, code = run("worktree", "prune", "prn", "--merged", "--confirm", "prn")
	if code != ExitOK || !strings.Contains(stdout, "removed") {
		t.Fatalf("prune --confirm = %d\n%s%s", code, stdout, stderr)
	}
	if _, err := os.Stat(linked); !os.IsNotExist(err) {
		t.Errorf("%s still exists", linked)
	}
	if out, err := exec.Command("git", "-C", main, "branch", "--list", "feature/x").Output(); err != nil || !strings.Contains(string(out), "feature/x") {
		t.Errorf("the branch was deleted: %q %v", out, err)
	}
	if _, err := os.Stat(wip); err != nil {
		t.Errorf("the unmerged worktree was removed")
	}
	if _, stderr, code := run("worktree", "prune", "prn", "--merged", "--into", "nope"); code != ExitError || !strings.Contains(stderr, "not a branch") {
		t.Errorf("--into nope: %d %s", code, stderr)
	}
}
