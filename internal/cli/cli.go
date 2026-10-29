// Package cli implements Theo's minimal command-line interface. It parses
// arguments, calls the engine and formats its results. It holds no domain
// logic.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
)

// Exit codes returned by Run.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

const usage = `Theo manages development projects and isolated environments based on Git and Docker Compose.

Usage:
  theo <command> [arguments]

Commands:
  project add <path> [--name <name>]           Register the Git repository at <path>
  project list [--json]                        List registered projects
  project remove <name>                        Unregister a project (files and containers are kept)
  worktrees <project> [--json]                 List the worktrees of a project
  services <project> [--worktree <w>] [--env-file <f>] [--json]
                                               List the services docker compose resolves
                                               for the main worktree, or for <w>
                                               (a branch, directory name or path).
                                               --env-file replaces the worktree's .env
  version                                      Print the Theo version
  help                                         Show this help

Theo only reads your repositories and Compose files: these commands never
change files, branches, containers or volumes.
`

// errUsage marks errors caused by wrong arguments.
var errUsage = errors.New("usage")

// Run executes the command described by args (without the program name),
// writing normal output to stdout and errors to stderr, and returns the
// process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return ExitUsage
	}

	var err error
	switch args[0] {
	case "version", "--version":
		fmt.Fprintf(stdout, "theo %s\n", engine.Version)
		return ExitOK
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return ExitOK
	case "project":
		err = withEngine(func(e *engine.Engine) error { return runProject(e, args[1:], stdout) })
	case "worktrees":
		err = withEngine(func(e *engine.Engine) error { return runWorktrees(e, args[1:], stdout) })
	case "services":
		err = withEngine(func(e *engine.Engine) error { return runServices(e, args[1:], stdout) })
	default:
		err = usageErrorf("unknown command %q", args[0])
	}

	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(stdout, usage)
		return ExitOK
	case errors.Is(err, errUsage):
		fmt.Fprintf(stderr, "theo: %s\n\n%s", strings.TrimPrefix(err.Error(), "usage: "), usage)
		return ExitUsage
	default:
		fmt.Fprintf(stderr, "theo: %s\n", err)
		return ExitError
	}
}

func withEngine(fn func(*engine.Engine) error) error {
	e, err := engine.Open()
	if err != nil {
		return err
	}
	return fn(e)
}

func usageErrorf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errUsage, fmt.Sprintf(format, a...))
}

// parse parses flags that may appear before or after positional arguments
// and returns the positional ones.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, usageErrorf("%s %v", fs.Name(), err)
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

func runProject(e *engine.Engine, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageErrorf("project needs a subcommand: add, list or remove")
	}
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("project add", flag.ContinueOnError)
		name := fs.String("name", "", "project name")
		pos, err := parse(fs, args[1:])
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return usageErrorf("project add needs exactly one path")
		}
		p, err := e.AddProject(context.Background(), pos[0], *name)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Registered project %q at %s\n", p.Name, p.Path)
		return nil

	case "list":
		fs := flag.NewFlagSet("project list", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "print JSON")
		pos, err := parse(fs, args[1:])
		if err != nil {
			return err
		}
		if len(pos) != 0 {
			return usageErrorf("project list takes no arguments")
		}
		projects, err := e.Projects()
		if err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(stdout, projects)
		}
		if len(projects) == 0 {
			fmt.Fprintln(stdout, "No projects registered. Add one with `theo project add <path>`.")
			return nil
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tPATH")
		for _, p := range projects {
			fmt.Fprintf(tw, "%s\t%s\n", p.Name, p.Path)
		}
		return tw.Flush()

	case "remove":
		fs := flag.NewFlagSet("project remove", flag.ContinueOnError)
		pos, err := parse(fs, args[1:])
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return usageErrorf("project remove needs exactly one project name")
		}
		p, err := e.RemoveProject(pos[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Removed project %q from the registry. Its files, containers and volumes were not touched (%s).\n", p.Name, p.Path)
		return nil

	default:
		return usageErrorf("unknown project subcommand %q", args[0])
	}
}

func runWorktrees(e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("worktrees", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("worktrees needs exactly one project name")
	}
	list, err := e.Worktrees(context.Background(), pos[0])
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, list)
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "BRANCH\tCHANGES\tPATH")
	for _, wt := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", worktreeRef(wt), worktreeState(wt), wt.Path)
	}
	return tw.Flush()
}

func worktreeRef(wt engine.Worktree) string {
	switch {
	case wt.Bare:
		return "(bare)"
	case wt.Branch != "":
		return wt.Branch
	case len(wt.Head) >= 7:
		return "(detached " + wt.Head[:7] + ")"
	default:
		return "(detached)"
	}
}

func worktreeState(wt engine.Worktree) string {
	var flags []string
	switch {
	case wt.Bare:
		flags = append(flags, "-")
	case wt.Prunable:
		flags = append(flags, "missing")
	case wt.Dirty:
		flags = append(flags, "uncommitted")
	default:
		flags = append(flags, "clean")
	}
	if wt.Locked {
		flags = append(flags, "locked")
	}
	return strings.Join(flags, ", ")
}

func runServices(e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("services", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	worktree := fs.String("worktree", "", "branch, directory name or path of the worktree")
	fs.StringVar(worktree, "w", "", "shorthand for --worktree")
	var envFiles stringList
	fs.Var(&envFiles, "env-file", "env file to use instead of the worktree's .env (repeatable)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("services needs exactly one project name")
	}
	res, err := e.Services(context.Background(), pos[0], *worktree, envFiles)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, res)
	}

	where := res.Workspace
	if res.Branch != "" {
		where = res.Branch + " · " + res.Workspace
	}
	fmt.Fprintf(stdout, "Project %s (compose project %q)\nWorktree %s\n", res.Project, res.Compose, where)
	if len(res.Services) == 0 {
		fmt.Fprintln(stdout, "\nNo services.")
		return nil
	}
	for _, s := range res.Services {
		fmt.Fprintf(stdout, "\n%s\n", s.Name)
		tw := tabwriter.NewWriter(stdout, 0, 4, 1, ' ', 0)
		if s.Image != "" {
			fmt.Fprintf(tw, "  image:\t%s\n", s.Image)
		}
		if s.Build != "" {
			fmt.Fprintf(tw, "  build:\t%s\n", relative(res.Workspace, s.Build))
		}
		writeList(tw, "ports", mapEach(s.Ports, formatPort))
		writeList(tw, "mounts", mapEach(s.Mounts, func(m engine.Mount) string { return formatMount(res.Workspace, m) }))
		writeList(tw, "depends on", s.DependsOn)
		writeList(tw, "networks", s.Networks)
		writeList(tw, "profiles", s.Profiles)
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// stringList is a flag that can be repeated.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

func writeList(w io.Writer, label string, items []string) {
	for i, item := range items {
		if i == 0 {
			fmt.Fprintf(w, "  %s:\t%s\n", label, item)
		} else {
			fmt.Fprintf(w, "  \t%s\n", item)
		}
	}
}

func mapEach[T any](items []T, fn func(T) string) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = fn(item)
	}
	return out
}

func formatPort(p compose.Port) string {
	proto := p.Protocol
	if proto == "" {
		proto = "tcp"
	}
	if p.Published == "" {
		return fmt.Sprintf("%d/%s (random host port)", p.Target, proto)
	}
	host := p.Published
	if p.HostIP != "" {
		host = p.HostIP + ":" + host
	}
	return fmt.Sprintf("%s -> %d/%s", host, p.Target, proto)
}

func formatMount(workspace string, m engine.Mount) string {
	source := m.Source
	if m.Type == "bind" {
		source = relative(workspace, source)
	}
	s := fmt.Sprintf("%s %s -> %s", m.Type, source, m.Target)
	if m.ReadOnly {
		s += " (read-only)"
	}
	if m.OutsideWorkspace {
		s += "  [outside the worktree]"
	}
	return s
}

// relative shows path relative to base when it is inside it.
func relative(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	if rel == "." {
		return "."
	}
	return "." + string(filepath.Separator) + rel
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
