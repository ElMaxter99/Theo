package cli

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// TestReconcile changes things outside Theo (removes containers, deletes a
// worktree, starts a stack by hand) and checks that reconcile reports each
// difference, that --apply forgets only Theo's own records, and that
// containers and volumes are never touched.
func TestReconcile(t *testing.T) {
	testutil.RequireDaemon(t)
	main, linked := testutil.NewRepo(t, map[string]string{
		".gitignore": "server\nDockerfile\n",
		"compose.yaml": `name: theo-test-recon
services:
  web:
    build: .
    volumes: [data:/data]
volumes:
  data: {}
`,
	})
	testutil.WriteServerImage(t, main)
	testutil.WriteServerImage(t, linked)
	home := t.TempDir()
	t.Setenv(registry.EnvHome, home)
	const linkedName = "app-app-feature"
	t.Cleanup(func() {
		run("down", "app")
		testutil.Docker(t, "compose", "-p", linkedName, "down", "--volumes", "--remove-orphans")
		testutil.Docker(t, "compose", "-p", "theo-test-recon", "down", "--volumes", "--remove-orphans")
	})
	if _, stderr, code := run("project", "add", main); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}

	// Nothing differs yet.
	stdout, _, _ := run("reconcile", "app")
	checkOutput(t, "clean", stdout, "matches what exists")

	// A stack started by hand in the linked worktree, with the name the file
	// gives it, is not the instance Theo would use there.
	cmd := exec.Command("docker", "compose", "up", "--detach", "--build")
	cmd.Dir = linked
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker compose up: %v\n%s", err, out)
	}
	stdout, _, _ = run("reconcile", "app")
	checkOutput(t, "foreign stack", stdout, "started outside Theo")
	checkOutput(t, "foreign stack", stdout, "theo-test-recon")
	checkOutput(t, "foreign stack", stdout, linkedName)
	testutil.Docker(t, "compose", "-p", "theo-test-recon", "down")
	// ...and the main worktree running under its own name is fine.
	if _, stderr, code := run("up", "app"); code != ExitOK {
		t.Fatalf("up main: %s", stderr)
	}
	stdout, _, _ = run("reconcile", "app")
	checkOutput(t, "main is fine", stdout, "matches what exists")

	// Theo starts the linked worktree with settings, then its containers are
	// removed behind its back.
	run("down", "app")
	if _, stderr, code := run("env", "set", "app", "-w", "feature/x", "A=1"); code != ExitOK {
		t.Fatalf("env set: %s", stderr)
	}
	if _, stderr, code := run("up", "app", "-w", "feature/x"); code != ExitOK {
		t.Fatalf("up linked: %s", stderr)
	}
	ids := testutil.Docker(t, "ps", "-aq", "--filter", "label=com.docker.compose.project="+linkedName)
	testutil.Docker(t, "rm", "--force", ids)
	stdout, _, _ = run("reconcile", "app")
	checkOutput(t, "removed containers", stdout, "containers removed")
	checkOutput(t, "removed containers", stdout, "removed outside Theo")

	// Start again, then delete the worktree with Git, leaving containers and a volume.
	if _, stderr, code := run("up", "app", "-w", "feature/x"); code != ExitOK {
		t.Fatalf("up linked again: %s", stderr)
	}
	if out, err := exec.Command("git", "-C", main, "worktree", "remove", "--force", linked).CombinedOutput(); err != nil {
		t.Fatalf("git worktree remove: %v\n%s", err, out)
	}
	if _, err := os.Stat(linked); err == nil {
		t.Fatalf("the worktree still exists")
	}
	stdout, _, code := run("reconcile", "app")
	if code != ExitOK {
		t.Fatalf("reconcile: code %d", code)
	}
	for _, want := range []string{"orphan containers", "orphan volumes", "stale settings", "docker compose -p " + linkedName + " down", linkedName + "_data"} {
		checkOutput(t, "orphans", stdout, want)
	}
	checkOutput(t, "orphans", stdout, "can be forgotten")

	// --apply forgets Theo's own records and nothing else.
	stdout, _, _ = run("reconcile", "app", "--apply")
	checkOutput(t, "apply", stdout, "Nothing else was touched")
	stdout, _, _ = run("reconcile", "app")
	if strings.Contains(stdout, "stale") {
		t.Errorf("stale records remain after --apply:\n%s", stdout)
	}
	checkOutput(t, "after apply", stdout, "orphan containers") // still reported: Theo does not touch them
	if ps := testutil.Docker(t, "ps", "-q", "--filter", "label=com.docker.compose.project="+linkedName); ps == "" {
		t.Errorf("--apply removed the containers")
	}
	if vols := testutil.Docker(t, "volume", "ls", "--format", "{{.Name}}"); !strings.Contains(vols, linkedName+"_data") {
		t.Errorf("--apply removed the volume")
	}
}
