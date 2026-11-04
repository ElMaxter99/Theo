package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// withStdin makes the next commands read this text as their standard input.
func withStdin(t *testing.T, text string) {
	t.Helper()
	orig := stdin
	t.Cleanup(func() { stdin = orig })
	stdin = strings.NewReader(text)
}

func hookProject(t *testing.T, files map[string]string) (main string) {
	t.Helper()
	main, _ = testutil.NewRepo(t, files)
	t.Setenv(registry.EnvHome, t.TempDir())
	if _, stderr, code := run("project", "add", main, "--name", "app"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	return main
}

func TestHookWorktreeCreateAndSetup(t *testing.T) {
	main := hookProject(t, map[string]string{".gitignore": ".env\n"})
	testutil.WriteFile(t, main, ".env", "A=1\n")
	testutil.WriteFile(t, main, ".theo.json", `{"copy":[".env"],"after_create":["echo done> marker.txt"]}`)
	testutil.Git(t, main, "add", ".theo.json")
	testutil.Git(t, main, "commit", "-m", "declare setup")

	withStdin(t, `{"hook_event_name":"WorktreeCreate","cwd":`+quoteJSON(main)+`,"name":"fix login"}`)
	stdout, stderr, code := run("hook", "worktree-create")
	if code != ExitOK {
		t.Fatalf("hook: %d %s", code, stderr)
	}
	path := strings.TrimSpace(stdout)
	if strings.Contains(path, "\n") || !strings.HasSuffix(filepath.ToSlash(path), "worktree-fix-login") {
		t.Fatalf("stdout must be only the path of the worktree, got %q", stdout)
	}
	if data, err := os.ReadFile(filepath.Join(path, ".env")); err != nil || string(data) != "A=1\n" {
		t.Errorf("the file declared in .theo.json was not copied: %q, %v", data, err)
	}
	// Commands from the repository wait for an approval a hook cannot ask for.
	if _, err := os.Stat(filepath.Join(path, "marker.txt")); err == nil {
		t.Errorf("after_create ran without approval")
	}
	if !strings.Contains(stderr, "theo worktree setup app worktree-fix-login") {
		t.Errorf("stderr should say how to approve: %q", stderr)
	}

	// The same name gives the same worktree.
	withStdin(t, `{"cwd":`+quoteJSON(main)+`,"name":"fix login"}`)
	again, _, code := run("hook", "worktree-create")
	if code != ExitOK || !samePathCLI(strings.TrimSpace(again), path) {
		t.Errorf("second create: %d %q, want %q", code, again, path)
	}

	// Not interactive and not approved: refused. With --yes: it runs, and is remembered.
	if _, stderr, code := run("worktree", "setup", "app", "worktree-fix-login"); code == ExitOK || !strings.Contains(stderr, "not been approved") {
		t.Errorf("setup without approval: %d %s", code, stderr)
	}
	if _, stderr, code := run("worktree", "setup", "app", "worktree-fix-login", "--yes"); code != ExitOK {
		t.Fatalf("setup --yes: %s", stderr)
	}
	if _, err := os.Stat(filepath.Join(path, "marker.txt")); err != nil {
		t.Errorf("after_create did not run: %v", err)
	}
	os.Remove(filepath.Join(path, "marker.txt"))
	if _, stderr, code := run("worktree", "setup", "app", "worktree-fix-login"); code != ExitOK {
		t.Errorf("approved commands should run without asking again: %s", stderr)
	}
	if _, err := os.Stat(filepath.Join(path, "marker.txt")); err != nil {
		t.Errorf("remembered approval did not run the commands: %v", err)
	}
}

func TestHookCreateOutsideProjectFails(t *testing.T) {
	hookProject(t, nil)
	withStdin(t, `{"cwd":`+quoteJSON(t.TempDir())+`,"name":"x"}`)
	if _, stderr, code := run("hook", "worktree-create"); code == ExitOK || !strings.Contains(stderr, "not in a project") {
		t.Errorf("create outside a project: %d %s", code, stderr)
	}
}

func TestHookAgentState(t *testing.T) {
	main := hookProject(t, nil)
	send := func(event string) {
		t.Helper()
		withStdin(t, `{"hook_event_name":"`+event+`","session_id":"s1","cwd":`+quoteJSON(main)+`}`)
		if _, stderr, code := run("hook", "agent-state"); code != ExitOK {
			t.Fatalf("%s: %s", event, stderr)
		}
	}
	agentOf := func() (state, source string, warnings []string) {
		t.Helper()
		stdout, stderr, code := run("status", "app", "--json")
		if code != ExitOK {
			t.Skipf("status needs Compose: %s", stderr)
		}
		var r struct {
			Projects []struct {
				Worktrees []struct {
					Agent    *struct{ State, Source string }
					Warnings []struct{ Kind string }
				}
			}
		}
		if err := json.Unmarshal([]byte(stdout), &r); err != nil {
			t.Fatal(err)
		}
		wt := r.Projects[0].Worktrees[0]
		for _, w := range wt.Warnings {
			warnings = append(warnings, w.Kind)
		}
		if wt.Agent != nil {
			return wt.Agent.State, wt.Agent.Source, warnings
		}
		return "", "", warnings
	}
	send("UserPromptSubmit")
	if s, src, w := agentOf(); s != "working" || src != "hook" || contains(w, "agent-waiting") || contains(w, "agent-done") {
		t.Errorf("after a prompt: %q %q %v", s, src, w)
	}
	send("Notification")
	if s, _, w := agentOf(); s != "waiting" || !contains(w, "agent-waiting") {
		t.Errorf("after a notification: %q %v", s, w)
	}
	send("Stop")
	if s, _, w := agentOf(); s != "done" || !contains(w, "agent-done") {
		t.Errorf("after stop: %q %v", s, w)
	}
	send("SessionEnd")
	if s, _, _ := agentOf(); s != "" {
		t.Errorf("after the session ended: %q", s)
	}
}

func TestMCPInstallHooks(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, ".claude/settings.local.json", `{"permissions":{"allow":["Bash(ls)"]},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo mine"}]}]}}`)
	t.Chdir(root)
	stdout, stderr, code := run("mcp", "install", "--hooks")
	if code != ExitOK || !strings.Contains(stdout, "Added Theo's hooks") {
		t.Fatalf("install --hooks: %d %s %s", code, stdout, stderr)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".claude", "settings.local.json"))
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["permissions"] == nil {
		t.Errorf("existing settings were lost: %s", data)
	}
	hooks := doc["hooks"].(map[string]any)
	if len(hooks["Stop"].([]any)) != 2 || hooks["WorktreeCreate"] == nil || hooks["SessionStart"] == nil {
		t.Errorf("hooks = %s", data)
	}
	if stdout, _, _ := run("mcp", "install", "--hooks"); !strings.Contains(stdout, "already") {
		t.Errorf("second install should change nothing: %q", stdout)
	}
}

func TestEnvExport(t *testing.T) {
	testutil.RequireCompose(t)
	main := hookProject(t, map[string]string{"compose.yaml": "services:\n  web:\n    image: alpine:3\n    ports: [\"18081:80\"]\n"})
	t.Chdir(main)
	stdout, stderr, code := run("env")
	if code != ExitOK {
		t.Fatalf("env: %s", stderr)
	}
	for _, want := range []string{"export THEO_PROJECT='app'\n", "export THEO_PORT_WEB='18081'\n", "export THEO_URL='http://localhost:18081'\n"} {
		checkOutput(t, "env", stdout, want)
	}
	stdout, _, _ = run("env", "--format", "powershell")
	checkOutput(t, "env powershell", stdout, "$env:THEO_PORT_WEB = '18081'")
	stdout, _, _ = run("env", "app", "--json")
	var m map[string]string
	if json.Unmarshal([]byte(stdout), &m) != nil || m["THEO_URL_WEB"] != "http://localhost:18081" {
		t.Errorf("env --json = %s", stdout)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
