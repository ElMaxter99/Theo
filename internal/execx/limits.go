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

// Breaker remembers that a tool timed out. For Cooldown afterwards, commands
// of that tool fail at once with ErrUnresponsive instead of each waiting for
// its own timeout, so a stuck Docker costs one timeout, not one per call.
// Docker is tracked as a whole (a stuck daemon stalls every command); other
// tools are tracked per directory, so one stuck repository does not stop the
// rest.
type Breaker struct {
	Cooldown time.Duration

	mu    sync.Mutex
	until map[string]time.Time
	now   func() time.Time
}

// NewBreaker returns a Breaker that stays open for cooldown after a timeout.
func NewBreaker(cooldown time.Duration) *Breaker {
	return &Breaker{Cooldown: cooldown, until: map[string]time.Time{}, now: time.Now}
}

func breakerKey(name, dir string) string {
	if name == "docker" {
		return name
	}
	return name + "|" + dir
}

func (b *Breaker) check(name, dir string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	until, ok := b.until[breakerKey(name, dir)]
	if !ok {
		return nil
	}
	left := until.Sub(b.now())
	if left <= 0 {
		delete(b.until, breakerKey(name, dir))
		return nil
	}
	return fmt.Errorf("%s did not answer a moment ago; not trying again for %s: %w", name, left.Round(time.Second), ErrUnresponsive)
}

func (b *Breaker) trip(name, dir string) {
	b.mu.Lock()
	b.until[breakerKey(name, dir)] = b.now().Add(b.Cooldown)
	b.mu.Unlock()
}

// Reset closes the breaker: commands are started again.
func (b *Breaker) Reset() {
	b.mu.Lock()
	b.until = map[string]time.Time{}
	b.mu.Unlock()
}
