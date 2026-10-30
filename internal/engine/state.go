package engine

import "github.com/ElMaxter99/Theo/internal/compose"

// Instance states, as `theo ps` and the graphical interface report them.
// They are derived from the containers Compose lists; see InstanceState.
const (
	// InstRunning: every expected service has a running container and
	// nothing has failed. Containers that completed (exit 0, like a
	// migration job) and healthchecks that are still starting do not count
	// against it.
	InstRunning = "running"
	// InstPartial: some of the expected services run and some do not.
	InstPartial = "partial"
	// InstUnhealthy: a container reports Health=unhealthy, is restarting,
	// or exited with a status other than 0. It wins over the other states.
	InstUnhealthy = "unhealthy"
	// InstStopped: containers exist and none runs.
	InstStopped = "stopped"
	// InstAbsent: no containers: never started, or removed by `down`.
	InstAbsent = "absent"
	// InstUnknown: Docker could not be asked.
	InstUnknown = "unknown"
)

// InstanceState derives the state of an instance from the containers
// Compose lists for it. expected are the services that should be running
// (the ones without a Compose profile; nil means: judge by the containers
// alone). dockerOK is false when Docker could not be asked, which makes the
// state unknown. The function is pure: it is the single rule behind
// `theo ps` and the API.
func InstanceState(containers []compose.Container, expected []string, dockerOK bool) string {
	if !dockerOK {
		return InstUnknown
	}
	if len(containers) == 0 {
		return InstAbsent
	}
	running := map[string]bool{}
	completed := map[string]bool{}
	other := false // created, paused, removing...
	for _, c := range containers {
		switch {
		case c.Health == "unhealthy", c.State == "restarting", c.State == "dead":
			return InstUnhealthy
		case c.State == "exited" && c.ExitCode != 0:
			return InstUnhealthy
		case c.State == "running":
			running[c.Service] = true
		case c.State == "exited": // exit status 0
			completed[c.Service] = true
		default:
			other = true
		}
	}
	if len(running) == 0 {
		return InstStopped
	}
	if other {
		return InstPartial
	}
	for _, name := range expected {
		if !running[name] && !completed[name] {
			return InstPartial
		}
	}
	return InstRunning
}

// expectedServices lists the services of a configuration that start without
// a profile.
func expectedServices(cfg *compose.Project) []string {
	var out []string
	for _, name := range compose.SortedKeys(cfg.Services) {
		if len(cfg.Services[name].Profiles) == 0 {
			out = append(out, name)
		}
	}
	return out
}

// ExpectedServices lists the services of an instance that start without a
// Compose profile.
func (e *Engine) ExpectedServices(inst *Instance) []string {
	if inst.config == nil {
		return nil
	}
	return expectedServices(inst.config)
}
