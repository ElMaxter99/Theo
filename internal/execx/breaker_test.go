package execx

import (
	"errors"
	"testing"
	"time"
)

func TestBreakerProbesOnceAndBacksOff(t *testing.T) {
	br := NewBreaker(10 * time.Second)
	now := time.Now()
	br.now = func() time.Time { return now }

	// Five commands that were already running time out together: one episode.
	for i := 0; i < 5; i++ {
		br.trip(breakerKey("docker", "", nil))
	}
	if st := br.state[breakerKey("docker", "", nil)]; st.trips != 1 || !st.until.Equal(now.Add(10*time.Second)) {
		t.Fatalf("one episode must open one pause of 10s, got %+v", st)
	}

	// After the pause exactly one command is let through to try again.
	now = now.Add(11 * time.Second)
	if err := br.check(breakerKey("docker", "", nil)); err != nil {
		t.Fatalf("the probe: %v", err)
	}
	if err := br.check(breakerKey("docker", "", nil)); !errors.Is(err, ErrUnresponsive) {
		t.Errorf("a second command while the probe runs: %v", err)
	}
	// The probe times out too: the pause doubles.
	br.trip(breakerKey("docker", "", nil))
	if st := br.state[breakerKey("docker", "", nil)]; st.trips != 2 || !st.until.Equal(now.Add(20*time.Second)) {
		t.Fatalf("a failed probe must double the pause, got %+v", st)
	}

	// A probe cancelled by its caller teaches nothing: someone else may try.
	now = now.Add(21 * time.Second)
	if err := br.check(breakerKey("docker", "", nil)); err != nil {
		t.Fatal(err)
	}
	br.abandon(breakerKey("docker", "", nil))
	if err := br.check(breakerKey("docker", "", nil)); err != nil {
		t.Errorf("after an abandoned probe: %v", err)
	}
	// A probe that works closes the breaker for good.
	br.worked(breakerKey("docker", "", nil))
	if err := br.peek(breakerKey("docker", "", nil)); err != nil || len(br.state) != 0 {
		t.Errorf("after success: %v %v", err, br.state)
	}
}

func TestBreakerKeys(t *testing.T) {
	daemon := breakerKey("docker", "/a", []string{"compose", "-p", "x", "ps", "--all"})
	if daemon != breakerKey("docker", "/b", []string{"ps"}) {
		t.Error("everything that needs the daemon is one tool, wherever it runs")
	}
	cfg := breakerKey("docker", "/a", []string{"compose", "--profile", "*", "config", "--format", "json"})
	if cfg == daemon {
		t.Error("`compose config` reads files and works with the daemon down: a stuck daemon must not block it")
	}
	if breakerKey("git", "/a", nil) == breakerKey("git", "/b", nil) {
		t.Error("git is tracked per directory")
	}
}

func TestBreakerPauseIsCapped(t *testing.T) {
	br := NewBreaker(20 * time.Second)
	now := time.Now()
	br.now = func() time.Time { return now }
	br.trip(breakerKey("docker", "", nil))
	for i := 0; i < 8; i++ {
		now = now.Add(2 * time.Minute)
		if err := br.check(breakerKey("docker", "", nil)); err != nil {
			t.Fatal(err)
		}
		br.trip(breakerKey("docker", "", nil))
	}
	if st := br.state[breakerKey("docker", "", nil)]; st.until.Sub(now) != maxBreakerPause {
		t.Errorf("pause = %s, want the cap %s", st.until.Sub(now), maxBreakerPause)
	}
}
