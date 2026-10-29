package engine

import (
	"context"
	"encoding/json"
	"errors"
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

type fakeCompose struct {
	configs map[string]*compose.Project
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
	p, err := e.AddProject(context.Background(), feature, "")
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
	if _, err := e.AddProject(ctx, root, "api"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AddProject(ctx, root, "again"); err == nil || !strings.Contains(err.Error(), "already registered as") {
		t.Errorf("same path: err = %v", err)
	}

	g.worktrees = []git.Worktree{{Path: abs("/src/other"), Branch: "main"}}
	if _, err := e.AddProject(ctx, abs("/src/other"), "API"); err == nil || !strings.Contains(err.Error(), "--name") {
		t.Errorf("same name: err = %v", err)
	}
	if _, err := e.AddProject(ctx, abs("/src/other"), "my api"); err == nil {
		t.Error("name with a space should be rejected")
	}
	if _, err := e.AddProject(ctx, abs("/elsewhere"), ""); err == nil {
		t.Error("path outside a repository should be rejected")
	}
}

func TestRemoveProject(t *testing.T) {
	e, store, _, _ := newEngine()
	ctx := context.Background()
	if _, err := e.AddProject(ctx, root, ""); err != nil {
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
	if _, err := e.AddProject(ctx, root, ""); err != nil {
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
	if _, err := e.AddProject(ctx, root, ""); err != nil {
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
	if res.Workspace != feature || res.Branch != "feature/login" || res.Compose != "app-feature" {
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

func TestEnvFileHint(t *testing.T) {
	dir := t.TempDir()
	main := git.Worktree{Path: filepath.Join(dir, "app")}
	linked := git.Worktree{Path: filepath.Join(dir, "app-feature")}
	for _, wt := range []git.Worktree{main, linked} {
		if err := os.Mkdir(wt.Path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if hint := envFileHint(linked, main, nil); !strings.Contains(hint, "no .env file") || !strings.Contains(hint, "--env-file <path>") {
		t.Errorf("no .env anywhere: hint = %q", hint)
	}

	mainEnv := filepath.Join(main.Path, ".env")
	if err := os.WriteFile(mainEnv, []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if hint := envFileHint(linked, main, nil); !strings.Contains(hint, "--env-file "+mainEnv) {
		t.Errorf("main has .env: hint = %q, want it to suggest %s", hint, mainEnv)
	}
	if hint := envFileHint(main, main, nil); hint != "" {
		t.Errorf("main worktree with .env: hint = %q, want none", hint)
	}
	if hint := envFileHint(linked, main, []string{mainEnv}); hint != "" {
		t.Errorf("with --env-file: hint = %q, want none", hint)
	}
}
