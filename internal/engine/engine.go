// Package engine is Theo's local engine. It holds the domain logic and
// exposes the operations that every interface (the CLI today, a graphical
// interface later) builds on. It must not depend on any interface.
package engine

// Version is the engine version. It is overridden at build time with
// -ldflags "-X github.com/ElMaxter99/Theo/internal/engine.Version=...".
var Version = "0.0.0-dev"

// Engine is the entry point to Theo's operations.
type Engine struct{}

// New returns a ready-to-use engine.
func New() *Engine {
	return &Engine{}
}

// Version reports the engine version.
func (e *Engine) Version() string {
	return Version
}
