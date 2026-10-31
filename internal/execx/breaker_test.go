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
		br.trip("docker", "")
	}
	if st := br.state["docker"]; st.trips != 1 || !st.until.Equal(now.Add(10*time.Second)) {
		t.Fatalf("one episode must open one pause of 10s, got %+v", st)
	}

	// After the pause exactly one command is let through to try again.
	now = now.Add(11 * time.Second)
	if err := br.check("docker", ""); err != nil {
		t.Fatalf("the probe: %v", err)
	}
	if err := br.check("docker", ""); !errors.Is(err, ErrUnresponsive) {
		t.Errorf("a second command while the probe runs: %v", err)
	}
	// The probe times out too: the pause doubles.
	br.trip("docker", "")
	if st := br.state["docker"]; st.trips != 2 || !st.until.Equal(now.Add(20*time.Second)) {
		t.Fatalf("a failed probe must double the pause, got %+v", st)
	}

	// A probe cancelled by its caller teaches nothing: someone else may try.
	now = now.Add(21 * time.Second)
	if err := br.check("docker", ""); err != nil {
		t.Fatal(err)
	}
	br.abandon("docker", "")
	if err := br.check("docker", ""); err != nil {
		t.Errorf("after an abandoned probe: %v", err)
	}
	// A probe that works closes the breaker for good.
	br.worked("docker", "")
	if err := br.peek("docker", ""); err != nil || len(br.state) != 0 {
		t.Errorf("after success: %v %v", err, br.state)
	}
}

func TestBreakerPauseIsCapped(t *testing.T) {
	br := NewBreaker(20 * time.Second)
	now := time.Now()
	br.now = func() time.Time { return now }
	br.trip("docker", "")
	for i := 0; i < 8; i++ {
		now = now.Add(2 * time.Minute)
		if err := br.check("docker", ""); err != nil {
			t.Fatal(err)
		}
		br.trip("docker", "")
	}
	if st := br.state["docker"]; st.until.Sub(now) != maxBreakerPause {
		t.Errorf("pause = %s, want the cap %s", st.until.Sub(now), maxBreakerPause)
	}
}
