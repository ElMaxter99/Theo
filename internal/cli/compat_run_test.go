package cli

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/testutil"
)

// dockerQuiet runs docker and ignores the result, for cleanup.
func dockerQuiet(args ...string) { exec.Command("docker", args...).Run() }

// cleanupProject removes the containers, networks and volumes of Compose
// projects and the given images when the test ends.
func cleanupProject(t *testing.T, projects []string, images ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, p := range projects {
			dockerQuiet("compose", "-p", p, "--profile", "*", "down", "--volumes", "--remove-orphans", "--timeout", "1")
		}
		for _, img := range images {
			dockerQuiet("rmi", "--force", img)
		}
	})
}

func key(port int, proto string) string { return strconv.Itoa(port) + "/" + proto }

// containersOf returns "name|ports" lines of the containers of a Compose project.
func containersOf(t *testing.T, project string) []string {
	t.Helper()
	out := testutil.Docker(t, "ps", "--filter", "label=com.docker.compose.project="+project, "--format", "{{.Names}}|{{.Ports}}")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

var hostPortRe = regexp.MustCompile(`:(\d+)->\d+/(\w+)`)

func hostPorts(lines []string) map[string]bool {
	out := map[string]bool{}
	for _, l := range lines {
		for _, m := range hostPortRe.FindAllStringSubmatch(l, -1) {
			out[m[1]+"/"+m[2]] = true
		}
	}
	return out
}

// TestComposeCompatRun starts two instances of a project that uses most
// Compose features at once, with a real Docker daemon: the second one needs
// every host port moved, fixed container names removed and variables set, and
// the generated file has to keep working through include, extends, anchors,
// an override file, !override, network_mode, profiles and depends_on
// conditions.
func TestComposeCompatRun(t *testing.T) {
	testutil.RequireDaemon(t)
	p := freePorts(t, 6)
	web, api, dns, tool1, tool2, cache := p[0], p[1], p[2], p[3], p[3]+1, p[4]
	built := p[5]
	compatProject(t, "t3-sink", map[string]string{
		"compose.yaml": fmt.Sprintf(`name: t3-sink
include:
  - path: infra.yaml
x-svc: &svc
  image: alpine:3.20
  command: ["sleep", "600"]
services:
  web:
    <<: *svc
    extends:
      file: base.yaml
      service: base
    container_name: t3-sink-web
    ports:
      - "%d:80"
      - target: 8080
        published: %d
        host_ip: 127.0.0.1
        protocol: tcp
      - "%d:53/udp"
    expose: ["3000"]
    env_file: [web.env]
    environment: [FROM_LIST=1]
    extra_hosts: ["db.local:127.0.0.1"]
    depends_on:
      db: {condition: service_healthy}
      cache: {condition: service_started, required: false}
  hostnet:
    <<: *svc
    network_mode: host
  side:
    <<: *svc
    network_mode: "service:web"
    depends_on: [web]
  tool:
    <<: *svc
    profiles: [dbg]
    ports: ["%d-%d:9000-9001"]
  built:
    build: ./built
    image: t3-sink-built:dev
    ports: ["%d:80"]
`, web, api, dns, tool1, tool2, built),
		"infra.yaml": fmt.Sprintf(`services:
  db:
    image: alpine:3.20
    command: ["sleep", "600"]
    healthcheck: {test: ["CMD", "true"], interval: 1s}
  cache:
    image: alpine:3.20
    command: ["sleep", "600"]
    ports: ["%d:6379"]
`, cache),
		"base.yaml":            "services:\n  base:\n    environment:\n      BASE: from-extends\n",
		"web.env":              "FROM_FILE=1\nOVERRIDDEN=file\n",
		"compose.override.yml": fmt.Sprintf("services:\n  web:\n    environment:\n      LAYER: override\n  tool:\n    ports: !override [\"%d:9000\", \"%d:9001/udp\"]\n", tool1, tool2),
		"built/Dockerfile":     "FROM alpine:3.20\nCMD [\"sleep\", \"600\"]\n",
	})
	const mainName, linkedName = "t3-sink", "t3-sink-app-feature"
	cleanupProject(t, []string{linkedName, mainName}, "t3-sink-built:dev")

	if _, stderr, code := run("up", "t3-sink", "--build", "--profile", "dbg"); code != ExitOK {
		t.Fatalf("up main: %s", stderr)
	}
	mainLines := containersOf(t, mainName)
	if len(mainLines) != 7 {
		t.Fatalf("main containers = %v", mainLines)
	}
	mainPorts := hostPorts(mainLines)
	for _, want := range []string{key(web, "tcp"), key(api, "tcp"), key(dns, "udp"), key(tool1, "tcp"), key(tool2, "udp"), key(cache, "tcp"), key(built, "tcp")} {
		if !mainPorts[want] {
			t.Errorf("main does not publish %s: %v", want, mainLines)
		}
	}

	for _, args := range [][]string{
		{"instance", "set", "t3-sink", "-w", "feature/x", "--neutralize-container-names"},
		{"env", "set", "t3-sink", "-w", "feature/x", "OVERRIDDEN=theo"},
		{"env", "set", "t3-sink", "-w", "feature/x", "LITERAL=a$HOME ${X:-y} $$"},
	} {
		if _, stderr, code := run(args...); code != ExitOK {
			t.Fatalf("%v: %s", args, stderr)
		}
	}
	// Without --auto-ports every port is taken and Docker refuses; with it, they all move.
	_, stderr, code := run("up", "t3-sink", "-w", "feature/x", "--build", "--profile", "dbg", "--auto-ports")
	if code != ExitOK {
		t.Fatalf("up linked: %s", stderr)
	}
	checkOutput(t, "up linked", stderr, "instead of")
	// B-009: the fixed image name of the build is shared by the two worktrees.
	checkOutput(t, "up --build", stderr, "built (t3-sink-built:dev)")

	lines := containersOf(t, linkedName)
	if len(lines) != 7 {
		t.Fatalf("linked containers = %v", lines)
	}
	for port := range hostPorts(lines) {
		if mainPorts[port] {
			t.Errorf("both instances publish %s: %v", port, lines)
		}
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "t3-sink-web|") {
			t.Errorf("the fixed container_name was not removed: %s", l)
		}
	}
	if got := containersOf(t, mainName); len(got) != 7 {
		t.Errorf("the main instance was disturbed: %v", got)
	}

	var webContainer string
	for _, l := range lines {
		if name, _, _ := strings.Cut(l, "|"); strings.Contains(name, "-web-") {
			webContainer = name
		}
	}
	env := testutil.Docker(t, "exec", webContainer, "env")
	for _, want := range []string{"FROM_FILE=1", "FROM_LIST=1", "BASE=from-extends", "LAYER=override", "OVERRIDDEN=theo", "LITERAL=a$HOME ${X:-y} $$"} {
		if !strings.Contains("\n"+env+"\n", "\n"+want+"\n") {
			t.Errorf("web lacks %s in:\n%s", want, env)
		}
	}
	// The sidecar shares the network namespace of web: network_mode was not touched.
	for _, l := range lines {
		if name, _, _ := strings.Cut(l, "|"); strings.Contains(name, "-side-") {
			if mode := testutil.Docker(t, "inspect", "--format", "{{.HostConfig.NetworkMode}}", name); !strings.HasPrefix(mode, "container:") {
				t.Errorf("side network mode = %s", mode)
			}
		}
		if name, _, _ := strings.Cut(l, "|"); strings.Contains(name, "-hostnet-") {
			if mode := testutil.Docker(t, "inspect", "--format", "{{.HostConfig.NetworkMode}}", name); mode != "host" {
				t.Errorf("hostnet network mode = %s", mode)
			}
		}
	}
}

// TestComposeImageNames checks what the instances of one project call the
// images they build (B-009): a build without image: is named per project,
// so each instance has its own; a fixed image: is one tag for all of them,
// and Theo warns on up --build; an image: that contains the project name is
// per instance again.
func TestComposeImageNames(t *testing.T) {
	testutil.RequireDaemon(t)
	compatProject(t, "t3-img", map[string]string{
		"compose.yaml": `name: t3-img
services:
  plain:
    build: .
  fixed:
    build: .
    image: t3-img-fixed:dev
  derived:
    build: .
    image: "t3-img-derived-${COMPOSE_PROJECT_NAME}:dev"
`,
		"Dockerfile": "FROM alpine:3.20\nCMD [\"sleep\", \"600\"]\n",
	})
	const mainName, linkedName = "t3-img", "t3-img-app-feature"
	images := []string{
		mainName + "-plain", linkedName + "-plain", "t3-img-fixed:dev",
		"t3-img-derived-" + mainName + ":dev", "t3-img-derived-" + linkedName + ":dev",
	}
	cleanupProject(t, []string{linkedName, mainName}, images...)

	_, stderr, code := run("up", "t3-img", "--build")
	if code != ExitOK {
		t.Fatalf("up main: %s", stderr)
	}
	checkOutput(t, "up main --build", stderr, "fixed (t3-img-fixed:dev)")
	_, stderr, code = run("up", "t3-img", "-w", "feature/x", "--build")
	if code != ExitOK {
		t.Fatalf("up linked: %s", stderr)
	}
	checkOutput(t, "up linked --build", stderr, "fixed (t3-img-fixed:dev)")
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, "warning") && (strings.Contains(line, "plain") || strings.Contains(line, "derived")) {
			t.Errorf("only the service with a fixed image name should be named:\n%s", line)
		}
	}

	have := testutil.Docker(t, "images", "--format", "{{.Repository}}:{{.Tag}}")
	for _, want := range images {
		if !strings.Contains("\n"+have+"\n", "\n"+want) {
			t.Errorf("image %s does not exist; images:\n%s", want, have)
		}
	}
	// One tag for both instances, and one image per instance for the others.
	if n := strings.Count("\n"+have+"\n", "\nt3-img-fixed:dev\n"); n != 1 {
		t.Errorf("the fixed tag exists %d times, want 1", n)
	}

	// Without --build there is nothing to warn about.
	_, stderr, _ = run("up", "t3-img", "-w", "feature/x")
	if strings.Contains(stderr, "fixed image name") {
		t.Errorf("warned without --build:\n%s", stderr)
	}
}
