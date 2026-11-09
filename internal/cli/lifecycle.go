package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/ElMaxter99/Theo/internal/agents"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ElMaxter99/Theo/internal/engine"
)

func init() {
	registerHelp(41, `
Forgotten work (see docs/cli/worktrees.md):
  worktrees <project> [--state <state>] [--parked] [--size] [--owner <name>] [--json]
                                               STATE is missing, in-use, kept, merged, inactive,
                                               unpushed, new or pushed; ACTIVITY says when and
                                               from what (commit, files, agent, instance, created)
  worktree clean <project> | --all [--down] [--delete-branches] [--images] [--confirm <project>|all] [--json]
                                               List the merged worktrees (also by squash or
                                               rebase) and the ones whose folder is gone; with
                                               --confirm, close them. --down takes running
                                               instances down first; --delete-branches deletes
                                               their local branch after checking again that it is
                                               merged; --images removes the images Compose built.
                                               Remote branches and volumes are never touched
  worktree park <project> <w> [--note <text>] [--confirm <dir>]
                                               Put away unfinished work: take the instance down,
                                               remove the folder, keep the branch. Refuses
                                               uncommitted changes; --confirm when ignored files
                                               (.env, node_modules...) would be lost
  worktree park-inactive <project> [--only <w>]... [--save-changes] [--include-ignored] [--rescue-detached] [--delete-branches] [--max-changes <n>] [--confirm <project>] [--json]
                                               Park every inactive worktree at once (or only the --only ones). Without
                                               --confirm it only lists what would be parked and
                                               what stays, with why. --save-changes keeps the
                                               uncommitted files in a local work-in-progress
                                               commit on the branch (never pushed); --include-ignored
                                               also parks the ones with ignored files that cannot
                                               be rebuilt (.env, certs...), which are deleted;
                                               node_modules, logs, dist... never stop it.
                                               --rescue-detached keeps a detached HEAD in a
                                               theo-rescue/<folder> branch before parking it. --delete-branches also deletes the
                                               local branch, only when all its commits are on its
                                               remote. Volumes stay; "worktree resume" brings
                                               parked ones back
  worktree resume <project> <branch> [--copy] Bring a parked worktree back
  worktree keep <project> <w> [--until <date>] [--note <text>] | --clear
                                               Never offer it for cleaning, and no warnings
  worktree base <project> <w> [--set <branch> | --clear]
                                               Show or record the branch it is compared with
                                               (branch.<name>.gh-merge-base)
  project set <name> --inactive-after <days>   Days without activity before a worktree is
                                               inactive (1-90, default 7)
`)
}

// stateOrder sorts the rows of `theo worktrees` the way the interface groups them.
var stateOrder = map[string]int{"": 0, engine.LifeInUse: 1, engine.LifeUnpushed: 2, engine.LifePushed: 3, engine.LifeNew: 4,
	engine.LifeMerged: 5, engine.LifeInactive: 6, engine.LifeMissing: 7, engine.LifeKept: 8}

func runWorktrees(e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("worktrees", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	owner := fs.String("owner", "", "only the workspaces owned by this name")
	state := fs.String("state", "", "only the workspaces in this state")
	parked := fs.Bool("parked", false, "list the parked worktrees")
	size := fs.Bool("size", false, "also measure the size of each folder (slow on big worktrees)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("worktrees needs exactly one project name")
	}
	if *state != "" && !validState(*state) {
		return usageErrorf("--state must be one of %s", strings.Join(engine.LifecycleStates, ", "))
	}
	report, err := e.WorktreeStates(context.Background(), pos[0], engine.StatesOptions{Size: *size})
	if err != nil {
		return err
	}
	if *parked {
		if *asJSON {
			return writeJSON(stdout, report.Parked)
		}
		return printParked(stdout, report)
	}
	list := []engine.WorktreeState{}
	for _, wt := range report.Worktrees {
		if *owner != "" && wt.Owner != *owner || *state != "" && wt.State != *state {
			continue
		}
		list = append(list, wt)
	}
	if *asJSON {
		return writeJSON(stdout, list)
	}
	now := time.Now()
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	head := "BRANCH\tKIND\tOWNER\tSTATE\tAGENT\tACTIVITY\tCHANGES\tPATH"
	if *size {
		head = "BRANCH\tKIND\tOWNER\tSTATE\tAGENT\tACTIVITY\tCHANGES\tSIZE\tPATH"
	}
	fmt.Fprintln(tw, head)
	for _, wt := range list {
		who := wt.Owner
		if who == "" {
			who = "-"
		}
		st := wt.State
		if st == "" {
			st = "-"
		}
		row := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s", worktreeRef(wt.Worktree), wt.Kind, who, st, agentText(wt.Agent), activityText(wt, now), changesText(wt))
		if *size {
			row += "\t" + sizeText(wt.SizeBytes)
		}
		fmt.Fprintf(tw, "%s\t%s\n", row, wt.Path)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if *state != "" || *owner != "" {
		return nil
	}
	var foot []string
	if n := len(report.Parked); n > 0 {
		foot = append(foot, fmt.Sprintf("%d parked: theo worktrees %s --parked", n, report.Project))
	}
	if n := report.Count(engine.LifeMerged) + report.Count(engine.LifeMissing); n > 0 {
		foot = append(foot, fmt.Sprintf("→ %d merged or gone can be closed: theo worktree clean %s", n, report.Project))
	}
	if n := report.Count(engine.LifeInactive); n > 0 {
		foot = append(foot, fmt.Sprintf("→ %d inactive for more than %d days: park or keep %s", n, report.InactiveAfterDays, plural(n, "it", "them")))
	}
	for _, n := range report.Notes {
		foot = append(foot, "Note: "+n)
	}
	if len(foot) > 0 {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, strings.Join(foot, "\n"))
	}
	return nil
}

func validState(s string) bool {
	for _, v := range engine.LifecycleStates {
		if v == s {
			return true
		}
	}
	return false
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func changesText(wt engine.WorktreeState) string {
	switch {
	case wt.Bare:
		return "-"
	case wt.Prunable:
		return "missing"
	case wt.State == "":
		if wt.Dirty {
			return "uncommitted"
		}
		return "clean"
	case wt.Changes > 0:
		return fmt.Sprint(wt.Changes)
	default:
		return "clean"
	}
}

// activityText is the ACTIVITY column: "commit 3d ago", "created today",
// "until 2026-12-01" for a kept worktree.
func activityText(wt engine.WorktreeState, now time.Time) string {
	if wt.State == engine.LifeKept {
		if wt.KeptUntil.IsZero() {
			return "kept"
		}
		return "until " + wt.KeptUntil.Local().Format("2006-01-02")
	}
	if wt.ActivityAt.IsZero() {
		return "-"
	}
	src := wt.ActivitySource
	if src == engine.ActivityMCP {
		src = "agent"
	}
	if src == engine.ActivityCreated && sameDay(wt.ActivityAt, now) {
		return "created today"
	}
	return src + " " + agoShort(now.Sub(wt.ActivityAt)) + " ago"
}

func sameDay(a, b time.Time) bool {
	a, b = a.Local(), b.Local()
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

func agoShort(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}

func sizeText(n int64) string {
	switch {
	case n <= 0:
		return "-"
	case n < 1<<20:
		return fmt.Sprintf("%d KB", (n+1023)/1024)
	case n < 1<<30:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
}

func printParked(w io.Writer, r *engine.StatesReport) error {
	if len(r.Parked) == 0 {
		fmt.Fprintf(w, "No parked worktrees in %s.\n", r.Project)
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "BRANCH\tPARKED\tOWNER\tNOTE")
	for _, p := range r.Parked {
		who, note := p.Owner, p.Note
		if who == "" {
			who = "-"
		}
		if p.BranchGone {
			note = "(branch deleted; cannot be resumed) " + note
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.Branch, p.ParkedAt.Local().Format("2006-01-02"), who, note)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(w, "\nResume one with: theo worktree resume %s <branch>\n", r.Project)
	return nil
}

func runWorktreeClean(ctx context.Context, e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("worktree clean", flag.ContinueOnError)
	all := fs.Bool("all", false, "every registered project")
	down := fs.Bool("down", false, "take running instances of merged worktrees down first (volumes are kept)")
	delBranches := fs.Bool("delete-branches", false, "delete the local branch of each closed worktree, after checking again that it is merged")
	images := fs.Bool("images", false, "remove the images Compose built for each closed instance")
	confirm := fs.String("confirm", "", "the project name (or all, with --all), to close them")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	var names []string
	switch {
	case *all && len(pos) == 0:
		projects, err := e.Projects()
		if err != nil {
			return err
		}
		for _, p := range projects {
			names = append(names, p.Name)
		}
		if *confirm != "" && *confirm != "all" {
			return fmt.Errorf("with --all, --confirm must be \"all\"; nothing was closed")
		}
	case !*all && len(pos) == 1:
		p, err := e.Project(pos[0])
		if err != nil {
			return err
		}
		if *confirm != "" && *confirm != p.Name {
			return fmt.Errorf("--confirm must be the project name, %q; nothing was closed", p.Name)
		}
		names = []string{p.Name}
	default:
		return usageErrorf("worktree clean needs a project name, or --all")
	}
	apply := *confirm != ""
	var reports []*engine.CleanReport
	for _, name := range names {
		r, err := e.Clean(ctx, name, engine.CleanOptions{Down: *down, DeleteBranches: *delBranches, Images: *images, Apply: apply})
		if err != nil {
			if *all {
				fmt.Fprintf(stdout, "%s: %s\n", name, firstLine(err.Error()))
				continue
			}
			return err
		}
		reports = append(reports, r)
	}
	if *asJSON {
		if !*all && len(reports) == 1 {
			return writeJSON(stdout, reports[0])
		}
		if reports == nil {
			reports = []*engine.CleanReport{}
		}
		return writeJSON(stdout, reports)
	}
	closable, closed := 0, 0
	for i, r := range reports {
		if *all {
			if i > 0 {
				fmt.Fprintln(stdout)
			}
			fmt.Fprintln(stdout, r.Project)
		}
		printClean(stdout, r)
		closable += len(r.Closable())
		for _, c := range r.Close {
			if c.Closed {
				closed++
			}
		}
	}
	target, conf := names, ""
	if len(names) == 1 {
		conf = names[0]
	} else {
		conf = "all"
	}
	fmt.Fprintln(stdout)
	switch {
	case apply && closed > 0:
		vol := "theo volumes <project>"
		if len(target) == 1 {
			vol = "theo volumes " + target[0]
		}
		fmt.Fprintf(stdout, "Volumes were kept: %s lists them.\n", vol)
	case apply:
		fmt.Fprintln(stdout, "Nothing was closed.")
	case closable > 0:
		cmd := "theo worktree clean " + conf
		if *all {
			cmd = "theo worktree clean --all"
		}
		for _, f := range []struct {
			on   bool
			name string
		}{{*down, "--down"}, {*delBranches, "--delete-branches"}, {*images, "--images"}} {
			if f.on {
				cmd += " " + f.name
			}
		}
		fmt.Fprintf(stdout, "Nothing was closed. To close the %d marked \"can be closed\":\n  %s --confirm %s\n", closable, cmd, conf)
		fmt.Fprintln(stdout, "(--down stops instances first · --delete-branches · --images)")
	default:
		fmt.Fprintln(stdout, "Nothing can be closed now.")
	}
	return nil
}

func printClean(w io.Writer, r *engine.CleanReport) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	rows := 0
	if !r.Applied {
		fmt.Fprintln(tw, "BRANCH\tRESULT")
	}
	for _, c := range r.Close {
		rows++
		fmt.Fprintf(tw, "%s\t%s\n", cleanRef(c), cleanResult(c, r.Applied))
	}
	for _, c := range r.Kept {
		rows++
		why := "kept with theo worktree keep"
		if c.Note != "" {
			why += ": " + c.Note
		}
		fmt.Fprintf(tw, "%s\tkept: %s\n", cleanRef(c), why)
	}
	if rows == 0 {
		fmt.Fprintf(w, "No merged worktree in %s, and none whose folder is gone.\n", r.Project)
	} else {
		tw.Flush()
	}
	if n := len(r.Inactive); n > 0 {
		refs := make([]string, n)
		for i, c := range r.Inactive {
			refs[i] = cleanRef(c)
		}
		fmt.Fprintf(w, "%d inactive for more than %d days, not closed: %s. Park or keep %s: theo worktrees %s --state inactive\n",
			n, r.InactiveAfterDays, strings.Join(refs, ", "), plural(n, "it", "them"), r.Project)
	}
	if len(r.Leftovers) > 0 {
		fmt.Fprintln(w, "Leftovers of worktrees that no longer exist:")
		for _, f := range r.Leftovers {
			line := "  " + f.Kind + ": " + f.Subject
			if f.Fix != "" && !f.Forgettable {
				line += " → " + f.Fix
			}
			fmt.Fprintln(w, line)
		}
		if r.Forgot > 0 {
			fmt.Fprintf(w, "  Theo forgot %d of its own records of them.\n", r.Forgot)
		}
	}
	for _, n := range r.Notes {
		fmt.Fprintln(w, "Note: "+n)
	}
}

func cleanRef(c engine.CleanEntry) string {
	if c.Branch != "" {
		return c.Branch
	}
	return c.Path
}

func cleanResult(c engine.CleanEntry, applied bool) string {
	if c.Reason != "" {
		return "kept: " + c.Reason
	}
	if !applied {
		switch {
		case c.State == engine.LifeMissing:
			return "can be closed: its folder is gone"
		case c.MergedBy == engine.MergedContent:
			return "can be closed: merged by squash or rebase (content already in the base)"
		default:
			return "can be closed: merged into its base"
		}
	}
	parts := []string{"closed"}
	if c.State == engine.LifeMissing {
		parts = []string{"forgotten (its folder was gone)"}
	}
	if c.WentDown {
		parts = append(parts, "instance taken down")
	}
	if c.BranchDeleted {
		parts = append(parts, "branch deleted")
	}
	if len(c.ImagesRemoved) > 0 {
		parts = append(parts, fmt.Sprintf("%d image(s) removed", len(c.ImagesRemoved)))
	}
	out := strings.Join(parts, " · ")
	for _, p := range c.Problems {
		out += " · " + p
	}
	return out
}

func runWorktreePark(ctx context.Context, e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("worktree park", flag.ContinueOnError)
	note := fs.String("note", "", "why it is parked, shown when listing and resuming")
	confirm := fs.String("confirm", "", "the folder name, to delete files Git ignores (.env, node_modules...)")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return usageErrorf("worktree park needs a project name and a worktree")
	}
	res, err := e.Park(ctx, pos[0], pos[1], engine.ParkOptions{Note: *note, Confirm: *confirm})
	var pe *engine.ParkError
	if errors.As(err, &pe) {
		return fmt.Errorf("these ignored files are lost with the folder:\n  %s\nTo park it anyway: --confirm %s. Nothing changed", strings.Join(pe.Ignored, "  "), pe.Name)
	}
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, res)
	}
	if res.WentDown != "" {
		fmt.Fprintf(stdout, "Took down %s (volumes kept)\n", res.WentDown)
	}
	pushed := "everything is pushed"
	if res.Unpushed > 0 {
		pushed = fmt.Sprintf("%d commit(s) not pushed", res.Unpushed)
	}
	fmt.Fprintf(stdout, "Parked %s: the branch stays; %s\n  resume: theo worktree resume %s %s\n", res.Branch, pushed, pos[0], res.Branch)
	return nil
}

func runWorktreeResume(ctx context.Context, e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("worktree resume", flag.ContinueOnError)
	copyFiles := fs.Bool("copy", false, "copy the files listed with `project set --copy-on-add` from the main worktree")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return usageErrorf("worktree resume needs a project name and a parked branch")
	}
	p, err := e.Project(pos[0])
	if err != nil {
		return err
	}
	var note string
	for _, pk := range p.Parked {
		if pk.Branch == pos[1] {
			note = pk.Note
		}
	}
	added, err := e.Resume(ctx, p.Name, pos[1], engine.ResumeOptions{Copy: *copyFiles})
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, added)
	}
	fmt.Fprintf(stdout, "Created worktree for %s at %s\n", added.Branch, added.Path)
	var restored []string
	if added.Owner != "" {
		restored = append(restored, "owner "+added.Owner)
	}
	if note != "" {
		restored = append(restored, fmt.Sprintf("note %q", note))
	}
	if len(restored) > 0 {
		fmt.Fprintf(stdout, "Restored: %s\n", strings.Join(restored, " · "))
	}
	if len(added.Copied) > 0 {
		fmt.Fprintf(stdout, "Copied from the main worktree: %s\n", strings.Join(added.Copied, ", "))
	}
	for _, n := range added.Notes {
		fmt.Fprintf(stdout, "Note: %s\n", n)
	}
	fmt.Fprintf(stdout, "  start it: theo up %s -w %s\n", p.Name, added.Branch)
	return nil
}

func runWorktreeKeep(ctx context.Context, e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("worktree keep", flag.ContinueOnError)
	until := fs.String("until", "", "date (YYYY-MM-DD) when the mark expires; default: until cleared")
	note := fs.String("note", "", "why it is kept")
	clear := fs.Bool("clear", false, "remove the mark")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return usageErrorf("worktree keep needs a project name and a worktree")
	}
	if *clear && (*until != "" || *note != "") {
		return usageErrorf("--clear takes no --until or --note")
	}
	var at time.Time
	if *until != "" {
		d, err := time.ParseInLocation("2006-01-02", *until, time.Local)
		if err != nil {
			return usageErrorf("--until must be a date like 2026-12-01")
		}
		at = d.Add(24*time.Hour - time.Second) // the whole day
	}
	rec, err := e.Keep(ctx, pos[0], pos[1], engine.KeepOptions{Until: at, Note: *note, Clear: *clear})
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, rec)
	}
	switch {
	case *clear:
		fmt.Fprintf(stdout, "%s is no longer kept.\n", pos[1])
	case !rec.KeptUntil.IsZero():
		fmt.Fprintf(stdout, "%s is kept until %s: no warnings, clean leaves it.\n", pos[1], rec.KeptUntil.Local().Format("2006-01-02"))
	default:
		fmt.Fprintf(stdout, "%s is kept: no warnings, clean leaves it. Undo with --clear.\n", pos[1])
	}
	return nil
}

func runWorktreeBase(ctx context.Context, e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("worktree base", flag.ContinueOnError)
	set := fs.String("set", "", "record this branch as the base")
	clear := fs.Bool("clear", false, "forget the recorded base (the main worktree's branch is used)")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return usageErrorf("worktree base needs a project name and a worktree")
	}
	if *set != "" && *clear {
		return usageErrorf("--set and --clear exclude each other")
	}
	if *set != "" || *clear {
		if _, err := e.SetWorktreeBase(ctx, pos[0], pos[1], *set); err != nil {
			return err
		}
	}
	branch, base, recorded, err := e.WorktreeBase(ctx, pos[0], pos[1])
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, struct {
			Branch   string `json:"branch"`
			Base     string `json:"base"`
			Recorded bool   `json:"recorded"`
		}{branch, base, recorded})
	}
	if recorded {
		fmt.Fprintf(stdout, "%s  (recorded in branch.%s.gh-merge-base; gh pr create uses it)\n", base, branch)
	} else {
		fmt.Fprintf(stdout, "%s  (the main worktree's branch; nothing recorded. Record one with --set <branch>)\n", base)
	}
	return nil
}

// forgottenLines is the "Forgotten work" part of `theo`: per project, how
// many worktrees are merged, inactive and with idle instances.
func forgottenLines(projects []engine.ProjectStatus) [][2]string {
	var out [][2]string
	for _, p := range projects {
		merged, inactive, idle := 0, 0, 0
		for _, wt := range p.Worktrees {
			if wt.Lifecycle != nil {
				switch wt.Lifecycle.State {
				case engine.LifeMerged, engine.LifeMissing:
					merged++
				case engine.LifeInactive:
					inactive++
				}
			}
			for _, w := range wt.Warnings {
				if w.Kind == "instance-idle" {
					idle++
				}
			}
		}
		if merged+inactive+idle == 0 {
			continue
		}
		var parts []string
		if merged > 0 {
			parts = append(parts, fmt.Sprintf("%d merged", merged))
		}
		if inactive > 0 {
			parts = append(parts, fmt.Sprintf("%d inactive", inactive))
		}
		if idle > 0 {
			parts = append(parts, fmt.Sprintf("%d idle %s", idle, plural(idle, "instance", "instances")))
		}
		fix := "theo worktrees " + p.Name + " --state inactive"
		if merged > 0 {
			fix = "theo worktree clean " + p.Name
		}
		out = append(out, [2]string{p.Name, strings.Join(parts, " · ") + "\x00" + fix})
	}
	return out
}

func printForgotten(w io.Writer, projects []engine.ProjectStatus, color bool) {
	lines := forgottenLines(projects)
	if len(lines) == 0 {
		return
	}
	width := 0
	for _, l := range lines {
		if n := len([]rune(l[0])); n > width {
			width = n
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, paint(color, ansiBold, "Forgotten work"))
	for _, l := range lines {
		what, fix, _ := strings.Cut(l[1], "\x00")
		fmt.Fprintf(w, "  %-*s  %s\n  %-*s  %s %s\n", width, l[0], what, width, "", paint(color, ansiCyan, "→"), fix)
	}
}

// agentText is the AGENT column: what the agent of a worktree does.
func agentText(a *agents.State) string {
	switch {
	case a == nil:
		return "-"
	case a.Source == agents.SourceProcess:
		return a.State + " (estimated)"
	}
	return a.State
}

func runWorktreeParkInactive(ctx context.Context, e *engine.Engine, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("worktree park-inactive", flag.ContinueOnError)
	save := fs.Bool("save-changes", false, "keep uncommitted files in a local work-in-progress commit before parking")
	ignored := fs.Bool("include-ignored", false, "also park worktrees with ignored files (.env, node_modules...), which are deleted")
	maxChanges := fs.Int("max-changes", engine.DefaultParkMaxChanges, "most uncommitted files to save per worktree")
	delBranches := fs.Bool("delete-branches", false, "also delete the local branch of each parked worktree, only when all its commits are on its remote")
	rescue := fs.Bool("rescue-detached", false, "park a worktree with a detached HEAD after creating a theo-rescue/<folder> branch at its commit")
	var only stringList
	fs.Var(&only, "only", "park only this worktree (branch or path); can be repeated")
	confirm := fs.String("confirm", "", "the project name, to park them")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("worktree park-inactive needs a project name")
	}
	p, err := e.Project(pos[0])
	if err != nil {
		return err
	}
	if *confirm != "" && *confirm != p.Name {
		return fmt.Errorf("--confirm must be the project name, %q; nothing was parked", p.Name)
	}
	apply := *confirm != ""
	r, err := e.ParkInactive(ctx, p.Name, engine.ParkInactiveOptions{SaveChanges: *save, IncludeIgnored: *ignored, MaxChanges: *maxChanges, DeleteBranches: *delBranches, RescueDetached: *rescue, Only: only, Apply: apply})
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSON(stdout, r)
	}
	if len(r.Entries) == 0 {
		fmt.Fprintf(stdout, "No inactive worktree in %s.\n", r.Project)
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "BRANCH\tIDLE\tRESULT")
	for _, c := range r.Entries {
		ref := c.Branch
		if ref == "" {
			ref = c.Path
		}
		var res string
		switch {
		case c.Parked:
			res = "parked"
			if c.Saved {
				res += fmt.Sprintf(" (%d file(s) saved in a WIP commit)", c.Changes)
			}
			if c.BranchDeleted {
				res += ", local branch deleted (its remote copy stays)"
			}
			for _, pr := range c.Problems {
				res += "; " + pr
			}
		case c.Reason != "":
			res = "stays: " + c.Reason
		case c.WillSave:
			res = fmt.Sprintf("can be parked (%d file(s) go into a WIP commit)", c.Changes)
		default:
			res = "can be parked"
			if *delBranches && c.BranchDeletable {
				res += ", its local branch would be deleted (all on the remote)"
			}
		}
		fmt.Fprintf(tw, "%s\t%dd\t%s\n", ref, c.IdleDays, res)
	}
	tw.Flush()
	for _, n := range r.Notes {
		fmt.Fprintln(stdout, "Note: "+n)
	}
	sm := r.Summary
	fmt.Fprintf(stdout, "\n%d inactive: %d can be parked, %d with nothing to lose (all on the remote), %d with uncommitted files, %d with unpushed commits, %d without remote branch, %d that cannot be parked.\n",
		sm.Total, sm.Parkable, sm.Safe, sm.WithChanges, sm.WithUnpushed, sm.UpstreamGone, sm.Blocked)
	fmt.Fprintln(stdout)
	switch {
	case apply:
		fmt.Fprintf(stdout, "Parked %d of %d. Volumes were kept. Branches too, unless deleted with --delete-branches; bring a parked one back: theo worktree resume %s <branch>\n", r.ParkedCount(), len(r.Entries), p.Name)
	case r.Parkable() > 0:
		cmd := "theo worktree park-inactive " + p.Name
		if *save {
			cmd += " --save-changes"
		}
		if *ignored {
			cmd += " --include-ignored"
		}
		if *delBranches {
			cmd += " --delete-branches"
		}
		if *rescue {
			cmd += " --rescue-detached"
		}
		for _, o := range only {
			cmd += " --only " + o
		}
		fmt.Fprintf(stdout, "Nothing was parked. To park the %d marked \"can be parked\":\n  %s --confirm %s\n", r.Parkable(), cmd, p.Name)
	default:
		fmt.Fprintln(stdout, "Nothing can be parked now; see the reasons above (--save-changes, --include-ignored).")
	}
	return nil
}
