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
	"reflect"
	"strings"
	"text/tabwriter"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/execx"
)

// Exit codes returned by Run.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

const usageBase = `Theo manages development projects and isolated environments based on Git and Docker Compose.

Usage:
  theo <command> [arguments]

Commands:
  project add <path> [--name <name>] [--env-file <f>]
                                               Register the Git repository at <path>
  project set <name> --env-file <f> | --no-env-file | --worktree-dir <d> | --default-worktree-dir
                                               Set or clear the env file shared by worktrees
                                               that have no .env of their own, or where new
                                               worktrees are created
  project list [--json]                        List registered projects
  project remove <name>                        Unregister a project (files and containers are kept)
  worktrees <project> [--json]                 List the worktrees of a project
  branches <project> [--json]                  List local and remote branches and which have a worktree
  worktree add <project> <branch> [--new [--from <base>]] [--path <dir>]
                                               Create a worktree for a branch. Never forces:
                                               a branch checked out elsewhere is refused
  worktree remove <project> <worktree> [--discard-changes --confirm <dirname>]
                                               Remove a linked worktree. Refused while it has
                                               containers or uncommitted/untracked files; the
                                               branch and volumes are always kept
  services <project> [--worktree <w>] [--env-file <f>] [--json]
                                               List the services docker compose resolves
                                               for the main worktree, or for <w>
                                               (a branch, directory name or path).
                                               --env-file overrides the env file choice

Env file used by services, in order: --env-file; the worktree's own .env; the
project's env file (project set); the main worktree's .env.

Running a worktree (each worktree runs as its own Compose project):
  up <project> [-w <w>] [--build] [--profile <p>] [--auto-ports]
                                                    Create and start its containers. A host port
                                                    that is taken only warns, unless --auto-ports
                                                    picks another one and keeps it for next time
  down <project> [-w <w>]                           Stop and remove its containers and
                                                    networks; volumes are always kept
  restart <project> [-w <w>]                        Restart its containers
  ps <project> [--json]                             Show which worktrees have containers
  ps <project> -w <w> [--json]                      Show the containers of one worktree
  logs <project> [-w <w>] [-f] [--tail <n>] [service...]
                                                    Show its logs
  reconcile [project] [--apply] [--json]            Compare what Theo recorded with Git and Docker
                                                    and report differences: removed worktrees or
                                                    containers, containers or volumes left behind.
                                                    --apply only forgets Theo's own stale records
  volumes <project> [-w <w>] [--json]               Volumes of the instance: in use, or kept with
                                                    their data after down; external ones are listed
                                                    and never touched
  volumes purge <project> -w <w> --confirm <compose-project> [--backup] [--out <dir>]
                                                    Delete the instance's data. Separate from down,
                                                    refused while it has containers
  env set <project> [-w <w>] NAME=VALUE [--secret] [--service <s>]
  env unset <project> [-w <w>] NAME
  env list <project> [-w <w>] [--show-secrets] [--json]
                                                    Variables for this instance only, applied at
                                                    up through a generated Compose file (the
                                                    containers' environment: wins over env_file)
                                                    and env file (${VAR} in your files; a variable
                                                    exported in your shell still wins there). Your
                                                    files and .env are not changed. Secrets are
                                                    masked. See docs/cli/env-precedence.md
  instance set <project> [-w <w>] --neutralize-container-names | --keep-container-names
  instance show <project> [-w <w>] [--json]         Settings Theo keeps for the instance
  instance config <project> [-w <w>] [--auto-ports] [--profile <p>]
                                                    The configuration it would run with
                                                    (secrets masked)
  history <project> [-w <w>] [--limit <n>] [--json] Operations Theo ran: up, down, restart
  ports <project> [-w <w>] [--profile <p>] [--auto-ports] [--json]
                                                    Show the host ports it publishes, whether
                                                    they are free, and their URLs
  These commands also accept --env-file. Without -w they use the main worktree,
  except ps, which then shows every worktree.
%MORE%
  version                                      Print the Theo version
  help                                         Show this help

Theo never changes your files or branches, and never deletes volumes.
`

// errUsage marks errors caused by wrong arguments.
var errUsage = errors.New("usage")

// Run executes the command described by args (without the program name),
// writing normal output to stdout and errors to stderr, and returns the
// process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	// Global flags come before the command: `theo --explain up app`.
	if len(args) > 0 && args[0] == "--explain" {
		args = args[1:]
		done := execx.Explain(stdout)
		defer done()
		stdout = io.Discard // the commands are listed; what they would have printed is not
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, helpText())
		return ExitUsage
	}

	var err error
	switch args[0] {
	case "version", "--version":
		if len(args) == 2 && args[1] == "--json" {
			writeJSON(stdout, map[string]string{"version": engine.Version})
			return ExitOK
		}
		fmt.Fprintln(stdout, engine.VersionLine())
		return ExitOK
	case "help", "--help", "-h":
		fmt.Fprint(stdout, helpText())
		return ExitOK
	case "project":
		err = withEngine(func(e *engine.Engine) error { return runProject(e, args[1:], stdout) })
	case "worktrees":
		err = withEngine(func(e *engine.Engine) error { return runWorktrees(e, args[1:], stdout) })
	case "branches":
		err = withEngine(func(e *engine.Engine) error { return runBranches(e, args[1:], stdout) })
	case "worktree":
		err = withEngine(func(e *engine.Engine) error { return runWorktree(e, args[1:], stdout) })
	case "services":
		err = withEngine(func(e *engine.Engine) error { return runServices(e, args[1:], stdout) })
	case "up", "down", "restart", "logs":
		err = withEngine(func(e *engine.Engine) error { return runInstance(e, args[0], args[1:], stdout, stderr) })
	case "ps":
		err = withEngine(func(e *engine.Engine) error { return runPS(e, args[1:], stdout) })
	case "reconcile":
		err = withEngine(func(e *engine.Engine) error { return runReconcile(e, args[1:], stdout) })
	case "volumes":
		err = withEngine(func(e *engine.Engine) error { return runVolumes(e, args[1:], stdout, stderr) })
	case "env":
		err = withEngine(func(e *engine.Engine) error { return runEnv(e, args[1:], stdout) })
	case "instance":
		err = withEngine(func(e *engine.Engine) error { return runInstanceCmd(e, args[1:], stdout) })
	case "history":
		err = withEngine(func(e *engine.Engine) error { return runHistory(e, args[1:], stdout) })
	case "ports":
		err = withEngine(func(e *engine.Engine) error { return runPorts(e, args[1:], stdout) })
	default:
		if c, ok := commands[args[0]]; ok {
			err = withEngine(func(e *engine.Engine) error { return c(e, args[1:], stdout, stderr) })
		} else {
			err = usageErrorf("unknown command %q", args[0])
		}
	}

	var exit *exitCodeError
	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &exit): // `theo exec`: the command's own exit code
		return exit.code
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(stdout, helpText())
		return ExitOK
	case errors.Is(err, errUsage):
		fmt.Fprintf(stderr, "theo: %s\n\n%s", strings.TrimPrefix(err.Error(), "usage: "), helpText())
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
	if execx.Explaining() {
		e.DiscardWrites()
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
		return usageErrorf("project needs a subcommand: add, set, show, rename, list or remove")
	}
	if fn, ok := projectSubcommands[args[0]]; ok {
		return fn(e, args[1:], stdout)
	}
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("project add", flag.ContinueOnError)
		name := fs.String("name", "", "project name")
		envFile := fs.String("env-file", "", "env file shared by worktrees without their own .env")
		asJSON := fs.Bool("json", false, "print JSON")
		pos, err := parse(fs, args[1:])
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return usageErrorf("project add needs exactly one path")
		}
		p, err := e.AddProject(context.Background(), pos[0], *name, *envFile)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(stdout, p)
		}
		fmt.Fprintf(stdout, "Registered project %q at %s\n", p.Name, p.Path)
		if p.EnvFile != "" {
			fmt.Fprintf(stdout, "Worktrees without their own .env will use %s\n", p.EnvFile)
		}
		return nil

	case "set":
		fs := flag.NewFlagSet("project set", flag.ContinueOnError)
		envFile := fs.String("env-file", "", "env file shared by worktrees without their own .env")
		noEnvFile := fs.Bool("no-env-file", false, "clear the env file; use the main worktree's .env")
		worktreeDir := fs.String("worktree-dir", "", "directory where `theo worktree add` creates worktrees")
		defaultDir := fs.Bool("default-worktree-dir", false, "go back to Theo's own worktree directory")
		var copyOnAdd stringList
		fs.Var(&copyOnAdd, "copy-on-add", "root-relative file that `worktree add --copy` copies (repeatable; replaces the list)")
		noCopyOnAdd := fs.Bool("no-copy-on-add", false, "clear the list of files `worktree add --copy` copies")
		var composeFiles stringList
		fs.Var(&composeFiles, "compose-file", "Compose file, relative to each worktree (repeatable; the order matters)")
		defaultCompose := fs.Bool("default-compose-files", false, "go back to the Compose files Compose finds by itself")
		asJSON := fs.Bool("json", false, "print the project as JSON")
		pos, err := parse(fs, args[1:])
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return usageErrorf("project set needs exactly one project name")
		}
		if len(copyOnAdd) > 0 || *noCopyOnAdd {
			if len(copyOnAdd) > 0 && *noCopyOnAdd {
				return usageErrorf("--copy-on-add and --no-copy-on-add exclude each other")
			}
			p, err := e.SetCopyOnAdd(pos[0], copyOnAdd)
			if err != nil {
				return err
			}
			if len(p.CopyOnAdd) == 0 {
				fmt.Fprintf(stdout, "Project %q: `worktree add --copy` copies nothing\n", p.Name)
			} else {
				fmt.Fprintf(stdout, "Project %q: `worktree add --copy` copies %s from the main worktree (only files Git does not track; existing files are never overwritten)\n", p.Name, strings.Join(p.CopyOnAdd, ", "))
			}
			if *envFile == "" && !*noEnvFile && *worktreeDir == "" && !*defaultDir && len(composeFiles) == 0 && !*defaultCompose {
				return nil
			}
		}
		setsEnv := *envFile != "" || *noEnvFile
		setsDir := *worktreeDir != "" || *defaultDir
		setsCompose := len(composeFiles) > 0 || *defaultCompose
		if *envFile != "" && *noEnvFile || *worktreeDir != "" && *defaultDir || len(composeFiles) > 0 && *defaultCompose || !setsEnv && !setsDir && !setsCompose && len(copyOnAdd) == 0 && !*noCopyOnAdd {
			return usageErrorf("project set needs --env-file <path> or --no-env-file, --worktree-dir <path> or --default-worktree-dir, --compose-file <path> or --default-compose-files, and/or --copy-on-add <file> or --no-copy-on-add")
		}
		if setsCompose {
			p, err := e.SetComposeFiles(context.Background(), pos[0], composeFiles)
			if err != nil {
				return err
			}
			if len(p.ComposeFiles) == 0 {
				fmt.Fprintf(stdout, "Project %q: Compose files are the ones Compose finds by itself\n", p.Name)
			} else {
				fmt.Fprintf(stdout, "Project %q: Compose files are %s (in this order, relative to each worktree)\n", p.Name, strings.Join(p.ComposeFiles, ", "))
			}
			if !setsEnv && !setsDir {
				return nil
			}
		}
		if *asJSON {
			if setsDir {
				if err := e.SetWorktreeDir(pos[0], *worktreeDir); err != nil {
					return err
				}
			}
			if setsEnv {
				if _, err := e.SetEnvFile(pos[0], *envFile); err != nil {
					return err
				}
			}
			p, err := e.Project(pos[0])
			if err != nil {
				return err
			}
			return writeJSON(stdout, p)
		}
		if setsDir {
			if err := e.SetWorktreeDir(pos[0], *worktreeDir); err != nil {
				return err
			}
			if *defaultDir {
				fmt.Fprintf(stdout, "Project %q: new worktrees go to Theo's own directory\n", pos[0])
			} else {
				fmt.Fprintf(stdout, "Project %q: new worktrees go to %s\n", pos[0], *worktreeDir)
			}
			if !setsEnv {
				return nil
			}
		}
		p, err := e.SetEnvFile(pos[0], *envFile)
		if err != nil {
			return err
		}
		if p.EnvFile == "" {
			fmt.Fprintf(stdout, "Project %q: worktrees without their own .env will use the main worktree's .env\n", p.Name)
		} else {
			fmt.Fprintf(stdout, "Project %q: worktrees without their own .env will use %s\n", p.Name, p.EnvFile)
		}
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
		fmt.Fprintln(tw, "NAME\tPATH\tENV FILE")
		for _, p := range projects {
			env := p.EnvFile
			if env == "" {
				env = "(main worktree's .env)"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Name, p.Path, env)
		}
		return tw.Flush()

	default:
		return usageErrorf("unknown project subcommand %q", args[0])
	}
}

func runWorktrees(e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("worktrees", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	owner := fs.String("owner", "", "only the workspaces owned by this name")
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
	if *owner != "" {
		var kept []engine.Worktree
		for _, wt := range list {
			if wt.Owner == *owner {
				kept = append(kept, wt)
			}
		}
		list = kept
	}
	if *asJSON {
		if list == nil {
			list = []engine.Worktree{}
		}
		return writeJSON(stdout, list)
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "BRANCH\tKIND\tOWNER\tCHANGES\tPATH")
	for _, wt := range list {
		who := wt.Owner
		if who == "" {
			who = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", worktreeRef(wt), wt.Kind, who, worktreeState(wt), wt.Path)
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
	var envFiles, composeFiles stringList
	fs.Var(&envFiles, "env-file", "env file to use instead of the worktree's .env (repeatable)")
	fs.Var(&composeFiles, "compose-file", "Compose file to use instead of the project's, relative to the worktree (repeatable)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("services needs exactly one project name")
	}
	res, err := e.ServicesFor(context.Background(), pos[0], *worktree, engine.InstanceOptions{EnvFiles: envFiles, ComposeFiles: composeFiles})
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
	env := "none"
	if res.EnvFile.Path != "" {
		env = res.EnvFile.Path + " (" + envSourceLabel[res.EnvFile.Source] + ")"
	}
	fmt.Fprintf(stdout, "Project  %s (compose project %q)\nWorktree %s\nEnv file %s\n", res.Project, res.Compose, where, env)
	if len(res.ComposeFiles) > 0 {
		fmt.Fprintf(stdout, "Compose  %s (%s)\n", strings.Join(mapEach(res.ComposeFiles, func(f string) string { return relative(res.Workspace, f) }), ", "), res.ComposeFilesSource)
	}
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

var envSourceLabel = map[string]string{
	engine.EnvFromFlag:     "--env-file",
	engine.EnvFromWorktree: "this worktree",
	engine.EnvFromProject:  "project setting",
	engine.EnvFromMain:     "main worktree",
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
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice && rv.IsNil() {
		v = reflect.MakeSlice(rv.Type(), 0, 0).Interface() // an empty list, not null
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
