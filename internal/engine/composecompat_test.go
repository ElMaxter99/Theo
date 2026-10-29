package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/compose"
)

// versionedCompose is a fakeCompose that reports a Compose version and counts
// how often it was asked.
type versionedCompose struct {
	*fakeCompose
	version compose.Version
	err     error
}

func (v *versionedCompose) Version(context.Context) (compose.Version, error) {
	return v.version, v.err
}

func TestOverlayNeedsNewEnoughCompose(t *testing.T) {
	e, c, inst := portsFixture(t, apiPorts(), 3303)
	ctx := context.Background()

	// Compose 2.23.0 does not know !override: say so and point to the doctor.
	e.compose = &versionedCompose{fakeCompose: c, version: compose.Version{Major: 2, Minor: 23, Patch: 0}}
	_, err := e.ApplyInstance(ctx, inst, nil, true)
	if err == nil || !strings.Contains(err.Error(), "2.23.0") || !strings.Contains(err.Error(), "2.24.4") || !strings.Contains(err.Error(), "theo doctor") {
		t.Fatalf("auto ports with an old Compose: %v", err)
	}
	if _, err := e.PlanPorts(ctx, inst, nil, true); err == nil || !strings.Contains(err.Error(), "theo doctor") {
		t.Errorf("ports --auto-ports with an old Compose: %v", err)
	}
	// Without a change to make, an old Compose is not a problem.
	if _, err := e.PlanPorts(ctx, inst, nil, false); err != nil {
		t.Errorf("plain ports: %v", err)
	}

	// A version that cannot be read does not block: `compose config` validates the file.
	e.compose = &versionedCompose{fakeCompose: c, err: errors.New("no docker")}
	c.resolved = &compose.Project{Services: map[string]compose.Service{"api": {Ports: []compose.Port{{Target: 3000, Published: "3304", Protocol: "tcp"}}}}}
	if _, err := e.PlanPorts(ctx, inst, nil, true); err != nil {
		t.Errorf("unknown version: %v", err)
	}
}

func TestEnvFileListKeepsTheProjectDotEnv(t *testing.T) {
	dir := t.TempDir()
	gen := filepath.Join(dir, "gen.env")
	// No .env and no --env-file: only the generated file.
	if got := withInstanceEnv(dir, nil, gen); !reflect.DeepEqual(got, []string{gen}) {
		t.Errorf("got %v", got)
	}
	// With a .env in the worktree, --env-file would make Compose ignore it: it is named first.
	own := filepath.Join(dir, ".env")
	os.WriteFile(own, []byte("A=1\n"), 0o644)
	if got := withInstanceEnv(dir, nil, gen); !reflect.DeepEqual(got, []string{own, gen}) {
		t.Errorf("got %v", got)
	}
	// Explicit env files are kept, the generated one last.
	if got := withInstanceEnv(dir, []string{"x.env"}, gen); !reflect.DeepEqual(got, []string{"x.env", gen}) {
		t.Errorf("got %v", got)
	}
	if got := withInstanceEnv(dir, []string{"x.env"}, ""); !reflect.DeepEqual(got, []string{"x.env"}) {
		t.Errorf("got %v", got)
	}
}

func TestComposeVarsComeFromProcessThenEnvFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env"), []byte("# c\nexport COMPOSE_FILE='a.yaml:b.yaml'\nCOMPOSE_PROFILES=dbg, web # trailing\n"), 0o644)
	inst := &Instance{Workspace: dir}

	t.Setenv("COMPOSE_FILE", "")
	t.Setenv("COMPOSE_PROFILES", "")
	t.Setenv("COMPOSE_PATH_SEPARATOR", "")
	sep := string(os.PathListSeparator)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("COMPOSE_FILE=a.yaml"+sep+"sub"+string(filepath.Separator)+"b.yaml\nCOMPOSE_PROFILES=dbg, web # trailing\n"), 0o644)
	want := []string{filepath.Join(dir, "a.yaml"), filepath.Join(dir, "sub", "b.yaml")}
	if got := composeFileList(inst); !reflect.DeepEqual(got, want) {
		t.Errorf("COMPOSE_FILE from .env: %v, want %v", got, want)
	}
	if got := composeProfiles(inst); !reflect.DeepEqual(got, []string{"dbg", "web"}) {
		t.Errorf("COMPOSE_PROFILES: %v", got)
	}

	// The process environment wins over the file, and a custom separator is honoured.
	t.Setenv("COMPOSE_FILE", "x.yaml,y.yaml")
	t.Setenv("COMPOSE_PATH_SEPARATOR", ",")
	want = []string{filepath.Join(dir, "x.yaml"), filepath.Join(dir, "y.yaml")}
	if got := composeFileList(inst); !reflect.DeepEqual(got, want) {
		t.Errorf("COMPOSE_FILE from the environment: %v, want %v", got, want)
	}

	// With --env-file, the project's .env is not read; a later file wins.
	t.Setenv("COMPOSE_FILE", "")
	t.Setenv("COMPOSE_PATH_SEPARATOR", "")
	e1, e2 := filepath.Join(dir, "e1.env"), filepath.Join(dir, "e2.env")
	os.WriteFile(e1, []byte("COMPOSE_FILE=one.yaml\n"), 0o644)
	os.WriteFile(e2, []byte("COMPOSE_FILE=two.yaml\n"), 0o644)
	inst.envFiles = []string{e1, e2}
	if got := composeFileList(inst); !reflect.DeepEqual(got, []string{filepath.Join(dir, "two.yaml")}) {
		t.Errorf("COMPOSE_FILE with env files: %v", got)
	}
	inst.envFiles = []string{e1}
	os.WriteFile(e1, []byte("OTHER=1\n"), 0o644)
	if got := composeFileList(inst); got != nil {
		t.Errorf("--env-file replaces .env, so COMPOSE_FILE is unset: %v", got)
	}
}

func TestBaseComposeFilesRefusesWhenItDisagreesWithCompose(t *testing.T) {
	e, c, inst := portsFixture(t, apiPorts(), 3303)
	ctx := context.Background()
	t.Setenv("COMPOSE_FILE", "")
	e.compose = &textByFiles{fakeCompose: c}
	if _, err := e.baseComposeFiles(ctx, inst); err == nil || !strings.Contains(err.Error(), "COMPOSE_FILE") {
		t.Errorf("a different result must be refused, naming the way out: %v", err)
	}
	e.compose = c
	if files, err := e.baseComposeFiles(ctx, inst); err != nil || len(files) != 1 {
		t.Errorf("same result: %v %v", files, err)
	}
}

// textByFiles prints a different configuration when files are given.
type textByFiles struct{ *fakeCompose }

func (t *textByFiles) ConfigText(_ context.Context, tg compose.Target) ([]byte, error) {
	if len(tg.Files) > 0 {
		return []byte("with files"), nil
	}
	return []byte("own"), nil
}

func TestCheckOverlay(t *testing.T) {
	v := func(s string) *string { return &s }
	cfg := &compose.Project{Services: map[string]compose.Service{
		"api": {Environment: map[string]*string{"A": v("1")}},
		"db":  {ContainerName: "still-there"},
	}}
	if err := checkOverlay(compose.Overlay{Environment: map[string]map[string]string{"api": {"A": "1"}}}, cfg); err != nil {
		t.Error(err)
	}
	if err := checkOverlay(compose.Overlay{Environment: map[string]map[string]string{"api": {"A": "2"}}}, cfg); err == nil {
		t.Error("a different value must be reported")
	}
	if err := checkOverlay(compose.Overlay{Environment: map[string]map[string]string{"api": {"B": "1"}}}, cfg); err == nil {
		t.Error("a missing variable must be reported")
	}
	if err := checkOverlay(compose.Overlay{ResetContainerName: []string{"db"}}, cfg); err == nil {
		t.Error("a container_name that stays must be reported")
	}
}

// namedImages resolves image names the way Compose does: a template with the
// project name changes with -p, a fixed name does not.
type namedImages struct{ *fakeCompose }

func (n *namedImages) ConfigFor(_ context.Context, t compose.Target) (*compose.Project, error) {
	return &compose.Project{Services: map[string]compose.Service{
		"fixed":   {Build: &compose.Build{Context: "."}, Image: "myapp:dev"},
		"derived": {Build: &compose.Build{Context: "."}, Image: "myapp-" + t.Name + ":dev"},
		"default": {Build: &compose.Build{Context: "."}},
		"pulled":  {Image: "redis:7"},
	}}, nil
}

func TestSharedImageBuilds(t *testing.T) {
	ctx := context.Background()
	svcs := map[string]compose.Service{
		"fixed":   {Build: &compose.Build{Context: "."}, Image: "myapp:dev"},
		"derived": {Build: &compose.Build{Context: "."}, Image: "myapp-app-feature:dev"},
		"default": {Build: &compose.Build{Context: "."}},
		"pulled":  {Image: "redis:7"},
	}
	e, _, _, c := newEngine()
	e.compose = &namedImages{c}
	inst := &Instance{Compose: "app-feature", siblings: 1, config: &compose.Project{Services: svcs}}
	got, err := e.SharedImageBuilds(ctx, inst)
	if err != nil || !reflect.DeepEqual(got, map[string]string{"fixed": "myapp:dev"}) {
		t.Errorf("shared = %v, %v", got, err)
	}
	inst.siblings = 0
	if got, _ := e.SharedImageBuilds(ctx, inst); len(got) != 0 {
		t.Errorf("a project with one worktree has nothing to share: %v", got)
	}
}
