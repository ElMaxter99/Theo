package api

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFlightRunsOnceForConcurrentCallers(t *testing.T) {
	var g flightGroup[int]
	var runs atomic.Int64
	release := make(chan struct{})
	const callers = 12
	var wg sync.WaitGroup
	results := make([]int, callers)
	shared := make([]bool, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, sh, err := g.do(context.Background(), "k", 1, func() (int, error) {
				runs.Add(1)
				<-release
				return 42, nil
			})
			if err != nil {
				t.Error(err)
			}
			results[i], shared[i] = v, sh
		}()
	}
	// Let every caller reach the flight before the work finishes.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		started := len(g.calls) == 1
		g.mu.Unlock()
		if started {
			break
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if runs.Load() != 1 {
		t.Fatalf("the work ran %d times for %d concurrent callers, want 1", runs.Load(), callers)
	}
	nShared := 0
	for i, v := range results {
		if v != 42 {
			t.Errorf("caller %d got %d", i, v)
		}
		if shared[i] {
			nShared++
		}
	}
	if nShared != callers-1 {
		t.Errorf("%d callers joined the first one, want %d", nShared, callers-1)
	}
	g.mu.Lock()
	left := len(g.calls)
	g.mu.Unlock()
	if left != 0 {
		t.Errorf("%d finished flights are still registered", left)
	}
}

func TestFlightKeysAreIndependentAndSequentialCallsRunAgain(t *testing.T) {
	var g flightGroup[string]
	var runs atomic.Int64
	fn := func() (string, error) { runs.Add(1); return "x", nil }
	g.do(context.Background(), "a", 1, fn)
	g.do(context.Background(), "b", 1, fn)
	g.do(context.Background(), "a", 1, fn)
	if runs.Load() != 3 {
		t.Errorf("runs = %d: a flight only shares work that is still running", runs.Load())
	}
}

func TestFlightNewGenerationDoesNotJoinAnOlderComputation(t *testing.T) {
	var g flightGroup[string]
	oldStarted, releaseOld := make(chan struct{}), make(chan struct{})
	oldDone := make(chan string, 1)
	go func() {
		v, _, _ := g.do(context.Background(), "k", 1, func() (string, error) {
			close(oldStarted)
			<-releaseOld
			return "before the change", nil
		})
		oldDone <- v
	}()
	<-oldStarted
	// The state changed (generation 2): this caller must not get the answer
	// of a computation that began before the change.
	v, shared, err := g.do(context.Background(), "k", 2, func() (string, error) { return "after the change", nil })
	if err != nil || shared || v != "after the change" {
		t.Fatalf("got %q shared=%v err=%v", v, shared, err)
	}
	close(releaseOld)
	if got := <-oldDone; got != "before the change" {
		t.Errorf("the older caller still gets its own answer, got %q", got)
	}
	g.mu.Lock()
	left := len(g.calls)
	g.mu.Unlock()
	if left != 0 {
		t.Errorf("%d flights left registered", left)
	}
}

func TestFlightCallerLeavingDoesNotStopTheWork(t *testing.T) {
	var g flightGroup[int]
	release := make(chan struct{})
	var finished atomic.Bool
	fn := func() (int, error) {
		<-release
		finished.Store(true)
		return 7, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() {
		_, _, err := g.do(ctx, "k", 1, fn)
		first <- err
	}()
	time.Sleep(50 * time.Millisecond)
	second := make(chan int, 1)
	go func() {
		v, _, _ := g.do(context.Background(), "k", 1, fn)
		second <- v
	}()
	time.Sleep(50 * time.Millisecond)
	cancel() // the client that started it goes away
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("the leaving caller got %v, want its cancellation", err)
	}
	if finished.Load() {
		t.Fatal("the work stopped with the caller that started it")
	}
	close(release)
	if v := <-second; v != 7 {
		t.Errorf("the caller that stayed got %d, want 7", v)
	}
}

func TestFlightPanicBecomesAnError(t *testing.T) {
	var g flightGroup[int]
	_, _, err := g.do(context.Background(), "k", 1, func() (int, error) { panic("boom") })
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v", err)
	}
	// and the key is usable again
	v, _, err := g.do(context.Background(), "k", 1, func() (int, error) { return 1, nil })
	if err != nil || v != 1 {
		t.Errorf("after a panic: %d, %v", v, err)
	}
}
