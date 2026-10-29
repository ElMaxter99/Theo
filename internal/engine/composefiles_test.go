package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/git"
)

// filesCompose records the Compose files each call received, on top of the
// shared fake.
type filesCompose struct {
	*fakeCompose
	configFiles [][]string // files of each ConfigFiles call
	configEnv   [][]string // env files of each ConfigFiles call
	upFiles     [][]string // files of each Up call
	upProfiles  [][]string
}

func (c *filesCompose) ConfigFiles(ctx context.Context, dir string, envFiles, files []string) (*compose.Project, error) {
	c.configFiles = append(c.configFiles, files)
	c.configEnv = append(c.configEnv, envFiles)
	return c.fakeCompose.Config(ctx, dir, envFiles)
}

func (c *filesCompose) Up(ctx context.Context, t compose.Target, o compose.UpOptions, so, se io.Writer) error {
	c.upFiles = append(c.upFiles, t.Files)
	c.upProfiles = append(c.upProfiles, o.Profiles)
	return c.fakeCompose.Up(ctx, t, o, so, se)
}

// diskFixture is an engine whose project "app" has a main worktree and a
// linked one in real temporary directories, so Compose files can exist.
type diskFixture struct {
	e            *Engine
	c            *filesCompose
	g            *fakeGit
	main, linked string
}

func newDiskFixture(t *testing.T) *diskFixture {
	t.Helper()
	dir := t.TempDir()
	f := &diskFixture{main: filepath.Join(dir, "app"), linked: filepath.Join(dir, "app-feature")}
	for _, d := range []string{f.main, f.linked} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.g = &fakeGit{worktrees: []git.Worktree{{Path: f.main, Branch: "main"}, {Path: f.linked, Branch: "feature/login"}}}
	fake := &fakeCompose{configs: map[string]*compose.Project{
		f.main:   {Name: "app", Services: map[string]compose.Service{"web": {}, "tools": {Profiles: []string{"debug"}}}},
		f.linked: {Name: "app", Services: map[string]compose.Service{"web": {}, "tools": {Profiles: []string{"debug"}}}},
	}}
	f.c = &filesCompose{fakeCompose: fake}
	f.e = New(&memStore{}, f.g, f.c)
	f.e.dataDir = filepath.Join(dir, "data")
	if _, err := f.e.AddProject(context.Background(), f.main, "", ""); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *diskFixture) write(t *testing.T, name string) {
	t.Helper()
	for _, root := range []string{f.main, f.linked} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("services: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func rel(root string, names ...string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = filepath.Join(root, filepath.FromSlash(n))
	}
	return out
}

func TestSetComposeFilesKeepsOrderAndAppliesToEveryWorktree(t *testing.T) {
	f := newDiskFixture(t)
	f.write(t, "b.yaml")
	f.write(t, "deploy/a.yaml")
	ctx := context.Background()

	if _, err := f.e.SetComposeFiles(ctx, "app", []string{"deploy/a.yaml", "b.yaml"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ worktree, root string }{{"", f.main}, {"feature/login", f.linked}} {
		inst, err := f.e.InstanceFor(ctx, "app", c.worktree, InstanceOptions{})
		if err != nil {
			t.Fatal(err)
		}
		want := rel(c.root, "deploy/a.yaml", "b.yaml")
		if !reflect.DeepEqual(inst.ComposeFiles, want) || inst.ComposeFilesSource != FilesFromProject {
			t.Errorf("%s: files = %v (%s), want %v", c.root, inst.ComposeFiles, inst.ComposeFilesSource, want)
		}
		if got := inst.target().Files; !reflect.DeepEqual(got, want) {
			t.Errorf("target files = %v, want %v", got, want)
		}
	}
	// Config was read with the files in that order.
	if got := f.c.configFiles[0]; !reflect.DeepEqual(got, rel(f.main, "deploy/a.yaml", "b.yaml")) {
		t.Errorf("Config received %v", got)
	}

	// The reverse order is a different selection (docker compose -f B -f A).
	if _, err := f.e.SetComposeFiles(ctx, "app", []string{"b.yaml", "deploy/a.yaml"}); err != nil {
		t.Fatal(err)
	}
	inst, _ := f.e.InstanceFor(ctx, "app", "", InstanceOptions{})
	if want := rel(f.main, "b.yaml", "deploy/a.yaml"); !reflect.DeepEqual(inst.ComposeFiles, want) {
		t.Errorf("files = %v, want %v", inst.ComposeFiles, want)
	}

	// Going back to the default leaves Compose to find the files.
	if _, err := f.e.SetComposeFiles(ctx, "app", nil); err != nil {
		t.Fatal(err)
	}
	inst, _ = f.e.InstanceFor(ctx, "app", "", InstanceOptions{})
	if len(inst.ComposeFiles) != 0 || inst.target().Files != nil {
		t.Errorf("default: files = %v / %v", inst.ComposeFiles, inst.target().Files)
	}
}

func TestComposeFilesFlagOverridesTheProjectSetting(t *testing.T) {
	f := newDiskFixture(t)
	f.write(t, "a.yaml")
	f.write(t, "b.yaml")
	ctx := context.Background()
	if _, err := f.e.SetComposeFiles(ctx, "app", []string{"a.yaml"}); err != nil {
		t.Fatal(err)
	}
	inst, err := f.e.InstanceFor(ctx, "app", "", InstanceOptions{ComposeFiles: []string{"b.yaml"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := rel(f.main, "b.yaml"); !reflect.DeepEqual(inst.ComposeFiles, want) || inst.ComposeFilesSource != FilesFromFlag {
		t.Errorf("files = %v (%s)", inst.ComposeFiles, inst.ComposeFilesSource)
	}
	services, err := f.e.ServicesFor(ctx, "app", "", InstanceOptions{ComposeFiles: []string{"b.yaml", "a.yaml"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := rel(f.main, "b.yaml", "a.yaml"); !reflect.DeepEqual(services.ComposeFiles, want) {
		t.Errorf("services files = %v, want %v", services.ComposeFiles, want)
	}
}

func TestComposeFilesAreValidatedWithClearErrors(t *testing.T) {
	f := newDiskFixture(t)
	f.write(t, "a.yaml")
	ctx := context.Background()

	// Missing in the main worktree: the error names the file and the worktree.
	_, err := f.e.SetComposeFiles(ctx, "app", []string{"a.yaml", "missing.yaml"})
	if err == nil || !strings.Contains(err.Error(), "missing.yaml") || !strings.Contains(err.Error(), f.main) {
		t.Errorf("missing file: err = %v", err)
	}
	for _, bad := range []string{"/etc/compose.yaml", "../other/compose.yaml", "", "a.yaml"} {
		files := []string{bad}
		if bad == "a.yaml" {
			files = []string{"a.yaml", "./a.yaml"}
		}
		if _, err := f.e.SetComposeFiles(ctx, "app", files); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}

	// A file that exists in the main worktree but not in a linked one.
	if err := os.WriteFile(filepath.Join(f.main, "only-main.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.SetComposeFiles(ctx, "app", []string{"only-main.yaml"}); err != nil {
		t.Fatal(err)
	}
	_, err = f.e.InstanceFor(ctx, "app", "feature/login", InstanceOptions{})
	if err == nil || !strings.Contains(err.Error(), "only-main.yaml") || !strings.Contains(err.Error(), f.linked) || !strings.Contains(err.Error(), "project setting") {
		t.Errorf("linked worktree without the file: err = %v", err)
	}
	if _, err := f.e.InstanceFor(ctx, "app", "", InstanceOptions{ComposeFiles: []string{"nope.yaml"}}); err == nil || !strings.Contains(err.Error(), "nope.yaml") {
		t.Errorf("flag with a missing file: err = %v", err)
	}
}

func TestComposeFileVariableIsTheBaseWhenNothingIsChosen(t *testing.T) {
	f := newDiskFixture(t)
	f.write(t, "a.yaml")
	f.write(t, "b.yaml")
	ctx := context.Background()
	sep := compose.PathSeparator()

	t.Run("process environment", func(t *testing.T) {
		t.Setenv("COMPOSE_FILE", "b.yaml"+sep+"a.yaml")
		inst, err := f.e.InstanceFor(ctx, "app", "feature/login", InstanceOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if want := rel(f.linked, "b.yaml", "a.yaml"); !reflect.DeepEqual(inst.ComposeFiles, want) || !strings.HasPrefix(inst.ComposeFilesSource, FilesFromEnv) {
			t.Errorf("files = %v (%s), want %v", inst.ComposeFiles, inst.ComposeFilesSource, want)
		}
	})
	t.Run("custom separator", func(t *testing.T) {
		t.Setenv("COMPOSE_FILE", "a.yaml,b.yaml")
		t.Setenv("COMPOSE_PATH_SEPARATOR", ",")
		inst, err := f.e.InstanceFor(ctx, "app", "", InstanceOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if want := rel(f.main, "a.yaml", "b.yaml"); !reflect.DeepEqual(inst.ComposeFiles, want) {
			t.Errorf("files = %v, want %v", inst.ComposeFiles, want)
		}
	})
	t.Run("the worktree's .env", func(t *testing.T) {
		t.Setenv("COMPOSE_FILE", "")
		if err := os.WriteFile(filepath.Join(f.linked, ".env"), []byte("# c\nexport COMPOSE_FILE=\"b.yaml"+sep+"a.yaml\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		inst, err := f.e.InstanceFor(ctx, "app", "feature/login", InstanceOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if want := rel(f.linked, "b.yaml", "a.yaml"); !reflect.DeepEqual(inst.ComposeFiles, want) {
			t.Errorf("files = %v, want %v", inst.ComposeFiles, want)
		}
	})
	t.Run("an env file passed with --env-file", func(t *testing.T) {
		t.Setenv("COMPOSE_FILE", "")
		env := filepath.Join(t.TempDir(), "shared.env")
		if err := os.WriteFile(env, []byte("COMPOSE_FILE=a.yaml\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		inst, err := f.e.InstanceFor(ctx, "app", "", InstanceOptions{EnvFiles: []string{env}})
		if err != nil {
			t.Fatal(err)
		}
		if want := rel(f.main, "a.yaml"); !reflect.DeepEqual(inst.ComposeFiles, want) || !strings.Contains(inst.ComposeFilesSource, env) {
			t.Errorf("files = %v (%s), want %v", inst.ComposeFiles, inst.ComposeFilesSource, want)
		}
	})
	t.Run("the setting and the flag win", func(t *testing.T) {
		t.Setenv("COMPOSE_FILE", "b.yaml")
		inst, _ := f.e.InstanceFor(ctx, "app", "", InstanceOptions{ComposeFiles: []string{"a.yaml"}})
		if want := rel(f.main, "a.yaml"); !reflect.DeepEqual(inst.ComposeFiles, want) {
			t.Errorf("flag: files = %v", inst.ComposeFiles)
		}
		if _, err := f.e.SetComposeFiles(ctx, "app", []string{"a.yaml", "b.yaml"}); err != nil {
			t.Fatal(err)
		}
		inst, _ = f.e.InstanceFor(ctx, "app", "", InstanceOptions{})
		if inst.ComposeFilesSource != FilesFromProject {
			t.Errorf("setting: source = %s", inst.ComposeFilesSource)
		}
		f.e.SetComposeFiles(ctx, "app", nil)
	})
	t.Run("a missing file is named", func(t *testing.T) {
		t.Setenv("COMPOSE_FILE", "gone.yaml")
		_, err := f.e.InstanceFor(ctx, "app", "", InstanceOptions{})
		if err == nil || !strings.Contains(err.Error(), "gone.yaml") || !strings.Contains(err.Error(), "COMPOSE_FILE") {
			t.Errorf("err = %v", err)
		}
	})
}

// With COMPOSE_FILE set, the instance's own settings, auto ports and shared
// files used to fail. Theo's generated file now goes after the chosen ones.
func TestApplyInstanceBuildsOnComposeFile(t *testing.T) {
	f := newDiskFixture(t)
	f.write(t, "a.yaml")
	f.write(t, "b.yaml")
	t.Setenv("COMPOSE_FILE", "b.yaml"+compose.PathSeparator()+"a.yaml")
	ctx := context.Background()
	inst, err := f.e.InstanceFor(ctx, "app", "feature/login", InstanceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.c.resolved = &compose.Project{Services: f.c.configs[f.linked].Services}
	if err := f.e.SetEnv(inst, "MODE", "x", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.ApplyInstance(ctx, inst, nil, true); err != nil {
		t.Fatalf("ApplyInstance: %v", err)
	}
	got := inst.target().Files
	if len(got) != 3 || !reflect.DeepEqual(got[:2], rel(f.linked, "b.yaml", "a.yaml")) || !strings.HasSuffix(got[2], ".yaml") {
		t.Errorf("files = %v, want b, a and the generated overlay", got)
	}

	// Without extra settings the instance runs with exactly the chosen files.
	if _, err := f.e.UnsetEnv(inst, "MODE"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.ApplyInstance(ctx, inst, nil, false); err != nil {
		t.Fatal(err)
	}
	if got := inst.target().Files; !reflect.DeepEqual(got, rel(f.linked, "b.yaml", "a.yaml")) {
		t.Errorf("files without settings = %v", got)
	}
}

func TestBaseComposeFilesWithoutAnyFileExplainsTheSetting(t *testing.T) {
	f := newDiskFixture(t)
	inst, err := f.e.InstanceFor(context.Background(), "app", "", InstanceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.e.baseFiles = func(string) []string { return nil }
	if _, err := f.e.baseComposeFiles(inst); err == nil || !strings.Contains(err.Error(), "--compose-file") {
		t.Errorf("err = %v", err)
	}
}

func TestProfilesPerInstance(t *testing.T) {
	f := newDiskFixture(t)
	ctx := context.Background()
	inst, err := f.e.InstanceFor(ctx, "app", "feature/login", InstanceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.e.SetProfiles(inst, []string{"nope"}); err == nil || !strings.Contains(err.Error(), "debug") {
		t.Errorf("unknown profile: err = %v (should list the real ones)", err)
	}
	if err := f.e.SetProfiles(inst, []string{"debug", "debug"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inst.Profiles, []string{"debug"}) {
		t.Errorf("profiles = %v", inst.Profiles)
	}

	// A new resolution of the instance sees them, and up merges them with --profile.
	inst, _ = f.e.InstanceFor(ctx, "app", "feature/login", InstanceOptions{})
	if !reflect.DeepEqual(inst.Profiles, []string{"debug"}) {
		t.Fatalf("reloaded profiles = %v", inst.Profiles)
	}
	f.c.resolved = &compose.Project{Services: f.c.configs[f.linked].Services}
	if err := f.e.Up(ctx, inst, compose.UpOptions{Profiles: []string{"extra", "debug"}}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := f.c.upProfiles[0]; !reflect.DeepEqual(got, []string{"debug", "extra"}) {
		t.Errorf("up profiles = %v", got)
	}

	// Services behind a persisted profile count for the port plan.
	f.c.configs[f.linked].Services["tools"] = compose.Service{Profiles: []string{"debug"}, Ports: []compose.Port{{Target: 9000, Published: "9000"}}}
	inst, _ = f.e.InstanceFor(ctx, "app", "feature/login", InstanceOptions{})
	f.e.portFree = func(string, int, string) bool { return true }
	plan, err := f.e.PlanPorts(ctx, inst, nil, false)
	if err != nil || len(plan.Mappings) != 1 || plan.Mappings[0].Service != "tools" {
		t.Errorf("plan = %+v, err %v", plan, err)
	}

	if err := f.e.SetProfiles(inst, nil); err != nil {
		t.Fatal(err)
	}
	inst, _ = f.e.InstanceFor(ctx, "app", "feature/login", InstanceOptions{})
	if len(inst.Profiles) != 0 {
		t.Errorf("profiles after clearing = %v", inst.Profiles)
	}
	if state, _ := f.e.settings.Load(); len(state) != 0 {
		t.Errorf("an empty configuration should not be kept: %v", state)
	}
}

// Compose reads the default .env from the directory of the first Compose
// file, so a file in a subdirectory needs the worktree's .env named.
func TestEnvFileOfTheWorktreeIsNamedWhenComposeFilesAreInASubdirectory(t *testing.T) {
	f := newDiskFixture(t)
	f.write(t, "deploy/compose.yaml")
	f.write(t, ".env")
	ctx := context.Background()
	if _, err := f.e.SetComposeFiles(ctx, "app", []string{"deploy/compose.yaml"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.InstanceFor(ctx, "app", "", InstanceOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := f.c.configEnv[len(f.c.configEnv)-1]; !reflect.DeepEqual(got, []string{filepath.Join(f.main, ".env")}) {
		t.Errorf("env files = %v", got)
	}
	// At the root of the worktree, Compose finds it by itself.
	f.write(t, "root.yaml")
	if _, err := f.e.InstanceFor(ctx, "app", "", InstanceOptions{ComposeFiles: []string{"root.yaml"}}); err != nil {
		t.Fatal(err)
	}
	if got := f.c.configEnv[len(f.c.configEnv)-1]; got != nil {
		t.Errorf("env files = %v, want none", got)
	}
}
