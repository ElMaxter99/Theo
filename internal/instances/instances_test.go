package instances

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestStoreRoundTripAndPrivacy(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	if st, err := s.Load(); err != nil || len(st) != 0 {
		t.Fatalf("empty store: %v %v", st, err)
	}
	want := State{"/w": {Env: map[string]Var{"A": {Value: "1", Secret: true, Services: []string{"api"}}}, NeutralizeContainerNames: true}}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, %v", got, err)
	}
	if info, err := os.Stat(filepath.Join(dir, configFile)); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		t.Errorf("the settings file may hold secrets and must be private: %v %v", info, err)
	}
	if !(Config{}).Empty() || want["/w"].Empty() {
		t.Errorf("Empty is wrong")
	}
}

func TestHistoryAppendsAndSkipsBrokenLines(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	when := time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC)
	for _, op := range []string{"up", "down"} {
		if err := s.Append(Event{Time: when, Project: "app", Workspace: "/w", Operation: op, Result: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	f, _ := os.OpenFile(filepath.Join(dir, historyFile), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("not json\n")
	f.Close()
	events, err := s.Events()
	if err != nil || len(events) != 2 || events[0].Operation != "up" || events[1].Operation != "down" {
		t.Errorf("events = %+v, %v", events, err)
	}
}
