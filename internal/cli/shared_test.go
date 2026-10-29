package cli

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// TestSharedServices shares a service between two instances of a project
// with a real Docker daemon: the shared service runs once in its own Compose
// project, the instances stop running their own copy and join the network,
// and every protection of FR-092 holds.
func TestSharedServices(t *testing.T) {
	testutil.RequireDaemon(t)
	port := testutil.FreePort(t)
	main, linked := testutil.NewRepo(t, map[string]string{
		".gitignore": "server\nDockerfile\n",
		"compose.yaml": fmt.Sprintf(`name: shtest
services:
  db:
    build: .
    ports: ["%d:8080"]
    volumes: [data:/data]
  api:
    build: .
    depends_on: [db]
volumes:
  data: {}
`, port),
	})
	testutil.WriteServerImage(t, main)
	testutil.WriteServerImage(t, linked)
	t.Setenv(registry.EnvHome, t.TempDir())
	const network, provider = "theo-shp-shared", "shp-shared"
	t.Cleanup(func() {
		for _, w := range []string{"", "feature/x"} {
			run("down", "shp", "-w", w)
		}
		exec.Command("docker", "compose", "-p", provider, "down", "--volumes").Run()
		exec.Command("docker", "network", "rm", network).Run()
	})
	if _, stderr, code := run("project", "add", main, "--name", "shp"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	containers := func(project string) string {
		return testutil.Docker(t, "ps", "-a", "--filter", "label=com.docker.compose.project="+project, "--format", "{{.Label \"com.docker.compose.service\"}}")
	}

	// Declaring shares nothing and starts nothing.
	if _, stderr, code := run("shared", "add", "shp", "nope"); code == ExitOK {
		t.Fatalf("an unknown service was accepted: %s", stderr)
	}
	if _, stderr, code := run("shared", "add", "shp", "db"); code != ExitOK {
		t.Fatalf("shared add: %s", stderr)
	}
	stdout, _, _ := run("shared", "status", "shp")
	checkOutput(t, "status", stdout, "not created")
	checkOutput(t, "status", stdout, "No instance uses them")

	// An instance that uses it refuses to start while the provider is down,
	// unless it is told to start it.
	for _, w := range []string{"", "feature/x"} {
		if _, stderr, code := run("shared", "use", "shp", "-w", w, "db"); code != ExitOK {
			t.Fatalf("shared use -w %q: %s", w, stderr)
		}
	}
	_, stderr, code := run("up", "shp")
	if code == ExitOK {
		t.Fatalf("up with the provider stopped must be refused")
	}
	checkOutput(t, "refusal", stderr, "--start-shared")
	if containers(provider) != "" {
		t.Fatalf("the shared services were started in silence")
	}
	if stdout, stderr, code := run("up", "shp", "--start-shared"); code != ExitOK {
		t.Fatalf("up --start-shared: %s\n%s", stdout, stderr)
	}
	if stdout, stderr, code := run("up", "shp", "-w", "feature/x"); code != ExitOK {
		t.Fatalf("up linked: %s\n%s", stdout, stderr)
	}

	// db runs once, in the shared project; the instances run only api.
	if got := strings.TrimSpace(containers(provider)); got != "db" {
		t.Errorf("shared project runs %q, want only db", got)
	}
	for _, p := range []string{"shtest", "shp-app-feature"} {
		if got := strings.TrimSpace(containers(p)); got != "api" {
			t.Errorf("instance %s runs %q, want only api", p, got)
		}
	}
	// The shared db is not published on the host; everybody is on the network.
	if ports := testutil.Docker(t, "ps", "--filter", "label=com.docker.compose.project="+provider, "--format", "{{.Ports}}"); strings.Contains(ports, fmt.Sprint(port)) {
		t.Errorf("the shared service publishes a host port: %s", ports)
	}
	members := testutil.Docker(t, "network", "inspect", network, "--format", "{{range .Containers}}{{.Name}} {{end}}")
	if n := len(strings.Fields(members)); n != 3 {
		t.Errorf("the shared network has %d containers (%s), want db and two api", n, members)
	}
	dbID := testutil.Docker(t, "ps", "-q", "--filter", "label=com.docker.compose.project="+provider)
	checkOutput(t, "alias", testutil.Docker(t, "inspect", "--format", "{{json .NetworkSettings.Networks}}", dbID), `"Aliases":["`)
	stdout, _, _ = run("shared", "status", "shp")
	checkOutput(t, "status", stdout, "running")
	checkOutput(t, "status", stdout, "Used by 2 instance(s)")
	checkOutput(t, "status", stdout, "feature/x")

	// Protections: stopping, removing and deleting the worktree that defines it.
	_, stderr, code = run("shared", "down", "shp")
	if code == ExitOK {
		t.Fatalf("shared down with running instances succeeded")
	}
	checkOutput(t, "down refused", stderr, "feature/x")
	checkOutput(t, "down refused", stderr, "--confirm shp-shared")
	if _, _, code := run("shared", "remove", "shp"); code == ExitOK {
		t.Fatalf("shared remove with linked instances succeeded")
	}
	if got := strings.TrimSpace(containers(provider)); got != "db" {
		t.Fatalf("a refused operation changed the shared services: %q", got)
	}

	// The instances stop using it, the shared services stop, then data and
	// declaration are removed as separate, explicit steps.
	for _, w := range []string{"", "feature/x"} {
		run("down", "shp", "-w", w)
		if _, stderr, code := run("shared", "unuse", "shp", "-w", w); code != ExitOK {
			t.Fatalf("shared unuse: %s", stderr)
		}
	}
	if _, stderr, code := run("shared", "down", "shp"); code != ExitOK {
		t.Fatalf("shared down: %s", stderr)
	}
	if containers(provider) != "" {
		t.Errorf("containers remain after shared down")
	}
	if !strings.Contains(testutil.Docker(t, "volume", "ls", "--format", "{{.Name}}"), "shp-shared_data") {
		t.Fatalf("shared down removed the volume")
	}
	if _, _, code := run("shared", "purge", "shp"); code == ExitOK {
		t.Errorf("purge without confirmation succeeded")
	}
	if _, stderr, code := run("shared", "purge", "shp", "--confirm", provider); code != ExitOK {
		t.Fatalf("shared purge: %s", stderr)
	}
	if strings.Contains(testutil.Docker(t, "volume", "ls", "--format", "{{.Name}}"), "shp-shared_data") {
		t.Errorf("purge left the volume")
	}
	if _, stderr, code := run("shared", "remove", "shp"); code != ExitOK {
		t.Fatalf("shared remove: %s", stderr)
	}
	if strings.Contains(testutil.Docker(t, "network", "ls", "--format", "{{.Name}}"), network) {
		t.Errorf("the shared network was not removed")
	}
	// Without sharing, the instance runs its own db again.
	if stdout, stderr, code := run("up", "shp"); code != ExitOK {
		t.Fatalf("up after unsharing: %s\n%s", stdout, stderr)
	}
	if got := strings.Fields(containers("shtest")); len(got) != 2 {
		t.Errorf("without sharing the instance runs %v, want db and api", got)
	}
}
