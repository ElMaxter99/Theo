package cli

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/ElMaxter99/Theo/internal/engine"
)

// openTargets are the values of `theo open --in` besides url.
var openTargets = []string{"terminal", "editor", "files", "url"}

// lookTool finds a program on PATH. Tests replace it.
var lookTool = exec.LookPath

// startTool starts a program and does not wait for it: an editor or a
// terminal outlives the command. Tests replace it.
var startTool = func(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reap it without holding the command
	return nil
}

var editorCandidates = []string{"code", "cursor", "windsurf", "zed", "subl"}

// toolFor chooses the program that opens dir for a target: the person's
// setting, else the first usual one found. It returns the program, its
// arguments (the folder included) and the name to show.
func toolFor(e *engine.Engine, target, dir string) (name string, args []string, label string, err error) {
	fail := func(what, key string) error {
		return fmt.Errorf("%s not found. Set one with: theo config set %s <command>", what, key)
	}
	custom := func(key string) (string, []string, bool) {
		v := strings.Fields(e.Prefs().Get(key))
		if len(v) == 0 {
			return "", nil, false
		}
		return v[0], v[1:], true
	}
	switch target {
	case "editor":
		if n, a, ok := custom("open.editor"); ok {
			if _, err := lookTool(n); err != nil {
				return "", nil, "", fail("Editor", "open.editor")
			}
			return n, append(a, dir), n, nil
		}
		for _, n := range editorCandidates {
			if _, err := lookTool(n); err == nil {
				return n, []string{dir}, n, nil
			}
		}
		return "", nil, "", fail("Editor", "open.editor")
	case "terminal":
		if n, a, ok := custom("open.terminal"); ok {
			if _, err := lookTool(n); err != nil {
				return "", nil, "", fail("Terminal", "open.terminal")
			}
			return n, a, n, nil // it starts in dir
		}
		switch runtime.GOOS {
		case "windows":
			if _, err := lookTool("wt.exe"); err == nil {
				return "wt.exe", []string{"-d", dir}, "wt", nil
			}
			return "cmd", []string{"/c", "start", "cmd.exe"}, "cmd", nil
		case "darwin":
			return "open", []string{"-a", "Terminal", dir}, "Terminal", nil
		}
		for _, n := range []string{"x-terminal-emulator", "gnome-terminal", "konsole", "xfce4-terminal", "xterm"} {
			if _, err := lookTool(n); err == nil {
				return n, nil, n, nil
			}
		}
		return "", nil, "", fail("Terminal", "open.terminal")
	case "files":
		if n, a, ok := custom("open.files"); ok {
			if _, err := lookTool(n); err != nil {
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
		if _, err := lookTool("xdg-open"); err == nil {
			return "xdg-open", []string{dir}, "xdg-open", nil
		}
		return "", nil, "", fail("File manager", "open.files")
	}
	return "", nil, "", usageErrorf("--in must be one of %s", strings.Join(openTargets, ", "))
}

// openIn starts the tool for a target in the folder of an instance.
func openIn(e *engine.Engine, inst *engine.Instance, target string) (string, error) {
	if _, err := os.Stat(inst.Workspace); err != nil {
		return "", fmt.Errorf("the folder of %s no longer exists: %s", worktreeArg(inst), inst.Workspace)
	}
	name, args, label, err := toolFor(e, target, inst.Workspace)
	if err != nil {
		return "", err
	}
	if err := startTool(inst.Workspace, name, args...); err != nil {
		return "", fmt.Errorf("could not start %s: %v", name, err)
	}
	return label, nil
}
