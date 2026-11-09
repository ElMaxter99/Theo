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
