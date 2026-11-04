package agents

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// KnownAgents are the program names Theo takes for coding agents, in lower
// case without extension.
var KnownAgents = []string{"claude", "codex", "aider", "gemini", "goose", "opencode", "amp", "cursor-agent", "copilot"}

// IsAgent reports whether a program name is a known coding agent.
func IsAgent(program string) (string, bool) {
	name := strings.ToLower(filepath.Base(program))
	name = strings.TrimSuffix(name, ".exe")
	for _, a := range KnownAgents {
		if name == a {
			return a, true
		}
	}
	return "", false
}

// Process is a running program and the folder it works in.
type Process struct {
	Program string
	Cwd     string
}

// Processes lists running programs with their working folder. Only Linux is
// supported (it reads /proc); elsewhere it returns nil, and the state stays
// unknown unless a hook says it. The detection is an estimate.
var Processes = func() []Process {
	if runtime.GOOS != "linux" {
		return nil
	}
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
				if a, ok := IsAgent(arg); ok {
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
		if c == root || strings.HasPrefix(c, strings.TrimSuffix(root, "/")+"/") {
			if name, ok := IsAgent(p.Program); ok && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}
