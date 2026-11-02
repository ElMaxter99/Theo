package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/registry"
)

func TestMCPInstallPrintsTheRegistration(t *testing.T) {
	t.Setenv(registry.EnvHome, t.TempDir())
	stdout, stderr, code := run("mcp", "install", "--owner", "agent-1", "--scope", "user")
	if code != ExitOK {
		t.Fatalf("mcp install: %d %s", code, stderr)
	}
	for _, want := range []string{"claude mcp add --scope user theo -- ", " mcp --owner agent-1", `"mcpServers"`, "## Running the project with Theo", `owner "agent-1"`} {
		checkOutput(t, "mcp install", stdout, want)
	}

	stdout, _, code = run("mcp", "install", "--read-only", "--json")
	if code != ExitOK {
		t.Fatalf("mcp install --json: %d", code)
	}
	var doc mcpInstall
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("--json: %v\n%s", err, stdout)
	}
	if strings.Join(doc.Args, " ") != "mcp --read-only" || doc.MCPServers["theo"].Command != doc.Command || doc.ClaudeCode[0] != "claude" {
		t.Errorf("--json: %+v", doc)
	}
	if !strings.Contains(doc.Snippet, "read-only") || strings.Contains(doc.Snippet, "add_worktree") {
		t.Errorf("read-only snippet: %s", doc.Snippet)
	}

	stdout, _, _ = run("mcp", "install", "--snippet")
	if !strings.HasPrefix(stdout, "## Running the project with Theo") || strings.Contains(stdout, "claude mcp add") {
		t.Errorf("--snippet: %s", stdout)
	}

	for _, bad := range [][]string{{"--json", "--snippet"}, {"--scope", "global"}, {"--name", "two words"}, {"extra"}} {
		if _, _, code := run(append([]string{"mcp", "install"}, bad...)...); code != ExitUsage {
			t.Errorf("mcp install %v: exit %d, want usage", bad, code)
		}
	}
}

func TestMCPInstallApplyRunsClaude(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the claude command")
	}
	t.Setenv(registry.EnvHome, t.TempDir())
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := filepath.Join(dir, "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$@\" > "+argsFile+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := lookClaude
	t.Cleanup(func() { lookClaude = orig })
	lookClaude = func() (string, error) { return script, nil }

	stdout, stderr, code := run("mcp", "install", "--apply", "--owner", "bot")
	if code != ExitOK {
		t.Fatalf("--apply: %d %s", code, stderr)
	}
	got, _ := os.ReadFile(argsFile)
	if !strings.HasPrefix(string(got), "mcp add theo -- ") || !strings.HasSuffix(strings.TrimSpace(string(got)), "mcp --owner bot") {
		t.Errorf("claude ran with %q", got)
	}
	checkOutput(t, "--apply", stdout, "Registered \"theo\" with Claude Code")

	lookClaude = func() (string, error) { return "", errors.New("not found") }
	if _, stderr, code := run("mcp", "install", "--apply"); code != ExitError || !strings.Contains(stderr, "not found on PATH") {
		t.Errorf("--apply without claude: %d %s", code, stderr)
	}
}
