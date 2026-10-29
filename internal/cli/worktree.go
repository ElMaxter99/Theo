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

func runBranches(e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("branches", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("branches needs exactly one project name")
	}
	list, err := e.Branches(context.Background(), pos[0])
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, list)
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "BRANCH\tKIND\tWORKTREE")
	for _, b := range list {
		kind, wt := "local", b.Worktree
		if b.Remote {
			kind = "remote"
		}
		if wt == "" {
			wt = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", b.Name, kind, wt)
	}
	return tw.Flush()
}

func runWorktree(e *engine.Engine, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageErrorf("worktree needs a subcommand: add or remove")
	}
	ctx := context.Background()
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("worktree add", flag.ContinueOnError)
		newBranch := fs.Bool("new", false, "create the branch")
		base := fs.String("from", "", "with --new: branch or commit to start from (default: the current HEAD)")
		path := fs.String("path", "", "directory for the worktree (default: the project's worktree directory)")
		asJSON := fs.Bool("json", false, "print JSON")
		pos, err := parse(fs, args[1:])
		if err != nil {
			return err
		}
		if len(pos) != 2 {
			return usageErrorf("worktree add needs a project name and a branch")
		}
		if *base != "" && !*newBranch {
			return usageErrorf("--from only makes sense with --new")
		}
		added, err := e.AddWorktree(ctx, pos[0], pos[1], engine.AddWorktreeOptions{NewBranch: *newBranch, Base: *base, Path: *path})
		if err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(stdout, added)
		}
		fmt.Fprintf(stdout, "Created worktree for %s at %s\n", added.Branch, added.Path)
		for _, n := range added.Notes {
			fmt.Fprintf(stdout, "Note: %s\n", n)
		}
		fmt.Fprintf(stdout, "Start it with `theo up %s -w %s`.\n", pos[0], added.Branch)
		return nil

	case "remove":
		fs := flag.NewFlagSet("worktree remove", flag.ContinueOnError)
		discard := fs.Bool("discard-changes", false, "remove it even if it has uncommitted or untracked files, which are lost")
		confirm := fs.String("confirm", "", "with --discard-changes: the directory name of the worktree, to confirm")
		pos, err := parse(fs, args[1:])
		if err != nil {
			return err
		}
		if len(pos) != 2 {
			return usageErrorf("worktree remove needs a project name and a worktree")
		}
		wt, err := e.RemoveWorktree(ctx, pos[0], pos[1], engine.RemoveWorktreeOptions{DiscardChanges: *discard, Confirm: *confirm})
		var changes *engine.ChangesError
		if errors.As(err, &changes) {
			return fmt.Errorf("%w\n\n%s\n\nCommit or move them, or discard them with:\n  theo worktree remove %s %s --discard-changes --confirm %s",
				err, indent(changes.Changes), pos[0], pos[1], changes.Name)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Removed worktree %s. The branch %s and any volumes were kept.\n", wt.Path, worktreeRef(engine.Worktree{Worktree: wt}))
		return nil
	}
	return usageErrorf("unknown worktree subcommand %q", args[0])
}

func indent(lines []string) string {
	return "  " + strings.Join(lines, "\n  ")
}
