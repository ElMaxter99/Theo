// Package cli implements Theo's minimal command-line interface. It parses
// arguments, calls the engine and formats its results. It holds no domain
// logic.
package cli

import (
	"fmt"
	"io"

	"github.com/ElMaxter99/Theo/internal/engine"
)

// Exit codes returned by Run.
const (
	ExitOK    = 0
	ExitUsage = 2
)

const usage = `Theo manages development projects and isolated environments based on Git and Docker Compose.

Usage:
  theo <command>

Commands:
  version    Print the Theo version
  help       Show this help
`

// Run executes the command described by args (without the program name),
// writing normal output to stdout and errors to stderr, and returns the
// process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return ExitUsage
	}

	eng := engine.New()

	switch args[0] {
	case "version", "--version":
		fmt.Fprintf(stdout, "theo %s\n", eng.Version())
		return ExitOK
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return ExitOK
	default:
		fmt.Fprintf(stderr, "theo: unknown command %q\n\n%s", args[0], usage)
		return ExitUsage
	}
}
