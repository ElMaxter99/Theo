package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// t2Cleanup removes the containers, networks, volumes and local images of
// Compose projects when the test ends. Names must start with "t2".
func t2Cleanup(t *testing.T, names ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, n := range names {
			exec.Command("docker", "compose", "-p", n, "down", "--volumes", "--remove-orphans", "--rmi", "local").Run()
		}
	})
}

// t2NoComposeFile makes sure COMPOSE_FILE is not set in the test process
// (Compose treats an empty value as set), and restores it afterwards.
func t2NoComposeFile(t *testing.T) {
	t.Helper()
	t.Setenv("COMPOSE_FILE", "x")
	os.Unsetenv("COMPOSE_FILE")
}

// dockerComposeConfig runs docker compose config with the given files in dir.
func dockerComposeConfig(t *testing.T, dir string, files ...string) string {
	t.Helper()
	args := []string{"compose"}
	for _, f := range files {
		args = append(args, "-f", f)
	}
	cmd := exec.Command("docker", append(args, "config")...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker compose %v config: %v", files, err)
	}
	return strings.TrimSpace(string(out))
}

// t2FilesRepo is a project whose default compose.yaml is a decoy: the real
// configuration is a.yaml and b.yaml, in that order of importance.
func t2FilesRepo(t *testing.T, port int) (main, linked string) {
	t.Helper()
	main, linked = testutil.NewRepo(t, map[string]string{
		".gitignore":   "server\nDockerfile\n",
		"compose.yaml": "name: t2-files-decoy\nservices:\n  decoy:\n    build: .\n",
		"a.yaml": `name: t2-files
services:
  web:
    build: .
    environment:
      WHO: a
`,
		"b.yaml": fmt.Sprintf(`services:
  web:
    environment:
      WHO: b
    ports: ["%d:8080"]
`, port),
	})
	testutil.WriteServerImage(t, main)
	testutil.WriteServerImage(t, linked)
	return main, linked
}

// TestT2ComposeFilesSelection checks FR-011 with a real Docker daemon: the
// selected files and their order reach config, ps, up and the file Theo
// generates for --auto-ports; the resolved configuration equals
// `docker compose -f B -f A config`.
func TestT2ComposeFilesSelection(t *testing.T) {
	testutil.RequireDaemon(t)
	port := testutil.FreePort(t)
	main, linked := t2FilesRepo(t, port)
	t.Setenv(registry.EnvHome, t.TempDir())
	t2NoComposeFile(t)
	t2Cleanup(t, "t2-files", "t2files-app-feature")

	if _, stderr, code := run("project", "add", main, "--name", "t2files"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	// Files are checked in the main worktree, relative to it, in order.
	if _, stderr, code := run("project", "set", "t2files", "--compose-file", "nope.yaml"); code == ExitOK {
		t.Fatal("a missing file should be refused")
	} else {
		checkOutput(t, "missing file", stderr, "nope.yaml")
		checkOutput(t, "missing file", stderr, main)
	}
	if _, stderr, code := run("project", "set", "t2files", "--compose-file", filepath.Join(main, "a.yaml")); code == ExitOK {
		t.Fatalf("an absolute path should be refused")
	} else {
		checkOutput(t, "absolute", stderr, "relative")
	}
	stdout, stderr, code := run("project", "set", "t2files", "--compose-file", "b.yaml", "--compose-file", "a.yaml")
	if code != ExitOK {
		t.Fatalf("project set: %s", stderr)
	}
	checkOutput(t, "project set", stdout, "b.yaml, a.yaml")

	stdout, _, _ = run("project", "show", "t2files")
	checkOutput(t, "project show", stdout, "b.yaml, a.yaml")
	stdout, _, _ = run("project", "show", "t2files", "--json")
	var shown struct {
		ComposeFiles []string `json:"compose_files"`
	}
	if err := json.Unmarshal([]byte(stdout), &shown); err != nil || len(shown.ComposeFiles) != 2 || shown.ComposeFiles[0] != "b.yaml" {
		t.Errorf("project show --json = %s (%v)", stdout, err)
	}

	// FR-011: the same configuration as docker compose -f B -f A config.
	stdout, stderr, code = run("instance", "config", "t2files")
	if code != ExitOK {
		t.Fatalf("instance config: %s", stderr)
	}
	want := dockerComposeConfig(t, main, "b.yaml", "a.yaml")
	if strings.TrimSpace(stdout) != want {
		t.Errorf("instance config differs from `docker compose -f b.yaml -f a.yaml config`:\n--- theo\n%s\n--- compose\n%s", stdout, want)
	}
	checkOutput(t, "WHO", stdout, "WHO: a")

	// The flag overrides the setting for one command; the order matters.
	stdout, _, code = run("instance", "config", "t2files", "--compose-file", "a.yaml", "--compose-file", "b.yaml")
	if code != ExitOK {
		t.Fatal("instance config with the flag failed")
	}
	if strings.TrimSpace(stdout) != dockerComposeConfig(t, main, "a.yaml", "b.yaml") {
		t.Errorf("flag order is not honored:\n%s", stdout)
	}
	checkOutput(t, "WHO", stdout, "WHO: b")
	stdout, _, _ = run("services", "t2files", "--compose-file", "a.yaml", "--json")
	var svc struct {
		ComposeFiles []string `json:"compose_files"`
		Services     []struct {
			Name string `json:"name"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(stdout), &svc); err != nil || len(svc.Services) != 1 || svc.Services[0].Name != "web" || len(svc.ComposeFiles) != 1 {
		t.Errorf("services = %s (%v): the decoy compose.yaml must not be used", stdout, err)
	}
	if _, stderr, code := run("ports", "t2files", "--compose-file", "missing.yaml"); code == ExitOK {
		t.Error("ports with a missing file should fail")
	} else {
		checkOutput(t, "ports", stderr, "missing.yaml")
	}

	// Start the main worktree, then the linked one: its host port is taken, so
	// --auto-ports adds a generated file after b.yaml and a.yaml.
	if _, stderr, code := run("up", "t2files"); code != ExitOK {
		t.Fatalf("up main: %s", stderr)
	}
	stdout, _, _ = run("ps", "t2files", "-w", "main")
	checkOutput(t, "ps main", stdout, fmt.Sprintf("%d -> 8080/tcp", port))
	if _, stderr, code := run("up", "t2files", "-w", "feature/x", "--auto-ports"); code != ExitOK {
		t.Fatalf("up linked: %s", stderr)
	} else {
		checkOutput(t, "up linked", stderr, "./b.yaml, ./a.yaml")
		checkOutput(t, "up linked", stderr, fmt.Sprintf("host port %d instead of %d", port+1, port))
	}
	stdout, _, _ = run("ps", "t2files", "-w", "feature/x")
	checkOutput(t, "ps linked", stdout, fmt.Sprintf("%d -> 8080/tcp", port+1))
	stdout, _, _ = run("instance", "config", "t2files", "-w", "feature/x", "--auto-ports")
	checkOutput(t, "config linked", stdout, "WHO: a")
	checkOutput(t, "config linked", stdout, fmt.Sprintf("published: \"%d\"", port+1))
	if _, stderr, code := run("logs", "t2files", "-w", "feature/x", "--tail", "3"); code != ExitOK {
		t.Errorf("logs: %s", stderr)
	}
	if _, stderr, code := run("restart", "t2files", "-w", "feature/x"); code != ExitOK {
		t.Errorf("restart: %s", stderr)
	}
	// The decoy never started.
	if out := testutil.Docker(t, "ps", "--all", "--filter", "label=com.docker.compose.service=decoy", "--format", "{{.Names}}"); out != "" {
		t.Errorf("the default compose.yaml was used: %s", out)
	}
	testutil.GitClean(t, main)
	testutil.GitClean(t, linked)

	for _, w := range []string{"main", "feature/x"} {
		if _, stderr, code := run("down", "t2files", "-w", w); code != ExitOK {
			t.Errorf("down %s: %s", w, stderr)
		}
	}
	// Back to Compose's own choice.
	if _, stderr, code := run("project", "set", "t2files", "--default-compose-files"); code != ExitOK {
		t.Fatalf("default: %s", stderr)
	}
	stdout, _, _ = run("services", "t2files", "--json")
	if !strings.Contains(stdout, `"decoy"`) {
		t.Errorf("after --default-compose-files the decoy is the project: %s", stdout)
	}
}

// TestT2ComposeFileVariable checks that a project that sets COMPOSE_FILE,
// in an env file or in the environment, works with --auto-ports, instance
// variables and the other instance commands, with a real Docker daemon.
func TestT2ComposeFileVariable(t *testing.T) {
	testutil.RequireDaemon(t)
	port := testutil.FreePort(t)
	sep := compose.PathSeparator()
	main, linked := testutil.NewRepo(t, map[string]string{
		".gitignore":   "server\nDockerfile\n",
		"compose.yaml": "name: t2-envvar-decoy\nservices:\n  decoy:\n    build: .\n",
		"base.yaml": `name: t2-envvar
services:
  web:
    build: .
    environment:
      WHO: base
`,
		"extra.yaml": fmt.Sprintf(`services:
  web:
    environment:
      WHO: extra
    ports: ["%d:8080"]
`, port),
		".env": "COMPOSE_FILE=base.yaml" + sep + "extra.yaml\n",
	})
	testutil.WriteServerImage(t, main)
	testutil.WriteServerImage(t, linked)
	t.Setenv(registry.EnvHome, t.TempDir())
	t2NoComposeFile(t)
	t2Cleanup(t, "t2-envvar", "t2envvar-app-feature")

	if _, stderr, code := run("project", "add", main, "--name", "t2envvar"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	stdout, _, _ := run("services", "t2envvar", "--json")
	var svc struct {
		ComposeFiles []string `json:"compose_files"`
		Source       string   `json:"compose_files_source"`
	}
	if err := json.Unmarshal([]byte(stdout), &svc); err != nil || len(svc.ComposeFiles) != 2 || !strings.HasPrefix(svc.Source, "COMPOSE_FILE") {
		t.Fatalf("services = %s (%v)", stdout, err)
	}

	// An instance variable and --auto-ports both need the base files: they
	// used to fail with "does not support COMPOSE_FILE".
	if _, stderr, code := run("env", "set", "t2envvar", "MODE=from-theo"); code != ExitOK {
		t.Fatalf("env set: %s", stderr)
	}
	stdout, stderr, code := run("instance", "config", "t2envvar", "--auto-ports")
	if code != ExitOK {
		t.Fatalf("instance config: %s", stderr)
	}
	checkOutput(t, "config", stdout, "MODE: from-theo")
	checkOutput(t, "config", stdout, "WHO: extra")
	if strings.Contains(stdout, "decoy") {
		t.Errorf("the default compose.yaml was used:\n%s", stdout)
	}
	if _, stderr, code := run("up", "t2envvar"); code != ExitOK {
		t.Fatalf("up: %s", stderr)
	}
	stdout, _, _ = run("ps", "t2envvar", "-w", "main")
	checkOutput(t, "ps", stdout, fmt.Sprintf("%d -> 8080/tcp", port))

	// The linked worktree has the committed .env too; its port is taken.
	if _, stderr, code := run("up", "t2envvar", "-w", "feature/x", "--auto-ports"); code != ExitOK {
		t.Fatalf("up linked: %s", stderr)
	} else {
		checkOutput(t, "up linked", stderr, fmt.Sprintf("host port %d instead of %d", port+1, port))
	}
	stdout, _, _ = run("ps", "t2envvar", "-w", "feature/x")
	checkOutput(t, "ps linked", stdout, fmt.Sprintf("%d -> 8080/tcp", port+1))
	for _, w := range []string{"main", "feature/x"} {
		if _, stderr, code := run("down", "t2envvar", "-w", w); code != ExitOK {
			t.Errorf("down %s: %s", w, stderr)
		}
	}

	// From the process environment, the same file list wins over .env, and
	// a project setting wins over both.
	t.Setenv("COMPOSE_FILE", "base.yaml")
	stdout, _, _ = run("instance", "config", "t2envvar", "--auto-ports")
	checkOutput(t, "env var config", stdout, "WHO: base")
	if strings.Contains(stdout, "published") {
		t.Errorf("extra.yaml should not be loaded:\n%s", stdout)
	}
	if _, stderr, code := run("project", "set", "t2envvar", "--compose-file", "extra.yaml", "--compose-file", "base.yaml"); code != ExitOK {
		t.Fatalf("project set: %s", stderr)
	}
	stdout, _, _ = run("instance", "config", "t2envvar")
	checkOutput(t, "setting wins", stdout, "WHO: base")
	checkOutput(t, "setting wins", stdout, "published")
}

// TestT2ProfilesPerInstance checks B-024 with a real Docker daemon: profiles
// persisted for an instance are applied on up, merged with --profile, and
// down, ps, logs and restart manage the same containers.
func TestT2ProfilesPerInstance(t *testing.T) {
	testutil.RequireDaemon(t)
	main, _ := testutil.NewRepo(t, map[string]string{
		".gitignore": "server\nDockerfile\n",
		"compose.yaml": `name: t2-profiles
services:
  web:
    build: .
  debugger:
    build: .
    profiles: [debug]
  metrics:
    build: .
    profiles: [metrics]
`,
	})
	testutil.WriteServerImage(t, main)
	t.Setenv(registry.EnvHome, t.TempDir())
	t2NoComposeFile(t)
	t2Cleanup(t, "t2-profiles", "t2profiles-app-feature")

	if _, stderr, code := run("project", "add", main, "--name", "t2profiles"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	if _, stderr, code := run("instance", "set", "t2profiles", "--profile", "nope"); code == ExitOK {
		t.Fatal("an unknown profile should be refused")
	} else {
		checkOutput(t, "unknown profile", stderr, "debug, metrics")
	}
	if _, stderr, code := run("instance", "set", "t2profiles"); code != ExitUsage {
		t.Errorf("instance set without options: code %d, %s", code, stderr)
	}
	stdout, stderr, code := run("instance", "set", "t2profiles", "--profile", "debug")
	if code != ExitOK {
		t.Fatalf("instance set: %s", stderr)
	}
	checkOutput(t, "instance set", stdout, "debug")
	stdout, _, _ = run("instance", "show", "t2profiles")
	checkOutput(t, "instance show", stdout, "profiles (theo): debug")
	stdout, _, _ = run("instance", "show", "t2profiles", "--json")
	checkOutput(t, "instance show json", stdout, `"profiles": [`)

	services := func(w string) []string {
		out, _, _ := run("ps", "t2profiles", "-w", w, "--json")
		var res struct {
			Containers []struct{ Service, State string } `json:"containers"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("ps json: %v\n%s", err, out)
		}
		var names []string
		for _, c := range res.Containers {
			names = append(names, c.Service+":"+c.State)
		}
		return names
	}

	// Persisted profile only: web and debugger.
	if _, stderr, code := run("up", "t2profiles"); code != ExitOK {
		t.Fatalf("up: %s", stderr)
	}
	got := strings.Join(services("main"), ",")
	if !strings.Contains(got, "web:running") || !strings.Contains(got, "debugger:running") || strings.Contains(got, "metrics") {
		t.Errorf("after up with the persisted profile: %s", got)
	}
	if _, stderr, code := run("restart", "t2profiles"); code != ExitOK {
		t.Errorf("restart: %s", stderr)
	}
	if _, stderr, code := run("logs", "t2profiles", "--tail", "2", "debugger"); code != ExitOK {
		t.Errorf("logs of a profile service: %s", stderr)
	}
	// down removes everything the instance started, profiles included.
	if _, stderr, code := run("down", "t2profiles"); code != ExitOK {
		t.Fatalf("down: %s", stderr)
	}
	if got := services("main"); len(got) != 0 {
		t.Errorf("down left containers: %v", got)
	}

	// --profile on up is merged with the persisted ones.
	if _, stderr, code := run("up", "t2profiles", "--profile", "metrics"); code != ExitOK {
		t.Fatalf("up --profile: %s", stderr)
	}
	got = strings.Join(services("main"), ",")
	for _, want := range []string{"web:running", "debugger:running", "metrics:running"} {
		if !strings.Contains(got, want) {
			t.Errorf("merged profiles: %s lacks %s", got, want)
		}
	}
	run("down", "t2profiles")

	// --no-profiles goes back to the default services.
	if _, stderr, code := run("instance", "set", "t2profiles", "--no-profiles"); code != ExitOK {
		t.Fatalf("--no-profiles: %s", stderr)
	}
	if _, stderr, code := run("up", "t2profiles"); code != ExitOK {
		t.Fatalf("up: %s", stderr)
	}
	if got := strings.Join(services("main"), ","); strings.Contains(got, "debugger") || !strings.Contains(got, "web:running") {
		t.Errorf("after --no-profiles: %s", got)
	}
	stdout, _, _ = run("instance", "show", "t2profiles")
	checkOutput(t, "instance show", stdout, "profiles (theo): none")
	run("down", "t2profiles")
}
