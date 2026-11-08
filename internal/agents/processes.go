package agents

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ElMaxter99/Theo/internal/execx"
)

// KnownAgents are the program names Theo takes for coding agents, in lower
// case without extension.
var KnownAgents = []string{"claude", "codex", "aider", "gemini", "goose", "opencode", "amp", "cursor-agent", "copilot"}

// IsAgent reports whether a program name is a known coding agent.
func IsAgent(program string) (string, bool) {
	name := strings.ToLower(filepath.Base(strings.ReplaceAll(program, `\`, "/")))
	name = strings.TrimSuffix(name, ".exe")
	for _, a := range KnownAgents {
		if name == a {
			return a, true
		}
	}
	return "", false
}

// IsAgentScript is IsAgent for the script a node program runs: codex.js,
// claude.mjs and so on.
func IsAgentScript(arg string) (string, bool) {
	for _, ext := range []string{".js", ".mjs", ".cjs"} {
		if strings.HasSuffix(strings.ToLower(arg), ext) {
			return IsAgent(arg[:len(arg)-len(ext)])
		}
	}
	return IsAgent(arg)
}

// Process is a running program and the folder it works in.
type Process struct {
	Program string
	Cwd     string
	// Args is the command line, when the system gives no working folder
	// (Windows): a worktree path in it counts as being in that worktree.
	Args string
}

// Processes lists running agent programs with their working folder. Linux
// reads /proc, macOS asks `ps` and `lsof`, and Windows asks PowerShell for the
// command lines (Windows gives no working folder of another process, so only
// an agent started with the worktree's path in its arguments is found). A
// failure returns nil and the state stays unknown unless a hook says it. The
// detection is always an estimate.
var Processes = func() []Process {
	switch runtime.GOOS {
	case "linux":
		return linuxProcesses()
	case "darwin":
		return slowScan.get(darwinProcesses)
	case "windows":
		return slowScan.get(windowsProcesses)
	}
	return nil
}

// slowScan serves the process list of systems where listing it means running
// other programs (ps and lsof, PowerShell: a second or more). It never makes
// the caller wait: it returns the last list and refreshes it in the
// background when it is older than scanEvery, so the first call after a long
// pause sees no agents and the next ones see them.
var slowScan scanCache

const scanEvery = 10 * time.Second

type scanCache struct {
	mu       sync.Mutex
	list     []Process
	at       time.Time
	scanning bool
}

func (c *scanCache) get(scan func() []Process) []Process {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.scanning && time.Since(c.at) > scanEvery {
		c.scanning = true
		go func() {
			list := scan()
			c.mu.Lock()
			c.list, c.at, c.scanning = list, time.Now(), false
			c.mu.Unlock()
		}()
	}
	return c.list
}

func darwinProcesses() []Process {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	ps, err := execx.Run(ctx, "", "ps", "-axo", "pid=,args=")
	if err != nil {
		return nil
	}
	found := ParseUnixPS(string(ps))
	if len(found) == 0 {
		return nil
	}
	pids := make([]string, 0, len(found))
	for _, f := range found {
		pids = append(pids, f.PID)
	}
	lsof, err := execx.Run(ctx, "", "lsof", "-a", "-d", "cwd", "-Fpn", "-p", strings.Join(pids, ","))
	if err != nil && len(lsof) == 0 {
		return nil // lsof exits 1 when some pid has gone; its output is still good
	}
	cwds := ParseLsofCwd(string(lsof))
	var out []Process
	for _, f := range found {
		if cwd, ok := cwds[f.PID]; ok {
			out = append(out, Process{Program: f.Program, Cwd: cwd})
		}
	}
	return out
}

func windowsProcesses() []Process {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	script := `Get-CimInstance Win32_Process | ForEach-Object { '{0}|{1}' -f $_.Name, $_.CommandLine }`
	out, err := execx.Run(ctx, "", "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if err != nil {
		return nil
	}
	return ParseWindowsProcesses(string(out))
}

// UnixProc is an agent process found in the output of `ps`.
type UnixProc struct{ PID, Program string }

// ParseUnixPS reads `ps -axo pid=,args=` and keeps the known agents, also when
// they run as a script of node.
func ParseUnixPS(out string) []UnixProc {
	var res []UnixProc
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if _, err := strconv.Atoi(fields[0]); err != nil {
			continue
		}
		args := fields[1:]
		if name, ok := IsAgent(args[0]); ok {
			res = append(res, UnixProc{fields[0], name})
			continue
		}
		if strings.ToLower(filepath.Base(args[0])) == "node" {
			for _, a := range args[1:] {
				if name, ok := IsAgentScript(a); ok {
					res = append(res, UnixProc{fields[0], name})
					break
				}
			}
		}
	}
	return res
}

// ParseLsofCwd reads `lsof -Fpn` output (p<pid> then n<path>) into pid -> folder.
func ParseLsofCwd(out string) map[string]string {
	res := map[string]string{}
	pid := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "p"):
			pid = line[1:]
		case strings.HasPrefix(line, "n") && pid != "":
			res[pid] = line[1:]
		}
	}
	return res
}

// ParseWindowsProcesses reads lines of "<name>|<command line>" and keeps the
// known agents, also when they run as a script of node.
func ParseWindowsProcesses(out string) []Process {
	var res []Process
	for _, line := range strings.Split(out, "\n") {
		name, args, ok := strings.Cut(strings.TrimRight(line, "\r"), "|")
		if !ok {
			continue
		}
		if a, ok := IsAgent(name); ok {
			res = append(res, Process{Program: a, Args: args})
			continue
		}
		if strings.EqualFold(strings.TrimSuffix(name, ".exe"), "node") {
			for _, arg := range strings.Fields(args)[min(1, len(strings.Fields(args))):] {
				if a, ok := IsAgentScript(strings.Trim(arg, `"`)); ok {
					res = append(res, Process{Program: a, Args: args})
					break
				}
			}
		}
	}
	return res
}

func linuxProcesses() []Process {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []Process
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		dir := filepath.Join("/proc", e.Name())
		comm, err := os.ReadFile(filepath.Join(dir, "comm"))
		if err != nil {
			continue
		}
		program := strings.TrimSpace(string(comm))
		if _, ok := IsAgent(program); !ok {
			// node-based agents show up as "node": look at the script name.
			if program != "node" {
				continue
			}
			cmdline, _ := os.ReadFile(filepath.Join(dir, "cmdline"))
			found := ""
			for _, arg := range strings.Split(string(cmdline), "\x00") {
				if a, ok := IsAgentScript(arg); ok {
					found = a
					break
				}
			}
			if found == "" {
				continue
			}
			program = found
		}
		cwd, err := os.Readlink(filepath.Join(dir, "cwd"))
		if err != nil {
			continue
		}
		out = append(out, Process{Program: program, Cwd: cwd})
	}
	return out
}

// InWorktree returns the agent programs whose working folder is inside the
// worktree (including its subfolders).
func InWorktree(procs []Process, worktree string) []string {
	root := key(worktree)
	var out []string
	seen := map[string]bool{}
	for _, p := range procs {
		c := key(p.Cwd)
		inside := p.Cwd != "" && (c == root || strings.HasPrefix(c, strings.TrimSuffix(root, "/")+"/"))
		if !inside && p.Cwd == "" && p.Args != "" {
			inside = argsMention(p.Args, worktree)
		}
		if inside {
			if name, ok := IsAgent(p.Program); ok && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

// argsMention reports whether a command line names the worktree folder, in
// either slash style and ignoring case (Windows paths).
func argsMention(args, worktree string) bool {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "\\", "/")) }
	return strings.Contains(norm(args), norm(filepath.Clean(worktree)))
}
