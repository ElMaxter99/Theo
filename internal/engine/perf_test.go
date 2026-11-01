package engine

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/registry"
)

// countingGit counts `git worktree list` per directory.
type countingGit struct {
	*fakeGit
	mu    sync.Mutex
	lists map[string]int
}

func (g *countingGit) Worktrees(ctx context.Context, dir string) ([]git.Worktree, error) {
	g.mu.Lock()
	if g.lists == nil {
		g.lists = map[string]int{}
	}
	g.lists[dir]++
	g.mu.Unlock()
	return g.fakeGit.Worktrees(ctx, dir)
}

func (g *countingGit) count(dir string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lists[dir]
}

func TestRequestMemoAsksGitOncePerDirectory(t *testing.T) {
	e, _, g, _ := newEngine()
	cg := &countingGit{fakeGit: g}
	e.git = cg
	ctx := context.Background()
	if _, err := e.AddProject(ctx, root, "", ""); err != nil {
		t.Fatal(err)
	}
	p, err := e.Project("app")
	if err != nil {
		t.Fatal(err)
	}

	base := cg.count(root)
	for i := 0; i < 3; i++ {
		if _, err := e.worktreeList(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if got := cg.count(root) - base; got != 3 {
		t.Fatalf("without a memo every call asks Git: %d calls, want 3", got)
	}

	memo := WithRequestMemo(ctx)
	base = cg.count(root)
	var first []git.Worktree
	for i := 0; i < 3; i++ {
		list, err := e.worktreeList(memo, p)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = list
			list[0].Branch = "scribbled" // a caller that edits its copy...
		} else if list[0].Branch != "main" || !reflect.DeepEqual(list[1:], first[1:]) {
			t.Fatalf("...must not change what the next caller gets: %+v", list)
		}
	}
	if got := cg.count(root) - base; got != 1 {
		t.Errorf("with a memo Git is asked once, got %d calls", got)
	}
	if WithRequestMemo(memo) != memo {
		t.Error("a context that already has a memo keeps it")
	}
}

// multiGit lists a single main worktree for any directory.
type multiGit struct{ *fakeGit }

func (multiGit) Worktrees(_ context.Context, dir string) ([]git.Worktree, error) {
	return []git.Worktree{{Path: dir, Branch: "main"}}, nil
}

// slowCompose answers Config after a pause and records how many ran at once.
type slowCompose struct {
	*fakeCompose
	delay         time.Duration
	running, peak atomic.Int64
	calls         atomic.Int64
}

func (c *slowCompose) Config(_ context.Context, dir string, _ []string) (*compose.Project, error) {
	n := c.running.Add(1)
	for {
		p := c.peak.Load()
		if n <= p || c.peak.CompareAndSwap(p, n) {
			break
		}
	}
	c.calls.Add(1)
	time.Sleep(c.delay)
	c.running.Add(-1)
	return &compose.Project{Name: filepath.Base(dir), Services: map[string]compose.Service{"web": {Image: "x"}}}, nil
}

func TestRegisteredInstancesAreResolvedInParallelInOrderAndBounded(t *testing.T) {
	store := &memStore{}
	var names []string
	for i := 0; i < 12; i++ {
		name := "p" + string(rune('a'+i))
		names = append(names, name)
		store.projects = append(store.projects, registry.Project{Name: name, Path: abs("/src/" + name)})
	}
	c := &slowCompose{fakeCompose: &fakeCompose{configs: map[string]*compose.Project{}}, delay: 40 * time.Millisecond}
	e := New(store, multiGit{&fakeGit{}}, c)

	start := time.Now()
	refs, err := e.RegisteredInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	if len(refs) != 12 {
		t.Fatalf("%d instances, want 12", len(refs))
	}
	for i, r := range refs {
		if r.Project != names[i] || r.Compose != names[i] {
			t.Errorf("refs[%d] = %+v, want project %s in registry order", i, r, names[i])
		}
	}
	if peak := c.peak.Load(); peak < 2 || peak > registeredParallelism {
		t.Errorf("%d `compose config` at once, want between 2 and %d", peak, registeredParallelism)
	}
	if serial := 12 * c.delay; took > serial*3/4 {
		t.Errorf("took %s, serial would be %s: not parallel", took, serial)
	}
}

func TestConfigCacheOnlyForPanelReadsAndFollowsTheFiles(t *testing.T) {
	dir := t.TempDir()
	compose1 := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(compose1, []byte("services:\n  web:\n    image: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &memStore{projects: []registry.Project{{Name: "app", Path: dir}}}
	c := &slowCompose{fakeCompose: &fakeCompose{configs: map[string]*compose.Project{}}}
	e := New(store, multiGit{&fakeGit{}}, c).EnableConfigCache(time.Minute)
	now := time.Now()
	e.cfgCache.now = func() time.Time { return now }

	resolve := func(ctx context.Context) {
		t.Helper()
		if _, err := e.InstanceFor(ctx, "app", dir, InstanceOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	plain, cached := context.Background(), WithCachedConfig(context.Background())

	resolve(plain)
	resolve(plain)
	if got := c.calls.Load(); got != 2 {
		t.Fatalf("a read that acts on the configuration always asks Compose: %d calls, want 2", got)
	}
	resolve(cached) // fills the cache
	resolve(cached)
	resolve(cached)
	if got := c.calls.Load(); got != 3 {
		t.Fatalf("panel reads share one answer: %d calls, want 3", got)
	}

	// Editing a Compose file is noticed at once.
	later := now.Add(2 * time.Second)
	if err := os.WriteFile(compose1, []byte("services:\n  web:\n    image: b\n  db:\n    image: c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(compose1, later, later); err != nil {
		t.Fatal(err)
	}
	resolve(cached)
	resolve(cached)
	if got := c.calls.Load(); got != 4 {
		t.Errorf("after editing compose.yaml: %d calls, want 4", got)
	}

	// An answer older than the TTL is asked again even if no file changed.
	now = now.Add(2 * time.Minute)
	resolve(cached)
	if got := c.calls.Load(); got != 5 {
		t.Errorf("after the TTL: %d calls, want 5", got)
	}

	// With the cache off the context hint does nothing.
	c.calls.Store(0)
	e.EnableConfigCache(0)
	resolve(cached)
	resolve(cached)
	if got := c.calls.Load(); got != 2 {
		t.Errorf("cache off: %d calls, want 2", got)
	}
}

func TestCachedConfigIsACopy(t *testing.T) {
	val := "v"
	orig := &compose.Project{Name: "n", Services: map[string]compose.Service{"web": {
		Image: "i", Build: &compose.Build{Context: "."}, Ports: []compose.Port{{Target: 80}},
		Profiles: []string{"dev"}, Environment: map[string]*string{"K": &val},
	}}, Volumes: map[string]compose.VolumeConfig{"data": {Name: "data"}}}
	cp := cloneProject(orig)
	svc := cp.Services["web"]
	svc.Ports[0].Target = 81
	svc.Profiles[0] = "prod"
	svc.Build.Context = "elsewhere"
	*svc.Environment["K"] = "changed"
	delete(cp.Volumes, "data")

	o := orig.Services["web"]
	if o.Ports[0].Target != 80 || o.Profiles[0] != "dev" || o.Build.Context != "." || *o.Environment["K"] != "v" || len(orig.Volumes) != 1 {
		t.Errorf("editing the copy changed the original: %+v", orig)
	}
	if cloneProject(nil) != nil {
		t.Error("nil stays nil")
	}
}

// snapCompose can read every container at once, like the real adapter.
type snapCompose struct {
	*fakeCompose
	all   map[string][]compose.Container
	err   error
	reads atomic.Int64
	ps    atomic.Int64
}

func (c *snapCompose) AllContainers(context.Context) (map[string][]compose.Container, error) {
	c.reads.Add(1)
	return c.all, c.err
}

func (c *snapCompose) PS(ctx context.Context, t compose.Target) ([]compose.Container, error) {
	c.ps.Add(1)
	return c.fakeCompose.PS(ctx, t)
}

func TestDockerSnapshotIsReadOnceForAllInstances(t *testing.T) {
	e, _, _, fc := newEngine()
	sc := &snapCompose{fakeCompose: fc}
	e.compose = sc
	ctx := context.Background()
	if _, err := e.AddProject(ctx, root, "", ""); err != nil {
		t.Fatal(err)
	}
	fc.configs[feature] = &compose.Project{Name: "app-feature", Services: map[string]compose.Service{"web": {Image: "x"}}}
	inst, err := e.Instance(ctx, "app", "feature/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	sc.all = map[string][]compose.Container{
		strings.ToLower(inst.Compose): {{Name: inst.Compose + "-web-1", Service: "web", State: "running"}},
	}

	snap, err := e.DockerSnapshot(ctx)
	if err != nil || snap == nil {
		t.Fatalf("snapshot = %v, %v", snap, err)
	}
	for i := 0; i < 5; i++ {
		cs, err := e.ContainersFrom(ctx, inst, snap)
		if err != nil || len(cs) != 1 || cs[0].Service != "web" {
			t.Fatalf("ContainersFrom = %+v, %v", cs, err)
		}
	}
	if sc.reads.Load() != 1 || sc.ps.Load() != 0 {
		t.Errorf("Docker was read %d times with %d `compose ps`, want 1 and 0", sc.reads.Load(), sc.ps.Load())
	}
	upper := *inst
	upper.Compose = strings.ToUpper(inst.Compose)
	if cs, _ := e.ContainersFrom(ctx, &upper, snap); len(cs) != 1 {
		t.Error("project names match without regard to case")
	}
	other := *inst
	other.Compose = "nothing-here"
	if cs, err := e.ContainersFrom(ctx, &other, snap); err != nil || len(cs) != 0 {
		t.Errorf("an instance with no containers: %+v, %v", cs, err)
	}

	// Port planning with the containers in hand does not ask Docker again.
	e.portFree = func(string, int, string) bool { return true }
	cs, _ := e.ContainersFrom(ctx, inst, snap)
	if _, err := e.PlanPortsWith(ctx, inst, nil, false, cs); err != nil {
		t.Fatal(err)
	}
	if sc.ps.Load() != 0 {
		t.Errorf("PlanPortsWith asked `compose ps` %d times", sc.ps.Load())
	}
	if _, err := e.PlanPorts(ctx, inst, nil, false); err != nil {
		t.Fatal(err)
	}
	if sc.ps.Load() != 1 {
		t.Errorf("PlanPorts is the one that reads the containers: %d `compose ps`, want 1", sc.ps.Load())
	}
}

func TestDockerSnapshotFallsBackWhenTheAdapterCannotReadAll(t *testing.T) {
	e, _, _, fc := newEngine() // fakeCompose has no AllContainers
	snap, err := e.DockerSnapshot(context.Background())
	if snap != nil || err != nil {
		t.Fatalf("snapshot = %v, %v; want nil, nil", snap, err)
	}
	ctx := context.Background()
	if _, err := e.AddProject(ctx, root, "", ""); err != nil {
		t.Fatal(err)
	}
	fc.configs[feature] = &compose.Project{Name: "app-feature", Services: map[string]compose.Service{"web": {Image: "x"}}}
	fc.containers = []compose.Container{{Service: "web", State: "running"}}
	inst, err := e.Instance(ctx, "app", "feature/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := e.ContainersFrom(ctx, inst, nil)
	if err != nil || len(cs) != 1 {
		t.Errorf("without a snapshot the instance is asked directly: %+v, %v", cs, err)
	}
}

func TestDockerSnapshotReportsDockerBeingDown(t *testing.T) {
	e, _, _, fc := newEngine()
	sc := &snapCompose{fakeCompose: fc, err: context.DeadlineExceeded}
	e.compose = sc
	if snap, err := e.DockerSnapshot(context.Background()); err == nil || snap != nil {
		t.Errorf("snapshot = %v, %v; want the error", snap, err)
	}
}
