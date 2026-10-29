// Package acceptance holds the acceptance suite of Theo: one test per use
// case of the MVP (UC-01 to UC-08), driven through the command-line
// interface against real Git and Docker. Parts that need a Docker daemon that
// runs Linux containers are skipped where there is none (the macOS and
// Windows runners of CI), and say so; the rest runs everywhere. The
// skip reasons are part of the report: they are the platform limits that
// remain unverified.
package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/cli"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// name is the Theo name of every project of the suite. It differs from the
// ones other packages use, so the Compose projects of linked worktrees
// (<name>-<directory>) never collide when packages run in parallel.
const name = "acc"

// theo runs a Theo command and returns its output and exit code.
func theo(args ...string) (stdout, stderr string, code int) {
	var out, errOut bytes.Buffer
	code = cli.Run(args, &out, &errOut)
	return out.String(), errOut.String(), code
}

// must runs a Theo command and fails the test if it does not succeed.
func must(t *testing.T, args ...string) string {
	t.Helper()
	stdout, stderr, code := theo(args...)
	if code != cli.ExitOK {
		t.Fatalf("theo %s: exit code %d\n%s\n%s", strings.Join(args, " "), code, stdout, stderr)
	}
	return stdout + stderr
}

func contains(t *testing.T, label, text string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Errorf("%s: output lacks %q:\n%s", label, w, text)
		}
	}
}

// project creates a repository with the given Compose file (and the test
// server image, which Compose builds from the project), registers it in a
// fresh Theo data directory, under the project name "acc" and returns the main and the linked worktree.
func project(t *testing.T, compose string, withImage bool) (main, linked string) {
	t.Helper()
	main, linked = testutil.NewRepo(t, map[string]string{
		".gitignore":   "server\nDockerfile\n",
		"compose.yaml": compose,
	})
	if withImage {
		testutil.WriteServerImage(t, main)
		testutil.WriteServerImage(t, linked)
	}
	t.Setenv(registry.EnvHome, t.TempDir())
	must(t, "project", "add", main, "--name", name)
	return main, linked
}

// cleanup removes the Compose projects and volumes a test created, even if it
// failed half way.
func cleanup(t *testing.T, composeProjects ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, name := range composeProjects {
			exec.Command("docker", "compose", "-p", name, "down", "--volumes", "--remove-orphans").Run()
		}
	})
}

func fixedPort(t *testing.T) int { return testutil.FreePort(t) }

// UC-01: bring up an existing Compose project, without changing its files.
func TestUC01_BringUpAnExistingProject(t *testing.T) {
	testutil.RequireCompose(t)
	main, _ := project(t, `name: uc01
services:
  web:
    build: .
    ports: ["8080"]
    volumes: [data:/data]
volumes:
  data: {}
`, false)

	// Registering shows services, ports and volumes (needs only docker compose config).
	out := must(t, "services", name)
	contains(t, "services", out, "web", "8080", "data")

	// Running needs a daemon.
	t.Run("run", func(t *testing.T) {
		testutil.RequireDaemon(t)
		testutil.WriteServerImage(t, main)
		cleanup(t, "uc01")
		must(t, "up", name)
		contains(t, "ps", must(t, "ps", name, "-w", "main"), "running")
		contains(t, "logs", must(t, "logs", name, "--tail", "5", "web"), "server ready")
		must(t, "down", name)
	})
}

// UC-02: try several branches at once, with a fixed port that would collide.
func TestUC02_SeveralBranchesAtOnce(t *testing.T) {
	testutil.RequireCompose(t)
	port := fixedPort(t)
	main, _ := project(t, fmt.Sprintf(`name: uc02
services:
  web:
    build: .
    ports: ["%d:8080"]
`, port), false)

	// A workspace per branch (Git only). The fixture already has one for
	// feature/x, so that branch is refused; new branches are fine.
	if _, stderr, code := theo("worktree", "add", name, "feature/x"); code == cli.ExitOK || !strings.Contains(stderr, "already checked out") {
		t.Errorf("a branch already in a worktree must be refused: code %d, %s", code, stderr)
	}
	must(t, "worktree", "add", name, "fix/new", "--new", "--from", "main")
	contains(t, "worktrees", must(t, "worktrees", name), "feature/x", "fix/new")

	t.Run("run", func(t *testing.T) {
		testutil.RequireDaemon(t)
		testutil.WriteServerImage(t, main)
		testutil.WriteServerImage(t, worktreeDir(t, "fix/new"))
		cleanup(t, "uc02", "acc-fix-new")
		must(t, "up", name)
		// The colliding port is detected before starting, and reassigned on request.
		contains(t, "ports", must(t, "ports", name, "-w", "fix/new"), "in use")
		must(t, "up", name, "-w", "fix/new", "--auto-ports")
		out := must(t, "ports", name, "-w", "fix/new")
		contains(t, "ports", out, fmt.Sprintf("http://localhost:%d", port+1))
		resp, err := http.Get(fmt.Sprintf("http://localhost:%d", port+1))
		if err != nil {
			t.Fatalf("the second instance does not answer: %v", err)
		}
		resp.Body.Close()
		testutil.GitClean(t, main)
	})
}

// UC-03: several agents, each in its own workspace and instance.
func TestUC03_WorkspacePerAgent(t *testing.T) {
	testutil.RequireCompose(t)
	main, _ := project(t, `name: uc03
services:
  web:
    build: .
`, false)
	agents := []string{"agent-1", "agent-2"}
	for _, a := range agents {
		must(t, "worktree", "add", name, a, "--new")
	}
	// Which workspace belongs to which branch is visible.
	out := must(t, "worktrees", name)
	contains(t, "worktrees", out, agents[0], agents[1])

	t.Run("run", func(t *testing.T) {
		testutil.RequireDaemon(t)
		testutil.WriteServerImage(t, main)
		for _, a := range agents {
			testutil.WriteServerImage(t, worktreeDir(t, a))
		}
		cleanup(t, "acc-agent-1", "acc-agent-2")
		for _, a := range agents {
			must(t, "up", name, "-w", a)
		}
		contains(t, "ps", must(t, "ps", name), "acc-agent-1", "acc-agent-2")
		// Stopping one leaves the other alone.
		must(t, "down", name, "-w", agents[0])
		out := must(t, "ps", name)
		if strings.Contains(out, "acc-agent-1") || !strings.Contains(out, "acc-agent-2") {
			t.Errorf("ps after stopping one agent:\n%s", out)
		}
	})
}

// UC-04: a project with several services and dependencies; logs of one.
func TestUC04_MultiServiceProject(t *testing.T) {
	testutil.RequireCompose(t)
	main, _ := project(t, `name: uc04
services:
  db:
    build: .
  api:
    build: .
    depends_on: [db]
  front:
    build: .
    depends_on: [api]
`, false)
	out := must(t, "services", name)
	contains(t, "services", out, "db", "api", "front", "depends on")

	t.Run("run", func(t *testing.T) {
		testutil.RequireDaemon(t)
		testutil.WriteServerImage(t, main)
		cleanup(t, "uc04")
		must(t, "up", name)
		contains(t, "logs of one service", must(t, "logs", name, "--tail", "3", "api"), "server ready")
		must(t, "down", name)
	})
}

// UC-05: instances share a dependency. Not built yet: it depends on Q-023.
func TestUC05_SharedDependencies(t *testing.T) {
	t.Skip("pending: shared services (FR-090 to FR-093) wait for the decision on Q-023")
}

// UC-06: fully isolated instances; down keeps volumes, deleting is explicit.
func TestUC06_IsolatedInstances(t *testing.T) {
	testutil.RequireDaemon(t)
	main, linked := project(t, `name: uc06
services:
  web:
    build: .
    volumes: [data:/data]
volumes:
  data: {}
`, true)
	_ = main
	_ = linked
	cleanup(t, "uc06", "acc-app-feature")
	must(t, "up", name)
	must(t, "up", name, "-w", "feature/x")

	// Each instance has its own volume.
	vols := testutil.Docker(t, "volume", "ls", "--format", "{{.Name}}")
	contains(t, "volumes", vols, "uc06_data", "acc-app-feature_data")

	// down keeps the data; purge is a separate, confirmed step.
	must(t, "down", name, "-w", "feature/x")
	contains(t, "volumes after down", must(t, "volumes", name, "-w", "feature/x"), "kept")
	if _, _, code := theo("volumes", "purge", name, "-w", "feature/x"); code == cli.ExitOK {
		t.Errorf("purge without confirmation succeeded")
	}
	must(t, "volumes", "purge", name, "-w", "feature/x", "--confirm", "acc-app-feature")
	vols = testutil.Docker(t, "volume", "ls", "--format", "{{.Name}}")
	if strings.Contains(vols, "acc-app-feature_data") || !strings.Contains(vols, "uc06_data") {
		t.Errorf("purge removed the wrong volumes: %s", vols)
	}
}

// UC-07: a simple project needs no advanced concepts.
func TestUC07_SimpleProject(t *testing.T) {
	testutil.RequireDaemon(t)
	main, _ := project(t, `name: uc07
services:
  api:
    build: .
`, true)
	_ = main
	cleanup(t, name)
	// Only the basic commands: no env, instance, ports or volumes settings.
	for _, args := range [][]string{{"up", name}, {"ps", name, "-w", "main"}, {"logs", name, "--tail", "3"}, {"down", name}} {
		must(t, args...)
	}
}

// UC-08: a WordPress-like project: database volume, bind mounts of plugins.
func TestUC08_WordPressLikeProject(t *testing.T) {
	testutil.RequireCompose(t)
	main, linked := testutil.NewRepo(t, map[string]string{
		".gitignore": "server\nDockerfile\n",
		"compose.yaml": `name: uc08
services:
  wordpress:
    build: .
    depends_on: [db]
    volumes:
      - ./plugins/my-plugin:/var/www/html/wp-content/plugins/my-plugin
      - ../uploads:/var/www/html/wp-content/uploads
  db:
    build: .
    volumes: [dbdata:/var/lib/mysql]
volumes:
  dbdata: {}
`,
		"plugins/my-plugin/plugin.php": "<?php\n",
	})
	t.Setenv(registry.EnvHome, t.TempDir())
	must(t, "project", "add", main, "--name", name)

	// Host mounts and named volumes are shown separately, and a mount that
	// leaves the workspace is flagged.
	out := must(t, "services", name)
	contains(t, "services", out, "plugins", "dbdata", "outside")

	t.Run("run", func(t *testing.T) {
		testutil.RequireDaemon(t)
		testutil.WriteServerImage(t, main)
		testutil.WriteServerImage(t, linked)
		cleanup(t, "uc08", "acc-app-feature")
		must(t, "up", name)
		must(t, "up", name, "-w", "feature/x")
		vols := testutil.Docker(t, "volume", "ls", "--format", "{{.Name}}")
		contains(t, "database volumes", vols, "uc08_dbdata", "acc-app-feature_dbdata")
	})
}

// Paths with spaces and non-ASCII characters work on every system.
func TestPathsWithSpacesAndAccents(t *testing.T) {
	testutil.RequireCompose(t)
	main, _ := project(t, `name: paths
services:
  web:
    image: scratch
`, false)
	dir := filepath.Join(testutil.TempDir(t), "mi carpeta", "año nuevo")
	out := must(t, "worktree", "add", name, "paths/x", "--new", "--path", dir)
	contains(t, "worktree add", out, "año nuevo")
	if _, err := os.Stat(filepath.Join(dir, "compose.yaml")); err != nil {
		t.Fatalf("the worktree is not there: %v", err)
	}
	contains(t, "services", must(t, "services", name, "-w", "paths/x"), "web")
	_ = main
}

// worktreeDir returns the directory of the worktree of a branch.
func worktreeDir(t *testing.T, branch string) string {
	t.Helper()
	out := must(t, "worktrees", name, "--json")
	var list []struct {
		Path   string `json:"path"`
		Branch string `json:"branch"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("worktrees --json: %v\n%s", err, out)
	}
	for _, wt := range list {
		if wt.Branch == branch {
			return wt.Path
		}
	}
	t.Fatalf("no worktree for %s in %s", branch, out)
	return ""
}
