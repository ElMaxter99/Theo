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
		return usageErrorf("worktree needs a subcommand: add, remove or owner")
	}
	ctx := context.Background()
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("worktree add", flag.ContinueOnError)
		newBranch := fs.Bool("new", false, "create the branch")
		base := fs.String("from", "", "with --new: branch or commit to start from (default: the current HEAD)")
		path := fs.String("path", "", "directory for the worktree (default: the project's worktree directory)")
		clone := fs.Bool("clone", false, "create an independent clone instead of a linked worktree (for projects with submodules)")
		owner := fs.String("owner", "", "label the workspace with an owner, such as an agent name")
		copyFiles := fs.Bool("copy", false, "copy the files listed with `project set --copy-on-add` from the main worktree")
		var oneOff stringList
		fs.Var(&oneOff, "copy-file", "copy this root-relative file from the main worktree (repeatable)")
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
		added, err := e.AddWorktree(ctx, pos[0], pos[1], engine.AddWorktreeOptions{
			NewBranch: *newBranch, Base: *base, Path: *path,
			Clone: *clone, Owner: *owner, Copy: *copyFiles, CopyFiles: oneOff,
		})
		if err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(stdout, added)
		}
		what := "worktree"
		if added.Kind == engine.KindClone {
			what = "independent clone"
		}
		fmt.Fprintf(stdout, "Created %s for %s at %s\n", what, added.Branch, added.Path)
		if added.Owner != "" {
			fmt.Fprintf(stdout, "Owner: %s\n", added.Owner)
		}
		if len(added.Copied) > 0 {
			fmt.Fprintf(stdout, "Copied from the main worktree: %s\n", strings.Join(added.Copied, ", "))
		}
		for _, n := range added.NotCopied {
			fmt.Fprintf(stdout, "Not copied: %s (%s)\n", n.Path, n.Reason)
		}
		for _, n := range added.Notes {
			fmt.Fprintf(stdout, "Note: %s\n", n)
		}
		fmt.Fprintf(stdout, "Start it with `theo up %s -w %s`.\n", pos[0], added.Branch)
		return nil

	case "remove":
		fs := flag.NewFlagSet("worktree remove", flag.ContinueOnError)
		discard := fs.Bool("discard-changes", false, "remove it even if it has uncommitted or untracked files, which are lost")
		confirm := fs.String("confirm", "", "with --discard-changes: the directory name of the worktree, to confirm")
		asJSON := fs.Bool("json", false, "print JSON")
		pos, err := parse(fs, args[1:])
		if err != nil {
			return err
		}
		if len(pos) != 2 {
			return usageErrorf("worktree remove needs a project name and a worktree")
		}
		res, err := e.RemoveWorkspace(ctx, pos[0], pos[1], engine.RemoveWorktreeOptions{DiscardChanges: *discard, Confirm: *confirm})
		var changes *engine.ChangesError
		if errors.As(err, &changes) {
			return fmt.Errorf("%w\n\n%s\n\nCommit or move them, or discard them with:\n  theo worktree remove %s %s --discard-changes --confirm %s",
				err, indent(changes.Changes), pos[0], pos[1], changes.Name)
		}
		if err != nil {
			return err
		}
		wt := res.Worktree
		if *asJSON {
			return writeJSON(stdout, struct {
				Path   string `json:"path"`
				Branch string `json:"branch,omitempty"`
				Kind   string `json:"kind"`
			}{wt.Path, wt.Branch, res.Kind})
		}
		switch {
		case res.Forgot:
			fmt.Fprintf(stdout, "The clone %s was already gone; Theo forgot its record.\n", wt.Path)
		case res.Kind == engine.KindClone:
			fmt.Fprintf(stdout, "Removed clone %s with its Git history. Branches that were pushed are on the remote; any volumes were kept.\n", wt.Path)
		default:
			fmt.Fprintf(stdout, "Removed worktree %s. The branch %s and any volumes were kept.\n", wt.Path, worktreeRef(engine.Worktree{Worktree: wt}))
		}
		return nil

	case "owner":
		fs := flag.NewFlagSet("worktree owner", flag.ContinueOnError)
		clear := fs.Bool("clear", false, "remove the owner")
		asJSON := fs.Bool("json", false, "print JSON")
		pos, err := parse(fs, args[1:])
		if err != nil {
			return err
		}
		if *clear && len(pos) != 2 || !*clear && len(pos) != 3 {
			return usageErrorf("worktree owner needs <project> <worktree> <name>, or <project> <worktree> --clear")
		}
		name := ""
		if !*clear {
			name = pos[2]
		}
		wt, err := e.SetOwner(ctx, pos[0], pos[1], name)
		if err != nil {
			return err
		}
		if *asJSON {
			return writeJSON(stdout, wt)
		}
		if wt.Owner == "" {
			fmt.Fprintf(stdout, "%s has no owner now.\n", wt.Path)
		} else {
			fmt.Fprintf(stdout, "%s is owned by %s.\n", wt.Path, wt.Owner)
		}
		return nil
	}
	return usageErrorf("unknown worktree subcommand %q", args[0])
}

func indent(lines []string) string {
	return "  " + strings.Join(lines, "\n  ")
}
