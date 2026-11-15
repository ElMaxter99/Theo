// Package notify tells the person, with a desktop notification, that an agent
// waits for them or has finished. The desktop app runs it; it is off unless
// the person set `notify.agent`.
package notify

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/agents"
)

// Message is one notification.
type Message struct{ Title, Body string }

// groupFrom is how many changes at once become a single notification.
const groupFrom = 3

// Tracker remembers the state it saw of each worktree and says what is new.
// The first look only records: nothing that was already waiting when the app
// opened is announced.
type Tracker struct {
	seen   map[string]stamp
	primed bool
	// Spanish picks the language of the text.
	Spanish bool
}

type stamp struct {
	state string
	since time.Time
}

type change struct{ label, agent, state string }

// Next returns the notifications for the states now. mode is the value of
// notify.agent: off, waiting or all. Only states from hooks are given, so an
// estimate never notifies. label names a worktree path for the person.
func (t *Tracker) Next(mode string, states map[string]agents.State, label func(path string) string) []Message {
	if t.seen == nil {
		t.seen = map[string]stamp{}
	}
	var changes []change
	next := map[string]stamp{}
	for path, st := range states {
		cur := stamp{st.State, st.Since}
		next[path] = cur
		if !t.primed || t.seen[path] == cur {
			continue
		}
		if st.State == agents.Waiting || (st.State == agents.Done && mode == "all") {
			changes = append(changes, change{label(path), st.Agent, st.State})
		}
	}
	t.seen, t.primed = next, true
	if mode != "waiting" && mode != "all" || len(changes) == 0 {
		return nil
	}
	if len(changes) >= groupFrom {
		return []Message{t.group(changes)}
	}
	out := make([]Message, 0, len(changes))
	for _, c := range changes {
		out = append(out, t.single(c))
	}
	return out
}

func agentName(a string) string {
	switch a {
	case "claude":
		return "Claude Code"
	case "copilot":
		return "GitHub Copilot"
	case "":
		return "The agent"
	}
	return a
}

func (t *Tracker) single(c change) Message {
	name := agentName(c.agent)
	if t.Spanish {
		if c.agent == "" {
			name = "El agente"
		}
		if c.state == agents.Waiting {
			return Message{"Te espera · " + c.label, name + " necesita tu respuesta."}
		}
		return Message{"Terminó · " + c.label, name + " terminó. Abre el worktree para revisarlo."}
	}
	if c.state == agents.Waiting {
		return Message{"Waiting for you · " + c.label, name + " needs your answer."}
	}
	return Message{"Done · " + c.label, name + " finished. Open the worktree to review it."}
}

func (t *Tracker) group(cs []change) Message {
	labels := make([]string, len(cs))
	for i, c := range cs {
		labels[i] = c.label
	}
	if len(labels) > 4 {
		labels = append(labels[:4], "...")
	}
	list := strings.Join(labels, ", ")
	if t.Spanish {
		return Message{fmt.Sprintf("%d agentes te esperan", len(cs)), list}
	}
	return Message{fmt.Sprintf("%d agents need you", len(cs)), list}
}

// Deps are what Watch needs from the app.
type Deps struct {
	Mode     func() string                  // value of notify.agent
	States   func() map[string]agents.State // states from hooks
	Label    func(path string) string       // "project / worktree" for a path; called only to notify
	Send     func(Message) error            // shows one notification
	Interval time.Duration                  // between looks; default 3 s
	Spanish  bool
	// Skip says the person is already looking at Theo's window, so nothing is
	// shown now. Optional. What changed meanwhile counts as seen.
	Skip    func() bool
	OnError func(error)
}

// Watch looks at the agents until ctx ends and sends what is new.
func Watch(ctx context.Context, d Deps) {
	if d.Interval <= 0 {
		d.Interval = 3 * time.Second
	}
	t := &Tracker{Spanish: d.Spanish}
	tick := time.NewTicker(d.Interval)
	defer tick.Stop()
	for {
		mode := d.Mode()
		if mode == "waiting" || mode == "all" {
			msgs := t.Next(mode, d.States(), d.Label)
			if d.Skip != nil && d.Skip() {
				msgs = nil
			}
			for _, m := range msgs {
				if err := d.Send(m); err != nil && d.OnError != nil {
					d.OnError(err)
				}
			}
		} else {
			// Off: keep the baseline current so turning it on announces nothing old.
			t.Next("off", d.States(), func(string) string { return "" })
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
