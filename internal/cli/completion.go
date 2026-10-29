package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ElMaxter99/Theo/internal/engine"
)

func init() {
	registerCommand("completion", runCompletion)
	registerCommand("__complete", runComplete)
	registerHelp(30, `
Shell completion:
  completion bash|zsh|fish|powershell               Print the completion script for a shell. Commands,
                                                    flags, project names, worktrees and services
                                                    complete. See docs/cli/completion.md
`)
}

// A flagSpec describes one flag for completion. value says what the flag
// takes: "" for none, "worktree", "service" or "file" for something Theo can
// suggest, and "text" for a value it cannot.
type flagSpec struct {
	names []string
	value string
}

// A cmdSpec describes a command for completion: its flags, its subcommands
// and what each positional argument is ("project", "service", "worktree",
// "branch", "path", "shell", "text"). A kind ending in "*" repeats.
//
// The table lives here, next to the commands, and registerCompletion lets a
// command defined in another file add itself. A command in `commands` that
// has no spec still completes by name.
type cmdSpec struct {
	name  string
	flags []flagSpec
	subs  []cmdSpec
	args  []string
}

var completionSpecs []cmdSpec

// registerCompletion adds a command to shell completion.
func registerCompletion(c cmdSpec) { completionSpecs = append(completionSpecs, c) }

var (
	jsonFlag  = flagSpec{names: []string{"--json"}}
	selFlags  = []flagSpec{{[]string{"-w", "--worktree"}, "worktree"}, {[]string{"--env-file"}, "file"}}
	profFlag  = flagSpec{[]string{"--profile"}, "text"}
	autoFlag  = flagSpec{names: []string{"--auto-ports"}}
	buildFlag = flagSpec{names: []string{"--build"}}
)

func with(base []flagSpec, more ...flagSpec) []flagSpec {
	return append(append([]flagSpec(nil), base...), more...)
}

func init() {
	for _, c := range []cmdSpec{
		{name: "project", subs: []cmdSpec{
			{name: "add", args: []string{"path"}, flags: []flagSpec{{[]string{"--name"}, "text"}, {[]string{"--env-file"}, "file"}}},
			{name: "set", args: []string{"project"}, flags: []flagSpec{{[]string{"--env-file"}, "file"}, {names: []string{"--no-env-file"}},
				{[]string{"--worktree-dir"}, "file"}, {names: []string{"--default-worktree-dir"}}}},
			{name: "list", flags: []flagSpec{jsonFlag}},
			{name: "remove", args: []string{"project"}},
		}},
		{name: "worktrees", args: []string{"project"}, flags: []flagSpec{jsonFlag}},
		{name: "branches", args: []string{"project"}, flags: []flagSpec{jsonFlag}},
		{name: "worktree", subs: []cmdSpec{
			{name: "add", args: []string{"project", "branch"}, flags: []flagSpec{{names: []string{"--new"}}, {[]string{"--from"}, "text"}, {[]string{"--path"}, "file"}, jsonFlag}},
			{name: "remove", args: []string{"project", "worktree"}, flags: []flagSpec{{names: []string{"--discard-changes"}}, {[]string{"--confirm"}, "text"}}},
		}},
		{name: "services", args: []string{"project"}, flags: with(selFlags, jsonFlag)},
		{name: "up", args: []string{"project"}, flags: with(selFlags, buildFlag, profFlag, autoFlag,
			flagSpec{[]string{"--service"}, "service"}, flagSpec{names: []string{"--force-recreate"}})},
		{name: "down", args: []string{"project"}, flags: selFlags},
		{name: "restart", args: []string{"project", "service*"}, flags: selFlags},
		{name: "stop", args: []string{"project", "service*"}, flags: selFlags},
		{name: "start", args: []string{"project", "service*"}, flags: selFlags},
		{name: "recreate", args: []string{"project", "service*"}, flags: with(selFlags, buildFlag, profFlag, autoFlag)},
		{name: "ps", args: []string{"project"}, flags: with(selFlags, jsonFlag)},
		{name: "logs", args: []string{"project", "service*"}, flags: with(selFlags, flagSpec{names: []string{"-f", "--follow"}}, flagSpec{[]string{"--tail"}, "text"})},
		{name: "exec", args: []string{"project", "service"}, flags: with(selFlags, flagSpec{names: []string{"-T"}})},
		{name: "shell", args: []string{"project", "service"}, flags: selFlags},
		{name: "reconcile", args: []string{"project"}, flags: []flagSpec{{names: []string{"--apply"}}, jsonFlag}},
		{name: "volumes", args: []string{"project"}, flags: with(selFlags, jsonFlag), subs: []cmdSpec{
			{name: "purge", args: []string{"project"}, flags: with(selFlags, flagSpec{[]string{"--confirm"}, "text"}, jsonFlag)},
		}},
		{name: "env", subs: []cmdSpec{
			{name: "set", args: []string{"project", "text"}, flags: with(selFlags, flagSpec{names: []string{"--secret"}}, flagSpec{[]string{"--service"}, "service"})},
			{name: "unset", args: []string{"project", "text"}, flags: selFlags},
			{name: "list", args: []string{"project"}, flags: with(selFlags, flagSpec{names: []string{"--show-secrets"}}, jsonFlag)},
		}},
		{name: "instance", subs: []cmdSpec{
			{name: "set", args: []string{"project"}, flags: with(selFlags, flagSpec{names: []string{"--neutralize-container-names"}}, flagSpec{names: []string{"--keep-container-names"}})},
			{name: "show", args: []string{"project"}, flags: with(selFlags, jsonFlag)},
			{name: "config", args: []string{"project"}, flags: with(selFlags, autoFlag, profFlag)},
		}},
		{name: "history", args: []string{"project"}, flags: []flagSpec{{[]string{"-w", "--worktree"}, "worktree"}, {[]string{"--limit"}, "text"}, jsonFlag}},
		{name: "ports", args: []string{"project"}, flags: with(selFlags, profFlag, autoFlag, jsonFlag)},
		{name: "status", args: []string{"project"}, flags: []flagSpec{jsonFlag}},
		{name: "completion", args: []string{"shell"}},
		{name: "version", flags: []flagSpec{jsonFlag}},
		{name: "help"},
	} {
		registerCompletion(c)
	}
}

var shellNames = []string{"bash", "zsh", "fish", "powershell"}

func runCompletion(_ *engine.Engine, args []string, stdout, _ io.Writer) error {
	if len(args) != 1 {
		return usageErrorf("completion needs a shell: %s", strings.Join(shellNames, ", "))
	}
	script, ok := completionScripts[args[0]]
	if !ok {
		return usageErrorf("completion: unknown shell %q (use %s)", args[0], strings.Join(shellNames, ", "))
	}
	_, err := io.WriteString(stdout, script)
	return err
}

// runComplete is the hidden command the completion scripts call:
//
//	theo __complete projects
//	theo __complete worktrees <project>
//	theo __complete services <project> [worktree]
//	theo __complete suggest --cur=<word> -- <words before it...>
//
// It prints one candidate per line and never fails: a completion that cannot
// be computed is an empty one.
func runComplete(e *engine.Engine, args []string, stdout, _ io.Writer) error {
	if len(args) == 0 {
		return nil
	}
	c := completer{e: e}
	var out []string
	switch args[0] {
	case "projects":
		out = c.projects()
	case "worktrees":
		if len(args) > 1 {
			out = c.worktrees(args[1])
		}
	case "services":
		switch len(args) {
		case 2:
			out = c.services(args[1], "")
		case 3:
			out = c.services(args[1], args[2])
		}
	case "suggest":
		fs := flag.NewFlagSet("suggest", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		cur := fs.String("cur", "", "")
		if fs.Parse(args[1:]) == nil {
			out = c.suggest(fs.Args(), *cur)
		}
	}
	for _, line := range out {
		fmt.Fprintln(stdout, line)
	}
	return nil
}

// filesMarker is printed last by `suggest` when the shell should also offer
// file names.
const filesMarker = ":files"

// completer computes candidates. Names come from the engine, which may be
// nil in tests of the static part.
type completer struct{ e *engine.Engine }

func (c completer) projects() []string {
	if c.e == nil {
		return nil
	}
	list, err := c.e.Projects()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, p := range list {
		out = append(out, p.Name)
	}
	sort.Strings(out)
	return out
}

func (c completer) worktrees(project string) []string {
	if c.e == nil || project == "" {
		return nil
	}
	list, err := c.e.Worktrees(context.Background(), project)
	if err != nil {
		return nil
	}
	var out []string
	for _, wt := range list {
		switch {
		case wt.Bare:
		case wt.Branch != "":
			out = append(out, wt.Branch)
		default:
			out = append(out, wt.Path)
		}
	}
	return out
}

func (c completer) branches(project string) []string {
	if c.e == nil || project == "" {
		return nil
	}
	list, err := c.e.Branches(context.Background(), project)
	if err != nil {
		return nil
	}
	var out []string
	for _, b := range list {
		out = append(out, b.Name)
	}
	return out
}

func (c completer) services(project, worktree string) []string {
	if c.e == nil || project == "" {
		return nil
	}
	inst, err := c.e.Instance(context.Background(), project, worktree, nil)
	if err != nil {
		return nil
	}
	return inst.ServiceNames()
}

// suggest returns the candidates for the word cur, after the words before it
// (the command line without the program name).
func (c completer) suggest(before []string, cur string) []string {
	if len(before) > 0 && before[0] == "--explain" {
		before = before[1:]
	}
	specs := completionSpecs
	names := map[string]bool{}
	var top []string
	for _, s := range specs {
		names[s.name] = true
		top = append(top, s.name)
	}
	for name := range commands { // commands without a spec still complete by name
		if !names[name] && !strings.HasPrefix(name, "__") {
			top = append(top, name)
		}
	}
	sort.Strings(top)

	if len(before) == 0 {
		if strings.HasPrefix(cur, "-") {
			return matching([]string{"--explain", "--help", "--version"}, cur)
		}
		return matching(top, cur)
	}

	var spec *cmdSpec
	for i := range specs {
		if specs[i].name == before[0] {
			spec = &specs[i]
		}
	}
	if spec == nil {
		return nil
	}
	rest := before[1:]
	// A subcommand narrows the spec; the flags of both apply.
	flags := spec.flags
	if len(rest) > 0 {
		for i := range spec.subs {
			if spec.subs[i].name == rest[0] {
				flags = append(append([]flagSpec(nil), flags...), spec.subs[i].flags...)
				spec = &spec.subs[i]
				rest = rest[1:]
				break
			}
		}
	}

	// Positional words so far, flag values skipped; the project and the
	// worktree, if given.
	var positional []string
	worktree := ""
	for i := 0; i < len(rest); i++ {
		w := rest[i]
		if !strings.HasPrefix(w, "-") || w == "-" {
			positional = append(positional, w)
			continue
		}
		name, value, hasValue := strings.Cut(w, "=")
		f := findFlag(flags, name)
		if f == nil || f.value == "" {
			continue
		}
		if !hasValue && i+1 < len(rest) {
			i++
			value = rest[i]
		}
		if f.value == "worktree" {
			worktree = value
		}
	}
	project := ""
	if len(positional) > 0 && len(spec.args) > 0 && spec.args[0] == "project" {
		project = positional[0]
	}

	// The word after a flag that takes a value.
	if len(before) > 0 {
		last := before[len(before)-1]
		if strings.HasPrefix(last, "-") && !strings.Contains(last, "=") {
			if f := findFlag(flags, last); f != nil && f.value != "" {
				return c.valueCandidates(f.value, cur, project, worktree)
			}
		}
	}
	if strings.HasPrefix(cur, "-") {
		var all []string
		for _, f := range flags {
			all = append(all, f.names...)
		}
		return matching(append(all, "--help"), cur)
	}

	// Subcommands come first at the first position, along with what the
	// command itself takes there (volumes <project> | volumes purge).
	var out []string
	idx := len(positional)
	if idx == 0 && len(spec.subs) > 0 {
		for _, s := range spec.subs {
			out = append(out, s.name)
		}
	}
	if kind := positionKind(spec.args, idx); kind != "" {
		out = append(out, c.kindCandidates(kind, project, worktree)...)
		if kind == "path" {
			out = append(out, filesMarker)
		}
	}
	return matchingKeep(out, cur)
}

func (c completer) valueCandidates(kind, cur, project, worktree string) []string {
	switch kind {
	case "worktree":
		return matching(c.worktrees(project), cur)
	case "service":
		return matching(c.services(project, worktree), cur)
	case "file":
		return []string{filesMarker}
	}
	return nil
}

func (c completer) kindCandidates(kind, project, worktree string) []string {
	switch strings.TrimSuffix(kind, "*") {
	case "project":
		return c.projects()
	case "service":
		return c.services(project, worktree)
	case "worktree":
		return c.worktrees(project)
	case "branch":
		return c.branches(project)
	case "shell":
		return shellNames
	}
	return nil
}

// positionKind returns the kind of the positional argument at idx.
func positionKind(args []string, idx int) string {
	if idx < len(args) {
		return args[idx]
	}
	if n := len(args); n > 0 && strings.HasSuffix(args[n-1], "*") {
		return args[n-1]
	}
	return ""
}

func findFlag(flags []flagSpec, name string) *flagSpec {
	for i := range flags {
		for _, n := range flags[i].names {
			if n == name {
				return &flags[i]
			}
		}
	}
	return nil
}

// matching keeps the candidates that start with prefix.
func matching(list []string, prefix string) []string {
	var out []string
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			out = append(out, s)
		}
	}
	return out
}

// matchingKeep is matching, but passes the files marker through.
func matchingKeep(list []string, prefix string) []string {
	var out []string
	for _, s := range list {
		if s == filesMarker || strings.HasPrefix(s, prefix) {
			out = append(out, s)
		}
	}
	return out
}

// completionScripts are the scripts of each shell. They all do the same:
// pass the words before the cursor, and the word at the cursor, to
// `theo __complete suggest`, and offer what it prints.
var completionScripts = map[string]string{
	"bash": `# bash completion for theo. Load it with:  source <(theo completion bash)
_theo() {
    local IFS=$'\n'
    local cur="${COMP_WORDS[COMP_CWORD]}"
    local out
    out=$("${COMP_WORDS[0]}" __complete suggest --cur="$cur" -- "${COMP_WORDS[@]:1:$((COMP_CWORD-1))}" 2>/dev/null)
    COMPREPLY=()
    local line
    for line in $out; do
        [[ "$line" == ":files" ]] || COMPREPLY+=("$line")
    done
}
complete -o default -F _theo theo
`,
	"zsh": `#compdef theo
# zsh completion for theo. Load it with:  source <(theo completion zsh)
_theo() {
    local out
    out=$(${words[1]} __complete suggest --cur="${words[CURRENT]}" -- "${(@)words[2,CURRENT-1]}" 2>/dev/null)
    local -a candidates
    local files=0 line
    for line in "${(@f)out}"; do
        [[ -z "$line" ]] && continue
        if [[ "$line" == ":files" ]]; then files=1; else candidates+=("$line"); fi
    done
    (( ${#candidates} )) && compadd -- "${candidates[@]}"
    (( files || ${#candidates} == 0 )) && _files
    return 0
}
if [ "$funcstack[1]" = "_theo" ]; then
    _theo "$@"
else
    compdef _theo theo
fi
`,
	"fish": `# fish completion for theo. Save it with:  theo completion fish > ~/.config/fish/completions/theo.fish
function __theo_suggest
    set -l tokens (commandline -opc)
    set -l current (commandline -ct)
    $tokens[1] __complete suggest --cur="$current" -- $tokens[2..-1] 2>/dev/null
end
function __theo_candidates
    __theo_suggest | string match -v ':files'
end
function __theo_wants_files
    __theo_suggest | string match -q ':files'
end
complete -c theo -f -a '(__theo_candidates)'
complete -c theo -F -n __theo_wants_files
`,
	"powershell": `# PowerShell completion for theo. Load it with:  theo completion powershell | Out-String | Invoke-Expression
Register-ArgumentCompleter -Native -CommandName theo -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)
    $elements = @($commandAst.CommandElements | ForEach-Object { $_.ToString() })
    $exe = $elements[0]
    $before = @()
    if ($elements.Count -gt 1) {
        $before = $elements[1..($elements.Count - 1)]
        if ($wordToComplete -ne '') { $before = @($before | Select-Object -SkipLast 1) }
    }
    & $exe __complete suggest "--cur=$wordToComplete" -- @before 2>$null |
        Where-Object { $_ -and $_ -ne ':files' } |
        ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) }
}
`,
}
