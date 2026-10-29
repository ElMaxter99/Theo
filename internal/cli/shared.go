package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/ElMaxter99/Theo/internal/engine"
)

func init() {
	registerCommand("shared", runShared)
	registerHelp(40, `
Shared services (nothing is shared unless you say so):
  shared add <project> <service>... [--from <w>] [--publish]
                                                    Declare services of the project as shared, defined
                                                    by the Compose files of worktree <w> (default: main)
  shared up <project>                               Start them in their own Compose project
                                                    (<project>-shared), on a network Theo creates
  shared use <project> -w <w> <service>... [--join <svc>]
                                                    Make an instance use them instead of running its
                                                    own copy; takes effect at its next up
  shared unuse <project> -w <w> [<service>...]      Run its own copy again
  shared status <project> [--json]                  State, and which instances use them
  shared down <project> [--confirm <compose-project>]
                                                    Stop them; refused while running instances use them
  shared remove <project> [<service>...]            Stop sharing (refused while instances use them)
  shared purge <project> --confirm <compose-project>
                                                    Delete their data (volumes)
  up ... --start-shared                             Start the shared services first if they are stopped
`)
}

func runShared(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageErrorf("shared needs a subcommand: add, up, use, unuse, status, down, remove or purge")
	}
	sub := args[0]
	fs := flag.NewFlagSet("shared "+sub, flag.ContinueOnError)
	var sel instanceFlags
	var from, confirm string
	var publish, asJSON bool
	var join stringList
	switch sub {
	case "add":
		fs.StringVar(&from, "from", "", "worktree whose Compose files define the shared services (default: main)")
		fs.BoolVar(&publish, "publish", false, "keep the host ports the project publishes for them")
	case "use", "unuse":
		sel.register(fs)
		if sub == "use" {
			fs.Var(&join, "join", "service of the instance that joins the shared network (repeatable)")
		}
	case "status":
		fs.BoolVar(&asJSON, "json", false, "print JSON")
	case "down", "purge":
		fs.StringVar(&confirm, "confirm", "", "the shared Compose project name, to confirm")
	case "up", "remove":
	default:
		return usageErrorf("unknown shared subcommand %q", sub)
	}
	pos, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return usageErrorf("shared %s needs a project name", sub)
	}
	project, rest := pos[0], pos[1:]
	ctx := context.Background()

	switch sub {
	case "add":
		set, err := e.SharedAdd(ctx, project, rest, from, publish)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Shared services of %s: %s (defined by %s).\nStart them with `theo shared up %s`. Nothing runs or changes until then.\n", project, strings.Join(set.Services, ", "), set.From, project)
	case "up":
		if len(rest) > 0 {
			return usageErrorf("shared up takes only the project name")
		}
		if err := e.SharedUp(ctx, project, stdout, stderr); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Shared services of %s are running. See them with `theo shared status %s`.\n", project, project)
	case "down":
		if err := e.SharedDown(ctx, project, confirm, stdout, stderr); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Stopped the shared services of %s. Their volumes were kept.\n", project)
	case "remove":
		if err := e.SharedRemove(ctx, project, rest); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Stopped sharing %s in %s. Volumes were kept.\n", describeServices(rest), project)
	case "purge":
		removed, err := e.SharedPurge(ctx, project, confirm)
		var pe *engine.PurgeError
		if errors.As(err, &pe) {
			msg := fmt.Sprintf("%s\n\n%s", err, volumeLines(pe.Volumes))
			if consumers, cerr := e.SharedConsumers(ctx, project); cerr == nil && len(consumers) > 0 {
				msg += "\n\nInstances linked to them: " + consumerList(consumers)
			}
			if strings.Contains(err.Error(), "for good") {
				msg += fmt.Sprintf("\n\nTo delete them:\n  theo shared purge %s --confirm %s", project, pe.Instance)
			}
			return errors.New(msg)
		}
		if err != nil {
			return err
		}
		if len(removed) == 0 {
			fmt.Fprintln(stdout, "The shared services have no volumes to delete.")
			return nil
		}
		fmt.Fprintf(stdout, "Deleted %d volume(s) of the shared services:\n%s\n", len(removed), volumeLines(removed))
	case "use", "unuse":
		inst, err := e.Instance(ctx, project, sel.worktree, sel.envFiles)
		if err != nil {
			return err
		}
		if sub == "use" {
			if err := e.SharedUse(inst, rest, join); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "%s will use the shared %s instead of its own copy, from its next `theo up %s -w %s`.\n", inst.Compose, strings.Join(rest, ", "), project, worktreeArg(inst))
			return nil
		}
		if err := e.SharedUnuse(inst, rest); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s will run its own copy of %s again from its next `theo up`.\n", inst.Compose, describeServices(rest))
	case "status":
		st, err := e.SharedStatus(ctx, project)
		if err != nil {
			return err
		}
		if asJSON {
			return writeJSON(stdout, st)
		}
		fmt.Fprintf(stdout, "compose project: %s\ndefined by:      %s\nnetwork:         %s\nhost ports:      %s\n\n", st.Compose, st.From, st.Network, map[bool]string{true: "published as the project says", false: "not published"}[st.Publish])
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "SERVICE\tSTATE\tIMAGE")
		for _, s := range st.Services {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Name, s.State, s.Image)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		if len(st.Consumers) == 0 {
			fmt.Fprintln(stdout, "\nNo instance uses them.")
		} else {
			fmt.Fprintf(stdout, "\nUsed by %d instance(s): %s\n", len(st.Consumers), consumerList(st.Consumers))
		}
		for _, w := range st.Warnings {
			fmt.Fprintf(stdout, "warning: %s\n", w)
		}
	}
	return nil
}

func describeServices(services []string) string {
	if len(services) == 0 {
		return "all shared services"
	}
	return strings.Join(services, ", ")
}

func consumerList(cs []engine.SharedConsumer) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		who := c.Branch
		if who == "" {
			who = c.Workspace
		}
		state := "linked"
		if c.Active {
			state = "running"
		}
		parts[i] = fmt.Sprintf("%s (%s: %s)", who, state, strings.Join(c.Services, ","))
	}
	return strings.Join(parts, "; ")
}
