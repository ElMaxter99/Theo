package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/api"
	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// perfCompose wraps the fake Compose with counters, delays and a snapshot
// read, to see how many times the server asks Docker for what.
type perfCompose struct {
	*fakeCompose
	snapshot bool // offers AllContainers (the real adapter does)

	stacks, ps, all atomic.Int64
	stacksDelay     time.Duration
	allDelay        time.Duration
	allErr          error
	blockUntilDone  bool // Stacks and AllContainers hang until their context ends
}

func (p *perfCompose) Stacks(ctx context.Context) ([]compose.Stack, error) {
	p.stacks.Add(1)
	if p.blockUntilDone {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	time.Sleep(p.stacksDelay)
	return p.fakeCompose.Stacks(ctx)
}

func (p *perfCompose) PS(ctx context.Context, t compose.Target) ([]compose.Container, error) {
	p.ps.Add(1)
	return p.fakeCompose.PS(ctx, t)
}

// withSnapshot is perfCompose seen through the optional interface: only a
// value of this type has AllContainers.
type withSnapshot struct{ *perfCompose }

func (w withSnapshot) AllContainers(ctx context.Context) (map[string][]compose.Container, error) {
	w.all.Add(1)
	if w.blockUntilDone {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	time.Sleep(w.allDelay)
	if w.allErr != nil {
		return nil, w.allErr
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[string][]compose.Container{}
	for name, cs := range w.containers {
		if len(cs) > 0 {
			out[strings.ToLower(name)] = append([]compose.Container(nil), cs...)
		}
	}
	return out, nil
}

// perfEnv is an env whose engine uses pc. extra registers more projects.
func perfEnv(t *testing.T, pc *perfCompose, extra int, opts ...func(*api.Config)) *env {
	t.Helper()
	main, linked := testutil.NewRepo(t, map[string]string{"compose.yaml": "services:\n  web:\n    image: scratch-ub\n"})
	dir := testutil.TempDir(t)
	var cmp engine.Compose = pc
	if pc.snapshot {
		cmp = withSnapshot{pc}
	}
	eng := engine.New(registry.Open(dir), git.Git{}, cmp).WithDataDir(dir)
	if _, err := eng.AddProject(context.Background(), main, "ubapp", ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < extra; i++ {
		m, _ := testutil.NewRepo(t, map[string]string{"compose.yaml": "services:\n  web:\n    image: scratch-ub\n"})
		if _, err := eng.AddProject(context.Background(), m, "extra"+string(rune('a'+i)), ""); err != nil {
			t.Fatal(err)
		}
	}
	return startEnv(t, eng, pc.fakeCompose, main, linked, dir, opts...)
}

func concurrently(n int, f func(i int)) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			f(i)
		}()
	}
	close(start)
	wg.Wait()
}

func TestHealthConcurrentRequestsShareOneReconcile(t *testing.T) {
	pc := &perfCompose{fakeCompose: &fakeCompose{}, stacksDelay: 300 * time.Millisecond}
	e := perfEnv(t, pc, 0)
	bodies := make([]string, 12)
	concurrently(len(bodies), func(i int) {
		st, body, _ := e.c.raw("GET", "/api/v1/health", nil, nil)
		if st != 200 {
			t.Errorf("status %d %s", st, body)
		}
		bodies[i] = string(body)
	})
	if got := pc.stacks.Load(); got != 1 {
		t.Errorf("%d concurrent polls ran %d reconciles, want 1", len(bodies), got)
	}
	for _, b := range bodies[1:] {
		if b != bodies[0] {
			t.Errorf("answers differ: %s vs %s", b, bodies[0])
		}
	}
	// Warm: no new computation.
	e.c.raw("GET", "/api/v1/health", nil, nil)
	if got := pc.stacks.Load(); got != 1 {
		t.Errorf("a warm cache still reconciled: %d", got)
	}
}

func TestHealthAfterAChangeIsNotServedFromAComputationThatBeganBefore(t *testing.T) {
	pc := &perfCompose{fakeCompose: &fakeCompose{}, stacksDelay: 250 * time.Millisecond}
	e := perfEnv(t, pc, 0)
	first := make(chan struct{})
	go func() { e.c.raw("GET", "/api/v1/health", nil, nil); close(first) }()
	time.Sleep(80 * time.Millisecond) // the first reconcile is running
	// An operation-style change: drop the cached state through the public API.
	if st, _, _ := e.c.raw("POST", "/api/v1/reconcile/apply", map[string]any{"all": true}, nil); st != 200 {
		t.Fatalf("apply: %d", st)
	}
	before := pc.stacks.Load()
	if st, _, _ := e.c.raw("GET", "/api/v1/health", nil, nil); st != 200 {
		t.Fatal(st)
	}
	if pc.stacks.Load() <= before {
		t.Error("health asked after a change joined a reconcile that started before it")
	}
	<-first
}

func TestReconcileGETSharesOneComputation(t *testing.T) {
	pc := &perfCompose{fakeCompose: &fakeCompose{}, stacksDelay: 250 * time.Millisecond}
	e := perfEnv(t, pc, 0)
	concurrently(8, func(int) {
		var rep struct {
			Findings []json.RawMessage `json:"findings"`
		}
		if st := e.c.call("GET", "/api/v1/reconcile", nil, &rep); st != 200 {
			t.Errorf("status %d", st)
		}
	})
	if got := pc.stacks.Load(); got != 1 {
		t.Errorf("8 concurrent GET /reconcile ran %d reconciles, want 1", got)
	}
}

// overviewShape is what the cards show, without paths (they differ per env).
func overviewShape(t *testing.T, e *env, project string) string {
	t.Helper()
	var ov struct {
		Docker    api.DockerInfo `json:"docker"`
		Worktrees []struct {
			Branch     string                 `json:"branch"`
			State      string                 `json:"state"`
			Compose    string                 `json:"compose_project"`
			Containers api.ContainerCount     `json:"containers"`
			Ports      []engine.PortStatus    `json:"ports"`
			Warnings   []engine.StatusWarning `json:"warnings"`
		} `json:"worktrees"`
	}
	if st := e.c.call("GET", "/api/v1/projects/"+project+"/overview?refresh=1", nil, &ov); st != 200 {
		t.Fatalf("overview: %d", st)
	}
	for i := range ov.Worktrees {
		ov.Worktrees[i].Compose = ""
	}
	b, _ := json.Marshal(ov)
	return string(b)
}

func TestOverviewFromTheSnapshotEqualsTheOnePerInstanceRead(t *testing.T) {
	var shapes [2]string
	for i, snapshot := range []bool{false, true} {
		pc := &perfCompose{fakeCompose: &fakeCompose{}, snapshot: snapshot}
		e := perfEnv(t, pc, 0)
		// The linked worktree runs; the main one does not.
		e.c.raw("POST", "/api/v1/projects/ubapp/worktrees/"+e.wtID(e.linked)+"/up", nil, nil)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && !strings.Contains(overviewShape(t, e, "ubapp"), `"running"`) {
			time.Sleep(50 * time.Millisecond)
		}
		psBefore := pc.ps.Load() // the operation itself may read the containers
		shapes[i] = overviewShape(t, e, "ubapp")
		if !strings.Contains(shapes[i], `"state":"running"`) || !strings.Contains(shapes[i], `"state":"absent"`) {
			t.Fatalf("the fixture did not produce a running and an absent worktree: %s", shapes[i])
		}
		if snapshot && pc.ps.Load() != psBefore {
			t.Errorf("the overview ran `compose ps` %d times in snapshot mode", pc.ps.Load()-psBefore)
		}
	}
	if shapes[0] != shapes[1] {
		t.Errorf("the overview changed with the way Docker is read:\n per instance: %s\n snapshot:     %s", shapes[0], shapes[1])
	}
}

func TestOverviewsOfManyProjectsReadDockerOnce(t *testing.T) {
	pc := &perfCompose{fakeCompose: &fakeCompose{}, snapshot: true, allDelay: 200 * time.Millisecond}
	e := perfEnv(t, pc, 5)
	projects := []string{"ubapp", "extraa", "extrab", "extrac", "extrad", "extrae"}
	concurrently(len(projects)*3, func(i int) {
		if st, body, _ := e.c.raw("GET", "/api/v1/projects/"+projects[i%len(projects)]+"/overview", nil, nil); st != 200 {
			t.Errorf("%s: %d %s", projects[i%len(projects)], st, body)
		}
	})
	if got := pc.all.Load(); got != 1 {
		t.Errorf("%d project cards read Docker %d times, want 1", len(projects)*3, got)
	}
	if got := pc.ps.Load(); got != 0 {
		t.Errorf("`compose ps` ran %d times, want 0", got)
	}
}

func TestOverviewIsSharedAndCachedPerProject(t *testing.T) {
	pc := &perfCompose{fakeCompose: &fakeCompose{}, snapshot: true, allDelay: 200 * time.Millisecond}
	e := perfEnv(t, pc, 0)
	concurrently(10, func(int) { e.c.raw("GET", "/api/v1/projects/ubapp/overview", nil, nil) })
	if got := pc.all.Load(); got != 1 {
		t.Errorf("10 concurrent requests for one project read Docker %d times, want 1", got)
	}
	e.c.raw("GET", "/api/v1/projects/ubapp/overview", nil, nil)
	if got := pc.all.Load(); got != 1 {
		t.Errorf("a warm cache read Docker again: %d", got)
	}
}

func TestOverviewWithDockerDownIsAnsweredOnceAndSaysSo(t *testing.T) {
	pc := &perfCompose{fakeCompose: &fakeCompose{}, snapshot: true, allErr: errors.New("Cannot connect to the Docker daemon"), allDelay: 100 * time.Millisecond}
	e := perfEnv(t, pc, 3)
	concurrently(8, func(i int) {
		name := []string{"ubapp", "extraa", "extrab", "extrac"}[i%4]
		var ov struct {
			Docker    api.DockerInfo `json:"docker"`
			Worktrees []struct {
				State string `json:"state"`
			} `json:"worktrees"`
		}
		if st := e.c.call("GET", "/api/v1/projects/"+name+"/overview", nil, &ov); st != 200 {
			t.Errorf("%s: %d", name, st)
			return
		}
		if ov.Docker.Available || !strings.Contains(ov.Docker.Error, "Docker daemon") {
			t.Errorf("%s: docker = %+v", name, ov.Docker)
		}
		for _, w := range ov.Worktrees {
			if w.State != "unknown" && w.State != "absent" {
				t.Errorf("%s: state %q with Docker down", name, w.State)
			}
		}
	})
	if got := pc.all.Load(); got != 1 {
		t.Errorf("a stopped Docker was asked %d times for 8 cards, want 1", got)
	}
}

func TestHungDockerEndsAtTheWorkTimeoutNotWhenTheClientGivesUp(t *testing.T) {
	pc := &perfCompose{fakeCompose: &fakeCompose{}, snapshot: true, blockUntilDone: true}
	e := perfEnv(t, pc, 0, func(c *api.Config) { c.WorkTimeout = 400 * time.Millisecond })

	start := time.Now()
	var ov struct {
		Docker api.DockerInfo `json:"docker"`
	}
	if st := e.c.call("GET", "/api/v1/projects/ubapp/overview", nil, &ov); st != 200 {
		t.Fatalf("overview: %d", st)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("a hung Docker held the overview for %s", d)
	}
	if ov.Docker.Available {
		t.Error("the overview says Docker is available")
	}

	start = time.Now()
	var h struct {
		Status string `json:"status"`
	}
	if st := e.c.call("GET", "/api/v1/health", nil, &h); st != 200 || h.Status != "docker_unavailable" {
		t.Fatalf("health = %d %+v", st, h)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("a hung Docker held /health for %s", d)
	}
}

// fakeDockerOnPath puts a `docker` on PATH that logs each call and hangs.
func fakeDockerOnPath(t *testing.T) (calls func() int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the docker binary")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	// `compose config` works without a daemon, as the real one does; every
	// command that needs the daemon hangs.
	script := "#!/bin/sh\ncase \"$*\" in *config*) echo '{\"name\":\"ubapp\",\"services\":{\"web\":{\"image\":\"scratch-ub\"}}}'; exit 0;; esac\necho \"$@\" >> '" + log + "'\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		b, err := os.ReadFile(log)
		if err != nil {
			return 0
		}
		return strings.Count(string(b), "\n")
	}
}

func TestHungDockerCommandIsKilledAndNotRetriedWhileItIsDown(t *testing.T) {
	calls := fakeDockerOnPath(t)
	main, _ := testutil.NewRepo(t, map[string]string{"compose.yaml": "services:\n  web:\n    image: scratch-ub\n"})
	dir := testutil.TempDir(t)
	eng := engine.New(registry.Open(dir), git.Git{}, compose.Compose{}).WithDataDir(dir)
	if _, err := eng.AddProject(context.Background(), main, "ubapp", ""); err != nil {
		t.Fatal(err)
	}
	e := startEnv(t, eng, &fakeCompose{}, main, "", dir, func(c *api.Config) {
		c.CommandTimeout = 300 * time.Millisecond
		c.ConfigCacheTTL = -1
	})

	start := time.Now()
	var ov struct {
		Docker api.DockerInfo `json:"docker"`
	}
	if st := e.c.call("GET", "/api/v1/projects/ubapp/overview?refresh=1", nil, &ov); st != 200 {
		t.Fatalf("overview: %d", st)
	}
	took := time.Since(start)
	if took > 10*time.Second {
		t.Errorf("the overview waited %s for a docker that never answers (its sleep is 30s)", took)
	}
	if ov.Docker.Available || !strings.Contains(ov.Docker.Error, "timed out") {
		t.Errorf("docker = %+v, want unavailable with a timeout", ov.Docker)
	}

	// While the breaker is open, the next looks do not start docker at all.
	n := calls()
	for i := 0; i < 3; i++ {
		e.c.raw("GET", "/api/v1/projects/ubapp/overview?refresh=1", nil, nil)
		time.Sleep(300 * time.Millisecond) // past the snapshot's freshness window
	}
	if after := calls(); after != n {
		t.Errorf("docker was started %d more times right after it timed out", after-n)
	}
	// No hung process is left behind.
	time.Sleep(500 * time.Millisecond)
	out, _ := exec.Command("pgrep", "-f", "sleep 30").Output()
	if len(strings.TrimSpace(string(out))) != 0 {
		t.Errorf("hung docker processes still running: %s", out)
	}
}

func TestPollingReadsDockerOncePerCycleNotOncePerProject(t *testing.T) {
	pc := &perfCompose{fakeCompose: &fakeCompose{}, snapshot: true}
	e := perfEnv(t, pc, 4) // 5 projects
	s := e.c.sse("/api/v1/events", "")
	s.next(t, 3*time.Second) // hello
	if st := e.c.call("POST", "/api/v1/events/focus", map[string]bool{"visible": true}, nil); st != 204 {
		t.Fatal(st)
	}
	start := time.Now()
	time.Sleep(1500 * time.Millisecond) // PollInterval is 100 ms in these tests
	cycles := int(time.Since(start)/(100*time.Millisecond)) + 2
	got := int(pc.all.Load())
	if got == 0 {
		t.Fatal("the poller never looked at Docker")
	}
	if got > cycles {
		t.Errorf("%d reads of Docker in at most %d cycles over 5 projects: it reads once per project", got, cycles)
	}
	if pc.ps.Load() != 0 {
		t.Errorf("the poller ran `compose ps` %d times", pc.ps.Load())
	}
}
