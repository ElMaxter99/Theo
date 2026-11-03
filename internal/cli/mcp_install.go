package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/mcp"
)

// lookClaude finds the claude command. Tests replace it.
var lookClaude = func() (string, error) { return exec.LookPath("claude") }

// The registration document lives in package mcp, so that the API server can
// build it too (GET /mcp/install).
type mcpInstall = mcp.Install

func mcpInstallDoc(command, name, owner, scope string, readOnly bool) mcpInstall {
	return mcp.InstallDoc(command, name, owner, scope, readOnly)
}

func mcpSnippet(name, owner string, readOnly bool) string { return mcp.Snippet(name, owner, readOnly) }

func theoCommand() string { return mcp.TheoCommand() }

func shellJoin(words []string) string { return mcp.ShellJoin(words) }

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
