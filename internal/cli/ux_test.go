package cli

import (
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// t5Compose has four services that need no image build (alpine, which a
// daemon with registry access pulls once): api depends on db, cache stands
// alone, and tools has a named volume and is where commands run.
const t5Compose = `name: %[1]s
services:
  api:
    image: alpine:3
    command: ["sleep", "3600"]
    stop_signal: SIGKILL
    depends_on: [db]
  db:
    image: alpine:3
    command: ["sleep", "3600"]
    stop_signal: SIGKILL
  cache:
    image: alpine:3
    command: ["sleep", "3600"]
    stop_signal: SIGKILL
  tools:
    image: alpine:3
    command: ["sleep", "3600"]
    stop_signal: SIGKILL
    volumes: [data:/data]
volumes:
  data: {}
`

// states returns service -> "state/container id" for the containers of a
// Compose project, straight from Docker.
func states(t *testing.T, project string) map[string]string {
	t.Helper()
	out := testutil.Docker(t, "ps", "--all", "--no-trunc", "--filter", "label=com.docker.compose.project="+project,
		"--format", `{{.Label "com.docker.compose.service"}} {{.State}} {{.ID}}`)
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) == 3 {
			m[f[0]] = f[1] + "/" + f[2]
		}
	}
	return m
}

func stateOf(m map[string]string, service string) string {
	state, _, _ := strings.Cut(m[service], "/")
	return state
}

func idOf(m map[string]string, service string) string {
	_, id, _ := strings.Cut(m[service], "/")
	return id
}

func cleanupProject(t *testing.T, project string, args ...string) {
	t.Helper()
	t.Cleanup(func() {
		run(append([]string{"down"}, args...)...)
		testutil.Docker(t, "volume", "rm", "--force", project+"_data")
	})
}

// TestServicesRecreateAndExec covers FR-046, FR-044 and FR-072 with a real
// Docker daemon: single services start and stop while the rest keeps
// running, recreating containers keeps the data of a named volume, and
// commands run in a container with input, output and exit code passed on.
func TestServicesRecreateAndExec(t *testing.T) {
	testutil.RequireDaemon(t)
	const project = "t5-ctl"
	main, _ := testutil.NewRepo(t, map[string]string{"compose.yaml": strings.Replace(t5Compose, "%[1]s", project, 1)})
	t.Setenv(registry.EnvHome, t.TempDir())
	if _, stderr, code := run("project", "add", main, "--name", "t5ctl"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	cleanupProject(t, project, "t5ctl")

	// up --service starts the service and what it depends on, not the rest.
	if stdout, stderr, code := run("up", "t5ctl", "--service", "api"); code != ExitOK {
		t.Fatalf("up --service api: %d\n%s\n%s", code, stdout, stderr)
	}
	m := states(t, project)
	if stateOf(m, "api") != "running" || stateOf(m, "db") != "running" || m["cache"] != "" || m["tools"] != "" {
		t.Fatalf("after up --service api: %v (want api and db running, nothing else)", m)
	}
	if _, stderr, code := run("up", "t5ctl", "--service", "nope"); code != ExitError || !strings.Contains(stderr, `no service "nope"`) || !strings.Contains(stderr, "api, cache, db, tools") {
		t.Errorf("up --service nope: code %d, stderr %s", code, stderr)
	}

	// Add the others; up applies to what is not running yet.
	if _, stderr, code := run("up", "t5ctl", "--service", "cache", "--service", "tools"); code != ExitOK {
		t.Fatalf("up --service cache --service tools: %s", stderr)
	}

	// stop one service: the others keep running.
	if stdout, stderr, code := run("stop", "t5ctl", "cache"); code != ExitOK {
		t.Fatalf("stop cache: %d %s %s", code, stdout, stderr)
	} else {
		checkOutput(t, "stop", stdout, "Stopped cache")
	}
	m = states(t, project)
	if stateOf(m, "cache") != "exited" || stateOf(m, "api") != "running" || stateOf(m, "db") != "running" || stateOf(m, "tools") != "running" {
		t.Fatalf("after stop cache: %v", m)
	}
	stdout, _, _ := run("status", "t5ctl", "--json")
	checkOutput(t, "status", stdout, `"state": "partial"`)

	if _, stderr, code := run("start", "t5ctl", "cache"); code != ExitOK {
		t.Fatalf("start cache: %s", stderr)
	}
	if m = states(t, project); stateOf(m, "cache") != "running" {
		t.Errorf("after start cache: %v", m)
	}

	// restart one service: only its container restarts (same id, new start).
	before := states(t, project)
	startedBefore := testutil.Docker(t, "inspect", "-f", "{{.State.StartedAt}}", idOf(before, "cache"))
	if _, stderr, code := run("restart", "t5ctl", "cache"); code != ExitOK {
		t.Fatalf("restart cache: %s", stderr)
	}
	after := states(t, project)
	if idOf(after, "cache") != idOf(before, "cache") ||
		testutil.Docker(t, "inspect", "-f", "{{.State.StartedAt}}", idOf(after, "cache")) == startedBefore {
		t.Errorf("restart cache: want the same container, started again; before %v after %v", before, after)
	}
	if testutil.Docker(t, "inspect", "-f", "{{.State.StartedAt}}", idOf(after, "tools")) != testutil.Docker(t, "inspect", "-f", "{{.State.StartedAt}}", idOf(before, "tools")) {
		t.Error("restart cache also restarted tools")
	}
	if _, stderr, code := run("stop", "t5ctl", "nope"); code != ExitError || !strings.Contains(stderr, `no service "nope"`) {
		t.Errorf("stop nope: code %d, stderr %s", code, stderr)
	}

	// exec: stdin, stdout, stderr and the exit code pass through.
	origStdin := stdin
	t.Cleanup(func() { stdin = origStdin })
	stdin = strings.NewReader("hello from stdin")
	stdout, stderr, code := run("exec", "t5ctl", "-T", "tools", "--", "sh", "-c", "cat; echo to-stderr >&2; exit 7")
	if code != 7 || stdout != "hello from stdin" || !strings.Contains(stderr, "to-stderr") {
		t.Errorf("exec: code %d, stdout %q, stderr %q; want 7, the input, and the stderr line", code, stdout, stderr)
	}
	stdin = strings.NewReader("")
	if _, _, code := run("exec", "t5ctl", "tools", "--", "true"); code != ExitOK {
		t.Errorf("exec true: code %d", code)
	}
	// Flags after -- belong to the command.
	stdin = strings.NewReader("")
	if stdout, _, code := run("exec", "t5ctl", "tools", "--", "ls", "-1", "/data"); code != ExitOK || stdout != "" {
		t.Errorf("exec ls -1: code %d, stdout %q", code, stdout)
	}
	if _, stderr, code := run("exec", "t5ctl", "tools"); code != ExitUsage || !strings.Contains(stderr, "needs a project, a service and a command") {
		t.Errorf("exec without a command: code %d, stderr %s", code, stderr)
	}
	// A shell: alpine has no bash, so it falls back to sh.
	stdin = strings.NewReader("echo shell-is-$0\nexit 5\n")
	stdout, _, code = run("shell", "t5ctl", "tools")
	if code != 5 || !strings.Contains(stdout, "shell-is-sh") {
		t.Errorf("shell: code %d, stdout %q; want sh and exit 5", code, stdout)
	}

	// FR-044: recreate keeps the data of the named volume.
	stdin = strings.NewReader("")
	if _, stderr, code := run("exec", "t5ctl", "-T", "tools", "--", "sh", "-c", "echo persisted > /data/marker"); code != ExitOK {
		t.Fatalf("writing the marker: %s", stderr)
	}
	before = states(t, project)
	if stdout, stderr, code := run("recreate", "t5ctl", "tools"); code != ExitOK {
		t.Fatalf("recreate tools: %d\n%s\n%s", code, stdout, stderr)
	} else {
		checkOutput(t, "recreate", stdout, "Named volumes were kept")
	}
	after = states(t, project)
	if idOf(after, "tools") == idOf(before, "tools") || stateOf(after, "tools") != "running" {
		t.Errorf("recreate tools: container %s -> %s, want a new running one", idOf(before, "tools"), idOf(after, "tools"))
	}
	if idOf(after, "api") != idOf(before, "api") || idOf(after, "db") != idOf(before, "db") {
		t.Errorf("recreate tools touched other services: before %v after %v", before, after)
	}
	// The data, read through the Theo exec and straight from the volume.
	stdin = strings.NewReader("")
	if stdout, _, code := run("exec", "t5ctl", "-T", "tools", "--", "cat", "/data/marker"); code != ExitOK || strings.TrimSpace(stdout) != "persisted" {
		t.Errorf("after recreate, exec cat: code %d, %q", code, stdout)
	}
	if got := testutil.Docker(t, "run", "--rm", "-v", project+"_data:/data", "alpine:3", "cat", "/data/marker"); got != "persisted" {
		t.Errorf("volume content after recreate = %q", got)
	}

	// up --force-recreate recreates everything (dependencies included).
	before = states(t, project)
	if stdout, stderr, code := run("up", "t5ctl", "--force-recreate"); code != ExitOK {
		t.Fatalf("up --force-recreate: %d\n%s\n%s", code, stdout, stderr)
	}
	after = states(t, project)
	for _, s := range []string{"api", "db", "cache", "tools"} {
		if idOf(after, s) == idOf(before, s) || stateOf(after, s) != "running" {
			t.Errorf("up --force-recreate: %s was %s, now %s", s, before[s], after[s])
		}
	}
	if got := testutil.Docker(t, "run", "--rm", "-v", project+"_data:/data", "alpine:3", "cat", "/data/marker"); got != "persisted" {
		t.Errorf("volume content after up --force-recreate = %q", got)
	}

	// stop without services stops all.
	if _, stderr, code := run("stop", "t5ctl"); code != ExitOK {
		t.Fatalf("stop: %s", stderr)
	}
	for s, st := range states(t, project) {
		if !strings.HasPrefix(st, "exited/") {
			t.Errorf("after stop (all): %s is %s", s, st)
		}
	}
	stdout, _, _ = run("status", "t5ctl", "--json")
	checkOutput(t, "status", stdout, `"state": "stopped"`)
}
