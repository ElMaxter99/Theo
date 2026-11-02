package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ElMaxter99/Theo/internal/engine"
)

// mcpInstall is the document of `theo mcp install --json`.
type mcpInstall struct {
	Name    string   `json:"name"`
	Command string   `json:"command"` // the theo binary the client should run
	Args    []string `json:"args"`
	// ClaudeCode is the command that registers the server with Claude Code.
	ClaudeCode []string `json:"claude_code"`
	// MCPServers is the entry other clients (and Claude Code's .mcp.json)
	// read, under "mcpServers".
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
	// Snippet is a paragraph for a repository's AGENTS.md or CLAUDE.md.
	Snippet string `json:"snippet"`
}

type mcpServerEntry struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// lookClaude finds the claude command. Tests replace it.
var lookClaude = func() (string, error) { return exec.LookPath("claude") }

func runMCPInstall(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mcp install", flag.ContinueOnError)
	owner := fs.String("owner", "", "owner label of the worktrees agents create through this server")
	readOnly := fs.Bool("read-only", false, "register the server with --read-only")
	scope := fs.String("scope", "", "claude mcp add --scope: local (default), user or project")
	name := fs.String("name", "theo", "name of the server in the client")
	apply := fs.Bool("apply", false, "run the claude mcp add command")
	asJSON := fs.Bool("json", false, "print JSON")
	snippet := fs.Bool("snippet", false, "print only the paragraph for AGENTS.md or CLAUDE.md")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return usageErrorf("mcp install takes no arguments")
	}
	exclusive := 0
	for _, b := range []bool{*apply, *asJSON, *snippet} {
		if b {
			exclusive++
		}
	}
	if exclusive > 1 {
		return usageErrorf("--apply, --json and --snippet exclude each other")
	}
	switch *scope {
	case "", "local", "user", "project":
	default:
		return usageErrorf("--scope must be local, user or project")
	}
	if *owner != "" {
		if *owner, err = engine.ValidateOwner(*owner); err != nil {
			return usageErrorf("mcp install --owner: %v", err)
		}
	}
	if *name == "" || strings.ContainsAny(*name, " \t\"'") {
		return usageErrorf("--name must be a single word")
	}

	doc := mcpInstallDoc(theoCommand(), *name, *owner, *scope, *readOnly)
	switch {
	case *asJSON:
		return writeJSON(stdout, doc)
	case *snippet:
		fmt.Fprint(stdout, doc.Snippet)
		return nil
	case *apply:
		claude, err := lookClaude()
		if err != nil {
			return errors.New("the claude command was not found on PATH; install Claude Code, or register the server by hand with the command `theo mcp install` prints")
		}
		cmd := exec.Command(claude, doc.ClaudeCode[1:]...)
		cmd.Stdout, cmd.Stderr = stdout, stderr
		fmt.Fprintf(stderr, "theo: running %s\n", shellJoin(doc.ClaudeCode))
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("claude mcp add failed: %w", err)
		}
		fmt.Fprintf(stdout, "Registered %q with Claude Code. Add this to the repository's AGENTS.md or CLAUDE.md so agents know when to use it (`theo mcp install --snippet` prints it alone):\n\n%s", *name, doc.Snippet)
		return nil
	}

	fmt.Fprintf(stdout, "Claude Code (run it once; --apply runs it for you):\n\n  %s\n\n", shellJoin(doc.ClaudeCode))
	fmt.Fprintln(stdout, "Other MCP clients (Claude Desktop, Cursor, a .mcp.json in the repository...), under \"mcpServers\":")
	fmt.Fprintln(stdout)
	var entry strings.Builder
	_ = writeJSON(&entry, map[string]any{"mcpServers": doc.MCPServers})
	for _, line := range strings.Split(strings.TrimRight(entry.String(), "\n"), "\n") {
		fmt.Fprintf(stdout, "  %s\n", line)
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "For the repository's AGENTS.md or CLAUDE.md (`theo mcp install --snippet >> AGENTS.md`):")
	fmt.Fprintln(stdout)
	fmt.Fprint(stdout, doc.Snippet)
	return nil
}

func mcpInstallDoc(command, name, owner, scope string, readOnly bool) mcpInstall {
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
	return mcpInstall{
		Name: name, Command: command, Args: serverArgs, ClaudeCode: claude,
		MCPServers: map[string]mcpServerEntry{name: {Command: command, Args: serverArgs}},
		Snippet:    mcpSnippet(name, owner, readOnly),
	}
}

func mcpSnippet(name, owner string, readOnly bool) string {
	var b strings.Builder
	b.WriteString("## Running the project with Theo\n\n")
	b.WriteString("This repository runs with Docker Compose through Theo, which gives each Git worktree its own isolated instance (containers, networks, volumes and host ports).\n")
	fmt.Fprintf(&b, "Use the `%s` MCP server instead of calling `docker compose` directly:\n\n", name)
	if readOnly {
		b.WriteString("- The server is read-only: use `status`, `ports`, `logs` and `proxy_routes` to inspect instances; ask the user to start or stop them.\n")
	} else {
		b.WriteString("- Work on a branch in its own worktree: `add_worktree` (never in the main worktree), then `up` for that worktree.\n")
		b.WriteString("- Find where it answers with `ports`, or `proxy_routes` for a name that does not change (`http://<instance>.<project>.localhost:7080`).\n")
		b.WriteString("- Read `logs` and `status`; `status` warns when the Compose files changed since the last `up`.\n")
		b.WriteString("- Call `down` when you are done. Volumes are kept; deleting data is left to the user.\n")
	}
	if owner != "" {
		fmt.Fprintf(&b, "- Worktrees you create are labelled with the owner %q; `list_worktrees` with `owner` lists only yours.\n", owner)
	}
	b.WriteString("- Use `explain` to see what an operation would run without running it.\n")
	return b.String()
}

// theoCommand is how a client should start this binary: "theo" when that
// name on PATH is this very file, so the registration survives upgrades;
// otherwise its absolute path.
func theoCommand() string {
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

// shellJoin quotes the words of a command for display, so a path with spaces
// can be pasted.
func shellJoin(words []string) string {
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
