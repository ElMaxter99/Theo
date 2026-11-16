package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/tools/capture"
)

func init() {
	registerHelp(56, `
  tool capture validate [path] [--json]        Check .theo/capturas/pantallas.json (the screens, states and flows to capture)
  tool capture init [path]                     Write a starting pantallas.json
  tool capture setup [--yes]                   Install the capture engine (Node, Playwright, Chromium) or report it
  tool capture run <project> [worktree] [--label L] [--url U] [--mode M] [--again] [--yes] [--json]
                                               Capture one deployment: every state at every viewport, twice
                                               to find what is unstable. Saves a set of captures
  tool capture run-pair <project> <before> <after> [--open] [--pr] [--again] [--yes] [--json]
                                               Start both branches, capture them, compare and write the
                                               reports (summary, before/after images, corporate)
  tool capture sets <project> [--json]         The saved sets of captures
  tool capture compare <project> <before> <after> [--open] [--json]
                                               Compare two saved sets and write the reports
  tool capture report <project> <label> [<label>] [--corporate|--summary|--failed] [--json]
                                               The corporate report of a set, or the summary of a comparison
`)
}

// captureSubcommand runs the capture commands of the 0.9. It reports false
// when name is not one of them.
func captureSubcommand(e *engine.Engine, name string, args []string, stdout, stderr io.Writer) (bool, error) {
	var err error
	switch name {
	case "validate":
		err = captureValidate(args, stdout)
	case "init":
		err = captureInit(args, stdout)
	case "setup":
		err = captureSetup(e, args, stdout, stderr)
	case "generate":
		err = captureGenerate(e, args, stdout, stderr)
	case "accept":
		err = captureAccept(e, args, stdout, stderr)
	case "run":
		err = captureRun(e, args, stdout, stderr)
	case "run-pair":
		err = capturePair(e, args, stdout, stderr, false)
	case "compare":
		err = capturePair(e, args, stdout, stderr, true)
	case "sets":
		err = captureSets(e, args, stdout)
	case "report":
		err = captureReport(e, args, stdout)
	default:
		return false, nil
	}
	return true, err
}

func captureValidate(args []string, stdout io.Writer) error {
	fl := flag.NewFlagSet("tool capture validate", flag.ContinueOnError)
	asJSON := fl.Bool("json", false, "print JSON")
	pos, err := parse(fl, args)
	if err != nil {
		return err
	}
	root := "."
	if len(pos) > 0 {
		root = pos[0]
	}
	inv, err := capture.LoadInventory(root)
	if err != nil {
		if errors.Is(err, capture.ErrNoInventory) {
			return missingInventory()
		}
		fmt.Fprintf(stdout, "%s:\n  %v\n", capture.InventoryPath, err)
		return &exitCodeError{code: ExitError}
	}
	errs := inv.Validate()
	if *asJSON {
		if err := writeJSON(stdout, map[string]any{"valid": len(errs) == 0, "errors": errs, "summary": inv.Summarize()}); err != nil {
			return err
		}
		if len(errs) > 0 {
			return &exitCodeError{code: ExitError}
		}
		return nil
	}
	if len(errs) > 0 {
		fmt.Fprintf(stdout, "%s:\n", capture.InventoryPath)
		for _, m := range errs {
			fmt.Fprintf(stdout, "  %s\n", m)
		}
		fmt.Fprintln(stdout, "(exit 1)")
		return &exitCodeError{code: ExitError}
	}
	s := inv.Summarize()
	fmt.Fprintf(stdout, "%s: ok\n", capture.InventoryPath)
	entry := ""
	if s.Entry != "" {
		entry = fmt.Sprintf(", entry %q", s.Entry)
	}
	fmt.Fprintf(stdout, "  %d viewports, %d screens, %d states, %d flows%s\n", s.Viewports, s.Screens, s.States, s.Flows, entry)
	if s.NoOrigin > 0 {
		fmt.Fprintf(stdout, "  note: %d screens have no \"origen\": they cannot be matched by file\n", s.NoOrigin)
	}
	return nil
}

func missingInventory() error {
	return fmt.Errorf("%s not found. Write one by hand (see docs/cli/tools.md)\n      or wait for the AI generation of 0.10. Starting point: theo tool capture init", capture.InventoryPath)
}

const starterInventory = `{
  "version": 1,
  "app": "my-app",
  "entrada": "inicio",
  "viewports": [
    { "id": "escritorio", "width": 1440, "height": 900 },
    { "id": "movil", "width": 390, "height": 844, "mobile": true, "scale": 2 }
  ],
  "mocks": { "modo": "mock", "carpeta": "mocks/" },
  "pantallas": [
    {
      "id": "inicio",
      "titulo": "Home",
      "ruta": "/",
      "estados": [
        { "id": "con-datos", "datos": { "GET /api/items": "mocks/items.json" } }
      ]
    }
  ]
}
`

func captureInit(args []string, stdout io.Writer) error {
	fl := flag.NewFlagSet("tool capture init", flag.ContinueOnError)
	pos, err := parse(fl, args)
	if err != nil {
		return err
	}
	root := "."
	if len(pos) > 0 {
		root = pos[0]
	}
	path := filepath.Join(root, filepath.FromSlash(capture.InventoryPath))
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists: nothing was written", capture.InventoryPath)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(starterInventory), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Written %s. Edit it, then check it: theo tool capture validate\n", capture.InventoryPath)
	return nil
}

func captureSetup(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("tool capture setup", flag.ContinueOnError)
	yes := fl.Bool("yes", false, "install without asking")
	if _, err := parse(fl, args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	eng := e.CaptureEngine()
	if st, err := eng.Check(ctx); err == nil {
		fmt.Fprintf(stdout, "Ready: chromium %s   (theo doctor checks it from now on)\n", st.Chromium)
		return nil
	}
	dir := tildePath(eng.Dir)
	fmt.Fprintf(stdout, "Capture needs Node 20+ and Playwright with Chromium (about 320 MB) in\n%s. Nothing leaves your machine except the download.\n", dir)
	if !*yes {
		if !interactive() {
			return fmt.Errorf("it needs your OK to install them: run `theo tool capture setup --yes`")
		}
		fmt.Fprint(stdout, "Install them? [y/N] ")
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(stdout, "Not installed. When you want it: theo tool capture setup --yes")
			return &exitCodeError{code: ExitError}
		}
	}
	fmt.Fprintln(stderr, "theo: setup: installing playwright and chromium")
	if err := eng.Install(ctx, stderr); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	st, err := eng.Check(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Ready: chromium %s   (theo doctor checks it from now on)\n", st.Chromium)
	return nil
}

// progress prints the events of a capture the way the design shows them.
type progress struct {
	stdout, stderr io.Writer
	detailed       bool // one line per state (run), or one per set (run-pair)
	states         map[string][]capture.Shot
	counts         map[string]*[3]int
	total          map[string]int
	sum            map[string]string
}

func newProgress(stdout, stderr io.Writer, detailed bool) *progress {
	return &progress{stdout: stdout, stderr: stderr, detailed: detailed, states: map[string][]capture.Shot{}, counts: map[string]*[3]int{}, total: map[string]int{}, sum: map[string]string{}}
}

func (p *progress) event(ev engine.CaptureEvent, nVP int) {
	switch ev.Kind {
	case "log":
		fmt.Fprintln(p.stderr, ev.Text)
	case "start":
		p.total[ev.Label] = ev.Total
		p.sum[ev.Label] = ev.Text
		p.counts[ev.Label] = &[3]int{}
		if p.detailed {
			fmt.Fprintf(p.stdout, "theo: capture %s: %s = %d shots, mode %s\n", ev.Label, ev.Text, ev.Total, ev.Mode)
		}
	case "shot":
		sh := *ev.Shot
		c := p.counts[ev.Label]
		switch sh.Status {
		case capture.ShotOK:
			c[0]++
		case capture.ShotUnstable:
			c[1]++
		default:
			c[2]++
			fmt.Fprintf(p.stderr, "theo: %s#%s: %s\n", sh.Screen, sh.State, sh.Reason)
		}
		if !p.detailed {
			return
		}
		key := ev.Label + "|" + sh.Screen + "#" + sh.State
		p.states[key] = append(p.states[key], sh)
		if len(p.states[key]) == nVP {
			line := fmt.Sprintf("  %-24s", sh.Screen+"#"+sh.State)
			for _, s := range p.states[key] {
				line += fmt.Sprintf(" %-8s %-8s", s.Viewport, s.Status)
				if s.Status == capture.ShotUnstable && s.Reason != "" {
					line += " (" + s.Reason + ")"
				}
			}
			fmt.Fprintln(p.stdout, strings.TrimRight(line, " "))
		}
	case "up-to-date":
		if p.detailed {
			fmt.Fprintf(p.stdout, "Set %q is up to date (commit %s): nothing to capture.\nUse --again to capture it anyway.\n", ev.Label, ev.Set.Commit)
		} else {
			fmt.Fprintf(p.stdout, "theo: capture %s: up to date (commit %s)\n", ev.Label, ev.Set.Commit)
		}
	case "saved":
		c := p.counts[ev.Label]
		if p.detailed {
			stable := p.total[ev.Label] - c[1] - c[2]
			fmt.Fprintf(p.stdout, "  control pass ......... %d of %d stable\n", stable, p.total[ev.Label])
			fmt.Fprintf(p.stdout, "Set %q saved: %s\n  %d captured, %d unstable, %d failed   (manifest.json)\n", ev.Label, tildePath(ev.Set.Dir), c[0]+c[1], c[1], c[2])
			if c[2] > 0 {
				fmt.Fprintf(p.stdout, "Set saved with failures. See them: theo tool capture report %s --failed\n", ev.Label)
			}
		} else {
			fmt.Fprintf(p.stdout, "theo: capture %-12s %s, %d shots   %d ok, %d unstable%s\n", ev.Label+":", strings.Replace(p.sum[ev.Label], " x ", " states x ", 1)[:strings.Index(p.sum[ev.Label]+" ", " ")]+" states", p.total[ev.Label], c[0], c[1], failedText(c[2]))
		}
	}
}

func failedText(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", %d failed", n)
}

func captureRun(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("tool capture run", flag.ContinueOnError)
	label := fl.String("label", "", "name of the set (default the branch)")
	url := fl.String("url", "", "capture an application that is already running")
	mode := fl.String("mode", "", "mock, real or grabar (default the inventory's)")
	again := fl.Bool("again", false, "capture even if a set of this commit exists")
	yes := fl.Bool("yes", false, "approve the commands of the run section")
	asJSON := fl.Bool("json", false, "print JSON")
	pos, err := parse(fl, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 {
		return usageErrorf("tool capture run needs a project and, optionally, a worktree")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	target := engine.CaptureTarget{Project: pos[0], URL: *url, Label: *label}
	if len(pos) == 2 {
		target.Selector = pos[1]
	}
	req := engine.CaptureRequest{CaptureTarget: target, Mode: *mode, Again: *again, Approve: *yes}
	if err := approveCapture(ctx, e, []engine.CaptureTarget{target}, &req.Approve, stdout, "theo tool capture run "+strings.Join(pos, " ")); err != nil {
		return err
	}
	out := stdout
	if *asJSON {
		out = stderr
	}
	nVP := 0
	pr := newProgress(out, stderr, true)
	set, err := e.CaptureRun(ctx, req, func(ev engine.CaptureEvent) {
		if ev.Kind == "start" && nVP == 0 {
			nVP = 1
			if inv, ierr := loadInvFor(e, target); ierr == nil {
				nVP = len(inv.Viewports)
			}
		}
		pr.event(ev, max(nVP, 1))
	})
	if err != nil {
		return captureError(err, target.Project)
	}
	if *asJSON {
		return writeJSON(stdout, set.Manifest)
	}
	return nil
}

func loadInvFor(e *engine.Engine, t engine.CaptureTarget) (*capture.Inventory, error) {
	path, err := e.WorkspacePath(context.Background(), t.Project, t.Selector)
	if err != nil {
		return nil, err
	}
	return capture.LoadInventory(path)
}

// approveCapture asks about the commands of the run sections that were not approved.
func approveCapture(ctx context.Context, e *engine.Engine, targets []engine.CaptureTarget, approve *bool, out io.Writer, again string) error {
	if *approve {
		return nil
	}
	for _, t := range targets {
		if t.URL != "" {
			continue
		}
		path, cmds, err := e.CaptureApproval(ctx, t)
		if err != nil {
			return captureError(err, t.Project)
		}
		if len(cmds) == 0 {
			continue
		}
		if !interactive() {
			return fmt.Errorf("%s (run) asks to run %d command(s) that you have not approved:\n  %s\nRead them, then approve with: %s --yes", repoconfigFile, len(cmds), strings.Join(cmds, "\n  "), again)
		}
		if !confirmRun(out, tildePath(path), cmds) {
			fmt.Fprintf(out, "Not run. When you want them: %s --yes\n", again)
			return &exitCodeError{code: ExitError}
		}
	}
	return nil
}

const repoconfigFile = ".theo.json"

func captureError(err error, project string) error {
	if errors.Is(err, capture.ErrNoInventory) {
		return missingInventory()
	}
	if errors.Is(err, capture.ErrEngineMissing) {
		return fmt.Errorf("%w", err)
	}
	return err
}

func capturePair(e *engine.Engine, args []string, stdout, stderr io.Writer, fromSets bool) error {
	name := "run-pair"
	if fromSets {
		name = "compare"
	}
	fl := flag.NewFlagSet("tool capture "+name, flag.ContinueOnError)
	open := fl.Bool("open", false, "open the gallery when done")
	pr := fl.Bool("pr", false, "print where the summary is, ready to paste in a pull request")
	again := fl.Bool("again", false, "capture even if sets of these commits exist")
	yes := fl.Bool("yes", false, "approve the commands of the run sections")
	mode := fl.String("mode", "", "mock, real or grabar")
	lang := fl.String("lang", "", "language of the reports (en or es; default: the language setting)")
	noAI := fl.Bool("no-ai", false, "do not ask an AI to pair the screens the rules could not")
	asJSON := fl.Bool("json", false, "print JSON")
	pos, err := parse(fl, args)
	if err != nil {
		return err
	}
	if len(pos) != 3 {
		return usageErrorf("tool capture %s needs a project and two branches or labels", name)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	project := pos[0]
	ta := engine.CaptureTarget{Project: project, Selector: pos[1], Label: pos[1]}
	tb := engine.CaptureTarget{Project: project, Selector: pos[2], Label: pos[2]}
	req := engine.ComparisonRequest{Project: project, A: ta, B: tb, Mode: *mode, Again: *again, Approve: *yes, UseSets: fromSets, Lang: *lang, NoAI: *noAI}
	if !fromSets {
		if err := approveCapture(ctx, e, []engine.CaptureTarget{ta, tb}, &req.Approve, stdout, "theo tool capture run-pair "+strings.Join(pos, " ")); err != nil {
			return err
		}
	}
	out := stdout
	if *asJSON {
		out = stderr
	}
	prog := newProgress(out, stderr, false)
	res, err := e.CapturePair(ctx, req, func(ev engine.CaptureEvent) {
		switch ev.Kind {
		case "matching":
			fmt.Fprintf(out, "theo: matching screens: %s\n", ev.Text)
		case "ai-pair":
			fmt.Fprintf(out, "  %s\n", ev.Text)
		case "log":
			fmt.Fprintf(out, "theo: %s\n", ev.Text)
		case "compare":
			fmt.Fprintf(out, "theo: compare %s\n", ev.Label)
		case "stopped":
			// printed at the end
		default:
			prog.event(ev, 1)
		}
	})
	if err != nil {
		return captureError(err, project)
	}
	if *asJSON {
		return writeJSON(stdout, map[string]any{"counts": res.Comparison.Counts, "dir": res.Files.Dir, "files": res.Files})
	}
	if w := browserWarning(res.A, res.B); w != "" {
		fmt.Fprintln(stderr, w)
	}
	printCounts(out, res.Comparison)
	fmt.Fprintf(out, "\nReports: %s/\n", tildePath(res.Files.Dir))
	line := fmt.Sprintf("  %-22s %-22s", res.Files.Summary, res.Files.Compositions)
	if res.Files.Corporate != "" {
		line += fmt.Sprintf(" %s (%s)", res.Files.Corporate, res.B.Label)
	}
	fmt.Fprintln(out, line)
	if !fromSets {
		fmt.Fprintln(out, "Stopped both instances.")
	}
	if *pr {
		fmt.Fprintf(out, "%s ready to paste in a pull request (--pr format).\n", res.Files.Summary)
	}
	if *open {
		_ = openBrowser("file://" + filepath.ToSlash(filepath.Join(res.Files.Dir, res.Files.Compositions)))
	}
	return nil
}

func browserWarning(a, b *capture.Set) string {
	if a.Chromium != "" && b.Chromium != "" && a.Chromium != b.Chromium {
		return fmt.Sprintf("theo: warning: chromium %s for %q, %s for %q: differences may come from the browser", a.Chromium, a.Label, b.Chromium, b.Label)
	}
	return ""
}

func printCounts(w io.Writer, c *capture.Comparison) {
	n := c.Counts
	fmt.Fprintf(w, "\n  changed  %-6d same  %-5d new  %-5d removed  %-5d moved  %d\n", n.Changed, n.Same, n.New, n.Removed, n.Moved)
	fmt.Fprintf(w, "  flow changed  %d of %d     not captured  %d     unstable  %d\n", n.FlowChanged, len(c.Flows), n.NotCaptured, n.Unstable)
	if byAI := countPairs(c, capture.ByAI); byAI > 0 {
		fmt.Fprintf(w, "  paired by AI  %d     (saved in %s: edit it and your version wins)\n", byAI, capture.PairsPath)
	}
}

func captureSets(e *engine.Engine, args []string, stdout io.Writer) error {
	fl := flag.NewFlagSet("tool capture sets", flag.ContinueOnError)
	asJSON := fl.Bool("json", false, "print JSON")
	pos, err := parse(fl, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("tool capture sets needs a project")
	}
	sets, err := e.CaptureSets(pos[0])
	if err != nil {
		return err
	}
	if *asJSON {
		out := make([]capture.Manifest, 0, len(sets))
		for _, s := range sets {
			out = append(out, s.Manifest)
		}
		return writeJSON(stdout, out)
	}
	if len(sets) == 0 {
		fmt.Fprintln(stdout, "No sets yet. Capture one: theo tool capture run "+pos[0])
		return nil
	}
	fmt.Fprintf(stdout, "%-14s %-9s %-11s %-5s %-6s %s\n", "LABEL", "COMMIT", "DATE", "MODE", "SHOTS", "UNSTABLE")
	for _, s := range sets {
		_, unstable, _ := s.Counts()
		date := s.Date
		if len(date) > 10 {
			date = date[:10]
		}
		fmt.Fprintf(stdout, "%-14s %-9s %-11s %-5s %-6d %d\n", s.Label, orDash(s.Commit), date, s.Mode, len(s.Shots), unstable)
	}
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func captureReport(e *engine.Engine, args []string, stdout io.Writer) error {
	fl := flag.NewFlagSet("tool capture report", flag.ContinueOnError)
	corp := fl.Bool("corporate", false, "write the corporate report of a set")
	summary := fl.Bool("summary", false, "print the summary of a comparison")
	failed := fl.Bool("failed", false, "list what failed in a set")
	lang := fl.String("lang", "", "language of the report (en or es)")
	asJSON := fl.Bool("json", false, "print JSON")
	pos, err := parse(fl, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 || len(pos) > 3 {
		return usageErrorf("tool capture report needs a project and a label (or two labels for --summary)")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch {
	case *failed:
		root, err := e.CaptureRoot(pos[0])
		if err != nil {
			return err
		}
		set, err := capture.FindSet(root, pos[1])
		if err != nil {
			return err
		}
		n := 0
		for _, sh := range set.Shots {
			if sh.Status != capture.ShotOK {
				fmt.Fprintf(stdout, "%s#%s %s: %s %s\n", sh.Screen, sh.State, sh.Viewport, sh.Status, sh.Reason)
				n++
			}
		}
		if n == 0 {
			fmt.Fprintln(stdout, "Nothing failed or unstable in this set.")
		}
		return nil
	case *summary:
		if len(pos) != 3 {
			return usageErrorf("--summary needs two labels")
		}
		return printCaptureSummary(e, pos[0], pos[1], pos[2], stdout, *asJSON)
	case *corp || len(pos) == 2:
		html, pdf, err := e.CaptureCorporate(ctx, pos[0], pos[1], *lang, true)
		if err != nil {
			return captureError(err, pos[0])
		}
		if *asJSON {
			return writeJSON(stdout, map[string]string{"html": html, "pdf": pdf})
		}
		fmt.Fprintf(stdout, "%s and %s: %s/\n", filepath.Base(html), filepath.Base(pdf), tildePath(filepath.Dir(html)))
		return nil
	}
	return usageErrorf("tool capture report: choose --corporate, --summary or --failed")
}

func printCaptureSummary(e *engine.Engine, project, a, b string, w io.Writer, asJSON bool) error {
	reports, err := e.CaptureReports(project)
	if err != nil {
		return err
	}
	for _, r := range reports {
		if r.A != a || r.B != b {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.Dir, "comparacion.json"))
		if err != nil {
			continue
		}
		var c capture.Comparison
		if err := json.Unmarshal(data, &c); err != nil {
			return err
		}
		if asJSON {
			return writeJSON(w, c.Counts)
		}
		for _, f := range c.Flows {
			if f.Class == "same" {
				continue
			}
			switch {
			case f.Entry:
				fmt.Fprintf(w, "Flow %-14s %s: %s\n", `"entry"`, a, strings.Join(f.A, " > "))
				fmt.Fprintf(w, "%-20s %s: %s   changed\n", "", b, strings.Join(f.B, " > "))
			default:
				for _, op := range f.Ops {
					fmt.Fprintf(w, "Flow %-14s %s %s   changed\n", `"`+f.ID+`"`, map[string]string{"add": "+", "remove": "-"}[op.Kind], op.Screen)
				}
			}
		}
		largest := ""
		if c.Largest != nil {
			largest = fmt.Sprintf(" (largest: %s %.1f%%)", c.Largest.Screen, c.Largest.Percent)
		}
		fmt.Fprintf(w, "Screens: %d changed%s, %d new, %d removed, %d moved\n", c.Counts.Changed, largest, c.Counts.New, c.Counts.Removed, c.Counts.Moved)
		if len(c.NotCaptured) > 0 {
			fmt.Fprintf(w, "Not captured (%d):\n", len(c.NotCaptured))
			sort.SliceStable(c.NotCaptured, func(i, j int) bool { return c.NotCaptured[i].Kind < c.NotCaptured[j].Kind })
			for _, nc := range c.NotCaptured {
				reason := nc.Reason
				if nc.Kind == "no_screen" {
					reason = "changed in the branch, belongs to no screen"
				}
				fmt.Fprintf(w, "  %-22s %s\n", nc.Item, reason)
			}
		}
		return nil
	}
	return fmt.Errorf("no report compares %q and %q yet: theo tool capture compare %s %s %s", a, b, project, a, b)
}

func countPairs(c *capture.Comparison, method string) int {
	n := 0
	for _, p := range c.Pairs {
		if p.Method == method {
			n++
		}
	}
	return n
}
