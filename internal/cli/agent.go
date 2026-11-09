package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/filelock"
)

func init() {
	registerCommand("agent", runAgent)
	registerHelp(52, `
  agent install [claude|copilot] [--scope S] [--print] [--force]
                                               Register Theo's hooks with the coding agents installed
                                               on this machine (Theo never installs them: if one is
                                               missing it says where to get it; --force writes the
                                               hooks anyway). claude is the same as mcp install
                                               --hooks. copilot writes .github/hooks/theo.json
                                               (--scope project, the default) or
                                               ~/.copilot/hooks/theo.json (--scope user)
`)
	registerCompletion(cmdSpec{name: "agent", subs: []cmdSpec{{name: "install", subs: []cmdSpec{{name: "claude"}, {name: "copilot"}}, flags: []flagSpec{{names: []string{"--scope"}}, {names: []string{"--print"}}, {names: []string{"--force"}}}}}})
}

func runAgent(_ *engine.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "install" {
		return usageErrorf("agent needs a subcommand: install claude|copilot")
	}
	fs := flag.NewFlagSet("agent install", flag.ContinueOnError)
	scope := fs.String("scope", "", "claude: local (default), user or project; copilot: project (default) or user")
	printOnly := fs.Bool("print", false, "print the settings instead of writing them")
	force := fs.Bool("force", false, "write the hooks even if the agent is not installed on this machine")
	pos, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return usageErrorf("agent install takes one agent: claude or copilot")
	}
	if len(pos) == 0 {
		if *printOnly || *scope != "" {
			return usageErrorf("--print and --scope need an agent: claude or copilot")
		}
		// Every agent that is on this machine; the others get a hint.
		found := false
		for _, a := range agentNames {
			if !agentInstalled(a) {
				fmt.Fprintf(stdout, "%s: not found on this machine, skipped. %s\n", agentLabel[a], agentHint[a])
				continue
			}
			found = true
			if err := installAgent(a, "", false, stdout); err != nil {
				return err
			}
		}
		if !found {
			fmt.Fprintln(stdout, "No supported coding agent found. Theo does not install them; install one and run this again.")
		}
		return nil
	}
	a := pos[0]
	if _, ok := agentLabel[a]; !ok {
		return usageErrorf("unknown agent %q (claude or copilot)", a)
	}
	if !*printOnly && !*force && !agentInstalled(a) {
		return fmt.Errorf("%s was not found on this machine, and Theo does not install it. %s\nTo write the hooks anyway (for a teammate or the cloud agent): theo agent install %s --force", agentLabel[a], agentHint[a], a)
	}
	return installAgent(a, *scope, *printOnly, stdout)
}

var agentNames = []string{"claude", "copilot"}

var agentLabel = map[string]string{"claude": "Claude Code", "copilot": "GitHub Copilot"}

var agentHint = map[string]string{
	"claude":  "Install Claude Code: https://code.claude.com/docs",
	"copilot": "Install Copilot (CLI or the VS Code extension): https://docs.github.com/en/copilot",
}

func installAgent(a, scope string, printOnly bool, stdout io.Writer) error {
	if a == "claude" {
		return installHooks(scope, printOnly, stdout)
	}
	return installCopilotHooks(scope, printOnly, stdout)
}

// agentInstalled says whether the agent is on this machine: its command on
// PATH or its folder in the home folder. Tests replace it.
var agentInstalled = func(a string) bool {
	command, dir := "claude", ".claude"
	if a == "copilot" {
		command, dir = "copilot", ".copilot"
	}
	if _, err := exec.LookPath(command); err == nil {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(home, dir)); err == nil {
		return true
	}
	if a == "copilot" { // the VS Code extension
		if m, _ := filepath.Glob(filepath.Join(home, ".vscode", "extensions", "github.copilot-*")); len(m) > 0 {
			return true
		}
	}
	return false
}

// copilotHooks are the Copilot events Theo listens to and the theo hook each
// runs. Copilot sends no event name, so each command says the state itself.
// notification only exists in Copilot CLI; the cloud agent ignores it.
var copilotHooks = []struct{ Event, Args string }{
	{"sessionStart", "session-start --agent copilot"},
	{"userPromptSubmitted", "agent-state --agent copilot --state working"},
	{"postToolUse", "agent-state --agent copilot --state working --skip-repeat"},
	{"notification", "agent-state --agent copilot --state waiting"},
	{"agentStop", "agent-state --agent copilot --state done"},
	{"sessionEnd", "agent-state --agent copilot --state clear"},
}

// copilotHooksPath is Theo's own hooks file of a scope: project
// (.github/hooks/theo.json, shared with the team) or user
// (~/.copilot/hooks/theo.json, or $COPILOT_HOME/hooks).
func copilotHooksPath(scope string) (string, error) {
	switch scope {
	case "user":
		if home := os.Getenv("COPILOT_HOME"); home != "" {
			return filepath.Join(home, "hooks", "theo.json"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".copilot", "hooks", "theo.json"), nil
	case "", "project":
		root, err := os.Getwd()
		if err != nil {
			return "", err
		}
		for dir := root; ; dir = filepath.Dir(dir) {
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				root = dir
				break
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
		return filepath.Join(root, ".github", "hooks", "theo.json"), nil
	}
	return "", usageErrorf("--scope must be project or user for copilot")
}

// copilotHooksDoc is the content of Theo's hooks file.
func copilotHooksDoc(command string) map[string]any {
	// PowerShell does not run a quoted path unless it is called with &.
	ps := command
	if strings.HasPrefix(ps, `"`) {
		ps = "& " + ps
	}
	hooks := map[string]any{}
	for _, h := range copilotHooks {
		hooks[h.Event] = []any{map[string]any{
			"type":       "command",
			"bash":       command + " hook " + h.Args,
			"powershell": ps + " hook " + h.Args,
			"timeoutSec": 30,
		}}
	}
	return map[string]any{"version": 1, "hooks": hooks}
}

func installCopilotHooks(scope string, printOnly bool, stdout io.Writer) error {
	path, err := copilotHooksPath(scope)
	if err != nil {
		return err
	}
	doc := copilotHooksDoc(shellJoin([]string{theoCommand()}))
	if printOnly {
		return writeJSON(stdout, doc)
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	want := []byte(buf.String())
	// The file is Theo's own: replace it only if it is ours.
	switch old, err := os.ReadFile(path); {
	case err == nil:
		if string(old) == string(want) {
			fmt.Fprintf(stdout, "Theo's hooks are already in %s.\n", path)
			return nil
		}
		if !strings.Contains(string(old), " hook ") || !strings.Contains(string(old), "--agent copilot") {
			return fmt.Errorf("%s exists and is not Theo's hooks file; move it or merge the hooks by hand (theo agent install copilot --print)", path)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := filelock.WriteFile(path, want, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Wrote Theo's Copilot hooks to %s.\nTheo now tells the session where it runs and shows whether Copilot works, waits or is done. Copilot has no worktree hooks, so create worktrees with theo worktree add.\n", path)
	return nil
}
