package execx

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// This file bounds the commands that only read (Run, RunChange): how many
// run at once in the process, how long each may take, and what happens
// after one hangs. Long-running streams (Stream*, `docker compose up`,
// `logs -f`) are never limited here.

// DefaultMaxConcurrent is how many Run/RunChange commands run at the same
// time in one process unless SetMaxConcurrent says otherwise.
const DefaultMaxConcurrent = 8

// ErrTimeout is wrapped by the error of a command that was killed because it
// ran longer than the Timeout of its Limits.
var ErrTimeout = errors.New("timed out")

// ErrUnresponsive is wrapped by the error of a command that was not started
// because an earlier one of the same tool timed out a moment ago (see
// Breaker). It spares the caller from waiting for a tool that is stuck.
var ErrUnresponsive = errors.New("not responding")

var (
	slotsMu sync.Mutex
	slots   = newSlots(DefaultMaxConcurrent)
)

type slotSet struct{ ch chan struct{} }

func newSlots(n int) *slotSet { return &slotSet{ch: make(chan struct{}, n)} }

// SetMaxConcurrent sets how many Run/RunChange commands may run at once in
// this process; the others wait for a free slot (or for their context).
// n <= 0 restores the default. Call it before starting work: commands that
// already hold a slot keep it.
func SetMaxConcurrent(n int) {
	if n <= 0 {
		n = DefaultMaxConcurrent
	}
	slotsMu.Lock()
	slots = newSlots(n)
	slotsMu.Unlock()
}

func currentSlots() *slotSet {
	slotsMu.Lock()
	defer slotsMu.Unlock()
	return slots
}

// acquire waits for a free slot and returns the function that frees it.
func acquire(ctx context.Context) (func(), error) {
	s := currentSlots()
	select {
	case s.ch <- struct{}{}:
		return func() { <-s.ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Limits are carried by a context (WithLimits) and apply to every Run and
// RunChange made with it.
type Limits struct {
	// Timeout kills each command that runs longer; 0 means no limit.
	Timeout time.Duration
	// Breaker, when set, stops starting commands of a tool for a while after
	// one of them timed out.
	Breaker *Breaker
}

type limitsKey struct{}

// WithLimits returns a context whose Run and RunChange calls obey l.
func WithLimits(ctx context.Context, l Limits) context.Context {
	return context.WithValue(ctx, limitsKey{}, l)
}

func limitsOf(ctx context.Context) Limits {
	l, _ := ctx.Value(limitsKey{}).(Limits)
	return l
}

// maxBreakerPause caps how long a repeatedly stuck tool is left alone.
const maxBreakerPause = time.Minute

// Breaker remembers that a tool timed out. For Cooldown afterwards, commands
// of that tool fail at once with ErrUnresponsive instead of each waiting for
// its own timeout, so a stuck Docker costs one timeout, not one per call.
// While the tool keeps timing out, each pause is twice the previous one (up
// to a minute); a command that works closes the breaker. Docker is tracked as
// a whole (a stuck daemon stalls every command); other tools are tracked per
// directory, so one stuck repository does not stop the rest.
type Breaker struct {
	Cooldown time.Duration

	mu    sync.Mutex
	state map[string]breakerState
	now   func() time.Time
}

type breakerState struct {
	until   time.Time
	trips   int  // consecutive timeouts
	probing bool // one command is trying the tool again
}

// NewBreaker returns a Breaker that stays open for cooldown after a timeout.
func NewBreaker(cooldown time.Duration) *Breaker {
	return &Breaker{Cooldown: cooldown, state: map[string]breakerState{}, now: time.Now}
}

func breakerKey(name, dir string) string {
	if name == "docker" {
		return name
	}
	return name + "|" + dir
}

func (b *Breaker) refusal(name string, left time.Duration) error {
	return fmt.Errorf("%s did not answer a moment ago; not trying again for %s: %w", name, left.Round(time.Second), ErrUnresponsive)
}

// peek fails while the breaker is open. It admits nothing: it only spares a
// command from queueing for a slot it would not be allowed to use.
func (b *Breaker) peek(name, dir string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if st, ok := b.state[breakerKey(name, dir)]; ok {
		if left := st.until.Sub(b.now()); left > 0 {
			return b.refusal(name, left)
		}
	}
	return nil
}

// check admits a command: it fails while the breaker is open, and once the
// pause is over lets exactly one command through to try the tool again
// (the others keep failing until it finishes).
func (b *Breaker) check(name, dir string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := breakerKey(name, dir)
	st, ok := b.state[k]
	if !ok {
		return nil
	}
	if left := st.until.Sub(b.now()); left > 0 {
		return b.refusal(name, left)
	}
	if st.probing {
		return b.refusal(name, 0)
	}
	st.probing = true
	b.state[k] = st
	return nil
}

// trip records a timeout. Commands that were already running when the first
// one timed out are not new evidence: only the first timeout of an episode,
// or the failure of a probe, opens (and lengthens) the pause.
func (b *Breaker) trip(name, dir string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := breakerKey(name, dir)
	st, ok := b.state[k]
	if ok && !st.probing {
		return
	}
	pause := b.Cooldown
	for i := 0; i < st.trips && pause < maxBreakerPause; i++ {
		pause *= 2
	}
	if pause > maxBreakerPause {
		pause = maxBreakerPause
	}
	b.state[k] = breakerState{until: b.now().Add(pause), trips: st.trips + 1}
}

// abandon ends a probe that was cancelled by its caller, so another command
// can try the tool.
func (b *Breaker) abandon(name, dir string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := breakerKey(name, dir)
	if st, ok := b.state[k]; ok && st.probing {
		st.probing = false
		b.state[k] = st
	}
}

// worked closes the breaker of a tool whose command just finished in time.
func (b *Breaker) worked(name, dir string) {
	b.mu.Lock()
	delete(b.state, breakerKey(name, dir))
	b.mu.Unlock()
}

// Reset closes the breaker: commands are started again.
func (b *Breaker) Reset() {
	b.mu.Lock()
	b.state = map[string]breakerState{}
	b.mu.Unlock()
}
