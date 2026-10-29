package cli

import (
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
