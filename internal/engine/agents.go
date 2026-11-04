package engine

import (
	"fmt"
	"time"

	"github.com/ElMaxter99/Theo/internal/agents"
	"github.com/ElMaxter99/Theo/internal/prefs"
)

// DataDir is where Theo keeps its own files.
func (e *Engine) DataDir() string { return e.dataDir }

func (e *Engine) agentStore() agents.Store { return agents.Store{Dir: e.dataDir} }

// SetAgentState records what the agent of a worktree is doing, as a hook of
// the agent reported it. An empty state forgets it.
func (e *Engine) SetAgentState(path, state, agent, session string) error {
	if state == "" {
		return e.agentStore().Clear(path)
	}
	switch state {
	case agents.Working, agents.Waiting, agents.Done:
	default:
		return fmt.Errorf("unknown agent state %q (working, waiting or done)", state)
	}
	return e.agentStore().Set(path, agents.State{State: state, Source: agents.SourceHook, Agent: agent, Session: session, Since: e.now().UTC().Truncate(time.Second)})
}

// agentResolver tells the state of the agent of each worktree. It lists the
// processes once.
type agentResolver struct {
	store agents.Store
	procs []agents.Process
}

func (e *Engine) agentResolver() agentResolver {
	return agentResolver{store: e.agentStore(), procs: agents.Processes()}
}

// of returns the state of the worktree's agent, or nil when nothing is known.
func (r agentResolver) of(path string) *agents.State {
	hook, ok := r.store.Get(path)
	st := agents.Resolve(hook, ok, agents.InWorktree(r.procs, path))
	if st.State == agents.Unknown {
		return nil
	}
	return &st
}

// agentWarnings says when an agent needs the person or has finished.
// Estimates from processes never warn: they cannot tell.
func agentWarnings(st *agents.State) []StatusWarning {
	if st == nil || st.Source != agents.SourceHook {
		return nil
	}
	switch st.State {
	case agents.Waiting:
		return []StatusWarning{{Kind: "agent-waiting", Message: "the agent in this worktree is waiting for you"}}
	case agents.Done:
		return []StatusWarning{{Kind: "agent-done", Message: "the agent in this worktree finished"}}
	}
	return nil
}

// Prefs is Theo's preferences store (tools to open, version check).
func (e *Engine) Prefs() prefs.Store { return prefs.Store{Dir: e.dataDir} }

// AgentStates returns what the agent of each worktree is doing; worktrees
// with nothing known are left out. It lists the processes once.
func (e *Engine) AgentStates(paths []string) map[string]*agents.State {
	r := e.agentResolver()
	out := map[string]*agents.State{}
	for _, p := range paths {
		if st := r.of(p); st != nil {
			out[p] = st
		}
	}
	return out
}
