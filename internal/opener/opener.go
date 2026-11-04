// Package opener opens a worktree folder in a terminal, an editor or the
// file manager. The CLI and the web interface share it.
package opener

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/ElMaxter99/Theo/internal/prefs"
)

// Targets are the folder targets of `theo open --in` (url is handled apart).
var Targets = []string{"terminal", "editor", "files"}

// ErrTarget means the target is not one of Targets.
var ErrTarget = errors.New("unknown target")

// Look finds a program on PATH. Tests replace it.
var Look = exec.LookPath

// Start starts a program and does not wait for it: an editor or a terminal
// outlives the command. Tests replace it.
var Start = func(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reap it without holding the caller
	return nil
}

var editorCandidates = []string{"code", "cursor", "windsurf", "zed", "subl"}

// Tool chooses the program that opens dir for a target: the person's
// setting, else the first usual one found. It returns the program, its
// arguments (the folder included) and the name to show.
func Tool(p prefs.Store, target, dir string) (name string, args []string, label string, err error) {
	fail := func(what, key string) error {
		return fmt.Errorf("%s not found. Set one with: theo config set %s <command>", what, key)
	}
	custom := func(key string) (string, []string, bool) {
		v := strings.Fields(p.Get(key))
		if len(v) == 0 {
			return "", nil, false
		}
		return v[0], v[1:], true
	}
	switch target {
	case "editor":
		if n, a, ok := custom("open.editor"); ok {
			if _, err := Look(n); err != nil {
				return "", nil, "", fail("Editor", "open.editor")
			}
			return n, append(a, dir), n, nil
		}
		for _, n := range editorCandidates {
			if _, err := Look(n); err == nil {
				return n, []string{dir}, n, nil
			}
		}
		return "", nil, "", fail("Editor", "open.editor")
	case "terminal":
		if n, a, ok := custom("open.terminal"); ok {
			if _, err := Look(n); err != nil {
				return "", nil, "", fail("Terminal", "open.terminal")
			}
			return n, a, n, nil // it starts in dir
		}
		switch runtime.GOOS {
		case "windows":
			if _, err := Look("wt.exe"); err == nil {
				return "wt.exe", []string{"-d", dir}, "wt", nil
			}
			return "cmd", []string{"/c", "start", "cmd.exe"}, "cmd", nil
		case "darwin":
			return "open", []string{"-a", "Terminal", dir}, "Terminal", nil
		}
		for _, n := range []string{"x-terminal-emulator", "gnome-terminal", "konsole", "xfce4-terminal", "xterm"} {
			if _, err := Look(n); err == nil {
				return n, nil, n, nil
			}
		}
		return "", nil, "", fail("Terminal", "open.terminal")
	case "files":
		if n, a, ok := custom("open.files"); ok {
			if _, err := Look(n); err != nil {
				return "", nil, "", fail("File manager", "open.files")
			}
			return n, append(a, dir), n, nil
		}
		switch runtime.GOOS {
		case "windows":
			return "explorer", []string{dir}, "explorer", nil
		case "darwin":
			return "open", []string{dir}, "open", nil
		}
		if _, err := Look("xdg-open"); err == nil {
			return "xdg-open", []string{dir}, "xdg-open", nil
		}
		return "", nil, "", fail("File manager", "open.files")
	}
	return "", nil, "", fmt.Errorf("%w: %q", ErrTarget, target)
}

// Available says whether a target can be opened now and, if not, why.
func Available(p prefs.Store, target, dir string) (ok bool, reason string) {
	if _, err := os.Stat(dir); err != nil {
		return false, "the folder no longer exists"
	}
	if _, _, _, err := Tool(p, target, dir); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// Open starts the tool for a target in dir and returns its display name.
func Open(p prefs.Store, target, dir string) (string, error) {
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("the folder no longer exists: %s", dir)
	}
	name, args, label, err := Tool(p, target, dir)
	if err != nil {
		return "", err
	}
	if err := Start(dir, name, args...); err != nil {
		return "", fmt.Errorf("could not start %s: %v", name, err)
	}
	return label, nil
}
