package agents

import (
	"testing"
	"time"
)

func TestStoreSetGetClear(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	wt := t.TempDir()
	if _, ok := s.Get(wt); ok {
		t.Fatal("empty store has a state")
	}
	if err := s.Set(wt, State{State: Waiting, Source: SourceHook, Agent: "claude", Since: time.Now()}); err != nil {
		t.Fatal(err)
	}
	st, ok := s.Get(wt)
	if !ok || st.State != Waiting {
		t.Fatalf("got %+v, %v", st, ok)
	}
	if err := s.Clear(wt); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(wt); ok {
		t.Fatal("cleared state is still there")
	}
}

func TestStaleStateIsNotBelieved(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	wt := t.TempDir()
	s.Set(wt, State{State: Working, Source: SourceHook, Since: time.Now().Add(-StaleAfter - time.Minute)})
	if _, ok := s.Get(wt); ok {
		t.Fatal("a state older than StaleAfter was believed")
	}
}

func TestResolve(t *testing.T) {
	if got := Resolve(State{State: Done, Source: SourceHook}, true, []string{"codex"}); got.State != Done {
		t.Errorf("hook must win: %+v", got)
	}
	if got := Resolve(State{}, false, []string{"claude"}); got.State != Working || got.Source != SourceProcess {
		t.Errorf("process estimate: %+v", got)
	}
	if got := Resolve(State{}, false, nil); got.State != Unknown {
		t.Errorf("no signal: %+v", got)
	}
}

func TestInWorktree(t *testing.T) {
	root := t.TempDir()
	procs := []Process{{Program: "claude", Cwd: root + "/src"}, {Program: "bash", Cwd: root}, {Program: "codex", Cwd: t.TempDir()}, {Program: "claude.exe", Cwd: root}}
	got := InWorktree(procs, root)
	if len(got) != 1 || got[0] != "claude" {
		t.Errorf("got %v", got)
	}
	if _, ok := IsAgent("/usr/bin/Aider"); !ok {
		t.Error("aider not recognised")
	}
}

func TestParseUnixPSAndLsof(t *testing.T) {
	ps := "  101 /usr/local/bin/claude --resume\n  102 /bin/zsh -l\n  103 node /opt/x/codex/bin/codex.js\n  104 /usr/bin/node server.js\nbad line\n"
	got := ParseUnixPS(ps)
	if len(got) != 2 || got[0].PID != "101" || got[0].Program != "claude" || got[1].PID != "103" || got[1].Program != "codex" {
		t.Fatalf("ps: %+v", got)
	}
	cwds := ParseLsofCwd("p101\nfcwd\nn/Users/m/shop-feature\np103\nfcwd\nn/Users/m/other\n")
	if cwds["101"] != "/Users/m/shop-feature" || cwds["103"] != "/Users/m/other" {
		t.Fatalf("lsof: %v", cwds)
	}
}

func TestWindowsProcessesMatchByCommandLine(t *testing.T) {
	out := "claude.exe|\"C:\\Users\\m\\bin\\claude.exe\" --add-dir C:\\work\\shop-feature\r\ncodex.exe|codex\r\nnode.exe|node C:\\x\\aider.js\r\nsvchost.exe|\r\n"
	procs := ParseWindowsProcesses(out)
	if len(procs) != 3 {
		t.Fatalf("procs: %+v", procs)
	}
	got := InWorktree(procs, `C:\work\shop-feature`)
	if len(got) != 1 || got[0] != "claude" {
		t.Errorf("only the agent naming the worktree counts: %v", got)
	}
}

func TestSlowScanNeverBlocksAndRefreshes(t *testing.T) {
	var c scanCache
	release := make(chan struct{})
	scan := func() []Process { <-release; return []Process{{Program: "claude", Cwd: "/x"}} }
	start := time.Now()
	if got := c.get(scan); got != nil || time.Since(start) > time.Second {
		t.Fatalf("first call must return at once and empty, got %v after %v", got, time.Since(start))
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := c.get(scan); len(got) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the background scan never filled the cache")
}
