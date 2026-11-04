package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/execx"
	"github.com/ElMaxter99/Theo/internal/repoconfig"
)

func init() {
	registerHelp(32, `
  worktree setup <project> <worktree> [--yes] [--json]
                                               Run the after_create commands that .theo.json declares
                                               (install dependencies, generate code) in a worktree.
                                               They run code from the repository, so Theo asks the
                                               first time and whenever the list changes; --yes
                                               approves without asking. worktree add runs them too
                                               (--no-setup skips them)
`)
}

// interactive says whether stdin is a terminal a person can answer on.
func interactive() bool {
	f, ok := stdin.(*os.File)
	return ok && execx.IsTerminal(f)
}

// confirmSetup shows the commands and asks whether to run them.
func confirmSetup(w io.Writer, commands []string) bool {
	fmt.Fprintf(w, "%s asks to run these commands in the new worktree. They run code from the repository:\n", repoconfig.FileName)
	for _, c := range commands {
		fmt.Fprintf(w, "  %s\n", c)
	}
	fmt.Fprint(w, "Run them (and remember this choice for these exact commands)? [y/N] ")
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// runSetupAfterAdd runs the after_create commands of a worktree that was just
// created, asking first when they are new. It returns the result, and an error
// when a command failed. What it cannot run it says, and the worktree stays.
func runSetupAfterAdd(ctx context.Context, e *engine.Engine, project string, added *engine.AddedWorktree, yes bool, out, stderr io.Writer) (*engine.SetupResult, error) {
	plan, err := e.PlanSetup(project, added.Path)
	if err != nil {
		fmt.Fprintf(stderr, "theo: warning: %v\n", err)
		return nil, nil
	}
	if len(plan.Commands) == 0 {
		return nil, nil
	}
	approve := yes
	if !plan.Trusted && !approve {
		if !interactive() {
			fmt.Fprintf(stderr, "theo: %s declares %d after_create command(s) that were not run: they need your approval. Run: theo worktree setup %s %s\n", repoconfig.FileName, len(plan.Commands), project, added.Branch)
			return &engine.SetupResult{SetupPlan: plan}, nil
		}
		if !confirmSetup(out, plan.Commands) {
			fmt.Fprintf(out, "Not run. When you want them: theo worktree setup %s %s\n", project, added.Branch)
			return &engine.SetupResult{SetupPlan: plan}, nil
		}
		approve = true
	}
	res, err := e.RunSetup(ctx, project, added.Path, approve, out, stderr)
	if err != nil {
		return res, fmt.Errorf("%w (the worktree was created; fix it and run: theo worktree setup %s %s)", err, project, added.Branch)
	}
	fmt.Fprintf(out, "Ran %d after_create command(s).\n", len(res.Ran))
	return res, nil
}

func runWorktreeSetup(ctx context.Context, e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("worktree setup", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "approve the commands without asking, and remember it")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return usageErrorf("worktree setup needs a project name and a worktree")
	}
	path, err := e.WorkspacePath(ctx, pos[0], pos[1])
	if err != nil {
		return err
	}
	out := stdout
	if *asJSON {
		out = stderr
	}
	plan, err := e.PlanSetup(pos[0], path)
	if err != nil {
		return err
	}
	if len(plan.Commands) == 0 {
		if *asJSON {
			return writeJSON(stdout, engine.SetupResult{SetupPlan: plan})
		}
		fmt.Fprintf(stdout, "%s declares no after_create commands for %s.\n", repoconfig.FileName, path)
		return nil
	}
	approve := *yes
	if !plan.Trusted && !approve {
		if !interactive() || !confirmSetup(out, plan.Commands) {
			return fmt.Errorf("%w: pass --yes to approve them, after reading them in %s", engine.ErrSetupNotApproved, repoconfig.FileName)
		}
		approve = true
	}
	res, err := e.RunSetup(ctx, pos[0], path, approve, out, stderr)
	if *asJSON {
		if werr := writeJSON(stdout, res); werr != nil {
			return werr
		}
		return err
	}
	if err != nil && !errors.Is(err, engine.ErrSetupNotApproved) {
		return err
	}
	fmt.Fprintf(stdout, "Ran %d after_create command(s) in %s.\n", len(res.Ran), path)
	return err
}
