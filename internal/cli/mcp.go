package cli

import (
	"context"
	"flag"
	"io"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/execx"
	"github.com/ElMaxter99/Theo/internal/mcp"
)

func init() {
	registerCommand("mcp", runMCP)
	registerHelp(50, `
AI agents:
  mcp [--read-only] [--owner <name>]           Serve Theo's operations to AI agents over the Model
                                               Context Protocol, on stdin and stdout. Register it
                                               with: claude mcp add theo -- theo mcp
                                               --read-only offers only tools that change nothing;
                                               --owner labels the worktrees agents create. Deleting
                                               data stays with you. See docs/cli/mcp.md
  mcp install [--owner <name>] [--read-only] [--scope local|user|project] [--apply | --json | --snippet]
                                               How to register the server: the claude mcp add command,
                                               the JSON other clients read, and a paragraph for the
                                               repository's AGENTS.md or CLAUDE.md. --apply runs the
                                               claude command; nothing else is written
`)
	ownerFlag := flagSpec{[]string{"--owner"}, "text"}
	registerCompletion(cmdSpec{name: "mcp", flags: []flagSpec{{names: []string{"--read-only"}}, ownerFlag},
		subs: []cmdSpec{{name: "install", flags: []flagSpec{ownerFlag, {names: []string{"--read-only"}}, {[]string{"--scope"}, "text"},
			{[]string{"--name"}, "text"}, {names: []string{"--apply"}}, jsonFlag, {names: []string{"--snippet"}}}}},
	})
}

func runMCP(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "install" {
		return runMCPInstall(args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	readOnly := fs.Bool("read-only", false, "offer only the tools that change nothing")
	owner := fs.String("owner", "", "owner of the worktrees created through MCP without one")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return usageErrorf("mcp takes no arguments")
	}
	if execx.Explaining() {
		return usageErrorf("mcp cannot run under --explain; use its explain tool")
	}
	if *owner != "" {
		if *owner, err = engine.ValidateOwner(*owner); err != nil {
			return usageErrorf("mcp --owner: %v", err)
		}
	}
	// Messages go to stdout and nothing else may: what the tools run writes
	// into buffers. Diagnostics go to stderr, which clients log.
	srv := mcp.New(mcp.Options{Engine: e, ReadOnly: *readOnly, Owner: *owner})
	return srv.Serve(context.Background(), stdin, stdout)
}
