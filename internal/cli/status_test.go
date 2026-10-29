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

func warningKinds(ws []engine.StatusWarning) []string {
	var kinds []string
	for _, w := range ws {
		kinds = append(kinds, w.Kind)
	}
	return kinds
}

func hasWarning(ws []engine.StatusWarning, kind string) bool {
	for _, w := range ws {
		if w.Kind == kind {
			return true
		}
	}
	return false
}

// TestStatusWithRealDocker checks the dashboard of a project whose main
// worktree runs and whose linked worktree wants the same host port, has a
// foreign stack, and has uncommitted files.
func TestStatusWithRealDocker(t *testing.T) {
	testutil.RequireDaemon(t)
	const project = "t5-stat"
	port := testutil.FreePort(t)
	compose := fmt.Sprintf(`name: %s
services:
  api:
    image: alpine:3
    command: ["sleep", "3600"]
    stop_signal: SIGKILL
    ports: ["%d:8080"]
    depends_on: [db]
  db:
    image: alpine:3
    command: ["sleep", "3600"]
    stop_signal: SIGKILL
  report:
    image: alpine:3
    command: ["sleep", "3600"]
    profiles: [reports]
`, project, port)
	main, linked := testutil.NewRepo(t, map[string]string{"compose.yaml": compose})
	t.Setenv(registry.EnvHome, t.TempDir())
	if _, stderr, code := run("project", "add", main, "--name", "t5stat"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	t.Cleanup(func() {
		run("down", "t5stat")
		cmd := exec.Command("docker", "compose", "-p", "t5-stat-foreign", "down")
		cmd.Dir = linked
		cmd.Run()
	})

	status := func(args ...string) *engine.StatusReport {
		t.Helper()
		stdout, stderr, code := run(append([]string{"status", "--json"}, args...)...)
		if code != ExitOK {
			t.Fatalf("status: %d\n%s", code, stderr)
		}
		var r engine.StatusReport
		if err := json.Unmarshal([]byte(stdout), &r); err != nil {
			t.Fatalf("status --json is not JSON: %v\n%s", err, stdout)
		}
		return &r
	}

	// Nothing runs yet.
	r := status("t5stat")
	if len(r.Projects) != 1 || len(r.Projects[0].Worktrees) != 2 {
		t.Fatalf("status = %+v", r)
	}
	if st := r.Projects[0].Worktrees[0].Instance; st == nil || st.State != engine.StateNotCreated || st.ComposeProject != project {
		t.Errorf("before up: main instance = %+v", st)
	}

	if _, stderr, code := run("env", "set", "t5stat", "TOKEN=secret", "--secret"); code != ExitOK {
		t.Fatalf("env set: %s", stderr)
	}
	if stdout, stderr, code := run("up", "t5stat"); code != ExitOK {
		t.Fatalf("up: %d\n%s\n%s", code, stdout, stderr)
	}
	// A stack started outside Theo from the linked worktree, and a dirty tree.
	cmd := exec.Command("docker", "compose", "-p", "t5-stat-foreign", "up", "--detach", "--no-deps", "db")
	cmd.Dir = linked
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("foreign stack: %v\n%s", err, out)
	}
	testutil.WriteFile(t, linked, "wip.txt", "x")

	r = status() // every project
	if len(r.Projects) != 1 {
		t.Fatalf("projects = %d", len(r.Projects))
	}
	p := r.Projects[0]
	m, l := p.Worktrees[0], p.Worktrees[1]
	if m.Kind != engine.KindMain || m.Branch != "main" || m.Path != main || m.Dirty {
		t.Errorf("main = %+v", m)
	}
	if l.Kind != engine.KindLinked || l.Branch != "feature/x" || l.Path != linked || !l.Dirty {
		t.Errorf("linked = %+v", l)
	}
	if m.Instance.State != engine.StateRunning || m.Instance.Counts.Running != 2 || m.Instance.Counts.Total != 2 || m.Instance.ComposeProject != project {
		t.Errorf("main instance = %+v", m.Instance)
	}
	url := fmt.Sprintf("http://localhost:%d", port)
	if len(m.Instance.Ports) != 1 || m.Instance.Ports[0].URL != url || m.Instance.Ports[0].Status != engine.PortRunning || m.Instance.Ports[0].HostPort != port {
		t.Errorf("main ports = %+v", m.Instance.Ports)
	}
	if m.Settings == nil || m.Settings.EnvVars != 1 || m.Settings.SecretEnvVars != 1 || len(m.Settings.Profiles) != 1 || m.Settings.Profiles[0] != "reports" {
		t.Errorf("main settings = %+v", m.Settings)
	}
	if len(m.Warnings) != 0 {
		t.Errorf("main warnings = %+v", m.Warnings)
	}
	if l.Instance.State != engine.StateNotCreated || l.Instance.ComposeProject != "t5stat-app-feature" {
		t.Errorf("linked instance = %+v", l.Instance)
	}
	if !hasWarning(l.Warnings, "port-conflict") || !hasWarning(l.Warnings, "foreign-stack") {
		t.Errorf("linked warnings = %v, want port-conflict and foreign-stack", warningKinds(l.Warnings))
	}
	if strings.Contains(fmt.Sprint(l.Warnings), "secret") {
		t.Error("a warning leaks a secret value")
	}

	// The JSON has stable keys for the GUI.
	stdout, _, _ := run("status", "t5stat", "--json")
	var raw map[string]any
	json.Unmarshal([]byte(stdout), &raw)
	wt := raw["projects"].([]any)[0].(map[string]any)["worktrees"].([]any)[0].(map[string]any)
	for _, key := range []string{"path", "branch", "kind", "dirty", "instance", "settings", "warnings", "env_file"} {
		if _, ok := wt[key]; !ok {
			t.Errorf("worktree JSON lacks %q: %v", key, wt)
		}
	}
	inst := wt["instance"].(map[string]any)
	for _, key := range []string{"compose_project", "state", "counts", "services", "ports"} {
		if _, ok := inst[key]; !ok {
			t.Errorf("instance JSON lacks %q: %v", key, inst)
		}
	}

	// Text output.
	stdout, stderr, code := run("status")
	if code != ExitOK {
		t.Fatalf("status: %s", stderr)
	}
	for _, want := range []string{"t5stat", "main", "feature/x", "dirty", "not created", "port-conflict", "foreign-stack", "2/2 running"} {
		checkOutput(t, "status text", stdout, want)
	}
	// Stop one service: partial, with counts by service.
	if _, stderr, code := run("stop", "t5stat", "api"); code != ExitOK {
		t.Fatalf("stop: %s", stderr)
	}
	m = status("t5stat").Projects[0].Worktrees[0]
	stdout, _, _ = run("status", "t5stat")
	checkOutput(t, "status text after stop", stdout, "partial")
	checkOutput(t, "status text after stop", stdout, "1/2 running, 1 stopped")
	if m.Instance.State != engine.StatePartial || m.Instance.Counts.Running != 1 || m.Instance.Counts.Stopped != 1 {
		t.Errorf("after stop api: %+v", m.Instance)
	}

	if _, stderr, code := run("status", "nope"); code != ExitError || !strings.Contains(stderr, `project "nope"`) {
		t.Errorf("status nope: %d %s", code, stderr)
	}
	if _, _, code := run("status", "a", "b"); code != ExitUsage {
		t.Errorf("status a b: code %d, want usage", code)
	}
}

func TestStatusMissingEnvFileAndEmpty(t *testing.T) {
	testutil.RequireCompose(t)
	t.Setenv(registry.EnvHome, t.TempDir())
	stdout, _, code := run("status", "--json")
	if code != ExitOK || !strings.Contains(stdout, `"projects": []`) || !strings.Contains(stdout, `"warnings": []`) {
		t.Errorf("empty status: code %d\n%s", code, stdout)
	}
	stdout, _, _ = run("status")
	checkOutput(t, "empty status", stdout, "No projects registered")

	main, _ := testutil.NewRepo(t, map[string]string{"compose.yaml": "name: t5-env\nservices:\n  web:\n    image: alpine:3\n    environment: [A=${NEEDED:?set NEEDED}]\n"})
	envFile := filepath.Join(t.TempDir(), "shared.env")
	os.WriteFile(envFile, []byte("NEEDED=1\n"), 0o644)
	if _, stderr, code := run("project", "add", main, "--name", "t5env", "--env-file", envFile); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	os.Remove(envFile)
	stdout, _, code = run("status", "t5env", "--json")
	var r engine.StatusReport
	if code != ExitOK || json.Unmarshal([]byte(stdout), &r) != nil {
		t.Fatalf("status: code %d\n%s", code, stdout)
	}
	for _, wt := range r.Projects[0].Worktrees {
		if !hasWarning(wt.Warnings, "missing-env-file") || wt.Instance == nil || wt.Instance.State != engine.StateUnknown {
			t.Errorf("worktree %s: %+v", wt.Path, wt)
		}
		if len(wt.Warnings) != 1 {
			t.Errorf("worktree %s: want one warning, got %v", wt.Path, warningKinds(wt.Warnings))
		}
	}
}
