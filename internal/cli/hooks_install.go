package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ElMaxter99/Theo/internal/filelock"
)

// hookEvents are the Claude Code events Theo listens to, and the theo
// command each runs.
var hookEvents = []struct{ Event, Sub string }{
	{"WorktreeCreate", "worktree-create"},
	{"WorktreeRemove", "worktree-remove"},
	{"SessionStart", "session-start"},
	{"UserPromptSubmit", "agent-state"},
	{"PostToolUse", "agent-state"},
	{"Notification", "agent-state"},
	{"Stop", "agent-state"},
	{"SessionEnd", "agent-state"},
}

// hookSettingsPath is the settings file of a scope: project
// (.claude/settings.json, shared with the team), local (.claude/settings.local.json,
// yours) or user (~/.claude/settings.json).
func hookSettingsPath(scope string) (string, error) {
	switch scope {
	case "user":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".claude", "settings.json"), nil
	case "", "local", "project":
		root, err := os.Getwd()
		if err != nil {
			return "", err
		}
		// The repository's root, if the current folder is inside one.
		for dir := root; ; dir = filepath.Dir(dir) {
			if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
				root = dir
				break
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
		name := "settings.local.json"
		if scope == "project" {
			name = "settings.json"
		}
		return filepath.Join(root, ".claude", name), nil
	}
	return "", usageErrorf("--scope must be local, user or project")
}

// mergeHooks adds Theo's hooks to a settings document, leaving everything
// else, and returns the events it added. A hook already there is not added
// twice.
func mergeHooks(doc map[string]any, command string) (added []string, err error) {
	hooks, _ := doc["hooks"].(map[string]any)
	if doc["hooks"] != nil && hooks == nil {
		return nil, errors.New(`"hooks" in the settings file is not an object`)
	}
	if hooks == nil {
		hooks = map[string]any{}
	}
	for _, h := range hookEvents {
		cmd := command + " hook " + h.Sub
		groups, _ := hooks[h.Event].([]any)
		if hooks[h.Event] != nil && groups == nil {
			return nil, fmt.Errorf("hooks.%s in the settings file is not a list", h.Event)
		}
		present := false
		for _, g := range groups {
			inner, _ := g.(map[string]any)["hooks"].([]any)
			for _, ih := range inner {
				if c, _ := ih.(map[string]any)["command"].(string); c == cmd {
					present = true
				}
			}
		}
		if present {
			continue
		}
		groups = append(groups, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd}}})
		hooks[h.Event] = groups
		added = append(added, h.Event)
	}
	doc["hooks"] = hooks
	return added, nil
}

func installHooks(scope string, printOnly bool, stdout io.Writer) error {
	command := shellJoin([]string{theoCommand()})
	doc := map[string]any{}
	path, err := hookSettingsPath(scope)
	if err != nil {
		return err
	}
	if printOnly {
		if _, err := mergeHooks(doc, command); err != nil {
			return err
		}
		return writeJSON(stdout, doc)
	}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("%s is not valid JSON, so Theo does not touch it: %w", path, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	added, err := mergeHooks(doc, command)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if len(added) == 0 {
		fmt.Fprintf(stdout, "Theo's hooks are already in %s.\n", path)
		return nil
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	out := []byte(strings.TrimRight(buf.String(), "\n"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := filelock.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return err
	}
	sort.Strings(added)
	fmt.Fprintf(stdout, "Added Theo's hooks to %s: %s.\nClaude Code now creates and removes worktrees through Theo, tells the session where it runs, and Theo shows whether the agent works, waits or is done.\n", path, strings.Join(added, ", "))
	return nil
}
