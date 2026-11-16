package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ElMaxter99/Theo/internal/ai"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/tools/capture"
)

func init() {
	registerHelp(57, `
  tool capture generate <project> [worktree] [--provider P[:model]] [--parallel N] [--resume] [--yes] [--json]
                                               Generate pantallas.json and its mocks with AI: one exploration,
                                               then screens and data in parallel. The AI reads the worktree and
                                               never writes in it: the result is a draft you check and accept
  tool capture accept <project> [worktree] [--run ID] [--replace|--merge] [--yes]
                                               Write the draft of a generation into the worktree. An existing
                                               pantallas.json is never overwritten unless you say so
`)
}

func askYN(w io.Writer, question string, def bool) bool {
	fmt.Fprint(w, question)
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	switch a {
	case "":
		return def
	case "y", "yes", "s", "si", "sí":
		return true
	}
	return false
}

func captureGenerate(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("tool capture generate", flag.ContinueOnError)
	asJSON := fl.Bool("json", false, "print JSON")
	yes := fl.Bool("yes", false, "accept the privacy notice and start without asking")
	resume := fl.Bool("resume", false, "continue the last unfinished run of this worktree")
	parallel := fl.Int("parallel", 0, "jobs at a time (default 3)")
	verify := fl.Bool("verify", false, "capture the draft with the application running and send what fails back to the AI to repair")
	provider := fl.String("provider", "", "provider (or provider:model) for both roles")
	pos, err := parse(fl, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 {
		return usageErrorf("tool capture generate <project> [worktree]")
	}
	req := engine.GenerateRequest{CaptureTarget: engine.CaptureTarget{Project: pos[0]}, Provider: *provider, Parallel: *parallel, Resume: *resume, Verify: *verify, Approve: *yes}
	if len(pos) == 2 {
		req.Selector = pos[1]
	}
	out := stdout
	if *asJSON {
		out = stderr
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	plan, err := e.GeneratePlan(ctx, req)
	if err != nil {
		return err
	}
	if !plan.Consent {
		fmt.Fprintf(out, "First time for this project: the code that is read is sent to the provider you chose\n(coordinator %s, worker %s). Theo sends nothing else.\n", plan.Roles.Coordinator, plan.Roles.Worker)
		if !*yes {
			if !interactive() {
				return fmt.Errorf("the code of %s would be sent to %s: read the notice, then approve with --yes", pos[0], plan.Roles.Coordinator.Provider)
			}
			if !askYN(out, "Continue? [y/N] ", false) {
				return errors.New("cancelled: nothing was sent")
			}
		}
		if err := e.GiveGenerateConsent(pos[0], plan.Roles); err != nil {
			return err
		}
	}
	req.Confirm = func(routes, jobs int, framework string) bool {
		par := *parallel
		if par <= 0 {
			par = 3
		}
		fmt.Fprintf(out, "\nRoute map (%s): %d routes\nPlan: 1 exploration + %d screens + %d data = %d jobs, %d at a time\n      (--parallel N to change it)\n", orDash(framework), routes, routes, routes, jobs, par)
		if *yes || !interactive() {
			return true
		}
		return askYN(out, "Start? [Y/n] ", true)
	}
	last := ""
	emit := func(ev capture.GenEvent) {
		switch ev.Kind {
		case "phase":
			if ev.Phase != "explore" && ev.Phase != "merge" {
				fmt.Fprintf(out, "theo: generate: %s\n", ev.Phase)
			} else if ev.Text != "" {
				fmt.Fprintf(out, "theo: generate: %s\n", ev.Text)
			}
		case "job":
			line := fmt.Sprintf("  %-8s %2d of %d   %s", ev.Phase, ev.Done, ev.Total, ev.Job)
			if line != last {
				fmt.Fprintln(out, line)
				last = line
			}
		case "retry":
			fmt.Fprintf(out, "  %s: %s\n", ev.Job, ev.Text)
		case "failed":
			fmt.Fprintf(out, "  %s: failed: %s\n", ev.Job, ev.Text)
		}
	}
	res, err := e.CaptureGenerate(ctx, req, emit)
	if err != nil {
		switch {
		case errors.Is(err, capture.ErrGenCancelled):
			return fmt.Errorf("%v. The exploration is saved: run it again with --resume", err)
		case errors.Is(err, ai.ErrLimit):
			return fmt.Errorf("%v", err)
		}
		return err
	}
	if *asJSON {
		return writeJSON(stdout, res)
	}
	fmt.Fprintf(out, "\ntheo: generate: run %s finished\n", res.RunID)
	fmt.Fprintf(out, "  %d screens, %d states, %d flows, %d without data\n", res.Screens, res.States, res.Flows, len(res.NoData))
	if len(res.NotGenerated) > 0 {
		var names []string
		for _, f := range res.NotGenerated {
			names = append(names, f.Screen)
		}
		fmt.Fprintf(out, "  not generated (%d):  %s    failed after retries (see run.json)\n", len(names), strings.Join(names, "   "))
	}
	if v := res.Verify; v != nil {
		fmt.Fprintf(out, "  verified with the application: %d rounds, %d repaired, %d still failing", v.Rounds, len(v.Repaired), len(v.Failing))
		if len(v.Failing) > 0 {
			fmt.Fprintf(out, " (%s)", strings.Join(v.Failing, ", "))
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintf(out, "Draft: %s   (nothing in the repository yet)\n", tildePath(res.Dir))
	fmt.Fprintf(out, "Check it, then: theo tool capture accept %s%s --run %s\n", pos[0], map[bool]string{true: " " + req.Selector, false: ""}[req.Selector != ""], res.RunID)
	return nil
}

func captureAccept(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("tool capture accept", flag.ContinueOnError)
	asJSON := fl.Bool("json", false, "print JSON")
	yes := fl.Bool("yes", false, "write without asking")
	run := fl.String("run", "", "run to accept (default the last finished one)")
	replace := fl.Bool("replace", false, "replace an existing pantallas.json")
	merge := fl.Bool("merge", false, "add only the screens an existing pantallas.json does not have")
	pos, err := parse(fl, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 || (*replace && *merge) {
		return usageErrorf("tool capture accept <project> [worktree] [--run ID] [--replace|--merge]")
	}
	req := engine.AcceptRequest{CaptureTarget: engine.CaptureTarget{Project: pos[0]}, Run: *run}
	if len(pos) == 2 {
		req.Selector = pos[1]
	}
	switch {
	case *replace:
		req.Mode = "replace"
	case *merge:
		req.Mode = "merge"
	}
	out := stdout
	if *asJSON {
		out = stderr
	}
	if !*yes && req.Mode == "" {
		fmt.Fprintf(out, "Will write in the worktree: %s and %s/ (the screens and data of the draft).\n", capture.InventoryPath, capture.MocksDir)
		if !interactive() {
			return errors.New("not writing without a terminal: add --yes")
		}
		if !askYN(out, "Write them? [y/N] ", false) {
			return errors.New("cancelled: nothing was written")
		}
	}
	res, id, err := e.CaptureAccept(context.Background(), req)
	if errors.Is(err, capture.ErrInventoryExists) {
		fmt.Fprintf(out, "%s already exists. Not overwritten.\nUse --merge to add the new screens, or --replace.\n", capture.InventoryPath)
		if *yes || !interactive() {
			return errors.New("an inventory already exists: nothing was written")
		}
		if !askYN(out, "Replace it with the generated one? [y/N] ", false) {
			return errors.New("kept the existing inventory: nothing was written")
		}
		req.Mode = "replace"
		res, id, err = e.CaptureAccept(context.Background(), req)
	}
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, res)
	}
	switch res.Mode {
	case "merge":
		fmt.Fprintf(out, "Merged run %s: %d screens added (%s), %d kept as they were.\n", id, len(res.Added), strings.Join(res.Added, ", "), len(res.Skipped))
	case "replace":
		fmt.Fprintf(out, "Replaced %s with run %s (%d screens).\n", capture.InventoryPath, id, res.Screens)
	default:
		fmt.Fprintf(out, "Wrote %s (%d screens) and %d mock files from run %s.\n", capture.InventoryPath, res.Screens, res.Mocks, id)
	}
	fmt.Fprintln(out, "Review with git diff, then validate: theo tool capture validate")
	return nil
}
