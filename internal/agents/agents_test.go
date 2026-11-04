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
	procs := []Process{{"claude", root + "/src"}, {"bash", root}, {"codex", t.TempDir()}, {"claude.exe", root}}
	got := InWorktree(procs, root)
	if len(got) != 1 || got[0] != "claude" {
		t.Errorf("got %v", got)
	}
	if _, ok := IsAgent("/usr/bin/Aider"); !ok {
		t.Error("aider not recognised")
	}
}
