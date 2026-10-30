package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/api"
	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// sseEvent is one parsed server-sent event.
type sseEvent struct {
	ID, Name, Data string
}

type sseStream struct {
	resp   *http.Response
	events chan sseEvent
	cancel context.CancelFunc
}

func (c *client) sse(path, lastID string) *sseStream {
	c.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", c.base+path, nil)
	req.AddCookie(&http.Cookie{Name: "theo_session", Value: c.cookie})
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	hc := &http.Client{} // no timeout: the stream is long
	resp, err := hc.Do(req)
	if err != nil {
		cancel()
		c.t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		cancel()
		c.t.Fatalf("SSE %s: %d %s", path, resp.StatusCode, b)
	}
	s := &sseStream{resp: resp, events: make(chan sseEvent, 10000), cancel: cancel}
	go func() {
		defer close(s.events)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev.Name != "" {
					s.events <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, "id: "):
				ev.ID = line[4:]
			case strings.HasPrefix(line, "event: "):
				ev.Name = line[7:]
			case strings.HasPrefix(line, "data: "):
				ev.Data = line[6:]
			}
		}
	}()
	c.t.Cleanup(s.close)
	return s
}

func (s *sseStream) close() { s.cancel(); s.resp.Body.Close() }

func (s *sseStream) next(t *testing.T, within time.Duration) (sseEvent, bool) {
	t.Helper()
	select {
	case ev, ok := <-s.events:
		return ev, ok
	case <-time.After(within):
		return sseEvent{}, false
	}
}

// until reads events until one named name arrives.
func (s *sseStream) until(t *testing.T, name string, within time.Duration) (sseEvent, []sseEvent) {
	t.Helper()
	deadline := time.Now().Add(within)
	var seen []sseEvent
	for time.Now().Before(deadline) {
		ev, ok := s.next(t, time.Until(deadline))
		if !ok {
			break
		}
		seen = append(seen, ev)
		if ev.Name == name {
			return ev, seen
		}
	}
	t.Fatalf("no %q event within %s; saw %v", name, within, seen)
	return sseEvent{}, nil
}

func TestMetaAndSession(t *testing.T) {
	e := newEnv(t)
	var m struct {
		APIVersion string `json:"api_version"`
		DataDir    string `json:"data_dir"`
		OS         string `json:"os"`
		Git        struct{ Available bool }
	}
	if st := e.c.call("GET", "/api/v1/meta", nil, &m); st != 200 || m.APIVersion != "1" || m.OS == "" || !m.Git.Available {
		t.Errorf("meta = %d %+v", st, m)
	}
	var s struct {
		Csrf string `json:"csrf_token"`
	}
	if st := e.c.call("GET", "/api/v1/session", nil, &s); st != 200 || s.Csrf != e.c.csrf {
		t.Errorf("GET /session = %d %+v", st, s)
	}
}

func TestStaticShellNeedsNoSession(t *testing.T) {
	e := newEnv(t)
	anon := newBareClient(t, e.srv.Port())
	st, body, hdr := anon.raw("GET", "/", nil, nil)
	if st != 200 || !strings.Contains(string(body), "<") || !strings.HasPrefix(hdr.Get("Content-Type"), "text/html") {
		t.Errorf("GET / = %d %s", st, hdr.Get("Content-Type"))
	}
	st, body, _ = anon.raw("GET", "/api/v1/nope", nil, nil)
	if st != 404 && st != 401 {
		t.Errorf("unknown API route = %d %s", st, body)
	}
}

func TestProjectsCRUD(t *testing.T) {
	e := newEnv(t)
	var list []map[string]any
	if st := e.c.call("GET", "/api/v1/projects", nil, &list); st != 200 || len(list) != 1 || list[0]["name"] != "ubapp" {
		t.Fatalf("projects = %d %v", st, list)
	}
	// Add a second repository.
	main2, _ := testutil.NewRepo(t, nil)
	var added map[string]any
	if st := e.c.call("POST", "/api/v1/projects", map[string]string{"path": main2, "name": "ubtwo"}, &added); st != 201 || added["name"] != "ubtwo" {
		t.Fatalf("add = %d %v", st, added)
	}
	var dup apiErr
	if st := e.c.call("POST", "/api/v1/projects", map[string]string{"path": main2, "name": "ubtwo"}, &dup); st != 409 || dup.Error.Code != "already_exists" {
		t.Errorf("duplicate = %d %+v", st, dup.Error)
	}
	var bad apiErr
	if st := e.c.call("POST", "/api/v1/projects", map[string]string{"path": t.TempDir()}, &bad); st < 400 || bad.Error.Code == "" {
		t.Errorf("not a repo = %d %+v", st, bad.Error)
	}
	var unk apiErr
	if st := e.c.call("POST", "/api/v1/projects", map[string]any{"path": main2, "bogus": 1}, &unk); st != 400 || unk.Error.Code != "unknown_field" {
		t.Errorf("unknown field = %d %+v", st, unk.Error)
	}
	// PATCH worktree_dir and clear it with null.
	dir := t.TempDir()
	var p map[string]any
	if st := e.c.call("PATCH", "/api/v1/projects/ubtwo", map[string]any{"worktree_dir": dir}, &p); st != 200 || p["worktree_dir"] != dir {
		t.Errorf("patch = %d %v", st, p)
	}
	p = nil
	if st := e.c.call("PATCH", "/api/v1/projects/ubtwo", map[string]any{"worktree_dir": nil}, &p); st != 200 || p["worktree_dir"] != nil {
		t.Errorf("patch null = %d %v", st, p)
	}
	var nf apiErr
	if st := e.c.call("PATCH", "/api/v1/projects/nope", map[string]any{"worktree_dir": nil}, &nf); st != 404 || nf.Error.Code != "project_not_found" {
		t.Errorf("unknown project = %d %+v", st, nf.Error)
	}
	var rm map[string]any
	if st := e.c.call("DELETE", "/api/v1/projects/ubtwo", nil, &rm); st != 200 || rm["files_kept"] != true || rm["containers_kept"] != true {
		t.Errorf("delete = %d %v", st, rm)
	}
	if _, err := os.Stat(main2); err != nil {
		t.Error("removing the project touched its files")
	}
	if st := e.c.call("GET", "/api/v1/projects", nil, &list); st != 200 || len(list) != 1 {
		t.Errorf("after delete: %d %v", st, list)
	}
}

func TestEmptyListsAreArrays(t *testing.T) {
	e := newEnv(t)
	id := e.wtID(e.main)
	for _, p := range []string{"/api/v1/projects/ubapp/worktrees/" + id + "/volumes", "/api/v1/projects/ubapp/worktrees/" + id + "/history", "/api/v1/projects/ubapp/worktrees/" + id + "/env", "/api/v1/operations"} {
		st, body, _ := e.c.raw("GET", p, nil, nil)
		if st != 200 || strings.TrimSpace(string(body)) != "[]" {
			t.Errorf("GET %s = %d %s, want []", p, st, body)
		}
	}
}

func TestOverviewAndWorktrees(t *testing.T) {
	e := newEnv(t)
	var ov struct {
		Project   string `json:"project"`
		Docker    struct{ Available bool }
		Worktrees []struct {
			ID         string `json:"id"`
			Path       string
			Branch     string
			Main       bool
			State      string
			Dirty      bool
			Containers struct{ Running, Total int }
			Ports      []struct {
				Service, URL string
				HostPort     int `json:"host_port"`
			}
			Busy any
		}
	}
	if st := e.c.call("GET", "/api/v1/projects/ubapp/overview", nil, &ov); st != 200 {
		t.Fatalf("overview = %d", st)
	}
	if ov.Project != "ubapp" || !ov.Docker.Available || len(ov.Worktrees) != 2 {
		t.Fatalf("overview = %+v", ov)
	}
	for _, w := range ov.Worktrees {
		if !strings.HasPrefix(w.ID, "wt_") || len(w.ID) != 13 {
			t.Errorf("id %q", w.ID)
		}
		if w.State != "absent" || w.Busy != nil {
			t.Errorf("%s: state %s busy %v", w.Branch, w.State, w.Busy)
		}
		if len(w.Ports) != 1 || w.Ports[0].HostPort != 18080 || !strings.HasPrefix(w.Ports[0].URL, "http") {
			t.Errorf("%s: ports %+v", w.Branch, w.Ports)
		}
	}
	if !ov.Worktrees[0].Main || ov.Worktrees[1].Branch != "feature/x" {
		t.Errorf("order/branches: %+v", ov.Worktrees)
	}
	// Detail and 404.
	id := ov.Worktrees[1].ID
	var d struct {
		ID         string
		Containers []any
		Settings   struct {
			Neutralize bool `json:"neutralize_container_names"`
		}
	}
	if st := e.c.call("GET", "/api/v1/projects/ubapp/worktrees/"+id, nil, &d); st != 200 || d.ID != id || d.Containers == nil {
		t.Errorf("detail = %d %+v", st, d)
	}
	var nf apiErr
	if st := e.c.call("GET", "/api/v1/projects/ubapp/worktrees/wt_0000000000", nil, &nf); st != 404 || nf.Error.Code != "worktree_not_found" {
		t.Errorf("unknown wt = %d %+v", st, nf.Error)
	}
	if st := e.c.call("GET", "/api/v1/projects/nope/overview", nil, &nf); st != 404 || nf.Error.Code != "project_not_found" {
		t.Errorf("unknown project = %d %+v", st, nf.Error)
	}
	// Paths of the disk never appear in URLs: ids are opaque.
	if strings.Contains(id, "/") {
		t.Error("id contains a path")
	}
}

func TestDockerDownKeepsRegistryViews(t *testing.T) {
	e := newEnv(t)
	e.fc.psErr = errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?")
	var ov struct {
		Docker struct {
			Available bool
			Error     string
		}
		Worktrees []struct{ State string }
	}
	if st := e.c.call("GET", "/api/v1/projects/ubapp/overview?refresh=1", nil, &ov); st != 200 {
		t.Fatalf("overview with Docker down = %d", st)
	}
	if ov.Docker.Available || ov.Docker.Error == "" || len(ov.Worktrees) != 2 || ov.Worktrees[0].State != "unknown" {
		t.Errorf("overview = %+v", ov)
	}
	var list []any
	if st := e.c.call("GET", "/api/v1/projects", nil, &list); st != 200 || len(list) != 1 {
		t.Errorf("projects = %d", st)
	}
	var br []any
	if st := e.c.call("GET", "/api/v1/projects/ubapp/branches", nil, &br); st != 200 || len(br) == 0 {
		t.Errorf("branches = %d %v", st, br)
	}
	// An operation reports the cause.
	id := e.wtID(e.main)
	var op struct{ ID string }
	e.fc.upHook = func(context.Context, compose.Target, io.Writer, io.Writer) error { return e.fc.psErr }
	if st := e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+id+"/up", nil, &op); st != 202 {
		t.Fatalf("up = %d", st)
	}
	final := e.waitOp(op.ID)
	if final.State != "failed" || final.Error == nil || final.Error.Code != "docker_unavailable" {
		t.Errorf("failed op = %+v", final)
	}
}

type opState struct {
	ID    string `json:"id"`
	Kind  string
	State string
	Error *struct{ Code, Message, Original string }
}

func (e *env) waitOp(id string) opState {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var op opState
		e.c.call("GET", "/api/v1/operations/"+id, nil, &op)
		if op.State != "queued" && op.State != "running" && op.State != "cancelling" {
			return op
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("operation %s did not finish", id)
	return opState{}
}

func TestUpDownLifecycle(t *testing.T) {
	e := newEnv(t)
	id := e.wtID(e.linked)
	base := "/api/v1/projects/ubapp/worktrees/" + id
	var op opState
	st, _, hdr := e.c.raw("POST", base+"/up", map[string]any{"auto_ports": false}, nil)
	if st != 202 || !strings.HasPrefix(hdr.Get("Location"), "/api/v1/operations/op_") {
		t.Fatalf("up = %d %v", st, hdr)
	}
	e.c.call("POST", base+"/up", nil, &op) // may be busy or a new op; ignore
	var list []opState
	e.c.call("GET", "/api/v1/operations", nil, &list)
	if len(list) == 0 {
		t.Fatal("no operations listed")
	}
	e.waitOp(list[0].ID)
	var w struct {
		State      string
		Containers []any
	}
	e.c.call("GET", base, nil, &w)
	if w.State != "running" || len(w.Containers) != 2 {
		t.Errorf("after up: %+v", w)
	}
	var down opState
	e.c.call("POST", base+"/down", nil, &down)
	if f := e.waitOp(down.ID); f.State != "succeeded" {
		t.Errorf("down = %+v", f)
	}
	e.c.call("GET", base, nil, &w)
	if w.State != "absent" {
		t.Errorf("after down: %+v", w)
	}
	// History is written by the engine exactly as the CLI does.
	var hist []struct{ Operation, Result string }
	e.c.call("GET", base+"/history", nil, &hist)
	if len(hist) < 2 || hist[len(hist)-1].Operation != "down" {
		t.Errorf("history = %+v", hist)
	}
}

func TestSecondUpOnSameInstanceIsBusy(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	e.fc.upHook = func(ctx context.Context, _ compose.Target, so, _ io.Writer) error {
		io.WriteString(so, "pulling\n")
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}
	a, b := e.wtID(e.main), e.wtID(e.linked)
	var first opState
	e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+a+"/up", nil, &first)
	var busy apiErr
	if st := e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+a+"/up", nil, &busy); st != 409 || busy.Error.Code != "busy" || busy.Error.Operation == nil || busy.Error.Operation.ID != first.ID {
		t.Errorf("second up = %d %+v", st, busy.Error)
	}
	// A different instance runs at the same time.
	var other opState
	if st := e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+b+"/up", nil, &other); st != 202 {
		t.Errorf("up on another instance = %d", st)
	}
	// The overview and the detail say who is busy.
	var d struct {
		Busy *struct {
			OperationID string `json:"operation_id"`
		}
	}
	e.c.call("GET", "/api/v1/projects/ubapp/worktrees/"+a, nil, &d)
	if d.Busy == nil || d.Busy.OperationID != first.ID {
		t.Errorf("busy = %+v", d.Busy)
	}
	// Removing a busy instance is refused.
	var rb apiErr
	if st := e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+a+"/remove", nil, &rb); st != 409 || rb.Error.Code != "busy" {
		t.Errorf("remove while busy = %d %+v", st, rb.Error)
	}
	close(release)
	e.waitOp(first.ID)
	e.waitOp(other.ID)
}

func TestIdempotencyKeyReturnsTheSameOperation(t *testing.T) {
	e := newEnv(t)
	id := e.wtID(e.main)
	var a, b opState
	h := map[string]string{"Idempotency-Key": "7b1c-test"}
	_, d1, _ := e.c.raw("POST", "/api/v1/projects/ubapp/worktrees/"+id+"/up", nil, h)
	_, d2, _ := e.c.raw("POST", "/api/v1/projects/ubapp/worktrees/"+id+"/up", nil, h)
	json.Unmarshal(d1, &a)
	json.Unmarshal(d2, &b)
	if a.ID == "" || a.ID != b.ID {
		t.Errorf("same key gave %q and %q", a.ID, b.ID)
	}
	e.waitOp(a.ID)
}

func TestCancelOperation(t *testing.T) {
	e := newEnv(t)
	started := make(chan struct{})
	e.fc.upHook = func(ctx context.Context, _ compose.Target, so, _ io.Writer) error {
		io.WriteString(so, "building\n")
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	id := e.wtID(e.main)
	var op opState
	e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+id+"/up", nil, &op)
	<-started
	if st := e.c.call("POST", "/api/v1/operations/"+op.ID+"/cancel", nil, nil); st != 202 {
		t.Fatalf("cancel = %d", st)
	}
	if f := e.waitOp(op.ID); f.State != "cancelled" {
		t.Errorf("state = %+v", f)
	}
	// The instance is free again.
	e.fc.upHook = nil
	if st := e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+id+"/up", nil, &op); st != 202 {
		t.Errorf("up after cancel = %d", st)
	}
	var nf apiErr
	if st := e.c.call("POST", "/api/v1/operations/op_nope/cancel", nil, &nf); st != 404 || nf.Error.Code != "operation_not_found" {
		t.Errorf("cancel unknown = %d %+v", st, nf.Error)
	}
}

func TestOperationEventsAndReconnect(t *testing.T) {
	e := newEnv(t)
	gate := make(chan struct{})
	e.fc.upHook = func(ctx context.Context, _ compose.Target, so, se io.Writer) error {
		for i := 1; i <= 5; i++ {
			io.WriteString(so, "line "+string(rune('0'+i))+"\n")
		}
		io.WriteString(se, "warning\n")
		<-gate
		io.WriteString(so, "after\n")
		return nil
	}
	id := e.wtID(e.main)
	var op opState
	e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+id+"/up", nil, &op)
	s := e.c.sse("/api/v1/operations/"+op.ID+"/events", "")
	var lastID string
	lines := 0
	for lines < 6 {
		ev, ok := s.next(t, 5*time.Second)
		if !ok {
			t.Fatal("stream ended early")
		}
		lastID = ev.ID
		if ev.Name == "log" {
			lines++
		}
	}
	s.close()
	close(gate)
	e.waitOp(op.ID)
	// Reconnect: only what came after lastID, ending with the result.
	s2 := e.c.sse("/api/v1/operations/"+op.ID+"/events", lastID)
	_, seen := s2.until(t, "result", 5*time.Second)
	for _, ev := range seen {
		if ev.ID != "" && ev.ID <= lastID && len(ev.ID) == len(lastID) {
			t.Errorf("replayed event %s <= %s", ev.ID, lastID)
		}
	}
	var sawAfter bool
	for _, ev := range seen {
		if ev.Name == "log" && strings.Contains(ev.Data, "after") {
			sawAfter = true
		}
	}
	if !sawAfter {
		t.Errorf("reconnect missed the line written while disconnected: %v", seen)
	}
	if ev, ok := s2.next(t, 3*time.Second); ok && ev.Name != "state" {
		t.Errorf("unexpected event after result: %+v", ev)
	}
}

func TestRingBufferGap(t *testing.T) {
	e := newEnv(t)
	e.fc.upHook = func(ctx context.Context, _ compose.Target, so, _ io.Writer) error {
		var sb strings.Builder
		for i := 0; i < 6000; i++ {
			sb.WriteString("x\n")
		}
		io.WriteString(so, sb.String())
		return nil
	}
	id := e.wtID(e.main)
	var op opState
	e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+id+"/up", nil, &op)
	e.waitOp(op.ID)
	s := e.c.sse("/api/v1/operations/"+op.ID+"/events", "3")
	gap, _ := s.until(t, "gap", 5*time.Second)
	if !strings.Contains(gap.Data, "dropped") {
		t.Errorf("gap = %+v", gap)
	}
}

func TestGlobalEventsAnnounceOperations(t *testing.T) {
	e := newEnv(t)
	s := e.c.sse("/api/v1/events", "")
	if ev, _ := s.next(t, 3*time.Second); ev.Name != "hello" {
		t.Fatalf("first event %+v", ev)
	}
	id := e.wtID(e.main)
	var op opState
	e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+id+"/up", nil, &op)
	s.until(t, "operation.created", 5*time.Second)
	ev, _ := s.until(t, "state.changed", 5*time.Second)
	if !strings.Contains(ev.Data, "ubapp") {
		t.Errorf("state.changed = %+v", ev)
	}
}

func TestPollingDetectsChangesMadeOutsideTheo(t *testing.T) {
	e := newEnv(t)
	s := e.c.sse("/api/v1/events", "")
	s.next(t, 3*time.Second) // hello
	if st := e.c.call("POST", "/api/v1/events/focus", map[string]bool{"visible": true}, nil); st != 204 {
		t.Fatal(st)
	}
	time.Sleep(300 * time.Millisecond) // first poll records the baseline
	cfg, _ := e.fc.Config(context.Background(), e.main, nil)
	e.fc.set(cfg.Name, running("web", cfg.Name))
	s.until(t, "state.changed", 5*time.Second)
}

func TestNoPollingWithoutVisibleTab(t *testing.T) {
	e := newEnv(t)
	s := e.c.sse("/api/v1/events", "")
	s.next(t, 3*time.Second)
	cfg, _ := e.fc.Config(context.Background(), e.main, nil)
	time.Sleep(300 * time.Millisecond)
	e.fc.set(cfg.Name, running("web", cfg.Name))
	if ev, ok := s.next(t, 700*time.Millisecond); ok {
		t.Errorf("event without a visible tab: %+v", ev)
	}
}

func TestCreateWorktreeAndRemoveWithConfirmation(t *testing.T) {
	e := newEnv(t)
	path := filepath.Join(t.TempDir(), "ub-new")
	var added struct {
		ID, Path, Branch string
		Notes            []string
	}
	if st := e.c.call("POST", "/api/v1/projects/ubapp/worktrees", map[string]any{"branch": "ub-feature", "new": true, "path": path}, &added); st != 201 {
		t.Fatalf("create = %d", st)
	}
	if added.ID == "" || added.Notes == nil || added.Branch != "ub-feature" {
		t.Errorf("created = %+v", added)
	}
	// The branch is in use now: a second worktree for it is refused.
	var dup apiErr
	if st := e.c.call("POST", "/api/v1/projects/ubapp/worktrees", map[string]any{"branch": "ub-feature", "path": filepath.Join(t.TempDir(), "x")}, &dup); st != 409 || dup.Error.Code != "branch_checked_out" {
		t.Errorf("duplicate branch = %d %+v", st, dup.Error)
	}
	var br []struct{ Name, Worktree string }
	e.c.call("GET", "/api/v1/projects/ubapp/branches", nil, &br)
	found := false
	for _, b := range br {
		if b.Name == "ub-feature" && b.Worktree != "" {
			found = true
		}
	}
	if !found {
		t.Errorf("branches %v do not show ub-feature in use", br)
	}

	// An uncommitted file: removing asks for the directory name.
	os.WriteFile(filepath.Join(path, "scratch.txt"), []byte("x"), 0o644)
	url := "/api/v1/projects/ubapp/worktrees/" + added.ID + "/remove"
	var need apiErr
	if st := e.c.call("POST", url, map[string]any{}, &need); st != 409 || need.Error.Code != "confirmation_required" {
		t.Fatalf("remove = %d %+v", st, need.Error)
	}
	if need.Error.Level != 3 || need.Error.Confirm == nil || need.Error.Confirm.Expected != "ub-new" || len(need.Error.Affected) == 0 || len(need.Error.Preserved) == 0 {
		t.Errorf("confirmation = %+v", need.Error)
	}
	if need.Error.Affected[0].Name != "scratch.txt" {
		t.Errorf("affected = %+v", need.Error.Affected)
	}
	// Wrong text: the server refuses again; the file is still there.
	var wrong apiErr
	if st := e.c.call("POST", url, map[string]any{"confirm": "ub-other"}, &wrong); st != 409 || wrong.Error.Code != "confirmation_required" {
		t.Errorf("wrong confirm = %d %+v", st, wrong.Error)
	}
	if _, err := os.Stat(filepath.Join(path, "scratch.txt")); err != nil {
		t.Fatal("the file was removed by a wrong confirmation")
	}
	// discard_changes without the text is not enough either.
	if st := e.c.call("POST", url, map[string]any{"discard_changes": true}, &wrong); st != 409 {
		t.Errorf("discard without text = %d", st)
	}
	var ok map[string]any
	if st := e.c.call("POST", url, map[string]any{"confirm": "ub-new"}, &ok); st != 200 || ok["removed"] != true {
		t.Fatalf("confirmed remove = %d %v", st, ok)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the directory still exists")
	}
	// The branch is kept.
	br = nil
	e.c.call("GET", "/api/v1/projects/ubapp/branches", nil, &br)
	kept := false
	for _, b := range br {
		if b.Name == "ub-feature" {
			kept = true
		}
	}
	if !kept {
		t.Error("the branch was deleted")
	}
}

func TestRemoveWorktreeWithContainersIsRejected(t *testing.T) {
	e := newEnv(t)
	id := e.wtID(e.linked)
	var op opState
	e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+id+"/up", nil, &op)
	e.waitOp(op.ID)
	// The fake lists the stack the way docker compose ls would.
	e.fc.files = map[string]string{"ubapp-app-feature": filepath.Join(e.linked, "compose.yaml")}
	var er apiErr
	st := e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+id+"/remove", map[string]any{"confirm": "app-feature"}, &er)
	if st != 409 || (er.Error.Code != "instance_has_containers" && er.Error.Code != "confirmation_required") {
		t.Errorf("remove with containers = %d %+v", st, er.Error)
	}
	if _, err := os.Stat(e.linked); err != nil {
		t.Error("the worktree was removed")
	}
	// The main worktree is never removed.
	mid := e.wtID(e.main)
	if st := e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+mid+"/remove", nil, &er); st < 400 {
		t.Errorf("removing the main worktree = %d", st)
	}
}

func TestEnvSecretsAreMasked(t *testing.T) {
	e := newEnv(t)
	id := e.wtID(e.linked)
	base := "/api/v1/projects/ubapp/worktrees/" + id + "/env"
	var r map[string]string
	if st := e.c.call("PUT", base+"/DB_URL", map[string]any{"value": "postgres://u:hunter2@db/x", "secret": true}, &r); st != 200 || r["applies_on"] != "next_up" {
		t.Fatalf("put = %d %v", st, r)
	}
	e.c.call("PUT", base+"/LEVEL", map[string]any{"value": "debug", "services": []string{"web"}}, &r)
	var bad apiErr
	if st := e.c.call("PUT", base+"/1BAD", map[string]any{"value": "x"}, &bad); st != 400 || bad.Error.Code != "invalid_name" {
		t.Errorf("bad name = %d %+v", st, bad.Error)
	}
	st, body, _ := e.c.raw("GET", base, nil, nil)
	if st != 200 || strings.Contains(string(body), "hunter2") {
		t.Fatalf("list leaks the secret: %d %s", st, body)
	}
	var vars []struct {
		Name, Value string
		Secret      bool
		Services    []string
	}
	json.Unmarshal(body, &vars)
	if len(vars) != 2 || vars[0].Name != "DB_URL" || vars[0].Value != "********" || !vars[0].Secret || vars[1].Value != "debug" || vars[1].Services[0] != "web" {
		t.Errorf("vars = %+v", vars)
	}
	// Nothing else leaks it: detail, settings, history, config.
	for _, p := range []string{"", "/settings", "/history", "/config"} {
		_, b, _ := e.c.raw("GET", "/api/v1/projects/ubapp/worktrees/"+id+p, nil, nil)
		if strings.Contains(string(b), "hunter2") {
			t.Errorf("GET %s leaks the secret", p)
		}
	}
	// reveal returns ONE variable, uncached.
	st, body, hdr := e.c.raw("POST", base+"/DB_URL/reveal", nil, nil)
	if st != 200 || !strings.Contains(string(body), "hunter2") || hdr.Get("Cache-Control") != "no-store" {
		t.Errorf("reveal = %d %s %q", st, body, hdr.Get("Cache-Control"))
	}
	if strings.Contains(string(body), "LEVEL") || strings.Contains(string(body), "debug") {
		t.Errorf("reveal returned other variables: %s", body)
	}
	if st := e.c.call("DELETE", base+"/DB_URL", nil, nil); st != 200 {
		t.Errorf("delete = %d", st)
	}
	var nf apiErr
	if st := e.c.call("DELETE", base+"/DB_URL", nil, &nf); st != 404 || nf.Error.Code != "env_not_found" {
		t.Errorf("delete again = %d %+v", st, nf.Error)
	}
}

func TestSettingsPatch(t *testing.T) {
	e := newEnv(t)
	id := e.wtID(e.linked)
	var s struct {
		Neutralize bool `json:"neutralize_container_names"`
		AutoPorts  bool `json:"auto_ports"`
	}
	if st := e.c.call("PATCH", "/api/v1/projects/ubapp/worktrees/"+id+"/settings", map[string]any{"neutralize_container_names": true, "auto_ports": true}, &s); st != 200 || !s.Neutralize || !s.AutoPorts {
		t.Errorf("patch = %d %+v", st, s)
	}
	e.c.call("GET", "/api/v1/projects/ubapp/worktrees/"+id+"/settings", nil, &s)
	if !s.Neutralize {
		t.Error("setting not stored")
	}
}

func TestVolumesAndPurge(t *testing.T) {
	e := newEnv(t)
	id := e.wtID(e.linked)
	cfg, _ := e.fc.Config(context.Background(), e.linked, nil)
	_ = cfg
	// The linked instance is named <project>-<dir>.
	name := "ubapp-app-feature"
	e.fc.volumes = map[string][]compose.DockerVolume{name: {{Name: name + "_pgdata", Labels: map[string]string{compose.LabelVolume: "pgdata"}}}}
	base := "/api/v1/projects/ubapp/worktrees/" + id
	var vols []struct{ Name, Status string }
	if st := e.c.call("GET", base+"/volumes", nil, &vols); st != 200 || len(vols) != 1 || vols[0].Status != "kept" {
		t.Fatalf("volumes = %d %+v", st, vols)
	}
	var need apiErr
	if st := e.c.call("POST", base+"/volumes/purge", map[string]any{}, &need); st != 409 || need.Error.Code != "confirmation_required" {
		t.Fatalf("purge = %d %+v", st, need.Error)
	}
	if need.Error.Level != 3 || need.Error.Confirm.Expected != name || len(need.Error.Affected) != 1 || need.Error.Affected[0].Kind != "volume" {
		t.Errorf("confirmation = %+v", need.Error)
	}
	if st := e.c.call("POST", base+"/volumes/purge", map[string]any{"confirm": "wrong"}, &need); st != 409 {
		t.Errorf("wrong text = %d", st)
	}
	var done struct{ Deleted []struct{ Name string } }
	if st := e.c.call("POST", base+"/volumes/purge", map[string]any{"confirm": name}, &done); st != 200 || len(done.Deleted) != 1 {
		t.Errorf("purge = %d %+v", st, done)
	}
	// With containers the purge is refused with its own code.
	e.fc.set(name, running("web", name))
	var hc apiErr
	if st := e.c.call("POST", base+"/volumes/purge", map[string]any{"confirm": name}, &hc); st != 409 || hc.Error.Code != "instance_has_containers" {
		t.Errorf("purge with containers = %d %+v", st, hc.Error)
	}
}

func TestLogsStreamAndGap(t *testing.T) {
	e := newEnv(t)
	id := e.wtID(e.linked)
	var stopped sync.WaitGroup
	stopped.Add(1)
	e.fc.logsHook = func(ctx context.Context, o compose.LogOptions, so, se io.Writer) error {
		defer stopped.Done()
		io.WriteString(so, "ubapp-app-feature-web-1  | first line\n")
		io.WriteString(se, "ubapp-app-feature-db-1  | err line\n")
		<-ctx.Done()
		return ctx.Err()
	}
	s := e.c.sse("/api/v1/projects/ubapp/worktrees/"+id+"/logs?follow=1&tail=10&service=web", "")
	var got []sseEvent
	for len(got) < 2 {
		ev, ok := s.next(t, 3*time.Second)
		if !ok {
			t.Fatalf("no log lines: %v", got)
		}
		if ev.Name == "log" {
			got = append(got, ev)
		}
	}
	if !strings.Contains(got[0].Data, `"service":"web"`) || !strings.Contains(got[0].Data, "first line") {
		t.Errorf("first = %+v", got[0])
	}
	if !strings.Contains(got[1].Data, `"stream":"stderr"`) {
		t.Errorf("second = %+v", got[1])
	}
	// Closing the client ends the logs process.
	s.close()
	done := make(chan struct{})
	go func() { stopped.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the logs process was not cancelled when the client left")
	}
	var bad apiErr
	if st := e.c.call("GET", "/api/v1/projects/ubapp/worktrees/"+id+"/logs?follow=1&service=nope", nil, &bad); st < 400 {
		t.Errorf("unknown service = %d", st)
	}
}

func TestLogsBackPressureReportsGap(t *testing.T) {
	e := newEnv(t)
	id := e.wtID(e.linked)
	e.fc.logsHook = func(ctx context.Context, o compose.LogOptions, so, se io.Writer) error {
		var sb strings.Builder
		for i := 0; i < 200000; i++ {
			sb.WriteString("w  | spam\n")
		}
		io.WriteString(so, sb.String())
		<-ctx.Done()
		return nil
	}
	// A client that does not read: open the stream and sleep before reading.
	s := e.c.sse("/api/v1/projects/ubapp/worktrees/"+id+"/logs?follow=1", "")
	time.Sleep(300 * time.Millisecond)
	_, seen := s.until(t, "gap", 20*time.Second)
	_ = seen
}

func TestLogsDownload(t *testing.T) {
	e := newEnv(t)
	id := e.wtID(e.linked)
	st, body, hdr := e.c.raw("GET", "/api/v1/projects/ubapp/worktrees/"+id+"/logs?follow=0&tail=all", nil, nil)
	if st != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/plain") || !strings.Contains(hdr.Get("Content-Disposition"), "attachment") || !strings.Contains(string(body), "hello") {
		t.Errorf("download = %d %v %s", st, hdr, body)
	}
}

func TestServicesPortsConfigHistory(t *testing.T) {
	e := newEnv(t)
	id := e.wtID(e.linked)
	base := "/api/v1/projects/ubapp/worktrees/" + id
	var svc struct{ Services []struct{ Name string } }
	if st := e.c.call("GET", base+"/services", nil, &svc); st != 200 || len(svc.Services) != 2 {
		t.Errorf("services = %d %+v", st, svc)
	}
	var plan struct {
		Ports   []struct{ Service, Status string } `json:"ports"`
		Compose string                             `json:"compose_project"`
	}
	if st := e.c.call("GET", base+"/ports?auto_ports=1", nil, &plan); st != 200 || len(plan.Ports) != 1 || plan.Compose == "" {
		t.Errorf("ports = %d %+v", st, plan)
	}
	st, body, hdr := e.c.raw("GET", base+"/config", nil, nil)
	if st != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/plain") || len(body) == 0 {
		t.Errorf("config = %d", st)
	}
	var bad apiErr
	if st := e.c.call("GET", base+"/history?limit=zz", nil, &bad); st != 400 {
		t.Errorf("bad limit = %d", st)
	}
}

func TestFSBrowseListsOnlyDirectories(t *testing.T) {
	e := newEnv(t)
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "alpha"), 0o755)
	os.Mkdir(filepath.Join(root, ".hidden"), 0o755)
	os.Mkdir(filepath.Join(root, "repo"), 0o755)
	os.Mkdir(filepath.Join(root, "repo", ".git"), 0o755)
	os.WriteFile(filepath.Join(root, "secret.txt"), []byte("TOPSECRET"), 0o644)
	st, body, _ := e.c.raw("GET", "/api/v1/fs/browse?path="+root, nil, nil)
	if st != 200 || strings.Contains(string(body), "secret.txt") || strings.Contains(string(body), "TOPSECRET") || strings.Contains(string(body), ".hidden") {
		t.Fatalf("browse = %d %s", st, body)
	}
	var out struct {
		Entries []struct {
			Name   string
			IsRepo bool `json:"is_repo"`
		}
		Parent string
	}
	json.Unmarshal(body, &out)
	if len(out.Entries) != 2 || out.Entries[0].Name != "alpha" || out.Entries[1].Name != "repo" || !out.Entries[1].IsRepo || out.Parent == "" {
		t.Errorf("entries = %+v", out)
	}
	var bad apiErr
	if st := e.c.call("GET", "/api/v1/fs/browse?path=relative/dir", nil, &bad); st != 400 {
		t.Errorf("relative path = %d", st)
	}
	if st := e.c.call("GET", "/api/v1/fs/browse?path="+filepath.Join(root, "secret.txt"), nil, &bad); st != 404 {
		t.Errorf("a file = %d", st)
	}
}

func TestIdleShutdown(t *testing.T) {
	// No SSE client and no operation: the server stops by itself.
	e := newEnv(t, func(c *api.Config) { c.IdleTimeout = 400 * time.Millisecond })
	e.c.call("GET", "/api/v1/meta", nil, nil)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(e.srv.Port()), 200*time.Millisecond); err != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Error("the idle server did not stop")
}

func TestServerStaysWhileSSEOrOperation(t *testing.T) {
	e := newEnv(t, func(c *api.Config) { c.IdleTimeout = 400 * time.Millisecond })
	s := e.c.sse("/api/v1/events", "")
	s.next(t, 2*time.Second)
	time.Sleep(1200 * time.Millisecond)
	if st := e.c.call("GET", "/api/v1/meta", nil, nil); st != 200 {
		t.Fatalf("the server stopped with an SSE client: %d", st)
	}
	s.close()
	// A running operation keeps it alive too.
	release := make(chan struct{})
	e.fc.upHook = func(ctx context.Context, _ compose.Target, _, _ io.Writer) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}
	id := e.wtID(e.main)
	var op opState
	e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+id+"/up", nil, &op)
	time.Sleep(1500 * time.Millisecond)
	if st := e.c.call("GET", "/api/v1/meta", nil, nil); st != 200 {
		t.Fatalf("the server stopped during an operation: %d", st)
	}
	close(release)
	e.waitOp(op.ID)
}

// Every error type of the engine is handled by the error table.
func TestEveryEngineErrorTypeIsMapped(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, "../engine", func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]bool{}
	hasError := map[string]bool{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				switch d := d.(type) {
				case *ast.GenDecl:
					for _, sp := range d.Specs {
						if ts, ok := sp.(*ast.TypeSpec); ok {
							if _, isStruct := ts.Type.(*ast.StructType); isStruct && strings.HasSuffix(ts.Name.Name, "Error") {
								types[ts.Name.Name] = true
							}
						}
					}
				case *ast.FuncDecl:
					if d.Name.Name == "Error" && d.Recv != nil {
						if star, ok := d.Recv.List[0].Type.(*ast.StarExpr); ok {
							if id, ok := star.X.(*ast.Ident); ok {
								hasError[id.Name] = true
							}
						}
					}
				}
			}
		}
	}
	src, _ := os.ReadFile("errors.go")
	for name := range types {
		if !hasError[name] {
			continue
		}
		if !strings.Contains(string(src), `"`+name+`":`) || !strings.Contains(string(src), "engine."+name) {
			t.Errorf("engine error type %s has no entry in internal/api/errors.go", name)
		}
	}
	if len(types) == 0 {
		t.Fatal("found no error types: the scan is broken")
	}
}
