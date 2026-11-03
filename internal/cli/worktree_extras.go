package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/ElMaxter99/Theo/internal/engine"
)

func init() {
	registerHelp(40, `
Workspace extras (see docs/cli/worktrees.md):
  worktree add <project> <branch> [--clone] [--owner <name>] [--copy] [--copy-file <f>]...
                                               --clone creates an independent clone (git clone
                                               --local, origin set to the project's remote,
                                               submodules initialized) instead of a linked
                                               worktree. --owner labels it. --copy copies the
                                               files listed with project set --copy-on-add from
                                               the main worktree; --copy-file <f> copies one
                                               file. Only untracked/ignored files, never
                                               overwriting; copied secrets are reported
  project set <name> --copy-on-add <f>... | --no-copy-on-add
                                               Root-relative files that --copy copies
  worktrees <project> [--owner <name>] [--json]
                                               Includes KIND (main, worktree, clone) and OWNER
  worktree owner <project> <w> <name> | --clear
                                               Set or clear the owner label of a workspace
  worktree prune <project> --merged [--into <branch>] [--confirm <project>] [--json]
                                               List the linked worktrees whose branch is already
                                               merged into the main worktree's branch (or
                                               <branch>); --confirm with the project name removes
                                               the ones with no containers and no uncommitted
                                               files. Branches, volumes and clones are kept
`)
}

func runWorktreePrune(ctx context.Context, e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("worktree prune", flag.ContinueOnError)
	merged := fs.Bool("merged", false, "worktrees whose branch is merged (required)")
	into := fs.String("into", "", "branch the others must be merged into (default: the main worktree's branch)")
	confirm := fs.String("confirm", "", "the project name, to remove them")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("worktree prune needs a project name")
	}
	if !*merged {
		return usageErrorf("worktree prune needs --merged: it only removes worktrees whose branch is already merged")
	}
	p, err := e.Project(pos[0])
	if err != nil {
		return err
	}
	if *confirm != "" && *confirm != p.Name {
		return fmt.Errorf("--confirm must be the project name, %q; nothing was removed", p.Name)
	}
	apply := *confirm != ""
	report, err := e.PruneMerged(ctx, p.Name, engine.PruneOptions{Into: *into, Apply: apply})
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, report)
	}
	if len(report.Merged) == 0 {
		fmt.Fprintf(stdout, "No linked worktree of %s has a branch merged into %s", p.Name, report.Into)
		if report.NotMerged > 0 {
			fmt.Fprintf(stdout, " (%d not merged)", report.NotMerged)
		}
		fmt.Fprintln(stdout, ".")
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "BRANCH\tPATH\tOWNER\tRESULT")
	for _, m := range report.Merged {
		result := "can be removed"
		switch {
		case m.Removed:
			result = "removed"
		case m.Reason != "":
			result = "kept: " + m.Reason
		}
		owner := m.Owner
		if owner == "" {
			owner = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.Branch, m.Path, owner, result)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	removable := len(report.Removable())
	switch {
	case apply:
		fmt.Fprintf(stdout, "\nBranches and volumes were kept. %d worktree(s) not merged into %s were left alone.\n", report.NotMerged, report.Into)
	case removable > 0:
		fmt.Fprintf(stdout, "\nNothing was removed. To remove the %d worktree(s) marked \"can be removed\" (branches and volumes are kept):\n  theo worktree prune %s --merged", removable, p.Name)
		if *into != "" {
			fmt.Fprintf(stdout, " --into %s", *into)
		}
		fmt.Fprintf(stdout, " --confirm %s\n", p.Name)
	default:
		fmt.Fprintln(stdout, "\nNothing can be removed now.")
	}
	return nil
}
