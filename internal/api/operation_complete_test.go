package api

import (
	"sync"
	"testing"
)

// A stream that sees an operation over must already find its "result" event:
// the operation used to become finished a moment before the event was added,
// and a stream that looked in between ended without ever sending it.
func TestCompletedOperationAlwaysHasItsResultEvent(t *testing.T) {
	for i := 0; i < 2000; i++ {
		op := &Operation{state: OpRunning, wake: make(chan struct{})}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				evs, _, _, done := op.since(0)
				if !done {
					continue
				}
				for _, e := range evs {
					if e.Name == "result" {
						return
					}
				}
				t.Error("operation over, but no result event in the ring")
				return
			}
		}()
		op.complete(&opResult{OK: true}, OpSucceeded)
		wg.Wait()
	}
}
