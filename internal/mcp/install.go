package mcp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Install is the document of `theo mcp install --json` and of GET /mcp/install.
type Install struct {
	Name    string   `json:"name"`
	Command string   `json:"command"` // the theo binary the client should run
	Args    []string `json:"args"`
	// ClaudeCode is the command that registers the server with Claude Code.
	ClaudeCode []string `json:"claude_code"`
	// MCPServers is the entry other clients (and Claude Code's .mcp.json)
	// read, under "mcpServers".
	MCPServers map[string]ServerEntry `json:"mcpServers"`
	// Snippet is a paragraph for a repository's AGENTS.md or CLAUDE.md.
	Snippet string `json:"snippet"`
}

type ServerEntry struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// InstallDoc builds the registration of the MCP server for a client.
func InstallDoc(command, name, owner, scope string, readOnly bool) Install {
	serverArgs := []string{"mcp"}
	if readOnly {
		serverArgs = append(serverArgs, "--read-only")
	}
	if owner != "" {
		serverArgs = append(serverArgs, "--owner", owner)
	}
	claude := []string{"claude", "mcp", "add"}
	if scope != "" {
		claude = append(claude, "--scope", scope)
	}
	claude = append(claude, name, "--", command)
	claude = append(claude, serverArgs...)
	return Install{
		Name: name, Command: command, Args: serverArgs, ClaudeCode: claude,
		MCPServers: map[string]ServerEntry{name: {Command: command, Args: serverArgs}},
		Snippet:    Snippet(name, owner, readOnly),
	}
}

// Snippet is the paragraph for a repository's AGENTS.md or CLAUDE.md.
func Snippet(name, owner string, readOnly bool) string {
	var b strings.Builder
	b.WriteString("## Running the project with Theo\n\n")
	b.WriteString("This repository runs with Docker Compose through Theo, which gives each Git worktree its own isolated instance (containers, networks, volumes and host ports).\n")
	fmt.Fprintf(&b, "Use the `%s` MCP server instead of calling `docker compose` directly:\n\n", name)
	if readOnly {
		b.WriteString("- The server is read-only: use `status`, `ports`, `logs` and `proxy_routes` to inspect instances; ask the user to start or stop them.\n")
	} else {
		b.WriteString("- Work on a branch in its own worktree: `add_worktree` (never in the main worktree), then `up` for that worktree.\n")
		b.WriteString("- Find where it answers with `ports`, or `proxy_routes` for a name that does not change (`http://<instance>.<project>.localhost:7080`).\n")
		b.WriteString("- After `up`, `restart` or `stop`, call `wait_for` (it returns when the instance is running, failed or stopped) instead of polling `status`.\n")
		b.WriteString("- Read `logs` and `status`; `status` warns when the Compose files changed since the last `up`.\n")
		b.WriteString("- Call `down` when you are done. Volumes are kept; deleting data is left to the user.\n")
		b.WriteString("- When your branch is merged or pushed and nothing is left to commit, call `down` and then `finish_worktree` to close your worktree, so worktrees do not pile up. Never close someone else's.\n")
	}
	if owner != "" {
		fmt.Fprintf(&b, "- Worktrees you create are labelled with the owner %q; `list_worktrees` with `owner` lists only yours.\n", owner)
	}
	b.WriteString("- Use `explain` to see what an operation would run without running it.\n")
	return b.String()
}

// TheoCommand is how a client should start this binary: "theo" when that
// name on PATH is this very file, so the registration survives upgrades;
// otherwise its absolute path.
func TheoCommand() string {
	self, err := os.Executable()
	if err != nil {
		return "theo"
	}
	if real, err := filepath.EvalSymlinks(self); err == nil {
		self = real
	}
	if onPath, err := exec.LookPath("theo"); err == nil {
		if real, err := filepath.EvalSymlinks(onPath); err == nil && sameFile(real, self) {
			return "theo"
		}
	}
	return self
}

func sameFile(a, b string) bool {
	ia, err1 := os.Stat(a)
	ib, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ia, ib)
}

// ShellJoin quotes the words of a command for display, so a path with spaces
// can be pasted.
func ShellJoin(words []string) string {
	out := make([]string, len(words))
	for i, w := range words {
		switch {
		case runtime.GOOS == "windows" && strings.ContainsAny(w, " \t'&|;<>()"):
			out[i] = "\"" + w + "\"" // PowerShell and cmd take backslashes as they are
		case runtime.GOOS != "windows" && (w == "" || strings.ContainsAny(w, " \t\"'\\$`&|;<>()*?!")):
			out[i] = "\"" + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(w) + "\""
		default:
			out[i] = w
		}
	}
	return strings.Join(out, " ")
}
