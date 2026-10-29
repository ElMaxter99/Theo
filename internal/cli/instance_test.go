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

// TestInstanceSettings runs two worktrees with the same fixed container_name,
// with a real Docker daemon: the second needs the name neutralized, and an
// instance variable reaches the container while the secret stays masked.
func TestInstanceSettings(t *testing.T) {
	testutil.RequireDaemon(t)
	main, linked := testutil.NewRepo(t, map[string]string{
		".gitignore": "server\nDockerfile\n",
		"compose.yaml": `name: theo-test-settings
services:
  web:
    build: .
    container_name: theo-test-fixed
    environment:
      FROM_FILE: original
`,
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
		testutil.Docker(t, "rm", "--force", "theo-test-fixed")
	})

	if _, stderr, code := run("up", "app"); code != ExitOK {
		t.Fatalf("up main: %s", stderr)
	}

	// Without neutralizing, the linked instance warns and Docker refuses.
	_, stderr, code := run("up", "app", "-w", "feature/x")
	if code == ExitOK {
		t.Fatalf("a second instance with the same container_name should fail")
	}
	checkOutput(t, "warning", stderr, "--neutralize-container-names")

	if _, stderr, code := run("instance", "set", "app", "-w", "feature/x", "--neutralize-container-names"); code != ExitOK {
		t.Fatalf("instance set: %s", stderr)
	}
	if _, stderr, code := run("env", "set", "app", "-w", "feature/x", "TOKEN=s3cret-value", "--secret"); code != ExitOK {
		t.Fatalf("env set: %s", stderr)
	}
	if _, stderr, code := run("env", "set", "app", "-w", "feature/x", "FROM_FILE=changed", "--service", "web"); code != ExitOK {
		t.Fatalf("env set: %s", stderr)
	}
	stdout, _, _ := run("env", "list", "app", "-w", "feature/x")
	checkOutput(t, "env list", stdout, "TOKEN")
	if strings.Contains(stdout, "s3cret-value") {
		t.Errorf("env list shows the secret:\n%s", stdout)
	}
	stdout, _, _ = run("env", "list", "app", "-w", "feature/x", "--show-secrets")
	checkOutput(t, "env list --show-secrets", stdout, "s3cret-value")

	stdout, _, _ = run("instance", "config", "app", "-w", "feature/x")
	if strings.Contains(stdout, "s3cret-value") {
		t.Errorf("instance config shows the secret:\n%s", stdout)
	}
	checkOutput(t, "instance config", stdout, "changed")

	if stdout, stderr, code := run("up", "app", "-w", "feature/x"); code != ExitOK {
		t.Fatalf("up linked: %s\n%s", stdout, stderr)
	}
	id := testutil.Docker(t, "ps", "-q", "--filter", "label=com.docker.compose.project=app-app-feature")
	env := testutil.Docker(t, "inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", id)
	for _, want := range []string{"TOKEN=s3cret-value", "FROM_FILE=changed"} {
		checkOutput(t, "container env", env, want)
	}
	if name := testutil.Docker(t, "inspect", "--format", "{{.Name}}", id); name == "/theo-test-fixed" {
		t.Errorf("the linked container kept the fixed name")
	}
	// The main instance keeps its own value and name.
	mainID := testutil.Docker(t, "ps", "-q", "--filter", "name=^theo-test-fixed$")
	mainEnv := testutil.Docker(t, "inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", mainID)
	checkOutput(t, "main env", mainEnv, "FROM_FILE=original")

	// The project's files were not touched.
	testutil.GitClean(t, main)
	testutil.GitClean(t, linked)

	stdout, _, _ = run("history", "app", "-w", "feature/x")
	checkOutput(t, "history", stdout, "up")
	checkOutput(t, "history", stdout, "error") // the first, refused up
	checkOutput(t, "history", stdout, "ok")
}

// TestVolumes follows a volume through up, down and an explicit purge with a
// real Docker daemon, and checks that an external volume is never touched.
func TestVolumes(t *testing.T) {
	testutil.RequireDaemon(t)
	main, _ := testutil.NewRepo(t, map[string]string{
		".gitignore": "server\nDockerfile\n",
		"compose.yaml": `name: theo-test-vols
services:
  web:
    build: .
    volumes: [data:/data, shared:/shared]
volumes:
  data: {}
  shared:
    external: true
    name: theo-test-external-vol
`,
	})
	testutil.WriteServerImage(t, main)
	testutil.Docker(t, "volume", "create", "theo-test-external-vol")
	t.Setenv(registry.EnvHome, t.TempDir())
	t.Cleanup(func() {
		run("down", "app")
		testutil.Docker(t, "volume", "rm", "--force", "theo-test-vols_data", "theo-test-external-vol")
	})
	if _, stderr, code := run("project", "add", main); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	if _, stderr, code := run("up", "app"); code != ExitOK {
		t.Fatalf("up: %s", stderr)
	}

	stdout, _, _ := run("volumes", "app")
	checkOutput(t, "volumes (up)", stdout, "in use")
	checkOutput(t, "volumes (up)", stdout, "external")

	// With containers, even a confirmed purge is refused.
	_, stderr, code := run("volumes", "purge", "app", "--confirm", "theo-test-vols")
	if code == ExitOK {
		t.Fatalf("purge with containers succeeded")
	}
	checkOutput(t, "purge refused", stderr, "theo down")

	if _, stderr, code := run("down", "app"); code != ExitOK {
		t.Fatalf("down: %s", stderr)
	}
	stdout, _, _ = run("volumes", "app")
	checkOutput(t, "volumes (down)", stdout, "kept")

	// No confirmation: the list, the exact command, and nothing is deleted.
	_, stderr, code = run("volumes", "purge", "app")
	if code == ExitOK {
		t.Fatalf("purge without confirmation succeeded")
	}
	checkOutput(t, "purge unconfirmed", stderr, "theo-test-vols_data")
	checkOutput(t, "purge unconfirmed", stderr, "--confirm theo-test-vols")
	if strings.Contains(stderr, "theo-test-external-vol") {
		t.Errorf("the external volume is in the purge list:\n%s", stderr)
	}
	if !strings.Contains(testutil.Docker(t, "volume", "ls", "--format", "{{.Name}}"), "theo-test-vols_data") {
		t.Fatalf("the volume was deleted without confirmation")
	}

	if stdout, stderr, code := run("volumes", "purge", "app", "--confirm", "theo-test-vols"); code != ExitOK {
		t.Fatalf("confirmed purge: %s\n%s", stdout, stderr)
	}
	volumes := testutil.Docker(t, "volume", "ls", "--format", "{{.Name}}")
	if strings.Contains(volumes, "theo-test-vols_data") {
		t.Errorf("the volume is still there after the purge")
	}
	if !strings.Contains(volumes, "theo-test-external-vol") {
		t.Errorf("the external volume was deleted")
	}
}
