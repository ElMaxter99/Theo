// Package tools is the frame for Theo's tools: self-contained features with
// their own scope (the first is the screen capture, "before and after").
// A tool is described here so the CLI, the API and, later, the interface list
// and run all of them the same way. See roadmap/plan-herramienta-capturas.md.
package tools

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Tool describes one tool.
type Tool struct {
	// ID is what a person types: `theo tool <id> ...`. Lowercase, no spaces.
	ID string `json:"id"`
	// Title and Summary are shown in lists, in English (the interface translates them).
	Title   string `json:"title"`
	Summary string `json:"summary"`
	// Scope groups tools ("capture", ...).
	Scope string `json:"scope"`
	// Commands are the subcommands that work today.
	Commands []string `json:"commands"`
	// Planned are subcommands described in the roadmap but not built yet.
	Planned []string `json:"planned,omitempty"`
}

var (
	mu       sync.RWMutex
	registry = map[string]Tool{}
)

// Register adds a tool. It panics on a duplicate or an invalid ID: tools are
// registered by init functions, so a mistake shows up in the first test.
func Register(t Tool) {
	if err := validate(t); err != nil {
		panic("tools: " + err.Error())
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := registry[t.ID]; ok {
		panic("tools: duplicate tool " + t.ID)
	}
	registry[t.ID] = t
}

func validate(t Tool) error {
	if t.ID == "" {
		return errors.New("a tool needs an ID")
	}
	for _, r := range t.ID {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return fmt.Errorf("tool ID %q must be lowercase letters, digits and dashes", t.ID)
		}
	}
	if t.Title == "" || t.Scope == "" {
		return fmt.Errorf("tool %q needs a title and a scope", t.ID)
	}
	return nil
}

// List returns the registered tools sorted by ID.
func List() []Tool {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Tool, 0, len(registry))
	for _, t := range registry {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Get finds a tool by ID.
func Get(id string) (Tool, bool) {
	mu.RLock()
	defer mu.RUnlock()
	t, ok := registry[id]
	return t, ok
}
