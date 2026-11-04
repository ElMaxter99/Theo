// Package agents records what the coding agents working in worktrees are
// doing: working, waiting for the person, or done. The state comes from the
// hooks of Claude Code (`theo hook agent-state`), which is exact, or from the
// processes running in the worktree, which is an estimate.
package agents

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/ElMaxter99/Theo/internal/filelock"
)

// States of an agent in a worktree.
const (
	Working = "working"
	Waiting = "waiting" // it needs the person
	Done    = "done"    // it finished its turn
	Unknown = "unknown" // no signal
)

// Sources of a state.
const (
	SourceHook    = "hook"
	SourceProcess = "process" // an estimate: an agent process runs there, nothing says what it does
)

// FileName is the state file, in Theo's data directory.
const FileName = "agents.json"

// StaleAfter is how long a state from a hook is believed. An agent that
// crashed or whose session was killed never sends its last event.
var StaleAfter = 12 * time.Hour

// State is what is known about the agent of one worktree.
type State struct {
	State   string    `json:"state"`
	Source  string    `json:"source"`
	Agent   string    `json:"agent,omitempty"`   // "claude" for hooks; the program name for processes
	Session string    `json:"session,omitempty"` // session id given by the hook
	Since   time.Time `json:"since"`
}

// Store keeps the states by worktree path.
type Store struct{ Dir string }

type file struct {
	Worktrees map[string]State `json:"worktrees"`
}

func (s Store) path() string { return filepath.Join(s.Dir, FileName) }

func (s Store) read() (file, error) {
	f := file{Worktrees: map[string]State{}}
	data, err := os.ReadFile(s.path())
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return file{Worktrees: map[string]State{}}, nil // a damaged file is only a lost estimate
	}
	if f.Worktrees == nil {
		f.Worktrees = map[string]State{}
	}
	return f, nil
}

// Set records a state for a worktree.
func (s Store) Set(worktree string, st State) error {
	return s.update(func(f *file) { f.Worktrees[key(worktree)] = st })
}

// Clear forgets the state of a worktree.
func (s Store) Clear(worktree string) error {
	return s.update(func(f *file) { delete(f.Worktrees, key(worktree)) })
}

func (s Store) update(fn func(*file)) error {
	if s.Dir == "" {
		return errors.New("no data directory for agent states")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	unlock, err := filelock.Lock(s.path() + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	f, err := s.read()
	if err != nil {
		return err
	}
	fn(&f)
	// Old states go with the write that would keep them.
	for k, v := range f.Worktrees {
		if time.Since(v.Since) > 7*24*time.Hour {
			delete(f.Worktrees, k)
		}
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return filelock.WriteFile(s.path(), append(data, '\n'), 0o600)
}

// Get returns the state of a worktree from hooks, or false. A state older
// than StaleAfter is not believed.
func (s Store) Get(worktree string) (State, bool) {
	f, err := s.read()
	if err != nil {
		return State{}, false
	}
	st, ok := f.Worktrees[key(worktree)]
	if !ok || time.Since(st.Since) > StaleAfter {
		return State{}, false
	}
	return st, true
}

// Counts totals agents by state.
func Counts(states []State) map[string]int {
	out := map[string]int{}
	for _, st := range states {
		out[st.State]++
	}
	return out
}

// Resolve gives the state of a worktree: the hook's if there is one, else the
// estimate from processes (or Unknown).
func Resolve(hook State, haveHook bool, procs []string) State {
	switch {
	case haveHook:
		return hook
	case len(procs) > 0:
		sort.Strings(procs)
		return State{State: Working, Source: SourceProcess, Agent: procs[0]}
	}
	return State{State: Unknown}
}

func key(path string) string {
	p := filepath.Clean(path)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return filepath.ToSlash(p)
}
