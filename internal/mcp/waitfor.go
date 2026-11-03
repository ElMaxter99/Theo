package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
)

// wait_for conditions.
const (
	untilRunning = "running" // every expected service runs, and none is still starting its health check
	untilStopped = "stopped" // nothing runs (stopped or no containers)
	untilChanged = "changed" // the state differs from the one at the call
)

const (
	defaultWait  = 60
	maxWait      = 120
	waitInterval = 2 * time.Second
)

func waitForTool() *tool {
	return &tool{
		name: "wait_for", title: "Wait for an instance", readOnly: true,
		description: fmt.Sprintf("Waits until a worktree's instance is running (every service up and healthy), stopped, or its state changed, and returns the state it reached. "+
			"Use it after up, restart or stop instead of calling status in a loop. It returns early when waiting for running and the instance fails "+
			"(a container exits with an error or is unhealthy), with reached=false. Waits at most timeout_seconds (default %d, at most %d); it changes nothing.", defaultWait, maxWait),
		params: []param{pProject, pWorktree,
			{name: "until", kind: "string", description: "What to wait for (default running).", enum: []string{untilRunning, untilStopped, untilChanged}},
			{name: "timeout_seconds", kind: "integer", description: fmt.Sprintf("How long to wait at most (default %d, at most %d).", defaultWait, maxWait)},
		},
		run: runWaitFor,
	}
}

// waitState is the state of the instance as wait_for judges it.
type waitState struct {
	State    string
	Starting []string // services whose health check has not passed yet
	Failed   []string // services that exited with an error or are unhealthy
	Running  int      // containers running or restarting
}

func judge(containers []compose.Container, expected []string, dockerOK bool) waitState {
	ws := waitState{State: engine.InstanceState(containers, expected, dockerOK)}
	for _, c := range containers {
		if c.State == "running" || c.State == "restarting" {
			ws.Running++
		}
		switch {
		case c.Health == "starting":
			ws.Starting = append(ws.Starting, c.Service)
		case c.Health == "unhealthy", c.State == "dead", c.State == "exited" && c.ExitCode != 0:
			ws.Failed = append(ws.Failed, c.Service)
		}
	}
	return ws
}

// reached says whether ws satisfies until (start is the state at the call),
// and whether waiting longer is pointless.
func reached(until string, start, ws waitState) (done, ok bool) {
	switch until {
	case untilStopped:
		// Containers stopped by a signal exit with an error code, so the
		// state may read unhealthy: what counts is that nothing runs.
		stopped := ws.State != engine.InstUnknown && ws.Running == 0
		return stopped, stopped
	case untilChanged:
		return ws.State != start.State, ws.State != start.State
	default:
		if ws.State == engine.InstUnhealthy || len(ws.Failed) > 0 {
			return true, false
		}
		ok := ws.State == engine.InstRunning && len(ws.Starting) == 0
		return ok, ok
	}
}

func runWaitFor(ctx context.Context, s *Server, a args) (*result, error) {
	e := s.opts.Engine
	inst, err := s.instance(ctx, e, a)
	if err != nil {
		return nil, err
	}
	until := a.str("until")
	if until == "" {
		until = untilRunning
	}
	timeout := a.int("timeout_seconds", defaultWait)
	if timeout < 1 || timeout > maxWait {
		return nil, fmt.Errorf("%w: timeout_seconds must be from 1 to %d", errBadArgs, maxWait)
	}
	look := func() waitState {
		containers, err := e.Containers(ctx, inst)
		return judge(containers, e.ExpectedServices(inst), err == nil)
	}
	began := time.Now()
	deadline := began.Add(time.Duration(timeout) * time.Second)
	start := look()
	ws, done, good := start, false, false
	if until != untilChanged {
		done, good = reached(until, start, ws)
	}
	pr := progressOf(ctx)
	for !done {
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(waitInterval):
		}
		ws = look()
		done, good = reached(until, start, ws)
		pr.notify(fmt.Sprintf("%s: %s", inst.Compose, ws.State), false)
	}
	elapsed := time.Since(began).Round(time.Second)
	var text string
	switch {
	case good && until == untilStopped:
		text = fmt.Sprintf("%s is stopped: no container runs (waited %s).", inst.Compose, elapsed)
	case good:
		text = fmt.Sprintf("%s is %s (waited %s).", inst.Compose, ws.State, elapsed)
	case done:
		text = fmt.Sprintf("%s failed while waiting for %s: %s (services: %v). Look at its logs.", inst.Compose, until, ws.State, ws.Failed)
	default:
		text = fmt.Sprintf("%s did not reach %s in %s: it is %s.", inst.Compose, until, elapsed, ws.State)
		if len(ws.Starting) > 0 {
			text += fmt.Sprintf(" Still starting: %v.", ws.Starting)
		}
	}
	return ok(text, map[string]any{
		"compose_project": inst.Compose, "until": until, "reached": good, "timed_out": !done,
		"state": ws.State, "initial_state": start.State, "starting": nonNil(ws.Starting), "failed": nonNil(ws.Failed),
		"waited_seconds": int(elapsed / time.Second),
	}), nil
}
