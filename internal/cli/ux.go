package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/execx"
)

// stdin is where `theo exec` and `theo shell` read from. Tests replace it.
var stdin io.Reader = os.Stdin

func init() {
	registerCommand("stop", runStop)
	registerCommand("start", runStart)
	registerCommand("recreate", runRecreate)
	registerCommand("exec", runExec)
	registerCommand("shell", runShell)
	registerHelp(10, `
Single services, recreate, and a shell inside a container:
  up <project> [-w <w>] --service <s> [--service <s>] [--force-recreate]
                                                    Start only those services and what they depend
                                                    on. --force-recreate recreates the containers;
                                                    named volumes are kept
  stop <project> [-w <w>] [service...]              Stop those services (all when none are named);
                                                    the others keep running, nothing is removed
  stop --all [project]                              Stop every running instance of every project (or
                                                    of one); containers and volumes are kept
  start <project> [-w <w>] [service...]             Start containers that exist and are stopped
  restart <project> [-w <w>] [service...]           Restart those services (all when none are named)
  recreate <project> [-w <w>] [service...] [--build] [--profile <p>] [--auto-ports]
                                                    Recreate their containers from the current image
                                                    and configuration, without touching their
                                                    dependencies. Named volumes are kept
  exec <project> [-w <w>] [-T] <service> -- <command> [args...]
                                                    Run a command in a running container. Input and
                                                    output pass through, and the exit code is the
                                                    command's. A terminal is allocated only when
                                                    stdin is one; -T never allocates one
  shell <project> [-w <w>] <service>                Open bash in a container, or sh when it has none
`)
}

// exitCodeError makes Run return a command's own exit code without printing
// a message (the command already said what it had to say).
type exitCodeError struct{ code int }

func (e *exitCodeError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// serviceArgs parses the arguments of a command that takes a project and
// then services: `<project> [service...]`.
func serviceArgs(cmd string, fs *flag.FlagSet, args []string) (project string, services []string, err error) {
	pos, err := parse(fs, args)
	if err != nil {
		return "", nil, err
	}
	if len(pos) == 0 {
		return "", nil, usageErrorf("%s needs a project name", cmd)
	}
	return pos[0], pos[1:], nil
}

func runStop(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	return runControl(e, "stop", args, stdout, stderr)
}

func runStart(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	return runControl(e, "start", args, stdout, stderr)
}

func runControl(e *engine.Engine, verb string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	var sel instanceFlags
	sel.register(fs)
	asJSON := fs.Bool("json", false, "print the result as JSON; Compose's own output goes to stderr")
	var all *bool
	if verb == "stop" {
		all = fs.Bool("all", false, "stop every running instance")
	}
	if all != nil && hasFlag(args, "--all") {
		pos, err := parse(fs, args)
		if err != nil {
			return err
		}
		if len(pos) > 1 || sel.worktree != "" || len(sel.envFiles) > 0 {
			return usageErrorf("stop --all takes at most a project name, and no -w, --env-file or services")
		}
		project := ""
		if len(pos) == 1 {
			project = pos[0]
		}
		return stopAll(context.Background(), e, project, stdout, stderr)
	}
	project, services, err := serviceArgs(verb, fs, args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	inst, err := e.Instance(ctx, project, sel.worktree, sel.envFiles)
	if err != nil {
		return err
	}
	describe(stderr, inst)
	out, report := jsonAction(*asJSON, verb, inst, services, stdout, stderr)
	if verb == "stop" {
		err = e.Stop(ctx, inst, services, out, stderr)
	} else {
		err = e.Start(ctx, inst, services, out, stderr)
	}
	if err != nil {
		if verb == "start" {
			return fmt.Errorf("%w\n(start only starts containers that exist; `theo up %s -w %s` creates them)", err, inst.Project, worktreeArg(inst))
		}
		return err
	}
	what := "all services"
	if len(services) > 0 {
		what = strings.Join(services, ", ")
	}
	if verb == "stop" {
		fmt.Fprintf(out, "Stopped %s of %s. The containers and volumes were kept; `theo start %s -w %s` starts them again.\n", what, inst.Compose, inst.Project, worktreeArg(inst))
	} else {
		fmt.Fprintf(out, "Started %s of %s.\n", what, inst.Compose)
	}
	return report.finish(stdout)
}

func runRecreate(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("recreate", flag.ContinueOnError)
	var sel instanceFlags
	sel.register(fs)
	var opts compose.UpOptions
	var profiles stringList
	fs.BoolVar(&opts.Build, "build", false, "build images before recreating")
	fs.Var(&profiles, "profile", "enable a Compose profile (repeatable)")
	auto := fs.Bool("auto-ports", false, "use other host ports for the ones that are taken, and keep them")
	asJSON := fs.Bool("json", false, "print the result as JSON; Compose's own output goes to stderr")
	project, services, err := serviceArgs("recreate", fs, args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	inst, err := e.Instance(ctx, project, sel.worktree, sel.envFiles)
	if err != nil {
		return err
	}
	describe(stderr, inst)
	opts.Profiles, opts.Services = profiles, services
	out, report := jsonAction(*asJSON, "recreate", inst, services, stdout, stderr)
	if err := preparePorts(ctx, e, inst, profiles, *auto, stderr); err != nil {
		return err
	}
	if err := e.Recreate(ctx, inst, opts, out, stderr); err != nil {
		return err
	}
	what := "its containers"
	if len(services) > 0 {
		what = strings.Join(services, ", ")
	}
	fmt.Fprintf(out, "Recreated %s of %s. Named volumes were kept.\n", what, inst.Compose)
	return report.finish(stdout)
}

func runExec(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	return runExecLike(e, "exec", args, stdout, stderr)
}

func runShell(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	return runExecLike(e, "shell", args, stdout, stderr)
}

// shellCommand starts bash when the container has it and sh otherwise, in
// one exec, so there is a single command to run (and to explain).
var shellCommand = []string{"sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash || exec sh"}

func runExecLike(e *engine.Engine, cmd string, args []string, stdout, stderr io.Writer) error {
	// Everything after -- is the command, with its own flags.
	var command []string
	for i, a := range args {
		if a == "--" {
			args, command = args[:i], args[i+1:]
			break
		}
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	var sel instanceFlags
	sel.register(fs)
	noTTY := false
	if cmd == "exec" {
		fs.BoolVar(&noTTY, "T", false, "do not allocate a terminal")
	}
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	switch {
	case cmd == "exec" && (len(pos) != 2 || len(command) == 0):
		return usageErrorf("exec needs a project, a service and a command: theo exec <project> [-w <w>] [-T] <service> -- <command> [args...]")
	case cmd == "shell" && len(pos) != 2:
		return usageErrorf("shell needs a project and a service: theo shell <project> [-w <w>] <service>")
	case cmd == "shell" && len(command) > 0:
		return usageErrorf("shell takes no command; use `theo exec` to run one")
	}
	if cmd == "shell" {
		command = shellCommand
	}
	// A terminal only when the input is one: otherwise Compose would fail
	// ("the input device is not a TTY") or mangle piped data.
	if f, ok := stdin.(*os.File); !ok || !execx.IsTerminal(f) {
		noTTY = true
	}

	ctx := context.Background()
	inst, err := e.Instance(ctx, pos[0], sel.worktree, sel.envFiles)
	if err != nil {
		return err
	}
	err = e.Exec(ctx, inst, compose.ExecOptions{Service: pos[1], Command: command, NoTTY: noTTY}, stdin, stdout, stderr)
	var ee *execx.ExitError
	if errors.As(err, &ee) {
		return &exitCodeError{ee.Code}
	}
	return err
}

// hasFlag reports whether args hold the boolean flag name (as -name or
// --name, alone or =true).
func hasFlag(args []string, name string) bool {
	bare := strings.TrimLeft(name, "-")
	for _, a := range args {
		if a == "--" {
			return false
		}
		a = strings.TrimLeft(a, "-")
		if a == bare || a == bare+"=true" {
			return true
		}
	}
	return false
}

// stopAll stops the instances that have running containers, in every
// project or in one. Nothing is removed.
func stopAll(ctx context.Context, e *engine.Engine, project string, stdout, stderr io.Writer) error {
	var names []string
	if project != "" {
		p, err := e.Project(project)
		if err != nil {
			return err
		}
		names = []string{p.Name}
	} else {
		projects, err := e.Projects()
		if err != nil {
			return err
		}
		for _, p := range projects {
			names = append(names, p.Name)
		}
	}
	var stopped []string
	var failed []string
	for _, name := range names {
		overview, err := e.Overview(ctx, name)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %s", name, firstLine(err.Error())))
			continue
		}
		for _, o := range overview {
			if !anyRunning(o.Stacks) {
				continue
			}
			inst, err := e.Instance(ctx, name, o.Worktree.Path, nil)
			if err != nil {
				failed = append(failed, fmt.Sprintf("%s %s: %s", name, o.Worktree.Path, firstLine(err.Error())))
				continue
			}
			describe(stderr, inst)
			if err := e.Stop(ctx, inst, nil, stdout, stderr); err != nil {
				failed = append(failed, fmt.Sprintf("%s: %s", inst.Compose, firstLine(err.Error())))
				continue
			}
			stopped = append(stopped, inst.Compose)
		}
	}
	switch {
	case len(stopped) == 0 && len(failed) == 0:
		fmt.Fprintln(stdout, "No instance is running.")
	case len(stopped) > 0:
		fmt.Fprintf(stdout, "Stopped %d instance(s): %s. Their containers and volumes were kept; `theo start <project> -w <worktree>` starts one again.\n", len(stopped), strings.Join(stopped, ", "))
	}
	if len(failed) > 0 {
		return fmt.Errorf("could not stop %d instance(s):\n  %s", len(failed), strings.Join(failed, "\n  "))
	}
	return nil
}

// anyRunning reports whether one of the Compose projects has a running
// container ("running(2)", "running(1), exited(1)").
func anyRunning(stacks []compose.Stack) bool {
	for _, s := range stacks {
		if strings.Contains(s.Status, "running") {
			return true
		}
	}
	return false
}
