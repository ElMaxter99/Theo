package cli

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// TestUpPSLogsDown runs two worktrees of the same project at the same time
// with a real Docker daemon, and checks that down keeps their volumes.
func TestUpPSLogsDown(t *testing.T) {
	testutil.RequireDaemon(t)
	main, linked := testutil.NewRepo(t, map[string]string{
		".gitignore": "server\n",
		"compose.yaml": `name: theo-test-app
services:
  web:
    build: .
    ports: ["8080"]
    volumes: [data:/data]
volumes:
  data: {}
`,
	})
	testutil.WriteServerImage(t, main)
	testutil.WriteServerImage(t, linked)
	t.Setenv(registry.EnvHome, t.TempDir())
	if _, stderr, code := run("project", "add", main); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}

	const mainName, linkedName = "theo-test-app", "app-app-feature"
	t.Cleanup(func() {
		for _, w := range []string{"", "feature/x"} {
			run("down", "app", "-w", w)
		}
		for _, name := range []string{mainName, linkedName} {
			testutil.Docker(t, "volume", "rm", "--force", name+"_data")
		}
	})

	for _, w := range []string{"", "feature/x"} {
		if stdout, stderr, code := run("up", "app", "-w", w); code != ExitOK {
			t.Fatalf("up -w %q: code %d\n%s\n%s", w, code, stdout, stderr)
		}
	}

	stdout, stderr, code := run("ps", "app")
	if code != ExitOK {
		t.Fatalf("ps: %s", stderr)
	}
	for _, want := range []string{mainName, linkedName, "running(1)", "0 of 2 worktrees have no containers"} {
		checkOutput(t, "ps", stdout, want)
	}

	stdout, _, _ = run("ps", "app", "-w", "feature/x")
	checkOutput(t, "ps -w", stdout, "running")
	checkOutput(t, "ps -w", stdout, "-> 8080/tcp")

	stdout, _, _ = run("logs", "app", "-w", "feature/x", "--tail", "5", "web")
	checkOutput(t, "logs", stdout, "server ready")

	if _, stderr, code := run("down", "app", "-w", "feature/x"); code != ExitOK {
		t.Fatalf("down: %s", stderr)
	}
	volumes := testutil.Docker(t, "volume", "ls", "--format", "{{.Name}}")
	if !strings.Contains(volumes, linkedName+"_data") {
		t.Errorf("down removed the volume %s_data; volumes: %s", linkedName, volumes)
	}
	stdout, _, _ = run("ps", "app")
	checkOutput(t, "ps after down", stdout, "1 of 2 worktrees have no containers")
	if strings.Contains(stdout, linkedName) {
		t.Errorf("ps after down still lists %s:\n%s", linkedName, stdout)
	}
}

// TestAutoPorts starts the main worktree and a linked one of a project with a
// fixed host port, with a real Docker daemon: the second one needs
// --auto-ports, and keeps its port when started again.
func TestAutoPorts(t *testing.T) {
	testutil.RequireDaemon(t)
	port := testutil.FreePort(t)
	main, linked := testutil.NewRepo(t, map[string]string{
		".gitignore": "server\n",
		"compose.yaml": fmt.Sprintf(`name: theo-test-ports
services:
  web:
    build: .
    ports: ["%d:8080"]
`, port),
	})
	testutil.WriteServerImage(t, main)
	testutil.WriteServerImage(t, linked)
	t.Setenv(registry.EnvHome, t.TempDir())
	if _, stderr, code := run("project", "add", main); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	t.Cleanup(func() {
		for _, w := range []string{"", "feature/x"} {
			run("down", "app", "-w", w)
		}
	})

	if _, stderr, code := run("up", "app"); code != ExitOK {
		t.Fatalf("up main: %s", stderr)
	}
	stdout, _, _ := run("ports", "app")
	checkOutput(t, "ports main", stdout, "running")

	// Without --auto-ports, the taken port is only a warning.
	stdout, _, _ = run("ports", "app", "-w", "feature/x")
	checkOutput(t, "ports linked", stdout, "in use")
	checkOutput(t, "ports linked", stdout, "--auto-ports")
	stdout, _, _ = run("ports", "app", "-w", "feature/x", "--auto-ports")
	checkOutput(t, "ports preview", stdout, "reassigned")
	checkOutput(t, "ports preview", stdout, fmt.Sprint(port+1))

	if stdout, stderr, code := run("up", "app", "-w", "feature/x", "--auto-ports"); code != ExitOK {
		t.Fatalf("up linked: %s\n%s", stdout, stderr)
	} else {
		checkOutput(t, "up --auto-ports", stderr, fmt.Sprintf("host port %d instead of %d", port+1, port))
	}
	stdout, _, _ = run("ps", "app", "-w", "feature/x")
	checkOutput(t, "ps linked", stdout, fmt.Sprintf("%d -> 8080/tcp", port+1))
	stdout, _, _ = run("ports", "app", "-w", "feature/x")
	checkOutput(t, "ports linked running", stdout, fmt.Sprintf("http://localhost:%d", port+1))
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d", port+1))
	if err != nil {
		t.Fatalf("the reassigned port does not answer: %v", err)
	}
	resp.Body.Close()

	// Stop both and start the linked one first: the main one's port is free
	// now, but the linked instance keeps the port it had.
	run("down", "app")
	run("down", "app", "-w", "feature/x")
	_, stderr, code := run("up", "app", "-w", "feature/x", "--auto-ports")
	if code != ExitOK {
		t.Fatalf("second up: %s", stderr)
	}
	checkOutput(t, "second up", stderr, fmt.Sprintf("host port %d instead of %d", port+1, port))
}
