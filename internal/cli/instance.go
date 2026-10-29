package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
)

// instanceFlags are the flags that select a worktree's instance.
type instanceFlags struct {
	worktree string
	envFiles stringList
}

func (f *instanceFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.worktree, "worktree", "", "branch, directory name or path of the worktree")
	fs.StringVar(&f.worktree, "w", "", "shorthand for --worktree")
	fs.Var(&f.envFiles, "env-file", "env file to use instead of the usual one (repeatable)")
}

func runInstance(e *engine.Engine, cmd string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	var sel instanceFlags
	sel.register(fs)
	var up compose.UpOptions
	var profiles stringList
	var logs compose.LogOptions
	switch cmd {
	case "up":
		fs.BoolVar(&up.Build, "build", false, "build images before starting")
		fs.Var(&profiles, "profile", "enable a Compose profile (repeatable)")
	case "logs":
		fs.BoolVar(&logs.Follow, "follow", false, "follow log output")
		fs.BoolVar(&logs.Follow, "f", false, "shorthand for --follow")
		fs.IntVar(&logs.Tail, "tail", 0, "number of lines to show per container")
	}
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 || (cmd != "logs" && len(pos) != 1) {
		return usageErrorf("%s needs exactly one project name", cmd)
	}

	ctx := context.Background()
	inst, err := e.Instance(ctx, pos[0], sel.worktree, sel.envFiles)
	if err != nil {
		return err
	}
	describe(stderr, inst)

	switch cmd {
	case "up":
		up.Profiles = profiles
		if err := e.Up(ctx, inst, up, stdout, stderr); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Started %s. See it with `theo ps %s -w %s`.\n", inst.Compose, inst.Project, worktreeArg(inst))
	case "down":
		if err := e.Down(ctx, inst, stdout, stderr); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Stopped %s. Its volumes were kept.\n", inst.Compose)
	case "restart":
		return e.Restart(ctx, inst, stdout, stderr)
	case "logs":
		logs.Services = pos[1:]
		return e.Logs(ctx, inst, logs, stdout, stderr)
	}
	return nil
}

// describe says which instance a command acts on, on stderr so that it does
// not mix with the command's own output.
func describe(w io.Writer, inst *engine.Instance) {
	where := inst.Workspace
	if inst.Branch != "" {
		where = inst.Branch + " · " + inst.Workspace
	}
	fmt.Fprintf(w, "theo: %s (compose project %q)\n", where, inst.Compose)
}

func worktreeArg(inst *engine.Instance) string {
	if inst.Branch != "" {
		return inst.Branch
	}
	return inst.Workspace
}

func runPS(e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("ps", flag.ContinueOnError)
	var sel instanceFlags
	sel.register(fs)
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("ps needs exactly one project name")
	}
	ctx := context.Background()

	if sel.worktree == "" && len(sel.envFiles) == 0 {
		overview, err := e.Overview(ctx, pos[0])
		if err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(stdout, overview)
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "BRANCH\tCOMPOSE PROJECT\tSTATUS\tPATH")
		idle := 0
		for _, o := range overview {
			if len(o.Stacks) == 0 {
				idle++
				continue
			}
			for _, s := range o.Stacks {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", worktreeRef(engine.Worktree{Worktree: o.Worktree}), s.Name, s.Status, o.Worktree.Path)
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "\n%d of %d worktrees have no containers.\n", idle, len(overview))
		return nil
	}

	inst, err := e.Instance(ctx, pos[0], sel.worktree, sel.envFiles)
	if err != nil {
		return err
	}
	containers, err := e.Containers(ctx, inst)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, struct {
			*engine.Instance
			Containers []compose.Container `json:"containers"`
		}{inst, containers})
	}
	describe(stdout, inst)
	if len(containers) == 0 {
		fmt.Fprintf(stdout, "No containers. Start them with `theo up %s -w %s`.\n", inst.Project, worktreeArg(inst))
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tSTATE\tPORTS\tCONTAINER")
	for _, c := range containers {
		state := c.State
		if c.Health != "" {
			state += " (" + c.Health + ")"
		}
		if c.State == "exited" {
			state += fmt.Sprintf(" (%d)", c.ExitCode)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.Service, state, formatPublishers(c.Publishers), c.Name)
	}
	return tw.Flush()
}

func formatPublishers(ps []compose.Publisher) string {
	var out []string
	seen := map[string]bool{}
	for _, p := range ps {
		if p.PublishedPort == 0 {
			continue
		}
		s := fmt.Sprintf("%d -> %d/%s", p.PublishedPort, p.TargetPort, p.Protocol)
		if !seen[s] { // Docker lists IPv4 and IPv6 bindings separately
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return "-"
	}
	return strings.Join(out, ", ")
}
