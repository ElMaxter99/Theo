package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/agents"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/repoconfig"
)

func init() {
	registerCommand("hook", runHook)
	registerHelp(51, `
  hook worktree-create|worktree-remove|session-start|agent-state
                                               The commands Claude Code's hooks call (they read the
                                               event as JSON on stdin; --cwd, --name and --path
                                               replace it by hand). worktree-create makes a Theo
                                               worktree and prints its path; worktree-remove removes
                                               it when it has no changes; session-start tells the
                                               session where it runs; agent-state records whether the
                                               agent works, waits or is done. Register them all with
                                               theo mcp install --hooks. Failures go to theo log
`)
	registerCompletion(cmdSpec{name: "hook", subs: []cmdSpec{{name: "worktree-create"}, {name: "worktree-remove"}, {name: "session-start"}, {name: "agent-state"}}})
}

// hookInput is the JSON Claude Code writes on a hook's stdin. Only what
// Theo reads is listed.
type hookInput struct {
	SessionID    string `json:"session_id"`
	Cwd          string `json:"cwd"`
	Event        string `json:"hook_event_name"`
	Name         string `json:"name"`          // WorktreeCreate
	WorktreePath string `json:"worktree_path"` // WorktreeRemove
	Source       string `json:"source"`        // SessionStart
}

// readHookInput reads the event from stdin unless it is a terminal.
func readHookInput() hookInput {
	var in hookInput
	if f, ok := stdin.(*os.File); ok {
		if info, err := f.Stat(); err != nil || info.Mode()&os.ModeCharDevice != 0 {
			return in
		}
	}
	data, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return in
	}
	_ = json.Unmarshal(data, &in) // anything else is not for Theo
	return in
}

// agentStateOf maps Claude Code's events to what the agent is doing. "" means
// the agent has no state yet or any more.
var agentStateOf = map[string]string{
	"UserPromptSubmit": agents.Working,
	"PostToolUse":      agents.Working,
	"Notification":     agents.Waiting,
	"Stop":             agents.Done,
	"SessionStart":     "",
	"SessionEnd":       "",
}

func runHook(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageErrorf("hook needs a subcommand: worktree-create, worktree-remove, session-start or agent-state")
	}
	sub := args[0]
	fs := flag.NewFlagSet("hook "+sub, flag.ContinueOnError)
	cwd := fs.String("cwd", "", "folder the agent works in (default: from the event, else the current folder)")
	name := fs.String("name", "", "worktree-create: name of the worktree")
	path := fs.String("path", "", "worktree-remove: path of the worktree")
	state := fs.String("state", "", "agent-state: working, waiting, done or clear (default: from the event)")
	pos, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return usageErrorf("hook %s takes no arguments", sub)
	}
	switch sub {
	case "worktree-create", "worktree-remove", "session-start", "agent-state":
	default:
		return usageErrorf("unknown hook %q", sub)
	}

	in := readHookInput()
	if *cwd != "" {
		in.Cwd = *cwd
	}
	if in.Cwd == "" {
		in.Cwd, _ = os.Getwd()
	}
	if *name != "" {
		in.Name = *name
	}
	if *path != "" {
		in.WorktreePath = *path
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var herr error
	switch sub {
	case "worktree-create":
		herr = hookWorktreeCreate(ctx, e, in, stdout, stderr)
	case "worktree-remove":
		herr = hookWorktreeRemove(ctx, e, in, stderr)
	case "session-start":
		herr = hookSessionStart(ctx, e, in, stdout)
	case "agent-state":
		herr = hookAgentState(ctx, e, in, *state)
	}
	if herr != nil {
		e.Diag("hook %s (%s): %v", sub, in.Cwd, herr)
		// Only worktree-create can fail the agent's request: it must return a
		// path or say why not. The others never get in the agent's way.
		if sub == "worktree-create" {
			return herr
		}
		fmt.Fprintf(stderr, "theo: hook %s: %v\n", sub, herr)
	}
	return nil
}

var errNotInProject = errors.New("this folder is not in a project registered with Theo (theo project add .)")

func hookWorktreeCreate(ctx context.Context, e *engine.Engine, in hookInput, stdout, stderr io.Writer) error {
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("the event has no worktree name")
	}
	project, _, ok := projectForDir(ctx, e, in.Cwd)
	if !ok {
		return errNotInProject
	}
	p, err := e.Project(project)
	if err != nil {
		return err
	}
	branch := "worktree-" + sanitizeBranch(in.Name)

	// Idempotent: the same name gives the same worktree.
	branches, err := e.Branches(ctx, project)
	if err != nil {
		return err
	}
	exists := false
	for _, b := range branches {
		if !b.Remote && b.Name == branch {
			exists = true
			if b.Worktree != "" {
				fmt.Fprintln(stdout, b.Worktree)
				return nil
			}
		}
	}
	decl, _ := repoconfig.Load(p.Path)
	added, err := e.AddWorktree(ctx, project, branch, engine.AddWorktreeOptions{
		NewBranch: !exists, Owner: "claude", Copy: len(decl.Copy) > 0 || len(p.CopyOnAdd) > 0,
	})
	if err != nil {
		return err
	}
	for _, n := range added.Notes {
		fmt.Fprintf(stderr, "theo: %s\n", n)
	}
	// Commands from the repository run only if the person approved exactly
	// these ones; a hook cannot ask.
	if res, err := e.RunSetup(ctx, project, added.Path, false, stderr, stderr); errors.Is(err, engine.ErrSetupNotApproved) {
		fmt.Fprintf(stderr, "theo: %s declares after_create commands that were not approved yet (%s). Approve them once with: theo worktree setup %s %s\n",
			repoconfig.FileName, strings.Join(res.Commands, "; "), project, branch)
	} else if err != nil {
		fmt.Fprintf(stderr, "theo: %v\n", err)
	}
	// Claude Code reads the path from stdout, and nothing else.
	fmt.Fprintln(stdout, added.Path)
	return nil
}

func hookWorktreeRemove(ctx context.Context, e *engine.Engine, in hookInput, stderr io.Writer) error {
	if in.WorktreePath == "" {
		return errors.New("the event has no worktree_path")
	}
	project, wt, ok := projectForDir(ctx, e, in.WorktreePath)
	if !ok {
		return nil // not a Theo worktree: nothing of ours to remove
	}
	if !samePathCLI(wt.Path, in.WorktreePath) {
		return fmt.Errorf("%s is inside the worktree %s, not a worktree itself", in.WorktreePath, wt.Path)
	}
	// Never discards changes: Claude Code asks the person before removing a
	// worktree that has any.
	_, err := e.RemoveWorkspace(ctx, project, wt.Path, engine.RemoveWorktreeOptions{})
	if err == nil {
		_ = e.SetAgentState(wt.Path, "", "", "")
	}
	return err
}

func hookAgentState(ctx context.Context, e *engine.Engine, in hookInput, override string) error {
	state, known := agentStateOf[in.Event]
	switch override {
	case "":
		if !known {
			return fmt.Errorf("unknown event %q: pass --state", in.Event)
		}
	case "clear":
		state = ""
	default:
		state = override
	}
	_, wt, ok := projectForDir(ctx, e, in.Cwd)
	if !ok {
		return nil
	}
	// PostToolUse fires after every tool call: do nothing when it changes nothing.
	if in.Event == "PostToolUse" {
		store := agents.Store{Dir: e.DataDir()}
		if cur, ok := store.Get(wt.Path); ok && cur.State == agents.Working {
			return nil
		}
	}
	return e.SetAgentState(wt.Path, state, "claude", in.SessionID)
}

func hookSessionStart(ctx context.Context, e *engine.Engine, in hookInput, stdout io.Writer) error {
	project, wt, ok := projectForDir(ctx, e, in.Cwd)
	if !ok {
		return nil
	}
	_ = e.SetAgentState(wt.Path, "", "", "") // a new session starts with no state
	inst, err := e.InstanceFor(ctx, project, wt.Path, engine.InstanceOptions{})
	if err != nil {
		return err
	}
	vars, warning := worktreeEnv(ctx, e, inst)

	// The variables reach the session's commands through CLAUDE_ENV_FILE.
	if file := os.Getenv("CLAUDE_ENV_FILE"); file != "" {
		if f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			for _, v := range vars {
				fmt.Fprintf(f, "export %s=%s\n", v.Name, shQuote(v.Value))
			}
			f.Close()
		}
	}

	// What the session reads as context.
	fmt.Fprintf(stdout, "Theo manages this worktree: project %s, worktree %s (compose project %s).\n", inst.Project, firstNonEmpty(inst.Branch, inst.Workspace), inst.Compose)
	for _, v := range vars {
		if strings.HasPrefix(v.Name, "THEO_URL") || strings.HasPrefix(v.Name, "THEO_PORT") {
			fmt.Fprintf(stdout, "  %s=%s\n", v.Name, v.Value)
		}
	}
	if warning != "" {
		fmt.Fprintf(stdout, "  (%s)\n", warning)
	}
	fmt.Fprintf(stdout, "Start it with `theo up %s -w %s`; see it with `theo ps %s -w %s`. These variables are exported in your shell (theo env prints them again).\n",
		inst.Project, worktreeArg(inst), inst.Project, worktreeArg(inst))
	return nil
}

func sanitizeBranch(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "agent"
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// samePathCLI reports whether two paths are the same folder.
func samePathCLI(a, b string) bool { return within(a, b) && within(b, a) }
