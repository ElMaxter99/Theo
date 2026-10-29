package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/registry"
)

type memStore struct{ projects []registry.Project }

func (s *memStore) Load() ([]registry.Project, error) {
	return append([]registry.Project(nil), s.projects...), nil
}

func (s *memStore) Save(p []registry.Project) error {
	s.projects = append([]registry.Project(nil), p...)
	return nil
}

type fakeGit struct {
	worktrees []git.Worktree
	dirty     map[string]bool
	checked   []string

	branches []git.Branch
	changes  map[string][]string
	ignored  []string
	calls    []string // "add <path> <branch> new=<bool> base=<base>", "remove <path> force=<bool>"
}

func (g *fakeGit) TopLevel(_ context.Context, dir string) (string, error) {
	for _, wt := range g.worktrees {
		if dir == wt.Path || strings.HasPrefix(dir, wt.Path+string(filepath.Separator)) {
			return wt.Path, nil
		}
	}
	return "", errors.New("not a git repository")
}

func (g *fakeGit) Worktrees(context.Context, string) ([]git.Worktree, error) {
	return g.worktrees, nil
}

func (g *fakeGit) Dirty(_ context.Context, dir string) (bool, error) {
	g.checked = append(g.checked, dir)
	return g.dirty[dir], nil
}

func (g *fakeGit) Branches(context.Context, string) ([]git.Branch, error) { return g.branches, nil }

func (g *fakeGit) AddWorktree(_ context.Context, _, path, branch string, newBranch bool, base string) error {
	g.calls = append(g.calls, fmt.Sprintf("add %s %s new=%v base=%s", path, branch, newBranch, base))
	g.worktrees = append(g.worktrees, git.Worktree{Path: path, Branch: branch})
	return nil
}

func (g *fakeGit) RemoveWorktree(_ context.Context, _, path string, force bool) error {
	g.calls = append(g.calls, fmt.Sprintf("remove %s force=%v", path, force))
	return nil
}

func (g *fakeGit) Changes(_ context.Context, dir string) ([]string, error) {
	return g.changes[dir], nil
}

func (g *fakeGit) IgnoredFiles(context.Context, string, string) ([]string, error) {
	return g.ignored, nil
}

type fakeCompose struct {
	configs    map[string]*compose.Project
	stacks     []compose.Stack
	calls      []string            // "up <name> <dir> <env files>"
	resolved   *compose.Project    // returned by ConfigFor
	text       string              // returned by ConfigText
	containers []compose.Container // returned by PS
}

func (c *fakeCompose) record(op string, t compose.Target) {
	c.calls = append(c.calls, strings.Join([]string{op, t.Name, t.Dir, strings.Join(t.EnvFiles, ",")}, " "))
}

func (c *fakeCompose) Up(_ context.Context, t compose.Target, _ compose.UpOptions, _, _ io.Writer) error {
	c.record("up", t)
	return nil
}

func (c *fakeCompose) Down(_ context.Context, t compose.Target, _, _ io.Writer) error {
	c.record("down", t)
	return nil
}

func (c *fakeCompose) Restart(_ context.Context, t compose.Target, _, _ io.Writer) error {
	c.record("restart", t)
	return nil
}

func (c *fakeCompose) Logs(_ context.Context, t compose.Target, _ compose.LogOptions, _, _ io.Writer) error {
	c.record("logs", t)
	return nil
}

func (c *fakeCompose) PS(_ context.Context, t compose.Target) ([]compose.Container, error) {
	c.record("ps", t)
	return c.containers, nil
}

func (c *fakeCompose) ConfigFor(_ context.Context, t compose.Target) (*compose.Project, error) {
	c.record("config", t)
	return c.resolved, nil
}

func (c *fakeCompose) ConfigText(_ context.Context, t compose.Target) ([]byte, error) {
	c.record("config-text", t)
	return []byte(c.text), nil
}

func (c *fakeCompose) Stacks(context.Context) ([]compose.Stack, error) {
	return c.stacks, nil
}

func (c *fakeCompose) Config(_ context.Context, dir string, _ []string) (*compose.Project, error) {
	if p, ok := c.configs[dir]; ok {
		return p, nil
	}
	return nil, errors.New("no configuration file provided: not found")
}

// abs turns a slash-separated absolute path into an absolute path of the
// current OS (with a drive letter on Windows).
func abs(p string) string {
	a, err := filepath.Abs(filepath.FromSlash(p))
	if err != nil {
		panic(err)
	}
	return a
}

var (
	root    = abs("/src/app")
	feature = abs("/src/app-feature")
	other   = abs("/src/other/app-feature")
)

func newEngine() (*Engine, *memStore, *fakeGit, *fakeCompose) {
	store := &memStore{}
	g := &fakeGit{
		worktrees: []git.Worktree{
			{Path: root, Branch: "main"},
			{Path: feature, Branch: "feature/login"},
			{Path: abs("/src/app-old"), Branch: "old", Prunable: true},
		},
		dirty: map[string]bool{feature: true},
	}
	c := &fakeCompose{configs: map[string]*compose.Project{}}
	e := New(store, g, c)
	e.now = func() time.Time { return time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC) }
	return e, store, g, c
}

func TestAddProjectFromLinkedWorktreeRegistersMainWorktree(t *testing.T) {
	e, store, _, _ := newEngine()
	p, err := e.AddProject(context.Background(), feature, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "app" || p.Path != root {
		t.Errorf("AddProject() = %+v, want name app at %s", p, root)
	}
	if len(store.projects) != 1 {
		t.Errorf("store has %d projects, want 1", len(store.projects))
	}
}

func TestAddProjectRejectsDuplicatesAndBadNames(t *testing.T) {
	e, _, g, _ := newEngine()
	ctx := context.Background()
	if _, err := e.AddProject(ctx, root, "api", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AddProject(ctx, root, "again", ""); err == nil || !strings.Contains(err.Error(), "already registered as") {
		t.Errorf("same path: err = %v", err)
	}

	g.worktrees = []git.Worktree{{Path: abs("/src/other"), Branch: "main"}}
	if _, err := e.AddProject(ctx, abs("/src/other"), "API", ""); err == nil || !strings.Contains(err.Error(), "--name") {
		t.Errorf("same name: err = %v", err)
	}
	if _, err := e.AddProject(ctx, abs("/src/other"), "my api", ""); err == nil {
		t.Error("name with a space should be rejected")
	}
	if _, err := e.AddProject(ctx, abs("/elsewhere"), "", ""); err == nil {
		t.Error("path outside a repository should be rejected")
	}
}

func TestRemoveProject(t *testing.T) {
	e, store, _, _ := newEngine()
	ctx := context.Background()
	if _, err := e.AddProject(ctx, root, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RemoveProject("APP"); err != nil {
		t.Fatal(err)
	}
	if len(store.projects) != 0 {
		t.Errorf("store still has %v", store.projects)
	}
	if _, err := e.RemoveProject("app"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second remove: err = %v, want ErrNotFound", err)
	}
}

func TestWorktreesReportsChangesAndSkipsMissingOnes(t *testing.T) {
	e, _, g, _ := newEngine()
	ctx := context.Background()
	if _, err := e.AddProject(ctx, root, "", ""); err != nil {
		t.Fatal(err)
	}
	list, err := e.Worktrees(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Dirty || !list[1].Dirty || list[2].Dirty {
		t.Errorf("Worktrees() = %+v", list)
	}
	if !reflect.DeepEqual(g.checked, []string{root, feature}) {
		t.Errorf("checked status of %v; the missing worktree must be skipped", g.checked)
	}
}

func TestServices(t *testing.T) {
	e, _, _, c := newEngine()
	ctx := context.Background()
	if _, err := e.AddProject(ctx, root, "", ""); err != nil {
		t.Fatal(err)
	}
	c.configs[feature] = &compose.Project{
		Name: "app-feature",
		Services: map[string]compose.Service{
			"web": {
				Build: &compose.Build{Context: filepath.Join(feature, "web")},
				Ports: []compose.Port{{Target: 80, Published: "8080", Protocol: "tcp"}},
				Volumes: []compose.Volume{
					{Type: "bind", Source: filepath.Join(feature, "src"), Target: "/src"},
					{Type: "bind", Source: abs("/src/shared"), Target: "/shared"},
					{Type: "volume", Source: "data", Target: "/data"},
				},
				DependsOn: map[string]json.RawMessage{"db": nil},
				Networks:  map[string]json.RawMessage{"default": nil, "back": nil},
			},
			"db": {Image: "postgres:16", Profiles: []string{"dev"}},
		},
	}

	res, err := e.Services(ctx, "app", "feature/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Workspace != feature || res.Branch != "feature/login" || res.Compose != "app-app-feature" {
		t.Errorf("Services() header = %+v", res)
	}
	if len(res.Services) != 2 || res.Services[0].Name != "db" || res.Services[1].Name != "web" {
		t.Fatalf("services = %+v, want db and web sorted", res.Services)
	}
	web := res.Services[1]
	if web.Build != filepath.Join(feature, "web") || !reflect.DeepEqual(web.DependsOn, []string{"db"}) || !reflect.DeepEqual(web.Networks, []string{"back", "default"}) {
		t.Errorf("web = %+v", web)
	}
	outside := []bool{web.Mounts[0].OutsideWorkspace, web.Mounts[1].OutsideWorkspace, web.Mounts[2].OutsideWorkspace}
	if !reflect.DeepEqual(outside, []bool{false, true, false}) {
		t.Errorf("outside workspace flags = %v, want only the /src/shared bind mount flagged", outside)
	}

	if _, err := e.Services(ctx, "app", "", nil); err == nil {
		t.Error("main worktree has no Compose files in the fake; expected the compose error")
	}
}

func TestSelectWorktree(t *testing.T) {
	list := []git.Worktree{
		{Path: root, Branch: "main"},
		{Path: feature, Branch: "feature/login"},
		{Path: other, Branch: "other"},
		{Path: abs("/src/app-hotfix"), Detached: true},
	}
	tests := []struct {
		selector string
		want     string
		wantErr  string
	}{
		{"", root, ""},
		{"feature/login", feature, ""},
		{"app-hotfix", abs("/src/app-hotfix"), ""},
		{feature, feature, ""},
		{"app-feature", "", "ambiguous"},
		{"nope", "", "not found"},
	}
	for _, tt := range tests {
		got, err := selectWorktree(list, tt.selector)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("selectWorktree(%q) err = %v, want %q", tt.selector, err, tt.wantErr)
			}
			continue
		}
		if err != nil || got.Path != tt.want {
			t.Errorf("selectWorktree(%q) = %q, %v; want %q", tt.selector, got.Path, err, tt.want)
		}
	}
}

func TestIsInside(t *testing.T) {
	base := abs("/w/app")
	tests := map[string]bool{
		base:                  true,
		abs("/w/app/src"):     true,
		abs("/w/app/../x"):    false,
		abs("/w/app-feature"): false,
		abs("/w"):             false,
		abs("/w/app/..x"):     true,
	}
	for path, want := range tests {
		if got := isInside(base, path); got != want {
			t.Errorf("isInside(%q, %q) = %v, want %v", base, path, got, want)
		}
	}
}

func TestResolveEnvFile(t *testing.T) {
	dir := t.TempDir()
	main := git.Worktree{Path: filepath.Join(dir, "app")}
	linked := git.Worktree{Path: filepath.Join(dir, "app-feature")}
	for _, wt := range []git.Worktree{main, linked} {
		if err := os.Mkdir(wt.Path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("A=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := registry.Project{Name: "app", Path: main.Path}
	check := func(name string, p registry.Project, wt git.Worktree, flags []string, wantSource, wantPath string, wantFiles []string) {
		t.Helper()
		env, files, err := resolveEnvFile(p, wt, main, flags)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if env.Source != wantSource || env.Path != wantPath || !reflect.DeepEqual(files, wantFiles) {
			t.Errorf("%s: got %+v %v, want %s %q %v", name, env, files, wantSource, wantPath, wantFiles)
		}
	}

	check("nothing anywhere", p, linked, nil, EnvNone, "", nil)

	mainEnv := filepath.Join(main.Path, ".env")
	write(mainEnv)
	check("linked worktree shares main .env", p, linked, nil, EnvFromMain, mainEnv, []string{mainEnv})
	check("main worktree uses its own", p, main, nil, EnvFromWorktree, mainEnv, nil)

	shared := filepath.Join(dir, "shared.env")
	write(shared)
	withEnv := p
	withEnv.EnvFile = shared
	check("project env file beats main .env", withEnv, linked, nil, EnvFromProject, shared, []string{shared})

	own := filepath.Join(linked.Path, ".env")
	write(own)
	check("worktree's own .env wins", withEnv, linked, nil, EnvFromWorktree, own, nil)
	check("flag wins over everything", withEnv, linked, []string{shared}, EnvFromFlag, shared, []string{shared})

	if _, _, err := resolveEnvFile(p, linked, main, []string{filepath.Join(dir, "missing.env")}); err == nil {
		t.Error("missing --env-file should fail")
	}
	gone := p
	gone.EnvFile = filepath.Join(dir, "gone.env")
	if err := os.Remove(own); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveEnvFile(gone, linked, main, nil); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("missing project env file: err = %v", err)
	}
}

func TestSetEnvFile(t *testing.T) {
	e, store, _, _ := newEngine()
	if _, err := e.AddProject(context.Background(), root, "", ""); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(t.TempDir(), "app.env")
	if err := os.WriteFile(envFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SetEnvFile("app", envFile); err != nil || store.projects[0].EnvFile != envFile {
		t.Fatalf("SetEnvFile() err = %v, stored %q", err, store.projects[0].EnvFile)
	}
	if _, err := e.SetEnvFile("app", ""); err != nil || store.projects[0].EnvFile != "" {
		t.Errorf("clearing: err = %v, stored %q", err, store.projects[0].EnvFile)
	}
	if _, err := e.SetEnvFile("app", envFile+".missing"); err == nil {
		t.Error("a missing env file should be rejected")
	}
}

func TestInstanceName(t *testing.T) {
	list := []git.Worktree{
		{Path: abs("/src/app")},
		{Path: abs("/src/wt/Feature.Login")},
		{Path: abs("/src/a/dup")},
		{Path: abs("/src/b/dup")},
	}
	if got := instanceName("gr", "gasolina-radar-api-dev", list[0], list); got != "gasolina-radar-api-dev" {
		t.Errorf("main worktree = %q, want Compose's own name", got)
	}
	if got := instanceName("GR", "x", list[1], list); got != "gr-feature-login" {
		t.Errorf("linked worktree = %q, want gr-feature-login", got)
	}
	a, b := instanceName("gr", "x", list[2], list), instanceName("gr", "x", list[3], list)
	if a == b || !strings.HasPrefix(a, "gr-dup-") || !strings.HasPrefix(b, "gr-dup-") {
		t.Errorf("same directory name: %q and %q, want distinct gr-dup-<hash>", a, b)
	}
	if a != instanceName("gr", "x", list[2], list) {
		t.Error("instance names must be deterministic")
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"api-cors-expose": "api-cors-expose", "_My App.v2": "my-app-v2", "...": "wt"} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInstanceOperationsUseTheWorktreeTarget(t *testing.T) {
	e, _, _, c := newEngine()
	ctx := context.Background()
	if _, err := e.AddProject(ctx, root, "", ""); err != nil {
		t.Fatal(err)
	}
	c.configs[feature] = &compose.Project{Name: "app-feature"}
	inst, err := e.Instance(ctx, "app", "feature/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	if inst.Compose != "app-app-feature" {
		t.Errorf("instance name = %q", inst.Compose)
	}
	_ = e.Up(ctx, inst, compose.UpOptions{}, io.Discard, io.Discard)
	_ = e.Down(ctx, inst, io.Discard, io.Discard)
	want := []string{"up app-app-feature " + feature + " ", "down app-app-feature " + feature + " "}
	if !reflect.DeepEqual(c.calls, want) {
		t.Errorf("calls = %q, want %q", c.calls, want)
	}
}

func TestOverviewAssignsStacksToInnermostWorktree(t *testing.T) {
	e, _, g, c := newEngine()
	ctx := context.Background()
	nested := abs("/src/app/.worktrees/x")
	g.worktrees = append(g.worktrees, git.Worktree{Path: nested, Branch: "x"})
	if _, err := e.AddProject(ctx, root, "", ""); err != nil {
		t.Fatal(err)
	}
	c.stacks = []compose.Stack{
		{Name: "app", Status: "running(2)", ConfigFiles: filepath.Join(root, "compose.yaml")},
		{Name: "app-x", Status: "exited(1)", ConfigFiles: filepath.Join(nested, "compose.yaml") + "," + filepath.Join(nested, "compose.override.yaml")},
		{Name: "elsewhere", Status: "running(1)", ConfigFiles: abs("/other/compose.yaml")},
	}
	overview, err := e.Overview(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, o := range overview {
		for _, s := range o.Stacks {
			got[o.Worktree.Path] = append(got[o.Worktree.Path], s.Name)
		}
	}
	want := map[string][]string{root: {"app"}, nested: {"app-x"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("overview = %v, want %v", got, want)
	}
}
