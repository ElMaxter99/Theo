package engine

import (
	"github.com/ElMaxter99/Theo/internal/instances"
	"github.com/ElMaxter99/Theo/internal/ports"
	"github.com/ElMaxter99/Theo/internal/registry"
)

// DiscardWrites makes the engine keep nothing of what it would record:
// registered projects, instance settings, reserved ports and the history are
// read as usual, and saves are dropped. `theo --explain` uses it, so that
// explaining an operation changes nothing, not even Theo's own state.
func (e *Engine) DiscardWrites() {
	e.store = readOnlyStore{e.store}
	e.portStore = readOnlyPorts{e.portStore}
	e.settings = readOnlySettings{e.settings}
}

type readOnlyStore struct{ Store }

func (readOnlyStore) Save([]registry.Project) error { return nil }

type readOnlyPorts struct{ PortStore }

func (readOnlyPorts) Save(ports.State) error { return nil }

type readOnlySettings struct{ SettingsStore }

func (readOnlySettings) Save(instances.State) error   { return nil }
func (readOnlySettings) Append(instances.Event) error { return nil }
