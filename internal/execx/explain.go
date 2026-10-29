package execx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// explain is the state of `theo --explain`. While it is on, every call of
// this package is listed on w, in the order it is made, with the directory
// it runs in. Calls that only read still run, because the operation needs
// their output to plan the next step. Calls that change Git or Docker are
// listed and not executed.
var explain struct {
	mu      sync.Mutex
	w       io.Writer
	n       int
	changes int
}

// Explain turns explain mode on, listing commands on w, or off when w is
// nil. It returns a function that turns it off and writes the closing line
// of the listing. The state is process-wide: Theo runs one command per
// process.
func Explain(w io.Writer) (done func()) {
	explain.mu.Lock()
	explain.w, explain.n, explain.changes = w, 0, 0
	explain.mu.Unlock()
	if w == nil {
		return func() {}
	}
	fmt.Fprintln(w, "# theo --explain: the Git and Docker commands this operation runs, in order.")
	fmt.Fprintln(w, "# read   = runs now, because planning needs its output")
	fmt.Fprintln(w, "# change = NOT run; this is what would change Git or Docker")
	return func() {
		explain.mu.Lock()
		n, changes := explain.n, explain.changes
		explain.w = nil
		explain.mu.Unlock()
		fmt.Fprintf(w, "# %d command(s): %d read, %d change. Nothing was changed.\n", n, n-changes, changes)
	}
}

// Explaining reports whether explain mode is on. Code that changes state
// outside Git and Docker (directories, files) checks it to hold back.
func Explaining() bool { return explaining() }

func explaining() bool {
	explain.mu.Lock()
	defer explain.mu.Unlock()
	return explain.w != nil
}

// note lists a command. Format, one line per command:
//
//	<n>. <read|change> <directory> $ <command line>
func note(dir, name string, args []string, change bool) {
	explain.mu.Lock()
	defer explain.mu.Unlock()
	if explain.w == nil {
		return
	}
	if dir == "" {
		dir = "."
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	kind := "read  "
	if change {
		kind = "change"
		explain.changes++
	}
	explain.n++
	fmt.Fprintf(explain.w, "%d. %s %s $ %s\n", explain.n, kind, dir, CommandLine(name, args))
}

// CommandLine renders a command the way a shell would take it, quoting the
// arguments that need it, so that it can be copied and run.
func CommandLine(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteArg(name))
	for _, a := range args {
		parts = append(parts, quoteArg(a))
	}
	return strings.Join(parts, " ")
}

func quoteArg(a string) string {
	if a == "" {
		return `''`
	}
	safe := true
	for _, r := range a {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("-_./:=@%+,", r):
		default:
			safe = false
		}
	}
	if safe {
		return a
	}
	return `'` + strings.ReplaceAll(a, `'`, `'\''`) + `'`
}

// looksMutating is a safety net for calls that used Run or Stream although
// they change something: it recognizes the Git and Docker verbs that do, so
// that --explain never executes them by mistake. Call sites still say what
// they do (RunChange, StreamChange); this only catches the ones that forgot.
func looksMutating(name string, args []string) bool {
	switch filepath.Base(name) {
	case "git":
		verb, rest := firstVerb(args)
		switch verb {
		case "worktree":
			sub, _ := firstVerb(rest)
			switch sub {
			case "add", "remove", "move", "prune", "lock", "unlock", "repair":
				return true
			}
		case "clone", "init", "checkout", "switch", "restore", "reset", "commit", "merge", "rebase", "pull", "push",
			"fetch", "add", "rm", "mv", "stash", "tag", "cherry-pick", "revert", "clean", "gc", "prune", "apply", "am":
			return true
		case "branch":
			for _, a := range rest {
				switch a {
				case "-d", "-D", "-m", "-M", "-c", "-C", "--delete", "--move", "--copy", "--force":
					return true
				}
			}
			return len(rest) > 0 && !strings.HasPrefix(rest[0], "-") // git branch <name> creates one
		}
	case "docker":
		verb, rest := firstVerb(args)
		switch verb {
		case "compose":
			switch composeVerb(rest) {
			case "up", "down", "restart", "stop", "start", "kill", "rm", "create", "run", "exec", "build", "pull", "push", "pause", "unpause", "cp", "scale", "watch":
				return true
			}
		case "volume", "network", "image", "container", "system", "builder":
			sub, _ := firstVerb(rest)
			switch sub {
			case "create", "rm", "remove", "prune", "connect", "disconnect", "pull", "push", "build", "tag", "load":
				return true
			}
		case "run", "exec", "rm", "rmi", "stop", "start", "restart", "kill", "create", "pull", "push", "build", "pause", "unpause", "cp", "rename", "update", "commit":
			return true
		}
	}
	return false
}

// firstVerb returns the first argument that is not an option, and what
// follows it.
func firstVerb(args []string) (string, []string) {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a, args[i+1:]
		}
	}
	return "", nil
}

// composeVerb finds the verb of a `docker compose` call, skipping the global
// options that take a value.
func composeVerb(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-p", "--project-name", "-f", "--file", "--env-file", "--profile", "--project-directory", "--ansi", "--parallel":
			i++
			continue
		}
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// IsTerminal reports whether f is an interactive terminal (and not a pipe,
// a file or the null device).
func IsTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(info, null) {
		return false
	}
	return true
}

// Describe lists a command under --explain without running it, for code that
// cannot run a read because a file it needs was not written (--explain writes
// none). It does nothing when explain mode is off.
func Describe(dir, name string, args ...string) {
	if explaining() {
		note(dir, name, args, false)
	}
}
