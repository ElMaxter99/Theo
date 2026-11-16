package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/repoconfig"
	"github.com/ElMaxter99/Theo/internal/tools"
	"github.com/ElMaxter99/Theo/internal/tools/capture"
)

func init() {
	registerCommand("tool", runTool)
	registerHelp(55, `
  tool list [--json]                           Theo's tools (self-contained features with their own scope)
  tool capture serve <project> [worktree] [--yes] [--check] [--json]
                                               Start the application a worktree's run section describes
                                               (kind script or static): install, build/start, wait until it answers, print
                                               its URL and keep it until Ctrl-C. Its commands run code from
                                               the repository, so Theo asks the first time and whenever they
                                               change (--yes approves). --check starts it, reports and stops
  tool capture detect [path] [--write] [--json]
                                               Work out how a project starts and propose the "run" section
                                               of .theo.json (Compose, a package.json script or a Makefile
                                               target). --write adds it to .theo.json; it never replaces a
                                               run section that is already there
`)
	registerCompletion(cmdSpec{name: "tool", subs: []cmdSpec{
		{name: "list"},
		{name: "ai", subs: []cmdSpec{{name: "doctor", flags: []flagSpec{{names: []string{"--no-ping"}}, {names: []string{"--json"}}}}, {name: "models", flags: []flagSpec{{names: []string{"--json"}}}}}},
		{name: "capture", subs: []cmdSpec{{name: "detect", flags: []flagSpec{{names: []string{"--write"}}, {names: []string{"--json"}}}}, {name: "serve", flags: []flagSpec{{names: []string{"--yes"}}, {names: []string{"--check"}}, {names: []string{"--json"}}}}, {name: "validate"}, {name: "init"}, {name: "setup"}, {name: "generate", flags: []flagSpec{{names: []string{"--yes"}}, {names: []string{"--resume"}}, {names: []string{"--json"}}}}, {name: "accept", flags: []flagSpec{{names: []string{"--yes"}}, {names: []string{"--replace"}}, {names: []string{"--merge"}}, {names: []string{"--json"}}}}, {name: "run"}, {name: "run-pair"}, {name: "sets"}, {name: "compare"}, {name: "report"}}},
	}})
}

func runTool(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageErrorf("tool needs a subcommand: list or <tool> <command> (see `theo tool list`)")
	}
	if args[0] == "list" {
		fl := flag.NewFlagSet("tool list", flag.ContinueOnError)
		asJSON := fl.Bool("json", false, "print JSON")
		if _, err := parse(fl, args[1:]); err != nil {
			return err
		}
		list := tools.List()
		if *asJSON {
			return writeJSON(stdout, list)
		}
		for _, t := range list {
			fmt.Fprintf(stdout, "%-10s %s\n", t.ID, t.Title)
			fmt.Fprintf(stdout, "%-10s %s\n", "", t.Summary)
			if len(t.Commands) > 0 {
				fmt.Fprintf(stdout, "%-10s works today: %s\n", "", strings.Join(t.Commands, ", "))
			}
			if len(t.Planned) > 0 {
				fmt.Fprintf(stdout, "%-10s planned: %s\n", "", strings.Join(t.Planned, ", "))
			}
		}
		return nil
	}
	if _, ok := tools.Get(args[0]); !ok {
		return usageErrorf("unknown tool %q (see `theo tool list`)", args[0])
	}
	if args[0] == "ai" {
		return runAI(e, args[1:], stdout, stderr)
	}
	if args[0] == capture.ID {
		return runCapture(e, args[1:], stdout, stderr)
	}
	return usageErrorf("tool %q has no commands yet", args[0])
}

func runCapture(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "serve" {
		return runCaptureServe(e, args[1:], stdout, stderr)
	}
	if len(args) > 0 {
		if handled, err := captureSubcommand(e, args[0], args[1:], stdout, stderr); handled {
			return err
		}
	}
	if len(args) == 0 || args[0] != "detect" {
		return usageErrorf("tool capture works with: detect, serve, validate, init, setup, generate, accept, run, run-pair, sets, compare, report")
	}
	fl := flag.NewFlagSet("tool capture detect", flag.ContinueOnError)
	asJSON := fl.Bool("json", false, "print JSON")
	write := fl.Bool("write", false, "add the proposal to .theo.json")
	pos, err := parse(fl, args[1:])
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return usageErrorf("tool capture detect takes one folder")
	}
	root := "."
	if len(pos) == 1 {
		root = pos[0]
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	d, err := capture.Detect(root)
	if err != nil {
		return err
	}
	wrote := false
	if *write {
		if d.Run == nil {
			return errors.New("nothing to write: no way to start this project was found")
		}
		if wrote, err = writeRunSection(root, d); err != nil {
			return err
		}
	}
	if *asJSON {
		return writeJSON(stdout, map[string]any{"detection": d, "written": wrote})
	}
	if d.Run == nil {
		fmt.Fprintln(stdout, "No way to start this project was found.")
	} else {
		fmt.Fprintf(stdout, "Proposed run section (%s confidence):\n", d.Confidence)
		fmt.Fprintln(stdout, compactJSON(struct {
			Version int                 `json:"version"`
			Run     *repoconfig.RunSpec `json:"run"`
		}{1, d.Run}))
	}
	for _, e := range d.Evidence {
		fmt.Fprintf(stdout, "  because: %s\n", e)
	}
	for _, n := range d.Notes {
		fmt.Fprintf(stdout, "  note: %s\n", strings.ReplaceAll(n, "\n", "\n        "))
	}
	name := registeredName(e, root)
	switch {
	case wrote && d.Run.EffectiveKind() == repoconfig.RunCompose:
		fmt.Fprintf(stdout, "Written to %s. Compose projects start with `theo up %s`.\n", repoconfig.FileName, name)
	case wrote:
		fmt.Fprintf(stdout, "Written to %s. Its commands run code from the repository: Theo asks before running them. Try it: theo tool capture serve %s --check\n", repoconfig.FileName, name)
	case *write && d.Run != nil:
		fmt.Fprintf(stdout, "Nothing was written: %s already has a run section.\n", repoconfig.FileName)
	case d.Run != nil:
		fmt.Fprintln(stdout, "Nothing was written. Add --write to put it in .theo.json.")
	}
	return nil
}

// registeredName is the name of the registered project whose folder is root,
// or a placeholder when the folder is not registered.
func registeredName(e *engine.Engine, root string) string {
	if e == nil {
		return "<project>"
	}
	list, err := e.Projects()
	if err != nil {
		return "<project>"
	}
	clean := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return filepath.Clean(p)
	}
	for _, p := range list {
		if clean(p.Path) == clean(root) {
			return p.Name
		}
	}
	return "<project>"
}

var (
	oneStringArray  = regexp.MustCompile(`\[\s+("[^"\n]*"(?:,\s+"[^"\n]*")*)\s+\]`)
	oneFieldObject  = regexp.MustCompile(`\{\s+("[^"\n]*": "[^"\n]*")\s+\}`)
	commaNewlineGap = regexp.MustCompile(`",\s+"`)
)

// compactJSON indents v and keeps short string lists and one-field objects on
// one line, the way the design shows .theo.json.
func compactJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return compactIndented(b)
}

// compactIndented collapses short string lists and one-field objects of
// indented JSON, keeping the order of its keys.
func compactIndented(b []byte) string {
	out := oneStringArray.ReplaceAllStringFunc(string(b), func(m string) string {
		inner := oneStringArray.FindStringSubmatch(m)[1]
		return "[" + commaNewlineGap.ReplaceAllString(inner, `", "`) + "]"
	})
	out = oneFieldObject.ReplaceAllString(out, "{ $1 }")
	return out
}

// writeRunSection adds the proposal to .theo.json, keeping what the file
// already says. It returns false when the file already has a run section.
func writeRunSection(root string, d capture.Detection) (bool, error) {
	path := filepath.Join(root, repoconfig.FileName)
	doc := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &doc); err != nil {
			return false, fmt.Errorf("%s is not valid: %w", repoconfig.FileName, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return false, err
	}
	if _, ok := doc["run"]; ok {
		return false, nil
	}
	run, err := json.Marshal(d.Run)
	if err != nil {
		return false, err
	}
	doc["run"] = run
	out, err := orderedConfig(doc)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(path, out, 0o644)
}

func runCaptureServe(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("tool capture serve", flag.ContinueOnError)
	yes := fl.Bool("yes", false, "approve the commands without asking, and remember it")
	check := fl.Bool("check", false, "start the application, report, and stop")
	asJSON := fl.Bool("json", false, "print JSON")
	pos, err := parse(fl, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 {
		return usageErrorf("tool capture serve needs a project and, optionally, a worktree")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	selector := ""
	if len(pos) == 2 {
		selector = pos[1]
	}
	path, err := e.WorkspacePath(ctx, pos[0], selector)
	if err != nil {
		return err
	}
	plan, err := e.PlanRun(pos[0], path)
	if err != nil {
		return err
	}
	if plan.Spec == nil {
		return fmt.Errorf("%s in %s has no run section: try\n`theo tool capture detect %s --write`", repoconfig.FileName, tildePath(path), tildePath(path))
	}
	out := stdout
	if *asJSON {
		out = stderr
	}
	again := "theo tool capture serve " + pos[0]
	if selector != "" {
		again += " " + selector
	}
	approve := *yes
	if !plan.Trusted && !approve && len(plan.Commands) > 0 {
		if !interactive() {
			return fmt.Errorf("%s (run) asks to run %d command(s) that you have not approved:\n  %s\nRead them, then approve with: %s --yes", repoconfig.FileName, len(plan.Commands), strings.Join(plan.Commands, "\n  "), again)
		}
		if !confirmRun(out, tildePath(path), plan.Commands) {
			fmt.Fprintf(out, "Not run. When you want them: %s --yes\n", again)
			return &exitCodeError{code: ExitError}
		}
		approve = true
	}
	proc, err := e.StartRun(ctx, plan, approve, out, stderr)
	if err != nil {
		return errors.New(strings.ReplaceAll(err.Error(), path, tildePath(path)))
	}
	defer proc.Stop()
	if *asJSON {
		if err := writeJSON(stdout, map[string]any{"url": proc.URL(), "port": proc.ListenPort(), "path": path, "kept": !*check}); err != nil {
			return err
		}
	} else if *check {
		fmt.Fprintf(stdout, "Ready at %s\n", proc.URL())
	} else {
		fmt.Fprintf(stdout, "Ready at %s   (Ctrl-C stops it)\n", proc.URL())
	}
	if !*check {
		select {
		case <-ctx.Done():
		case <-proc.Done():
			return fmt.Errorf("the application stopped: %v", proc.Err())
		}
	}
	proc.Stop()
	if !*asJSON {
		fmt.Fprintln(stdout, "Stopped.")
	}
	return nil
}

// confirmRun shows the commands of a run section and asks whether to run them.
func confirmRun(w io.Writer, dir string, commands []string) bool {
	fmt.Fprintf(w, "%s (run) asks to run these commands in %s. They run code from the repository:\n", repoconfig.FileName, dir)
	for _, c := range commands {
		fmt.Fprintf(w, "  %s\n", c)
	}
	fmt.Fprint(w, "Run them (and remember this choice for these exact commands)? [y/N] ")
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// orderedConfig writes .theo.json with "version" first, then the other keys in
// alphabetical order, one per line, short lists on one line.
func orderedConfig(doc map[string]json.RawMessage) ([]byte, error) {
	if _, ok := doc["version"]; !ok {
		doc["version"] = json.RawMessage("1")
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		if k != "version" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	keys = append([]string{"version"}, keys...)
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, k := range keys {
		kb, _ := json.Marshal(k)
		var ind bytes.Buffer
		if err := json.Indent(&ind, doc[k], "", "  "); err != nil {
			return nil, err
		}
		vs := compactIndented(ind.Bytes())
		vs = strings.ReplaceAll(vs, "\n", "\n  ")
		b.WriteString("  " + string(kb) + ": " + vs)
		if i < len(keys)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.Bytes(), nil
}
