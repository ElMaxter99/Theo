package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
)

func TestProgressWriterSendsLines(t *testing.T) {
	var out bytes.Buffer
	s := &Server{out: &out}
	ctx := withProgress(context.Background(), s, json.RawMessage(`"tok-1"`))
	p := progressOf(ctx)
	if p == nil {
		t.Fatal("no progress reporter for a token")
	}
	now := time.Unix(0, 0)
	p.clock = func() time.Time { return now }
	p.mask = func(x string) string { return strings.ReplaceAll(x, "s3cret", "********") }
	p.notify("up app", true)
	now = now.Add(time.Second)
	p.Write([]byte(" Container app-web-1  Creat"))
	p.Write([]byte("ed\n Container app-db-1 s3cret Started\n"))
	now = now.Add(50 * time.Millisecond) // within the gap: dropped
	p.Write([]byte(" Container app-web-1 Started\n"))
	var got []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("not JSON: %q", line)
		}
		if m["method"] != "notifications/progress" {
			t.Fatalf("method: %v", m["method"])
		}
		got = append(got, m["params"].(map[string]any))
	}
	if len(got) != 2 {
		t.Fatalf("want 2 notifications, got %d: %v", len(got), got)
	}
	if got[0]["progressToken"] != "tok-1" || got[0]["progress"].(float64) != 1 || got[1]["progress"].(float64) != 2 {
		t.Errorf("tokens/progress: %v", got)
	}
	if got[1]["message"] != "Container app-db-1 ******** Started" {
		t.Errorf("message not the last line, or not masked: %v", got[1]["message"])
	}
	if withProgress(context.Background(), s, nil) != context.Background() || progressOf(context.Background()) != nil {
		t.Error("no token must mean no reporter")
	}
	var nilP *progress
	nilP.notify("ignored", true) // must not panic
}

func TestWaitForJudge(t *testing.T) {
	expected := []string{"web", "db"}
	run := func(svc, health string) compose.Container {
		return compose.Container{Service: svc, State: "running", Health: health}
	}
	start := judge(nil, expected, true)
	cases := []struct {
		name       string
		until      string
		containers []compose.Container
		done, ok   bool
	}{
		{"running", untilRunning, []compose.Container{run("web", ""), run("db", "healthy")}, true, true},
		{"health starting", untilRunning, []compose.Container{run("web", ""), run("db", "starting")}, false, false},
		{"partial", untilRunning, []compose.Container{run("web", "")}, false, false},
		{"failed", untilRunning, []compose.Container{run("web", ""), {Service: "db", State: "exited", ExitCode: 1}}, true, false},
		{"stopped", untilStopped, nil, true, true},
		{"not stopped", untilStopped, []compose.Container{run("web", "")}, false, false},
		{"stopped by a signal", untilStopped, []compose.Container{{Service: "web", State: "exited", ExitCode: 137}}, true, true},
		{"changed", untilChanged, []compose.Container{run("web", "")}, true, true},
		{"unchanged", untilChanged, nil, false, false},
	}
	for _, c := range cases {
		ws := judge(c.containers, expected, true)
		done, ok := reached(c.until, start, ws)
		if done != c.done || ok != c.ok {
			t.Errorf("%s: done=%v ok=%v, want %v %v (state %s)", c.name, done, ok, c.done, c.ok, ws.State)
		}
	}
	if judge(nil, expected, false).State != engine.InstUnknown {
		t.Error("docker down must be unknown")
	}
}
