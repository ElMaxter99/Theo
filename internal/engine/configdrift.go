package engine

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/ElMaxter99/Theo/internal/instances"
)

// WarnComposeChanged is the kind of the warning Status and the web interface
// give when the Compose configuration of an instance with containers is not
// the one it was last started with (FR-014).
const WarnComposeChanged = "compose-changed"

// ConfigChange says that the Compose configuration of an instance changed
// since its last successful up or recreate.
type ConfigChange struct {
	Since     time.Time // when that up or recreate ran
	Operation string    // "up" or "recreate"
}

// ConfigChanged compares the configuration Compose resolves now for inst
// with the one recorded at its last successful up or recreate. It returns nil
// when they match, or when either is unknown: an instance started before
// Theo recorded digests, or one never started by Theo. It reads only Theo's
// history; it does not look at containers, so callers ask only for
// instances that have some.
func (e *Engine) ConfigChanged(inst *Instance) *ConfigChange {
	if inst == nil || inst.config == nil || inst.config.Digest == "" {
		return nil
	}
	starts, err := e.lastStarts()
	if err != nil {
		return nil
	}
	ev, ok := starts[filepath.Clean(inst.Workspace)]
	if !ok {
		for ws, candidate := range starts {
			if samePath(ws, inst.Workspace) {
				ev, ok = candidate, true
				break
			}
		}
	}
	if !ok || ev.ConfigDigest == "" || ev.ConfigDigest == inst.config.Digest {
		return nil
	}
	return &ConfigChange{Since: ev.Time, Operation: ev.Operation}
}

// ComposeChangedWarning is the warning for a ConfigChange of inst; selector
// names the worktree in the suggested command.
func ComposeChangedWarning(inst *Instance, ch *ConfigChange, selector string) StatusWarning {
	return StatusWarning{
		Kind: WarnComposeChanged,
		Message: fmt.Sprintf("the Compose configuration changed since the last %s (%s): the containers still run the previous one",
			ch.Operation, ch.Since.Local().Format("2006-01-02 15:04")),
		Fix: fmt.Sprintf("theo up %s -w %s", inst.Project, selector),
	}
}

func (e *Engine) lastStarts() (map[string]instances.Event, error) {
	if s, ok := e.settings.(interface {
		LastStarts() (map[string]instances.Event, error)
	}); ok {
		return s.LastStarts()
	}
	events, err := e.settings.Events()
	if err != nil {
		return nil, err
	}
	return instances.LastStartsOf(events), nil
}

// recordStart records an up or recreate, with the digest of the
// configuration it started from when it succeeded.
func (e *Engine) recordStart(inst *Instance, op string, err error) {
	digest := ""
	if err == nil && inst.config != nil {
		digest = inst.config.Digest
	}
	e.recordWith(inst, op, err, digest)
}
