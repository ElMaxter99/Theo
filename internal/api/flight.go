package api

import (
	"context"
	"fmt"
	"sync"
)

// flightGroup makes identical concurrent computations run once: the first
// caller starts the work, the others wait for the same answer.
//
// Two details matter for a server that polls:
//   - The work does not belong to the caller that started it. It runs on its
//     own goroutine and context, so a client that disconnects does not fail
//     the others, and a caller leaving early only stops waiting. What bounds
//     the work is the timeout its function sets up.
//   - A flight is only joined by callers of the same generation. The server
//     bumps the generation when something changed state (an operation ended,
//     a record was forgotten), so a request made after that never gets the
//     answer of a computation that began before it.
type flightGroup[T any] struct {
	mu    sync.Mutex
	calls map[string]*flightCall[T]
}

type flightCall[T any] struct {
	gen  uint64
	done chan struct{}
	val  T
	err  error
}

// do returns the result of fn for key. shared is true when the caller joined
// a computation another caller had started.
func (g *flightGroup[T]) do(ctx context.Context, key string, gen uint64, fn func() (T, error)) (val T, shared bool, err error) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = map[string]*flightCall[T]{}
	}
	if c, ok := g.calls[key]; ok && c.gen == gen {
		g.mu.Unlock()
		val, err = g.wait(ctx, c)
		return val, true, err
	}
	c := &flightCall[T]{gen: gen, done: make(chan struct{})}
	g.calls[key] = c // an older generation keeps running for those waiting on it
	g.mu.Unlock()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				c.err = fmt.Errorf("internal error: %v", r)
			}
			close(c.done)
			g.mu.Lock()
			if g.calls[key] == c {
				delete(g.calls, key)
			}
			g.mu.Unlock()
		}()
		c.val, c.err = fn()
	}()
	val, err = g.wait(ctx, c)
	return val, false, err
}

func (g *flightGroup[T]) wait(ctx context.Context, c *flightCall[T]) (T, error) {
	select {
	case <-c.done:
		return c.val, c.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}
