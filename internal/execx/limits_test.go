package execx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSleepHelper is not a test: the tests below run the test binary again as
// a command that sleeps THEO_EXECX_SLEEP_MS and prints when it started and
// ended (unix nanoseconds).
func TestSleepHelper(t *testing.T) {
	ms, err := strconv.Atoi(os.Getenv("THEO_EXECX_SLEEP_MS"))
	if err != nil {
		return
	}
	start := time.Now().UnixNano()
	time.Sleep(time.Duration(ms) * time.Millisecond)
	fmt.Printf("%d %d\n", start, time.Now().UnixNano())
}

func sleeper(t *testing.T, ms int) (string, []string) {
	t.Helper()
	t.Setenv("THEO_EXECX_SLEEP_MS", strconv.Itoa(ms))
	return os.Args[0], []string{"-test.run=TestSleepHelper"}
}

func TestRunTimeoutKillsAndNamesTheLimit(t *testing.T) {
	name, args := sleeper(t, 20000)
	ctx := WithLimits(context.Background(), Limits{Timeout: 200 * time.Millisecond})
	start := time.Now()
	_, err := Run(ctx, "", name, args...)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("a hung command held the caller for %s", d)
	}
}

func TestRunWithoutLimitsIsUnchanged(t *testing.T) {
	name, args := sleeper(t, 10)
	out, err := Run(context.Background(), "", name, args...)
	if err != nil || !strings.Contains(string(out), " ") {
		t.Fatalf("out %q, err %v", out, err)
	}
}

func TestClientCancelIsNotATimeout(t *testing.T) {
	name, args := sleeper(t, 20000)
	ctx, cancel := context.WithCancel(WithLimits(context.Background(), Limits{Timeout: time.Minute, Breaker: NewBreaker(time.Minute)}))
	time.AfterFunc(100*time.Millisecond, cancel)
	_, err := Run(ctx, "", name, args...)
	if err == nil || errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want a plain cancellation", err)
	}
}

func TestBreakerStopsCallsAfterATimeout(t *testing.T) {
	name, args := sleeper(t, 20000)
	br := NewBreaker(time.Minute)
	ctx := WithLimits(context.Background(), Limits{Timeout: 150 * time.Millisecond, Breaker: br})
	if _, err := Run(ctx, "", name, args...); !errors.Is(err, ErrTimeout) {
		t.Fatalf("first call: %v", err)
	}
	// The second call must fail at once: it never starts the command.
	start := time.Now()
	_, err := Run(ctx, "", name, args...)
	if !errors.Is(err, ErrUnresponsive) {
		t.Fatalf("second call: %v, want ErrUnresponsive", err)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("the open breaker still cost %s", d)
	}
	br.Reset()
	quick, qargs := sleeper(t, 5)
	roomy := WithLimits(context.Background(), Limits{Timeout: 10 * time.Second, Breaker: br})
	if _, err := Run(roomy, "", quick, qargs...); err != nil {
		t.Errorf("after Reset: %v", err)
	}
}

func TestBreakerCoolsDown(t *testing.T) {
	br := NewBreaker(time.Minute)
	now := time.Now()
	br.now = func() time.Time { return now }
	br.trip(breakerKey("git", "/a", nil))
	if err := br.check(breakerKey("git", "/a", nil)); !errors.Is(err, ErrUnresponsive) {
		t.Fatalf("inside the cooldown: %v", err)
	}
	if err := br.check(breakerKey("git", "/b", nil)); err != nil {
		t.Errorf("git is tracked per directory, /b is fine: %v", err)
	}
	br.trip(breakerKey("docker", "/a", nil))
	if err := br.check(breakerKey("docker", "/elsewhere", nil)); !errors.Is(err, ErrUnresponsive) {
		t.Errorf("docker is tracked as a whole: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if err := br.check(breakerKey("git", "/a", nil)); err != nil {
		t.Errorf("after the cooldown: %v", err)
	}
}

func TestMaxConcurrentBoundsCommands(t *testing.T) {
	SetMaxConcurrent(2)
	defer SetMaxConcurrent(0)
	name, args := sleeper(t, 150)
	var (
		mu     sync.Mutex
		spans  [][2]int64
		failed error
		wg     sync.WaitGroup
	)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := Run(context.Background(), "", name, args...)
			if err != nil {
				mu.Lock()
				failed = err
				mu.Unlock()
				return
			}
			var s, e int64
			for _, line := range strings.Split(string(out), "\n") {
				if n, _ := fmt.Sscanf(line, "%d %d", &s, &e); n == 2 {
					mu.Lock()
					spans = append(spans, [2]int64{s, e})
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if failed != nil {
		t.Fatal(failed)
	}
	if len(spans) != 6 {
		t.Fatalf("%d commands reported, want 6", len(spans))
	}
	peak := 0
	for _, a := range spans { // overlap at the start of each command
		n := 0
		for _, b := range spans {
			if b[0] <= a[0] && a[0] < b[1] {
				n++
			}
		}
		if n > peak {
			peak = n
		}
	}
	if peak > 2 {
		t.Errorf("%d commands ran at once, the limit is 2", peak)
	}
}

func TestWaitingForASlotEndsWithTheContext(t *testing.T) {
	SetMaxConcurrent(1)
	defer SetMaxConcurrent(0)
	name, args := sleeper(t, 1500)
	go Run(context.Background(), "", name, args...)
	time.Sleep(200 * time.Millisecond) // the first command holds the only slot
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Run(ctx, "", name, args...)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the context's deadline", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("waited %s for a slot after the context ended", d)
	}
}
