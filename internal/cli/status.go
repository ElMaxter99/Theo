package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/ElMaxter99/Theo/internal/engine"
)

func init() {
	registerCommand("status", runStatus)
	registerHelp(20, `
Dashboard:
  status [project] [--json]                         Every project (or one) and its worktrees: branch,
                                                    kind, uncommitted changes, instance state and
                                                    services, published ports with URLs, settings,
                                                    and warnings (port conflicts, foreign stacks,
                                                    missing env file, drift from reconcile)

Global flag, before the command:
  --explain                                         theo --explain up app -w x : list the Git and
                                                    Docker commands the operation runs, with their
                                                    directory, and run none that changes anything
`)
}

func runStatus(e *engine.Engine, args []string, stdout, _ io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return usageErrorf("status takes at most one project name")
	}
	project := ""
	if len(pos) == 1 {
		project = pos[0]
	}
	report, err := e.Status(context.Background(), project)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, report)
	}
	return printStatus(stdout, report)
}

func printStatus(w io.Writer, r *engine.StatusReport) error {
	if r.DockerError != "" {
		fmt.Fprintf(w, "Docker could not be asked, so states are unknown:\n  %s\n\n", firstLine(r.DockerError))
	}
	if len(r.Projects) == 0 {
		fmt.Fprintln(w, "No projects registered. Add one with `theo project add <path>`.")
	}
	for i, p := range r.Projects {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s  %s\n", p.Name, p.Path)
		if p.Error != "" {
			fmt.Fprintf(w, "  cannot read it: %s\n", p.Error)
		}
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for j, wt := range p.Worktrees {
			if j == 0 {
				fmt.Fprintln(tw, "  BRANCH\tKIND\tGIT\tINSTANCE\tSERVICES\tPORTS\tSETTINGS")
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\n", statusRef(wt), wt.Kind, statusGit(wt),
				statusState(wt), statusServices(wt), statusPorts(wt), statusSettings(wt))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		for _, wt := range p.Worktrees {
			for _, warn := range wt.Warnings {
				fmt.Fprintf(w, "  ! %s: %s [%s]\n", statusRef(wt), warn.Message, warn.Kind)
				if warn.Fix != "" {
					fmt.Fprintf(w, "      -> %s\n", warn.Fix)
				}
			}
		}
		for _, warn := range p.Warnings {
			fmt.Fprintf(w, "  ! %s [%s]\n", warn.Message, warn.Kind)
			if warn.Fix != "" {
				fmt.Fprintf(w, "      -> %s\n", warn.Fix)
			}
		}
	}
	for _, warn := range r.Warnings {
		fmt.Fprintf(w, "\n! %s [%s]\n", warn.Message, warn.Kind)
		if warn.Fix != "" {
			fmt.Fprintf(w, "    -> %s\n", warn.Fix)
		}
	}
	return nil
}

func statusRef(wt engine.WorktreeStatus) string {
	switch {
	case wt.Kind == engine.KindBare:
		return "(bare)"
	case wt.Branch != "":
		return wt.Branch
	case len(wt.Head) >= 7:
		return "(detached " + wt.Head[:7] + ")"
	default:
		return "(detached)"
	}
}

func statusGit(wt engine.WorktreeStatus) string {
	switch {
	case wt.Kind == engine.KindBare:
		return "-"
	case wt.Missing:
		return "missing"
	case wt.Dirty:
		return "dirty"
	}
	return "clean"
}

func statusState(wt engine.WorktreeStatus) string {
	if wt.Instance == nil {
		return "-"
	}
	return wt.Instance.State
}

func statusServices(wt engine.WorktreeStatus) string {
	if wt.Instance == nil || wt.Instance.Counts.Total == 0 {
		return "-"
	}
	c := wt.Instance.Counts
	parts := []string{fmt.Sprintf("%d/%d running", c.Running, c.Total)}
	if c.Stopped > 0 {
		parts = append(parts, fmt.Sprintf("%d stopped", c.Stopped))
	}
	if c.Completed > 0 {
		parts = append(parts, fmt.Sprintf("%d completed", c.Completed))
	}
	return strings.Join(parts, ", ")
}

func statusPorts(wt engine.WorktreeStatus) string {
	if wt.Instance == nil {
		return "-"
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range wt.Instance.Ports {
		if p.URL == "" {
			continue
		}
		s := p.URL
		if p.Status != engine.PortRunning {
			s += " (" + p.Status + ")"
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return "-"
	}
	return strings.Join(out, ", ")
}

func statusSettings(wt engine.WorktreeStatus) string {
	s := wt.Settings
	if s == nil {
		return "-"
	}
	var parts []string
	if s.EnvVars > 0 {
		parts = append(parts, fmt.Sprintf("%d env var(s)", s.EnvVars))
	}
	if s.NeutralizeContainerNames {
		parts = append(parts, "container names neutralized")
	}
	if len(s.Profiles) > 0 {
		parts = append(parts, "profiles: "+strings.Join(s.Profiles, ","))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "; ")
}
