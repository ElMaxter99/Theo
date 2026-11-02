package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

func TestMCPCommand(t *testing.T) {
	main, _ := testutil.NewRepo(t, nil)
	t.Setenv(registry.EnvHome, t.TempDir())
	if _, stderr, code := run("project", "add", main, "--name", "mcpcli"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	stdin = strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_worktrees","arguments":{"project":"mcpcli"}}}`,
	}, "\n") + "\n")
	t.Cleanup(func() { stdin = os.Stdin })

	stdout, stderr, code := run("mcp", "--read-only", "--owner", "bot")
	if code != ExitOK {
		t.Fatalf("mcp: %d %s", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 answers on stdout and nothing else, got:\n%s", stdout)
	}
	checkOutput(t, "initialize", lines[0], `"protocolVersion":"2025-06-18"`)
	checkOutput(t, "tools/list", lines[1], `"list_worktrees"`)
	if strings.Contains(lines[1], `"name":"up"`) {
		t.Error("--read-only lists up")
	}
	checkOutput(t, "tools/call", lines[2], `"isError":false`)
	// The path travels inside JSON, so Windows backslashes arrive escaped.
	quoted, _ := json.Marshal(main)
	checkOutput(t, "tools/call", lines[2], strings.Trim(string(quoted), `"`))

	if _, stderr, code := run("mcp", "extra"); code != ExitUsage {
		t.Errorf("mcp extra: %d %s", code, stderr)
	}
	if _, stderr, code := run("--explain", "mcp"); code != ExitUsage || !strings.Contains(stderr, "explain tool") {
		t.Errorf("--explain mcp: %d %s", code, stderr)
	}
	stdout, _, _ = run("help")
	checkOutput(t, "help", stdout, "claude mcp add theo -- theo mcp")
}
