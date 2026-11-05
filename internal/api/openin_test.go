package api_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/opener"
)

func TestOpenInAndAgentInOverview(t *testing.T) {
	e := newEnv(t)
	origLook, origStart := opener.Look, opener.Start
	t.Cleanup(func() { opener.Look, opener.Start = origLook, origStart })
	opener.Look = func(name string) (string, error) {
		if name == "code" || name == "xdg-open" {
			return "/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
	var started []string
	opener.Start = func(dir, name string, args ...string) error {
		started = append(started, name+" "+strings.Join(args, " "))
		return nil
	}
	var ov struct {
		Worktrees []struct {
			ID    string
			Path  string
			Agent *struct{ State, Source string }
		}
	}
	if st := e.c.call("GET", "/api/v1/projects/ubapp/overview", nil, &ov); st != 200 {
		t.Fatalf("overview = %d", st)
	}
	id := ov.Worktrees[1].ID
	var opts struct {
		Options []struct {
			Target    string
			Available bool
			Reason    string
		}
	}
	if st := e.c.call("GET", "/api/v1/projects/ubapp/worktrees/"+id+"/open", nil, &opts); st != 200 || len(opts.Options) != 3 {
		t.Fatalf("options = %d %+v", st, opts)
	}
	if !opts.Options[1].Available || opts.Options[1].Target != "editor" {
		t.Errorf("editor should be available: %+v", opts.Options)
	}
	var done struct{ Opened bool }
	if st := e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+id+"/open", map[string]string{"target": "editor"}, &done); st != 200 || !done.Opened || len(started) != 1 {
		t.Fatalf("open = %d %+v %v", st, done, started)
	}
	var bad apiErr
	if st := e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+id+"/open", map[string]string{"target": "rm"}, &bad); st != 400 {
		t.Errorf("unknown target = %d", st)
	}

	// An agent that works there shows in the overview and keeps the worktree.
	if err := e.eng.SetAgentState(ov.Worktrees[1].Path, "working", "claude", "s1"); err != nil {
		t.Fatal(err)
	}
	if st := e.c.call("GET", "/api/v1/projects/ubapp/overview", nil, &ov); st != 200 || ov.Worktrees[1].Agent == nil || ov.Worktrees[1].Agent.State != "working" {
		t.Fatalf("agent in overview: %+v", ov.Worktrees[1])
	}
	var alive apiErr
	if st := e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+id+"/remove", nil, &alive); st != 409 || alive.Error.Code != "agent_alive" {
		t.Errorf("remove with agent = %d %+v", st, alive.Error)
	}
}
