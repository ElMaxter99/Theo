package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/testutil"
)

func TestAgentInstallCopilotPrintAndWrite(t *testing.T) {
	withInstalled(t, "copilot", "claude")
	stdout, stderr, code := run("agent", "install", "copilot", "--print")
	if code != ExitOK {
		t.Fatalf("print: %d %s", code, stderr)
	}
	var doc struct {
		Version int
		Hooks   map[string][]struct {
			Type, Bash, Powershell string
			TimeoutSec             int
		}
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if doc.Version != 1 || len(doc.Hooks) != len(copilotHooks) {
		t.Fatalf("unexpected document: %+v", doc)
	}
	h := doc.Hooks["agentStop"][0]
	if h.Type != "command" || !strings.Contains(h.Bash, " hook agent-state --agent copilot --state done") || h.Powershell == "" || h.TimeoutSec != 30 {
		t.Errorf("agentStop hook: %+v", h)
	}

	main, _ := testutil.NewRepo(t, nil)
	t.Chdir(main)
	if _, stderr, code := run("agent", "install", "copilot"); code != ExitOK {
		t.Fatalf("write: %d %s", code, stderr)
	}
	path := filepath.Join(main, ".github", "hooks", "theo.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("hooks file missing: %v", err)
	}
	if out, _, _ := run("agent", "install", "copilot"); !strings.Contains(out, "already in") {
		t.Errorf("second run should change nothing: %q", out)
	}
	// A file that is not Theo's is never overwritten.
	if err := os.WriteFile(path, []byte(`{"version":1,"hooks":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := run("agent", "install", "copilot"); code == ExitOK || !strings.Contains(stderr, "not Theo's") {
		t.Errorf("foreign file: %d %s", code, stderr)
	}
}

func TestAgentInstallClaudeIsMCPHooks(t *testing.T) {
	a, _, _ := run("agent", "install", "claude", "--print")
	b, _, _ := run("mcp", "install", "--hooks", "--print")
	if a == "" || a != b {
		t.Errorf("agent install claude should equal mcp install --hooks")
	}
	if _, _, code := run("agent", "install", "cursor"); code == ExitOK {
		t.Errorf("unknown agent should fail")
	}
}

func TestHookCopilotAgentStateAndSessionStart(t *testing.T) {
	main := hookProject(t, map[string]string{"compose.yaml": "services:\n  web:\n    image: nginx\n    ports:\n      - \"8080:80\"\n"})
	send := func(args ...string) string {
		t.Helper()
		withStdin(t, `{"sessionId":"c1","timestamp":1,"cwd":`+quoteJSON(main)+`}`)
		out, stderr, code := run(append([]string{"hook"}, args...)...)
		if code != ExitOK {
			t.Fatalf("%v: %s", args, stderr)
		}
		return out
	}
	send("agent-state", "--agent", "copilot", "--state", "working")
	send("agent-state", "--agent", "copilot", "--state", "working", "--skip-repeat")
	send("agent-state", "--agent", "copilot", "--state", "waiting")
	send("agent-state", "--agent", "copilot", "--state", "clear")

	withStdin(t, `{"sessionId":"c1","cwd":`+quoteJSON(main)+`}`)
	out, errOut, _ := run("hook", "session-start", "--agent", "copilot")
	if errOut != "" {
		t.Skipf("session-start needs Compose: %s", errOut)
	}
	var ctx struct{ AdditionalContext string }
	if err := json.Unmarshal([]byte(out), &ctx); err != nil || !strings.Contains(ctx.AdditionalContext, "Theo manages this worktree") {
		t.Errorf("session-start for copilot should print additionalContext JSON: %q (%v)", out, err)
	}
	if _, stderr, code := run("hook", "agent-state", "--agent", "cursor"); code == ExitOK || !strings.Contains(stderr, "--agent") {
		t.Errorf("unknown agent: %d %s", code, stderr)
	}
}

// withInstalled makes only these agents look installed.
func withInstalled(t *testing.T, names ...string) {
	t.Helper()
	orig := agentInstalled
	t.Cleanup(func() { agentInstalled = orig })
	agentInstalled = func(a string) bool {
		for _, n := range names {
			if n == a {
				return true
			}
		}
		return false
	}
}

func TestAgentInstallNeedsTheAgentOnThisMachine(t *testing.T) {
	main, _ := testutil.NewRepo(t, nil)
	t.Chdir(main)
	withInstalled(t) // nothing installed
	_, stderr, code := run("agent", "install", "copilot")
	if code == ExitOK || !strings.Contains(stderr, "does not install it") || !strings.Contains(stderr, "--force") {
		t.Errorf("a missing agent should be refused with a hint: %d %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(main, ".github", "hooks", "theo.json")); err == nil {
		t.Errorf("nothing should be written for a missing agent")
	}
	if out, _, code := run("agent", "install"); code != ExitOK || !strings.Contains(out, "No supported coding agent found") {
		t.Errorf("none installed: %d %q", code, out)
	}
	if _, _, code := run("agent", "install", "copilot", "--print"); code != ExitOK {
		t.Errorf("--print does not need the agent")
	}
	if _, stderr, code := run("agent", "install", "copilot", "--force"); code != ExitOK {
		t.Errorf("--force: %d %s", code, stderr)
	}
	os.Remove(filepath.Join(main, ".github", "hooks", "theo.json"))

	withInstalled(t, "copilot") // only Copilot
	out, _, code := run("agent", "install")
	if code != ExitOK || !strings.Contains(out, "Claude Code: not found") || !strings.Contains(out, "Wrote Theo's Copilot hooks") {
		t.Errorf("only the installed agent gets hooks: %d %q", code, out)
	}
}
