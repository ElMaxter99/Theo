package engine

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/instances"
)

// uxCompose adds service control and exec to the shared fake.
type uxCompose struct {
	*fakeCompose
	controls []string
	upOpts   []compose.UpOptions
	execs    []compose.ExecOptions
	stdin    string
}

func (c *uxCompose) Up(ctx context.Context, t compose.Target, o compose.UpOptions, so, se io.Writer) error {
	c.upOpts = append(c.upOpts, o)
	return c.fakeCompose.Up(ctx, t, o, so, se)
}

func (c *uxCompose) Control(_ context.Context, t compose.Target, verb string, services []string, _, _ io.Writer) error {
	c.controls = append(c.controls, verb+" "+t.Name+" "+strings.Join(services, ","))
	return nil
}

func (c *uxCompose) Exec(_ context.Context, _ compose.Target, o compose.ExecOptions, in io.Reader, out, _ io.Writer) error {
	c.execs = append(c.execs, o)
	data, _ := io.ReadAll(in)
	c.stdin = string(data)
	out.Write(data)
	return nil
}

func uxEngine(t *testing.T) (*Engine, *uxCompose, *Instance) {
	t.Helper()
	e, _, _, fc := newEngine()
	uc := &uxCompose{fakeCompose: fc}
	e.compose = uc
	fc.configs[root] = &compose.Project{Name: "app", Services: map[string]compose.Service{
		"api": {Image: "x", Ports: []compose.Port{{Target: 8080, Published: "8080", Protocol: "tcp"}}},
		"db":  {Image: "y"},
		"job": {Image: "z", Profiles: []string{"tools"}},
	}}
	ctx := context.Background()
	if _, err := e.AddProject(ctx, root, "", ""); err != nil {
		t.Fatal(err)
	}
	inst, err := e.Instance(ctx, "app", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return e, uc, inst
}

func TestStopStartRestartServices(t *testing.T) {
	e, uc, inst := uxEngine(t)
	ctx := context.Background()
	var out bytes.Buffer
	if err := e.Stop(ctx, inst, []string{"api"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx, inst, nil, &out, &out); err != nil {
		t.Fatal(err)
	}
	if err := e.RestartServices(ctx, inst, []string{"api", "db"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	want := []string{"stop app api", "start app ", "restart app api,db"}
	if !reflect.DeepEqual(uc.controls, want) {
		t.Errorf("controls = %q, want %q", uc.controls, want)
	}

	// Without services, restart is the whole-instance restart.
	if err := e.RestartServices(ctx, inst, nil, &out, &out); err != nil {
		t.Fatal(err)
	}
	if got := uc.calls[len(uc.calls)-1]; !strings.HasPrefix(got, "restart app") {
		t.Errorf("last call = %q, want the whole-instance restart", got)
	}

	// A service that does not exist is refused before Docker is asked, and
	// the message says which exist.
	err := e.Stop(ctx, inst, []string{"nope"}, &out, &out)
	if err == nil || !strings.Contains(err.Error(), `no service "nope"`) || !strings.Contains(err.Error(), "api, db, job") {
		t.Errorf("unknown service: err = %v", err)
	}
	if len(uc.controls) != 3 {
		t.Errorf("an unknown service reached Docker: %q", uc.controls)
	}

	events, _ := e.settings.Events()
	var ops []string
	for _, ev := range events {
		ops = append(ops, ev.Operation)
	}
	if !reflect.DeepEqual(ops, []string{"stop", "start", "restart", "restart"}) {
		t.Errorf("history = %v", ops)
	}
}

func TestUpWithServicesAndRecreate(t *testing.T) {
	e, uc, inst := uxEngine(t)
	ctx := context.Background()
	var out bytes.Buffer
	if err := e.Up(ctx, inst, compose.UpOptions{Services: []string{"api"}}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if err := e.Up(ctx, inst, compose.UpOptions{Services: []string{"nope"}}, &out, &out); err == nil {
		t.Error("up with an unknown service must fail")
	}
	if err := e.Recreate(ctx, inst, compose.UpOptions{Services: []string{"db"}}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if err := e.Recreate(ctx, inst, compose.UpOptions{}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if len(uc.upOpts) != 3 {
		t.Fatalf("Up reached the adapter %d times, want 3", len(uc.upOpts))
	}
	if o := uc.upOpts[0]; o.ForceRecreate || o.NoDeps || !reflect.DeepEqual(o.Services, []string{"api"}) {
		t.Errorf("up --service = %+v", o)
	}
	if o := uc.upOpts[1]; !o.ForceRecreate || !o.NoDeps || !reflect.DeepEqual(o.Services, []string{"db"}) {
		t.Errorf("recreate db = %+v: want force-recreate and no-deps", o)
	}
	if o := uc.upOpts[2]; !o.ForceRecreate || o.NoDeps {
		t.Errorf("recreate all = %+v: want force-recreate and dependencies included", o)
	}
}

func TestExecPassesStdinAndChecksService(t *testing.T) {
	e, uc, inst := uxEngine(t)
	var out bytes.Buffer
	opts := compose.ExecOptions{Service: "api", Command: []string{"cat"}, NoTTY: true}
	if err := e.Exec(context.Background(), inst, opts, strings.NewReader("hello"), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello" || uc.stdin != "hello" || !reflect.DeepEqual(uc.execs, []compose.ExecOptions{opts}) {
		t.Errorf("exec: out %q, stdin %q, execs %+v", out.String(), uc.stdin, uc.execs)
	}
	if err := e.Exec(context.Background(), inst, compose.ExecOptions{Service: "nope"}, nil, &out, &out); err == nil {
		t.Error("exec in an unknown service must fail")
	}
}

func TestDiscardWritesKeepsStateUntouched(t *testing.T) {
	e, _, inst := uxEngine(t)
	e.DiscardWrites()
	if _, err := e.AddProject(context.Background(), feature, "second", ""); err == nil {
		// the fake git has the same repository: the duplicate check still reads the store
		t.Log("registered under --explain (discarded)")
	}
	if err := e.SetNeutralizeContainerNames(inst, true); err != nil {
		t.Fatal(err)
	}
	e.record(inst, "up", nil)
	state, _ := e.settings.Load()
	if state[inst.Workspace].NeutralizeContainerNames {
		t.Errorf("settings changed under DiscardWrites: %+v", state[inst.Workspace])
	}
	if events, _ := e.settings.Events(); len(events) != 0 {
		t.Errorf("history grew under DiscardWrites: %+v", events)
	}
	if projects, _ := e.Projects(); len(projects) != 1 {
		t.Errorf("projects = %d, want the one registered before", len(projects))
	}
}

func container(service, state string, exit int) compose.Container {
	return compose.Container{Name: service + "-1", Service: service, State: state, ExitCode: exit}
}

func TestSummarizeStates(t *testing.T) {
	cfg := &compose.Project{Services: map[string]compose.Service{
		"api": {}, "db": {}, "job": {Profiles: []string{"tools"}},
	}}
	tests := []struct {
		name       string
		containers []compose.Container
		state      string
		counts     ServiceCounts
	}{
		{"nothing", nil, StateNotCreated, ServiceCounts{NotCreated: 2, Total: 2}},
		{"all running", []compose.Container{container("api", "running", 0), container("db", "running", 0)}, StateRunning, ServiceCounts{Running: 2, Total: 2}},
		{"one stopped", []compose.Container{container("api", "running", 0), container("db", "exited", 1)}, StatePartial, ServiceCounts{Running: 1, Stopped: 1, Total: 2}},
		{"all stopped", []compose.Container{container("api", "exited", 137), container("db", "exited", 0)}, StateStopped, ServiceCounts{Stopped: 1, Completed: 1, Total: 2}},
		{"only api created", []compose.Container{container("api", "running", 0)}, StatePartial, ServiceCounts{Running: 1, NotCreated: 1, Total: 2}},
		{"a finished job does not make it partial", []compose.Container{container("api", "running", 0), container("db", "running", 0), container("job", "exited", 0)},
			StateRunning, ServiceCounts{Running: 2, Completed: 1, Total: 3}},
	}
	for _, tt := range tests {
		state, counts, services := summarize(cfg, tt.containers)
		if state != tt.state || counts != tt.counts || len(services) != counts.Total {
			t.Errorf("%s: state %q counts %+v (%d services), want %q %+v", tt.name, state, counts, len(services), tt.state, tt.counts)
		}
	}
}

func TestStatusReportsWorktreesPortsSettingsAndDrift(t *testing.T) {
	e, uc, inst := uxEngine(t)
	ctx := context.Background()
	uc.fakeCompose.configs[feature] = uc.fakeCompose.configs[root]
	uc.containers = []compose.Container{
		{Name: "app-api-1", Service: "api", State: "running", Publishers: []compose.Publisher{{PublishedPort: 8080, TargetPort: 8080, Protocol: "tcp"}}},
		{Name: "app-db-1", Service: "db", State: "exited", ExitCode: 1},
	}
	if err := e.SetEnv(inst, "TOKEN", "x", true, nil); err != nil {
		t.Fatal(err)
	}
	e.portFree = func(string, int, string) bool { return false } // every port is taken by someone else
	e.settings.Append(instances.Event{Project: "app", Workspace: root, Compose: "app", Operation: "up", Result: "ok"})
	uc.stacks = nil

	r, err := e.Status(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Projects) != 1 || r.Projects[0].Name != "app" {
		t.Fatalf("projects = %+v", r.Projects)
	}
	wts := r.Projects[0].Worktrees
	if len(wts) != 3 {
		t.Fatalf("worktrees = %d, want 3 (main, feature, a missing one)", len(wts))
	}
	main, feat, gone := wts[0], wts[1], wts[2]
	if main.Kind != KindMain || feat.Kind != KindWorktree || !feat.Dirty || main.Dirty {
		t.Errorf("kinds/dirty: %+v / %+v", main, feat)
	}
	if main.Instance == nil || main.Instance.State != StatePartial || main.Instance.Counts.Running != 1 || main.Instance.Counts.Stopped != 1 {
		t.Errorf("main instance = %+v", main.Instance)
	}
	if main.Settings == nil || main.Settings.EnvVars != 1 || main.Settings.SecretEnvVars != 1 || !reflect.DeepEqual(main.Settings.Profiles, []string{"tools"}) {
		t.Errorf("main settings = %+v", main.Settings)
	}
	if len(main.Instance.Ports) != 1 || main.Instance.Ports[0].URL != "http://localhost:8080" || main.Instance.Ports[0].Status != PortRunning {
		t.Errorf("main ports = %+v", main.Instance.Ports)
	}
	// The feature worktree shares the fake containers, but its port is taken
	// by something that is not its own instance: that is a conflict.
	if gone.Instance != nil || !gone.Missing {
		t.Errorf("missing worktree = %+v", gone)
	}
	// The fake project path does not exist on disk, so reconcile's finding
	// about it reaches the project's warnings.
	foundProject := false
	for _, w := range r.Projects[0].Warnings {
		if w.Kind == FindProjectMissing {
			foundProject = true
		}
	}
	if !foundProject {
		t.Errorf("project warnings = %+v, want reconcile's finding", r.Projects[0].Warnings)
	}
	if feat.Instance == nil || feat.Instance.ComposeProject != "app-app-feature" {
		t.Errorf("feature instance = %+v", feat.Instance)
	}
	if r.Warnings == nil || r.Projects[0].Warnings == nil || main.Warnings == nil {
		t.Error("warnings must be empty lists, not null, in JSON")
	}
}

func TestStatusDockerDown(t *testing.T) {
	e, uc, _ := uxEngine(t)
	e.compose = &dockerDown{uc}
	r, err := e.Status(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if r.DockerError == "" || r.Projects[0].Worktrees[0].Instance.State != StateUnknown {
		t.Errorf("docker down: %+v", r)
	}
	if len(r.Projects[0].Worktrees[0].Warnings) == 0 || r.Projects[0].Worktrees[0].Warnings[0].Kind != "docker-unavailable" {
		t.Errorf("warnings = %+v", r.Projects[0].Worktrees[0].Warnings)
	}
}

type dockerDown struct{ *uxCompose }

func (dockerDown) Stacks(context.Context) ([]compose.Stack, error) {
	return nil, context.DeadlineExceeded
}

func (dockerDown) PS(context.Context, compose.Target) ([]compose.Container, error) {
	return nil, context.DeadlineExceeded
}

func TestMissingWorktreeEnvFile(t *testing.T) {
	wt := filepath.Join(string(filepath.Separator), "w", "feature")
	msg := func(p string) string {
		return "docker compose config: env file " + p + " not found: stat " + p + ": no such file"
	}
	if got := missingWorktreeEnvFile(msg(filepath.Join(wt, ".env")), wt); got != ".env" {
		t.Errorf("got %q", got)
	}
	if got := missingWorktreeEnvFile(msg(filepath.Join(wt, "config", "api.env")), wt); got != "config/api.env" {
		t.Errorf("got %q", got)
	}
	if got := missingWorktreeEnvFile(msg(filepath.Join(string(filepath.Separator), "elsewhere", ".env")), wt); got != "" {
		t.Errorf("a file outside the worktree is not ours to copy: %q", got)
	}
	if got := missingWorktreeEnvFile("some other error", wt); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestWarnSharedComposeNames(t *testing.T) {
	wt := func(cp string) WorktreeStatus {
		return WorktreeStatus{Warnings: []StatusWarning{}, Instance: &InstanceStatus{ComposeProject: cp}}
	}
	report := &StatusReport{Projects: []ProjectStatus{
		{Name: "a", Worktrees: []WorktreeStatus{wt("demo"), wt("a-feature")}},
		{Name: "b", Worktrees: []WorktreeStatus{wt("demo")}},
		{Name: "c", Worktrees: []WorktreeStatus{wt("other")}},
	}}
	warnSharedComposeNames(report)
	if w := report.Projects[0].Worktrees[0].Warnings; len(w) != 1 || w[0].Kind != "shared-compose-name" || !strings.Contains(w[0].Message, "b") {
		t.Errorf("a/main warnings = %+v", w)
	}
	if w := report.Projects[1].Worktrees[0].Warnings; len(w) != 1 || !strings.Contains(w[0].Message, "a") {
		t.Errorf("b/main warnings = %+v", w)
	}
	if len(report.Projects[0].Worktrees[1].Warnings) != 0 || len(report.Projects[2].Worktrees[0].Warnings) != 0 {
		t.Errorf("unique names must not warn: %+v", report.Projects)
	}
}
