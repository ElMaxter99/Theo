package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
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
	var autoPorts, startShared bool
	switch cmd {
	case "up":
		fs.BoolVar(&up.Build, "build", false, "build images before starting")
		fs.Var(&profiles, "profile", "enable a Compose profile (repeatable)")
		fs.BoolVar(&autoPorts, "auto-ports", false, "use other host ports for the ones that are taken; remembered for next starts (turn off with `instance set --no-auto-ports`)")
		fs.BoolVar(&startShared, "start-shared", false, "start the shared services this instance uses if they are stopped")
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
		if autoPorts {
			if err := e.SetAutoPorts(inst, true); err != nil {
				return err
			}
		}
		warnings, err := e.EnsureShared(ctx, inst, startShared, stdout, stderr)
		if err != nil {
			return err
		}
		for _, w := range warnings {
			fmt.Fprintf(stderr, "theo: warning: %s\n", w)
		}
		if err := preparePorts(ctx, e, inst, profiles, autoPorts, stderr); err != nil {
			return err
		}
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

// preparePorts prepares the instance before `up`: it applies the instance's
// settings and checks the host ports. With auto it reassigns the ones that
// are taken; without it, it only warns, and Docker has the last word.
func preparePorts(ctx context.Context, e *engine.Engine, inst *engine.Instance, profiles []string, auto bool, stderr io.Writer) error {
	plan, err := e.ApplyInstance(ctx, inst, profiles, auto)
	if err != nil {
		return err
	}
	for _, m := range plan.Mappings {
		if m.Status == engine.PortReassigned || m.Status == engine.PortKept {
			fmt.Fprintf(stderr, "theo: %s publishes %d/%s on host port %d instead of %s (%s)\n", m.Service, m.Target, m.Protocol, m.Effective, m.Configured, m.Status)
		}
	}
	for _, m := range plan.Conflicts() {
		fmt.Fprintf(stderr, "theo: warning: host port %s (%s, container port %d/%s) is already in use. Free it, or start with --auto-ports.\n", m.Configured, m.Service, m.Target, m.Protocol)
	}
	for _, f := range inst.GeneratedFiles() {
		fmt.Fprintf(stderr, "theo: using generated file %s\n", f)
	}
	if names := inst.FixedContainerNames(); len(names) > 0 && !inst.Main && !inst.Neutralized() {
		fmt.Fprintf(stderr, "theo: warning: %s set a fixed container_name, so another instance of this project cannot run at the same time. `theo instance set %s -w %s --neutralize-container-names` removes it for this instance.\n",
			strings.Join(compose.SortedKeys(names), ", "), inst.Project, worktreeArg(inst))
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

func runPorts(e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("ports", flag.ContinueOnError)
	var sel instanceFlags
	sel.register(fs)
	var profiles stringList
	fs.Var(&profiles, "profile", "include the services of a Compose profile (repeatable)")
	auto := fs.Bool("auto-ports", false, "show what `theo up --auto-ports` would do")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("ports needs exactly one project name")
	}
	ctx := context.Background()
	inst, err := e.Instance(ctx, pos[0], sel.worktree, sel.envFiles)
	if err != nil {
		return err
	}
	plan, err := e.PlanPorts(ctx, inst, profiles, *auto)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, struct {
			*engine.Instance
			*engine.PortPlan
		}{inst, plan})
	}
	describe(stdout, inst)
	if len(plan.Mappings) == 0 {
		fmt.Fprintln(stdout, "No service publishes ports on the host.")
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tCONTAINER\tCONFIGURED\tHOST PORT\tSTATUS\tURL")
	for _, m := range plan.Mappings {
		host := "-"
		switch {
		case m.Running != 0:
			host = strconv.Itoa(m.Running)
		case m.Effective != 0:
			host = strconv.Itoa(m.Effective)
		}
		configured := m.Configured
		if configured == "" {
			configured = "-"
		}
		url := m.URL()
		if url == "" {
			url = "-"
		}
		fmt.Fprintf(tw, "%s\t%d/%s\t%s\t%s\t%s\t%s\n", m.Service, m.Target, m.Protocol, configured, host, m.Status, url)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if n := len(plan.Conflicts()); n > 0 {
		fmt.Fprintf(stdout, "\n%d port(s) in use. `theo up %s --auto-ports` starts with other ones.\n", n, inst.Project)
	}
	return nil
}
