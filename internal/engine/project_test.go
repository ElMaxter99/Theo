package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/instances"
	"github.com/ElMaxter99/Theo/internal/ports"
	"github.com/ElMaxter99/Theo/internal/registry"
)

func TestProjectInfoShowsEverySetting(t *testing.T) {
	f := newDiskFixture(t)
	f.write(t, "a.yaml")
	ctx := context.Background()
	if _, err := f.e.SetComposeFiles(ctx, "app", []string{"a.yaml"}); err != nil {
		t.Fatal(err)
	}
	if err := f.e.SetWorktreeDir("app", filepath.Join(f.main, "..", "wt")); err != nil {
		t.Fatal(err)
	}
	info, err := f.e.ProjectInfo("APP")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "app" || info.Path != f.main || !reflect.DeepEqual(info.ComposeFiles, []string{"a.yaml"}) || info.WorktreeDir == "" || info.WorktreeDirEffective != info.WorktreeDir {
		t.Errorf("info = %+v", info)
	}
	if _, err := f.e.ProjectInfo("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown project: err = %v", err)
	}
}

// renameFixture has settings, history and ports recorded for the linked
// worktree of project "app".
func renameFixture(t *testing.T) *diskFixture {
	t.Helper()
	f := newDiskFixture(t)
	inst, err := f.e.InstanceFor(context.Background(), "app", "feature/login", InstanceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.e.SetEnv(inst, "MODE", "x", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.e.Up(context.Background(), inst, compose.UpOptions{}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestRenameProjectMigratesRecords(t *testing.T) {
	f := renameFixture(t)
	ctx := context.Background()
	// Theo's default worktree directory is named after the project.
	oldDir := filepath.Join(f.e.dataDir, "worktrees", "app")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := f.e.RenameProject(ctx, "app", "shop", RenameOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Records != 2 { // the instance settings and the `up` event
		t.Errorf("records = %d, want 2", res.Records)
	}
	if _, err := f.e.Project("app"); !errors.Is(err, ErrNotFound) {
		t.Errorf("old name still resolves: %v", err)
	}
	p, err := f.e.Project("shop")
	if err != nil {
		t.Fatal(err)
	}
	if p.WorktreeDir != oldDir || res.WorktreeDir != oldDir {
		t.Errorf("worktree dir = %q, want it pinned to %s so worktrees stay together", p.WorktreeDir, oldDir)
	}
	state, _ := f.e.settings.Load()
	if cfg := state[f.linked]; cfg.Project != "shop" || cfg.Env["MODE"].Value != "x" {
		t.Errorf("settings = %+v", cfg)
	}
	history, err := f.e.History(ctx, "shop", "", 0)
	if err != nil || len(history) != 1 || history[0].Project != "shop" {
		t.Errorf("history = %+v, err %v", history, err)
	}
}

func TestRenameProjectRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("name taken, or unchanged, or invalid", func(t *testing.T) {
		f := newDiskFixture(t)
		other := filepath.Join(filepath.Dir(f.main), "other")
		f.e.store.Save(append(mustProjects(t, f.e), registryProject("other", other)))
		if _, err := f.e.RenameProject(ctx, "app", "OTHER", RenameOptions{}); err == nil || !strings.Contains(err.Error(), "already registered") {
			t.Errorf("collision: err = %v", err)
		}
		if _, err := f.e.RenameProject(ctx, "app", "app", RenameOptions{}); err == nil {
			t.Error("same name should be refused")
		}
		if _, err := f.e.RenameProject(ctx, "app", "a b", RenameOptions{}); err == nil {
			t.Error("invalid name should be refused")
		}
		if _, err := f.e.RenameProject(ctx, "nope", "x", RenameOptions{}); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown project: err = %v", err)
		}
	})
	t.Run("containers of the old compose names", func(t *testing.T) {
		f := renameFixture(t)
		f.c.stacks = []compose.Stack{{Name: "app-app-feature", Status: "running(1)"}}
		_, err := f.e.RenameProject(ctx, "app", "shop", RenameOptions{Yes: true})
		if err == nil || !strings.Contains(err.Error(), "app-app-feature") || !strings.Contains(err.Error(), "derive from the project name") {
			t.Fatalf("err = %v", err)
		}
		if _, err := f.e.Project("app"); err != nil {
			t.Error("a refused rename must change nothing")
		}
		if state, _ := f.e.settings.Load(); state[f.linked].Project != "app" {
			t.Errorf("settings changed by a refused rename: %+v", state[f.linked])
		}
	})
	t.Run("the new names are taken in Docker", func(t *testing.T) {
		f := renameFixture(t)
		f.c.stacks = []compose.Stack{{Name: "shop-app-feature", Status: "exited(1)"}}
		if _, err := f.e.RenameProject(ctx, "app", "shop", RenameOptions{}); err == nil || !strings.Contains(err.Error(), "shop-app-feature") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("kept volumes need --yes", func(t *testing.T) {
		f := renameFixture(t)
		f.c.volumes = map[string][]compose.DockerVolume{"app-app-feature": {{Name: "app-app-feature_data"}}}
		_, err := f.e.RenameProject(ctx, "app", "shop", RenameOptions{})
		if err == nil || !strings.Contains(err.Error(), "app-app-feature_data") || !strings.Contains(err.Error(), "--yes") {
			t.Fatalf("err = %v", err)
		}
		res, err := f.e.RenameProject(ctx, "app", "shop", RenameOptions{Yes: true})
		if err != nil || len(res.Notes) == 0 {
			t.Errorf("with --yes: res %+v, err %v", res, err)
		}
	})
	t.Run("docker unreachable", func(t *testing.T) {
		f := renameFixture(t)
		f.e.compose = &stacksFail{f.c}
		if _, err := f.e.RenameProject(ctx, "app", "shop", RenameOptions{}); err == nil || !strings.Contains(err.Error(), "cannot check") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a case-only rename never touches compose names", func(t *testing.T) {
		f := renameFixture(t)
		f.c.stacks = []compose.Stack{{Name: "app-app-feature", Status: "running(1)"}}
		if _, err := f.e.RenameProject(ctx, "app", "App", RenameOptions{}); err != nil {
			t.Errorf("err = %v", err)
		}
	})
}

type stacksFail struct{ *filesCompose }

func (stacksFail) Stacks(context.Context) ([]compose.Stack, error) {
	return nil, errors.New("docker is not running")
}

func TestRenameMigratesStoresAddedWithAddProjectRenamer(t *testing.T) {
	f := newDiskFixture(t)
	r := &recordingRenamer{}
	f.e.AddProjectRenamer(r)
	if _, err := f.e.RenameProject(context.Background(), "app", "shop", RenameOptions{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.calls, []string{"app>shop"}) {
		t.Errorf("calls = %v", r.calls)
	}
	// A failing store stops the rename before the registry changes.
	f2 := newDiskFixture(t)
	f2.e.AddProjectRenamer(&recordingRenamer{err: errors.New("disk full")})
	if _, err := f2.e.RenameProject(context.Background(), "app", "shop", RenameOptions{}); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := f2.e.Project("app"); err != nil {
		t.Error("the project must keep its name when a store fails")
	}
}

type recordingRenamer struct {
	calls []string
	err   error
}

func (r *recordingRenamer) RenameProject(o, n string) (int, error) {
	r.calls = append(r.calls, o+">"+n)
	return 1, r.err
}

// removeFixture has a stack with containers, a kept volume, a generated
// file, settings, a port reservation and history for the linked worktree.
func removeFixture(t *testing.T) *diskFixture {
	t.Helper()
	f := renameFixture(t)
	f.write(t, "compose.yaml")
	inst, _ := f.e.InstanceFor(context.Background(), "app", "feature/login", InstanceOptions{})
	f.c.resolved = &compose.Project{Services: f.c.configs[f.linked].Services}
	if _, err := f.e.ApplyInstance(context.Background(), inst, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := f.e.portStore.Save(ports.State{f.linked: {"web|80/tcp": 8081}}); err != nil {
		t.Fatal(err)
	}
	f.c.volumes = map[string][]compose.DockerVolume{"app-app-feature": {{Name: "app-app-feature_data"}}}
	return f
}

func TestInspectProjectListsWhatKeepsExisting(t *testing.T) {
	f := removeFixture(t)
	f.c.stacks = []compose.Stack{{Name: "app-app-feature", Status: "running(1)", ConfigFiles: filepath.Join(f.linked, "compose.yaml")}}
	res, err := f.e.InspectProject(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Worktrees, []string{f.main, f.linked}) {
		t.Errorf("worktrees = %v", res.Worktrees)
	}
	if len(res.Stacks) != 1 || res.Stacks[0].Name != "app-app-feature" || res.Stacks[0].Worktree != f.linked {
		t.Errorf("stacks = %+v", res.Stacks)
	}
	if !reflect.DeepEqual(res.Volumes, []string{"app-app-feature_data"}) {
		t.Errorf("volumes = %v", res.Volumes)
	}
	if len(res.GeneratedFiles) != 2 {
		t.Errorf("generated = %v", res.GeneratedFiles)
	}
	if res.Records != (RecordCounts{Settings: 1, Ports: 1, History: 1}) {
		t.Errorf("records = %+v", res.Records)
	}
}

func TestRemoveProjectNeedsYesWhenThereAreContainers(t *testing.T) {
	f := removeFixture(t)
	ctx := context.Background()
	f.c.stacks = []compose.Stack{{Name: "app-app-feature", Status: "running(1)", ConfigFiles: filepath.Join(f.linked, "compose.yaml")}}

	res, err := f.e.RemoveProjectChecked(ctx, "app", RemoveProjectOptions{})
	var busy *ContainersError
	if !errors.As(err, &busy) || res == nil || len(res.Stacks) != 1 {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "--yes") || !strings.Contains(err.Error(), "app-app-feature") {
		t.Errorf("message = %v", err)
	}
	if _, err := f.e.Project("app"); err != nil {
		t.Error("a refused removal must keep the project")
	}

	if _, err := f.e.RemoveProjectChecked(ctx, "app", RemoveProjectOptions{Yes: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.Project("app"); !errors.Is(err, ErrNotFound) {
		t.Error("the project should be gone")
	}
	// Nothing else was touched: records and generated files stay.
	state, _ := f.e.settings.Load()
	assigned, _ := f.e.portStore.Load()
	if len(state) != 1 || len(assigned) != 1 {
		t.Errorf("records changed without --forget-records: %v %v", state, assigned)
	}
	for _, g := range res.GeneratedFiles {
		if !fileExists(g) {
			t.Errorf("%s was deleted", g)
		}
	}
	if len(f.c.removed) != 0 {
		t.Errorf("volumes removed: %v", f.c.removed)
	}
}

func TestRemoveProjectForgetRecords(t *testing.T) {
	f := removeFixture(t)
	ctx := context.Background()
	res, err := f.e.RemoveProjectChecked(ctx, "app", RemoveProjectOptions{ForgetRecords: true})
	if err != nil {
		t.Fatal(err)
	}
	state, _ := f.e.settings.Load()
	assigned, _ := f.e.portStore.Load()
	if len(state) != 0 || len(assigned) != 0 {
		t.Errorf("records remain: %v %v", state, assigned)
	}
	for _, g := range res.GeneratedFiles {
		if fileExists(g) {
			t.Errorf("generated file %s remains", g)
		}
	}
	events, _ := f.e.settings.Events()
	if len(events) != 1 {
		t.Errorf("the history must stay: %v", events)
	}
	for _, dir := range []string{f.main, f.linked} {
		if !dirExists(dir) {
			t.Errorf("%s was deleted", dir)
		}
	}
}

func TestRemoveProjectWithoutContainersJustRemoves(t *testing.T) {
	f := newDiskFixture(t)
	if _, err := f.e.RemoveProjectChecked(context.Background(), "app", RemoveProjectOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestMemSettingsAndStoreRenameTheSameWay(t *testing.T) {
	dir := t.TempDir()
	for name, store := range map[string]SettingsStore{"memory": &memSettings{}, "files": instances.Open(dir)} {
		cfg := instances.Config{Project: "app", Env: map[string]instances.Var{"A": {Value: "1"}}}
		if err := store.Save(instances.State{"/w1": cfg, "/w2": {Project: "other", NeutralizeContainerNames: true}}); err != nil {
			t.Fatal(err)
		}
		store.Append(instances.Event{Project: "app", Workspace: "/w1", Operation: "up", Result: "ok"})
		store.Append(instances.Event{Project: "other", Workspace: "/w2", Operation: "up", Result: "ok"})
		n, err := store.(ProjectRenamer).RenameProject("APP", "shop")
		if err != nil || n != 2 {
			t.Errorf("%s: n = %d, err %v", name, n, err)
		}
		state, _ := store.Load()
		events, _ := store.Events()
		if state["/w1"].Project != "shop" || state["/w2"].Project != "other" || events[0].Project != "shop" || events[1].Project != "other" {
			t.Errorf("%s: state %+v events %+v", name, state, events)
		}
	}
}

// perProjectGit answers the worktrees of each project directory.
type perProjectGit struct {
	*fakeGit
	byDir map[string][]git.Worktree
}

func (g *perProjectGit) Worktrees(_ context.Context, dir string) ([]git.Worktree, error) {
	if l, ok := g.byDir[dir]; ok {
		return l, nil
	}
	return nil, errors.New("not a git repository")
}

func (g *perProjectGit) TopLevel(_ context.Context, dir string) (string, error) {
	if _, ok := g.byDir[dir]; ok {
		return dir, nil
	}
	return "", errors.New("not a git repository")
}

// twoProjects registers "app" (main + linked worktree "feature", whose
// Compose name is app-feature) and "other" (main only, whose Compose name is
// otherName).
func twoProjects(t *testing.T, otherName string) (e *Engine, c *filesCompose, appLinked, otherMain string) {
	t.Helper()
	dir := t.TempDir()
	appMain, appLinked, otherMain := filepath.Join(dir, "app"), filepath.Join(dir, "feature"), filepath.Join(dir, "other")
	for _, d := range []string{appMain, appLinked, otherMain} {
		os.MkdirAll(d, 0o755)
	}
	g := &perProjectGit{fakeGit: &fakeGit{}, byDir: map[string][]git.Worktree{
		appMain:   {{Path: appMain, Branch: "main"}, {Path: appLinked, Branch: "feature"}},
		otherMain: {{Path: otherMain, Branch: "main"}},
	}}
	c = &filesCompose{fakeCompose: &fakeCompose{configs: map[string]*compose.Project{
		appMain:   {Name: "app"},
		appLinked: {Name: "app"},
		otherMain: {Name: otherName},
	}}}
	e = New(&memStore{}, g, c)
	e.dataDir = filepath.Join(dir, "data")
	for _, d := range []string{appMain, otherMain} {
		if _, err := e.AddProject(context.Background(), d, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	return e, c, appLinked, otherMain
}

func TestUpRefusesAComposeNameUsedByAnotherInstance(t *testing.T) {
	e, c, appLinked, otherMain := twoProjects(t, "app-feature")
	ctx := context.Background()

	inst, err := e.InstanceFor(ctx, "app", "feature", InstanceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if inst.Compose != "app-feature" {
		t.Fatalf("compose name = %s", inst.Compose)
	}
	err = e.Up(ctx, inst, compose.UpOptions{}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("up must be refused")
	}
	for _, want := range []string{`"app-feature"`, `project "other"`, otherMain, "project rename", "name:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
	for _, call := range c.calls {
		if strings.HasPrefix(call, "up ") {
			t.Errorf("up reached Compose: %v", c.calls)
		}
	}

	// The other side is refused too, naming this one.
	other, err := e.InstanceFor(ctx, "other", "", InstanceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Up(ctx, other, compose.UpOptions{}, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), appLinked) {
		t.Errorf("other side: err = %v", err)
	}

	// Reconcile reports it as a finding of its own kind, for either project.
	for _, scope := range []string{"", "app", "other"} {
		report, err := e.Reconcile(ctx, scope)
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, f := range report.Findings {
			if f.Kind == FindDuplicateName {
				found++
				if f.Subject != "app-feature" || !strings.Contains(f.Detail, otherMain) || f.Fix == "" {
					t.Errorf("finding = %+v", f)
				}
			}
		}
		if found != 1 {
			t.Errorf("scope %q: %d duplicate findings, want 1", scope, found)
		}
	}
}

func TestDistinctComposeNamesAreFine(t *testing.T) {
	e, _, _, _ := twoProjects(t, "other")
	ctx := context.Background()
	inst, err := e.InstanceFor(ctx, "app", "feature", InstanceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Up(ctx, inst, compose.UpOptions{}, io.Discard, io.Discard); err != nil {
		t.Errorf("up: %v", err)
	}
	report, _ := e.Reconcile(ctx, "")
	for _, f := range report.Findings {
		if f.Kind == FindDuplicateName {
			t.Errorf("unexpected finding %+v", f)
		}
	}
}

func mustProjects(t *testing.T, e *Engine) []registry.Project {
	t.Helper()
	list, err := e.Projects()
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func registryProject(name, path string) registry.Project {
	return registry.Project{Name: name, Path: path}
}
