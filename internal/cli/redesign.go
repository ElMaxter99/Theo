package cli

// The 0.2 redesign of the command line (docs/cli/output.md): `theo` with no
// arguments prints a summary, the project and worktree come from the
// current folder when they are left out, the help is split by topic, and
// errors end with the next step.

import (
	"context"
	"errors"
	"fmt"
	"github.com/ElMaxter99/Theo/internal/agents"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/execx"
)

// --- color ---

// colorMode is the value of the global --color flag: auto, always or never.
var colorMode = "auto"

// useColor reports whether output to w gets ANSI colors: --color always, or
// auto on a terminal without NO_COLOR (https://no-color.org).
func useColor(w io.Writer) bool {
	switch colorMode {
	case "always":
		return true
	case "never":
		return false
	}
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	f, ok := underlying(w).(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

const (
	ansiGreen  = "32"
	ansiYellow = "33"
	ansiRed    = "31"
	ansiCyan   = "36"
	ansiDim    = "2"
	ansiBold   = "1"
)

func paint(on bool, code, s string) string {
	if !on || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// globalFlags strips the flags that come before the command: --explain and
// --color. It returns the remaining arguments and whether --explain was set.
func globalFlags(args []string) (rest []string, explain bool, err error) {
	for len(args) > 0 {
		a := args[0]
		switch {
		case a == "--explain":
			explain = true
			args = args[1:]
		case a == "--no-color":
			colorMode = "never"
			args = args[1:]
		case a == "--color" && len(args) > 1:
			colorMode, args = args[1], args[2:]
		case strings.HasPrefix(a, "--color="):
			colorMode, args = strings.TrimPrefix(a, "--color="), args[1:]
		default:
			goto done
		}
		switch colorMode {
		case "auto", "always", "never":
		default:
			return nil, false, usageErrorf("--color must be auto, always or never")
		}
	}
done:
	return args, explain, nil
}

// --- state symbols ---

// stateSymbol is the symbol of an instance state, the same as the
// interface's: ▶ running, ◐ partial, ▲ unhealthy, ■ stopped, ○ not created,
// ? unknown.
func stateSymbol(state string) (symbol, color string) {
	switch state {
	case engine.InstRunning:
		return "▶", ansiGreen
	case engine.InstPartial:
		return "◐", ansiYellow
	case engine.InstUnhealthy:
		return "▲", ansiRed
	case engine.InstStopped:
		return "■", ansiDim
	case engine.InstAbsent:
		return "○", ansiDim
	}
	return "?", ansiDim
}

func stateLabel(state string) string {
	if state == engine.InstAbsent {
		return "not created"
	}
	if state == "" {
		return engine.InstUnknown
	}
	return state
}

// --- theo with no arguments: the summary ---

// summaryRows is how many worktrees of a project the summary shows before
// it counts the rest.
const summaryRows = 5

// runSummary prints what runs, in every project: `theo` with no arguments.
func runSummary(e *engine.Engine, stdout io.Writer, asJSON bool) error {
	ctx := context.Background()
	report, err := e.Status(ctx, "")
	if err != nil {
		return err
	}
	addProxyURLs(report)
	if asJSON {
		return writeJSON(stdout, report)
	}
	here, _ := os.Getwd()
	if err := printSummary(stdout, report, here, useColor(stdout)); err != nil {
		return err
	}
	if lines := updateNotice(ctx, e, time.Now()); len(lines) > 0 {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, strings.Join(lines, "\n"))
	}
	return nil
}

func printSummary(w io.Writer, r *engine.StatusReport, here string, color bool) error {
	if len(r.Projects) == 0 {
		fmt.Fprintln(w, "Theo runs every Git branch of a project as its own Docker Compose instance.")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "No projects yet. In the folder of a repository:")
		fmt.Fprintln(w, "  "+paint(color, ansiCyan, "theo project add ."))
		fmt.Fprintln(w, "Or open the interface: "+paint(color, ansiCyan, "theo ui")+" · all commands: "+paint(color, ansiCyan, "theo help"))
		return nil
	}
	if r.DockerError != "" {
		fmt.Fprintf(w, "Docker could not be asked, so states are unknown: %s\n  → start Docker, then run `theo doctor`\n\n", firstLine(r.DockerError))
	}
	warnings := len(r.Warnings)
	for i, p := range r.Projects {
		if i > 0 {
			fmt.Fprintln(w)
		}
		head := paint(color, ansiBold, p.Name) + "  " + paint(color, ansiDim, tildePath(p.Path))
		rows := append([]engine.WorktreeStatus(nil), p.Worktrees...)
		for _, wt := range rows {
			if here != "" && within(here, wt.Path) {
				head += paint(color, ansiDim, "  (this folder: "+statusRef(wt)+")")
			}
		}
		fmt.Fprintln(w, head)
		if p.Error != "" {
			fmt.Fprintf(w, "  cannot read it: %s\n", firstLine(p.Error))
		}
		warnings += len(p.Warnings)
		// Running and failing first, then the main worktree, then the rest.
		sort.SliceStable(rows, func(a, b int) bool { return summaryRank(rows[a]) < summaryRank(rows[b]) })
		width, stateWidth, countWidth := 0, 0, 0
		for _, wt := range rows {
			warnings += len(wt.Warnings)
			if n := len([]rune(statusRef(wt))); n > width {
				width = n
			}
			state, counts := summaryState(wt)
			if n := len(stateLabel(state)); n > stateWidth {
				stateWidth = n
			}
			if n := len(counts); n > countWidth {
				countWidth = n
			}
		}
		hidden := 0
		for j, wt := range rows {
			if wt.Kind == engine.KindBare {
				continue
			}
			if j >= summaryRows && summaryRank(wt) >= 2 {
				hidden++
				continue
			}
			state, counts := summaryState(wt)
			sym, col := stateSymbol(state)
			ref := statusRef(wt)
			line := fmt.Sprintf("  %s %-*s  %-*s  %-*s", paint(color, col, sym), width, ref, stateWidth, stateLabel(state), countWidth, counts)
			if detail := summaryDetail(wt); detail != "" {
				line += "  " + detail
			}
			fmt.Fprintln(w, strings.TrimRight(line, " "))
			for _, warn := range wt.Warnings {
				if warn.Fix != "" && (state == engine.InstPartial || state == engine.InstUnhealthy) {
					fmt.Fprintf(w, "    %s %s\n", paint(color, ansiCyan, "→"), warn.Fix)
					break
				}
			}
		}
		if hidden > 0 {
			fmt.Fprintf(w, "  … %d more not running (theo status %s)\n", hidden, p.Name)
		}
	}
	printForgotten(w, r.Projects, color)
	printAgents(w, r.Projects, color)
	fmt.Fprintln(w)
	switch {
	case warnings == 1:
		fmt.Fprintf(w, "1 warning: %s · all commands: %s\n", paint(color, ansiCyan, "theo status"), paint(color, ansiCyan, "theo help"))
	case warnings > 1:
		fmt.Fprintf(w, "%d warnings: %s · all commands: %s\n", warnings, paint(color, ansiCyan, "theo status"), paint(color, ansiCyan, "theo help"))
	default:
		fmt.Fprintf(w, "Details: %s · all commands: %s\n", paint(color, ansiCyan, "theo status"), paint(color, ansiCyan, "theo help"))
	}
	return nil
}

// summaryState is the state of a worktree's instance and its services as
// "running/total".
func summaryState(wt engine.WorktreeStatus) (state, counts string) {
	if wt.Instance == nil {
		return engine.InstUnknown, ""
	}
	if c := wt.Instance.Counts; c.Total > 0 {
		counts = fmt.Sprintf("%d/%d", c.Running, c.Total)
	}
	return wt.Instance.InstanceState, counts
}

// summaryRank orders the worktrees of the summary: 0 running or failing,
// 1 the main worktree, 2 the rest.
func summaryRank(wt engine.WorktreeStatus) int {
	if wt.Instance != nil {
		switch wt.Instance.InstanceState {
		case engine.InstRunning, engine.InstPartial, engine.InstUnhealthy:
			return 0
		}
	}
	if wt.Kind == engine.KindMain {
		return 1
	}
	return 2
}

// summaryDetail is the address of a running instance, or its first warning.
func summaryDetail(wt engine.WorktreeStatus) string {
	if wt.Instance == nil {
		return ""
	}
	if len(wt.Warnings) > 0 && wt.Instance.InstanceState != engine.InstRunning {
		return firstLine(wt.Warnings[0].Message)
	}
	if len(wt.Instance.ProxyURLs) > 0 {
		return wt.Instance.ProxyURLs[0]
	}
	for _, p := range wt.Instance.Ports {
		if p.URL != "" && p.Status == engine.PortRunning {
			return p.URL
		}
	}
	return ""
}

func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if rel, err := filepath.Rel(home, p); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return p
}

// within reports whether dir is base or inside it.
func within(dir, base string) bool {
	if base == "" {
		return false
	}
	norm := func(p string) string {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		return filepath.Clean(p)
	}
	d, b := norm(dir), norm(base)
	rel, err := filepath.Rel(b, d)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// --- project and worktree from the current folder ---

// inferable lists the commands whose project can be left out (or given as
// "."), and whether they take -w. For env, instance and volumes the project
// comes after their subcommand.
var inferable = map[string]bool{
	"up": true, "down": true, "restart": true, "logs": true, "services": true, "stop": true,
	"start": true, "recreate": true, "exec": true, "shell": true, "open": true, "ports": true,
	"history": true, "env": true, "instance": true, "volumes": true,
	"ps": false, "worktrees": false, "branches": false,
}

// subcommands of the commands whose project comes second.
var projectAfterSub = map[string][]string{
	"env":      {"set", "unset", "list"},
	"instance": {"set", "show", "config"},
	"volumes":  {"purge", "backup", "restore", "clone"},
}

// inferProject fills in the project (and the worktree) from the current
// folder when the command leaves the project out or gives ".", and says so
// on stderr. An explicit project name is never changed: without -w it still
// means the main worktree.
func inferProject(args []string, stderr io.Writer) []string {
	if len(args) == 0 {
		return args
	}
	takesW, ok := inferable[args[0]]
	if !ok {
		return args
	}
	at := 1
	if subs, ok := projectAfterSub[args[0]]; ok && len(args) > 1 {
		for _, s := range subs {
			if args[1] == s {
				at = 2
			}
		}
		if at == 1 && args[0] != "volumes" { // env and instance always need a subcommand
			return args
		}
	}
	dot := len(args) > at && args[at] == "."
	missing := len(args) <= at || (strings.HasPrefix(args[at], "-") && args[at] != "-")
	if !dot && !missing {
		return args
	}
	if args[0] == "exec" && missing && len(args) > at && args[at] == "--" {
		return args
	}
	// `stop --all` without a project stops every project: never narrow it to this folder.
	if args[0] == "stop" && hasFlag(args, "--all") && !dot {
		return args
	}
	here, err := os.Getwd()
	if err != nil {
		return args
	}
	e, err := engine.Open()
	if err != nil {
		return args
	}
	// Flags first, project later (`stop --all shop`): the project is there, nothing to infer.
	if missing && len(args) > at {
		if list, err := e.Projects(); err == nil {
			for _, a := range args[at:] {
				for _, p := range list {
					if a == p.Name {
						return args
					}
				}
			}
		}
	}
	project, wt, ok := projectForDir(context.Background(), e, here)
	if !ok {
		if dot {
			fmt.Fprintln(stderr, "theo: the current folder is not in a registered project")
		}
		return args
	}
	out := append([]string{}, args[:at]...)
	out = append(out, project)
	if dot {
		out = append(out, args[at+1:]...)
	} else {
		out = append(out, args[at:]...)
	}
	where := "main"
	if takesW && !hasFlag(args, "-w") && !hasFlag(args, "--worktree") {
		out = append(out, "-w", wt.Path)
		where = worktreeLabel(wt)
	} else if !takesW {
		where = ""
	}
	if where != "" {
		fmt.Fprintf(stderr, "theo: %s · %s (from the current folder)\n", project, where)
	} else {
		fmt.Fprintf(stderr, "theo: %s (from the current folder)\n", project)
	}
	return out
}

func worktreeLabel(wt engine.Worktree) string {
	if wt.Branch != "" {
		return wt.Branch
	}
	return filepath.Base(wt.Path)
}

// projectForDir finds the registered project and the worktree that contain
// dir: the deepest worktree wins, so a worktree created inside the main one
// is found.
func projectForDir(ctx context.Context, e *engine.Engine, dir string) (string, engine.Worktree, bool) {
	projects, err := e.Projects()
	if err != nil {
		return "", engine.Worktree{}, false
	}
	var best engine.Worktree
	bestProject, bestLen := "", -1
	for _, p := range projects {
		list, err := e.Worktrees(ctx, p.Name)
		if err != nil {
			continue
		}
		for _, wt := range list {
			if wt.Bare || wt.Path == "" || !within(dir, wt.Path) {
				continue
			}
			if n := len(filepath.Clean(wt.Path)); n > bestLen {
				best, bestProject, bestLen = wt, p.Name, n
			}
		}
	}
	return bestProject, best, bestLen >= 0
}

// --- help by topic ---

const helpTopics = `Theo runs every Git branch of a project as its own Docker Compose instance.

Usage: theo <command> [project] [-w <worktree>] [flags]
       Inside a project folder, the project and the worktree can be left out
       (or given as "."): theo up, theo logs . web -f

Every day
  status         What runs, in every project or one (theo alone: a summary)
  up / down      Start, or stop and remove the containers (volumes are kept)
  stop / start   Pause or resume            restart   Restart
  open           Open in the browser        logs      Show the logs
  ps             Containers                 exec, shell
Worktrees        worktree add|remove|owner|prune, worktrees, branches
Projects         project add|set|show|rename|list|remove
Configuration    env, instance, ports, services, recreate
Data             volumes [backup|restore|clone|purge]
Shared           shared add|use|unuse|up|down|status|remove|purge
Health           reconcile, doctor, history
Interfaces       ui, serve, proxy, mcp
Other            completion, version, help

theo help <command>   its flags and what it does
theo help all         the full reference

Global flags, before the command: --explain (list the Git and Docker commands
without changing anything), --color auto|always|never (NO_COLOR is honored).
Theo never changes your files or branches, and never deletes volumes.
`

// helpFor returns the lines of the full help about one command.
func helpFor(cmd string) (string, bool) {
	var b strings.Builder
	take := false
	for _, line := range strings.Split(helpText(), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		if indent == 2 && trimmed != "" {
			word := strings.Fields(trimmed)[0]
			take = word == cmd || strings.TrimSuffix(word, "s") == cmd
			if !take && cmd == "stop" && word == "stop" {
				take = true
			}
		} else if indent < 2 || trimmed == "" {
			take = false
		}
		if take {
			b.WriteString(line + "\n")
		}
	}
	if b.Len() == 0 {
		return "", false
	}
	return b.String(), true
}

// runHelp is `theo help [all|<command>]`.
func runHelp(args []string, stdout io.Writer) error {
	switch {
	case len(args) == 0:
		fmt.Fprint(stdout, helpTopics)
		return nil
	case len(args) == 1 && args[0] == "all":
		fmt.Fprint(stdout, helpText())
		return nil
	case len(args) == 1:
		text, ok := helpFor(args[0])
		if !ok {
			return usageErrorf("there is no command %q", args[0])
		}
		fmt.Fprintf(stdout, "Usage:\n%s\nAll commands: theo help · the full reference: theo help all\n", text)
		return nil
	}
	return usageErrorf("help takes at most one command")
}

// --- errors with the next step ---

// hinter is an error that knows the next step.
type hinter interface{ Hint() string }

// errorHint is the next step for an error, or "".
func errorHint(err error) string {
	var h hinter
	if errors.As(err, &h) {
		return h.Hint()
	}
	low := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, execx.ErrNotFound):
		return "theo doctor   (shows how to install what is missing)"
	case strings.Contains(low, "cannot connect to the docker daemon"), strings.Contains(low, "is the docker daemon running"),
		strings.Contains(low, "error during connect"):
		return "start Docker, then run `theo doctor`"
	case strings.Contains(low, "port is already allocated"), strings.Contains(low, "address already in use"), strings.Contains(low, "ports are not available"):
		return "start it again with --auto-ports to use free host ports"
	case errors.Is(err, engine.ErrNotFound) && strings.Contains(low, "project"):
		return "theo project list   (or theo project add <path>)"
	case errors.Is(err, engine.ErrNotFound) && strings.Contains(low, "worktree"):
		return "theo worktrees <project>"
	}
	return ""
}

// printError writes an error the way every command does: `theo: <message>`
// and, when Theo knows it, `  → <next step>`.
func printError(w io.Writer, err error) {
	fmt.Fprintf(w, "theo: %s\n", err)
	if hint := errorHint(err); hint != "" && !strings.Contains(err.Error(), hint) {
		fmt.Fprintf(w, "  %s %s\n", paint(useColor(w), ansiCyan, "→"), hint)
	}
}

// printAgents says how many agents work and wait, and where one waits.
func printAgents(w io.Writer, projects []engine.ProjectStatus, color bool) {
	counts := map[string]int{}
	var waiting []string
	for _, p := range projects {
		for _, wt := range p.Worktrees {
			if wt.Agent == nil {
				continue
			}
			counts[wt.Agent.State]++
			if wt.Agent.State == agents.Waiting {
				waiting = append(waiting, fmt.Sprintf("  %s  %s waits for you  %s theo open %s -w %s --in editor", p.Name, statusRef(wt), paint(color, ansiCyan, "→"), p.Name, statusRef(wt)))
			}
		}
	}
	var parts []string
	for _, st := range []string{agents.Working, agents.Waiting, agents.Done} {
		if n := counts[st]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, st))
		}
	}
	if len(parts) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s %s\n", paint(color, ansiBold, "Agents:"), strings.Join(parts, ", "))
	for _, l := range waiting {
		fmt.Fprintln(w, l)
	}
}
