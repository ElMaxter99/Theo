package notify

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/agents"
)

var t0 = time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)

func label(p string) string { return "shop / " + p }

func st(state string, since time.Time) agents.State {
	return agents.State{State: state, Source: agents.SourceHook, Agent: "claude", Since: since}
}

func TestFirstLookOnlyRecords(t *testing.T) {
	var tr Tracker
	got := tr.Next("all", map[string]agents.State{"a": st(agents.Waiting, t0)}, label)
	if len(got) != 0 {
		t.Fatalf("an agent already waiting when the app opens must not notify: %v", got)
	}
	if got := tr.Next("all", map[string]agents.State{"a": st(agents.Waiting, t0)}, label); len(got) != 0 {
		t.Fatalf("no change, no notification: %v", got)
	}
}

func TestWaitingNotifiesOncePerChange(t *testing.T) {
	var tr Tracker
	tr.Next("waiting", map[string]agents.State{}, label)
	waiting := map[string]agents.State{"a": st(agents.Waiting, t0)}
	got := tr.Next("waiting", waiting, label)
	if len(got) != 1 || !strings.Contains(got[0].Title, "Waiting for you · shop / a") || !strings.Contains(got[0].Body, "Claude Code") {
		t.Fatalf("got %+v", got)
	}
	if got := tr.Next("waiting", waiting, label); len(got) != 0 {
		t.Errorf("still waiting must not repeat: %v", got)
	}
	// Working again and waiting again is a new change.
	tr.Next("waiting", map[string]agents.State{"a": st(agents.Working, t0.Add(time.Minute))}, label)
	if got := tr.Next("waiting", map[string]agents.State{"a": st(agents.Waiting, t0.Add(2*time.Minute))}, label); len(got) != 1 {
		t.Errorf("a new wait should notify: %v", got)
	}
}

func TestDoneOnlyWithAll(t *testing.T) {
	for mode, want := range map[string]int{"waiting": 0, "all": 1, "off": 0} {
		var tr Tracker
		tr.Next(mode, map[string]agents.State{}, label)
		got := tr.Next(mode, map[string]agents.State{"a": st(agents.Done, t0)}, label)
		if len(got) != want {
			t.Errorf("mode %s: got %d notifications, want %d", mode, len(got), want)
		}
	}
}

func TestThreeOrMoreAreGrouped(t *testing.T) {
	tr := Tracker{Spanish: true}
	tr.Next("waiting", map[string]agents.State{}, label)
	got := tr.Next("waiting", map[string]agents.State{
		"a": st(agents.Waiting, t0), "b": st(agents.Waiting, t0), "c": st(agents.Waiting, t0),
	}, label)
	if len(got) != 1 || got[0].Title != "3 agentes te esperan" {
		t.Fatalf("got %+v", got)
	}
}

func TestSessionAndPathsNeverInTheText(t *testing.T) {
	var tr Tracker
	tr.Next("all", nil, label)
	s := agents.State{State: agents.Waiting, Source: agents.SourceHook, Agent: "copilot", Session: "secret-session", Since: t0}
	m := tr.Next("all", map[string]agents.State{"/home/x/w": s}, func(string) string { return "shop / w" })
	if len(m) != 1 || strings.Contains(m[0].Title+m[0].Body, "secret") || strings.Contains(m[0].Title+m[0].Body, "/home") {
		t.Errorf("%+v", m)
	}
}

func TestWatchSendsAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	states := map[string]agents.State{}
	sent := make(chan Message, 4)
	done := make(chan struct{})
	go func() {
		Watch(ctx, Deps{
			Mode:     func() string { return "waiting" },
			States:   func() map[string]agents.State { mu.Lock(); defer mu.Unlock(); return states },
			Label:    label,
			Send:     func(m Message) error { sent <- m; return nil },
			Interval: 5 * time.Millisecond,
		})
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	states = map[string]agents.State{"a": st(agents.Waiting, t0)}
	mu.Unlock()
	select {
	case <-sent:
	case <-time.After(2 * time.Second):
		t.Error("nothing was sent")
	}
	cancel()
	<-done
}

func TestWindowsToastEscapes(t *testing.T) {
	ps := windowsToast(Message{Title: `a'b <c>`, Body: `"d" & e`})
	for _, bad := range []string{"<c>", `"d" & e`} {
		if strings.Contains(ps, bad) {
			t.Errorf("unescaped %q in %s", bad, ps)
		}
	}
}

// While the window is in front nothing is shown, and what changed meanwhile
// is not announced later when the window goes behind.
func TestWatchSkipsWhileInFront(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	states := map[string]agents.State{}
	front := true
	sent := make(chan Message, 4)
	done := make(chan struct{})
	go func() {
		Watch(ctx, Deps{
			Mode:     func() string { return "waiting" },
			States:   func() map[string]agents.State { mu.Lock(); defer mu.Unlock(); return states },
			Label:    label,
			Send:     func(m Message) error { sent <- m; return nil },
			Skip:     func() bool { mu.Lock(); defer mu.Unlock(); return front },
			Interval: 5 * time.Millisecond,
		})
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	states = map[string]agents.State{"a": st(agents.Waiting, t0)}
	mu.Unlock()
	select {
	case m := <-sent:
		t.Fatalf("notified with the window in front: %+v", m)
	case <-time.After(100 * time.Millisecond):
	}
	mu.Lock()
	front = false
	mu.Unlock()
	select {
	case m := <-sent:
		t.Fatalf("announced something already seen: %+v", m)
	case <-time.After(100 * time.Millisecond):
	}
	// A new change with the window behind does notify.
	mu.Lock()
	states = map[string]agents.State{"b": st(agents.Waiting, t0.Add(time.Minute))}
	mu.Unlock()
	select {
	case <-sent:
	case <-time.After(2 * time.Second):
		t.Error("nothing was sent with the window behind")
	}
	cancel()
	<-done
}
