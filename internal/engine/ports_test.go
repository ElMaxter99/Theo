package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/ports"
)

// portsFixture registers the project and returns the instance of the
// feature worktree, whose services publish the given ports. busy lists the
// host ports that something else listens on.
func portsFixture(t *testing.T, services map[string]compose.Service, busy ...int) (*Engine, *fakeCompose, *Instance) {
	t.Helper()
	e, _, _, c := newEngine()
	ctx := context.Background()
	if _, err := e.AddProject(ctx, root, "", ""); err != nil {
		t.Fatal(err)
	}
	c.configs[feature] = &compose.Project{Name: "app-feature", Services: services}
	inst, err := e.Instance(ctx, "app", "feature/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	taken := map[int]bool{}
	for _, p := range busy {
		taken[p] = true
	}
	e.portFree = func(_ string, port int, _ string) bool { return !taken[port] }
	e.dataDir = t.TempDir()
	e.baseFiles = func(dir string) []string { return []string{filepath.Join(dir, "compose.yaml")} }
	return e, c, inst
}

func apiPorts() map[string]compose.Service {
	return map[string]compose.Service{
		"api": {Ports: []compose.Port{{Target: 3000, Published: "3303", Protocol: "tcp", HostIP: "0.0.0.0"}}},
	}
}

func mapping(t *testing.T, plan *PortPlan, service string) PortMapping {
	t.Helper()
	for _, m := range plan.Mappings {
		if m.Service == service {
			return m
		}
	}
	t.Fatalf("no mapping for %s in %+v", service, plan.Mappings)
	return PortMapping{}
}

func TestPlanPortsReportsConflictsWithoutChangingAnything(t *testing.T) {
	e, _, inst := portsFixture(t, apiPorts(), 3303)
	plan, err := e.PlanPorts(context.Background(), inst, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	m := mapping(t, plan, "api")
	if m.Status != PortInUse || m.Effective != 3303 || !m.Conflict() {
		t.Errorf("mapping = %+v, want 3303 in use", m)
	}
	if plan.Overlay != "" || len(plan.Conflicts()) != 1 {
		t.Errorf("plan = %+v", plan)
	}
}

func TestPlanPortsFreePortIsLeftAlone(t *testing.T) {
	e, _, inst := portsFixture(t, apiPorts())
	plan, _ := e.PlanPorts(context.Background(), inst, nil, true)
	if m := mapping(t, plan, "api"); m.Status != PortFree || m.Effective != 3303 || plan.Overlay != "" {
		t.Errorf("mapping = %+v, overlay %q", m, plan.Overlay)
	}
}

func TestPlanPortsIgnoresServicesBehindDisabledProfiles(t *testing.T) {
	svcs := apiPorts()
	svcs["worker"] = compose.Service{Profiles: []string{"worker"}, Ports: []compose.Port{{Target: 9000, Published: "9000"}}}
	e, _, inst := portsFixture(t, svcs, 9000)
	plan, _ := e.PlanPorts(context.Background(), inst, nil, false)
	if len(plan.Mappings) != 1 {
		t.Errorf("mappings = %+v, want only api", plan.Mappings)
	}
	plan, _ = e.PlanPorts(context.Background(), inst, []string{"worker"}, false)
	if len(plan.Conflicts()) != 1 {
		t.Errorf("with the profile the worker's busy port should conflict: %+v", plan.Mappings)
	}
}

func TestPlanPortsOwnContainersAreNotConflicts(t *testing.T) {
	e, c, inst := portsFixture(t, apiPorts(), 3303)
	c.containers = []compose.Container{{Service: "api", Publishers: []compose.Publisher{{TargetPort: 3000, PublishedPort: 3303, Protocol: "tcp"}}}}
	plan, _ := e.PlanPorts(context.Background(), inst, nil, false)
	m := mapping(t, plan, "api")
	if m.Status != PortRunning || m.Running != 3303 || m.URL() != "http://localhost:3303" {
		t.Errorf("mapping = %+v url %q", m, m.URL())
	}
}

func TestPlanPortsRandomAndRangesAreNotManaged(t *testing.T) {
	svcs := map[string]compose.Service{"web": {Ports: []compose.Port{
		{Target: 80}, {Target: 81, Published: "8000-8010"},
	}}}
	e, _, inst := portsFixture(t, svcs)
	plan, _ := e.PlanPorts(context.Background(), inst, nil, true)
	if plan.Mappings[0].Status != PortRandom || plan.Mappings[1].Status != PortRange || plan.Overlay != "" {
		t.Errorf("plan = %+v", plan)
	}
}

func TestApplyPortsReassignsAndKeepsThePortBetweenRuns(t *testing.T) {
	e, c, inst := portsFixture(t, apiPorts(), 3303)
	c.resolved = &compose.Project{Services: map[string]compose.Service{
		"api": {Ports: []compose.Port{{Target: 3000, Published: "3304", Protocol: "tcp"}}},
	}}

	plan, err := e.ApplyInstance(context.Background(), inst, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if m := mapping(t, plan, "api"); m.Status != PortReassigned || m.Effective != 3304 {
		t.Fatalf("mapping = %+v", m)
	}
	if got := inst.target().Files; len(got) != 2 || !strings.HasSuffix(got[1], ".yaml") {
		t.Fatalf("files = %v", got)
	}
	data, err := os.ReadFile(inst.files[1])
	if err != nil || !strings.Contains(string(data), `published: "3304"`) {
		t.Errorf("overlay: %v\n%s", err, data)
	}

	// Later 3303 becomes free again, but the instance keeps 3304.
	e.portFree = func(string, int, string) bool { return true }
	plan, _ = e.PlanPorts(context.Background(), inst, nil, true)
	if m := mapping(t, plan, "api"); m.Status != PortKept || m.Effective != 3304 {
		t.Errorf("second run: %+v", m)
	}
}

func TestApplyPortsRejectsAResultThatDoesNotPublishThePlan(t *testing.T) {
	e, c, inst := portsFixture(t, apiPorts(), 3303)
	c.resolved = &compose.Project{Services: map[string]compose.Service{
		"api": {Ports: []compose.Port{{Target: 3000, Published: "3303"}}},
	}}
	if _, err := e.ApplyInstance(context.Background(), inst, nil, true); err == nil || !strings.Contains(err.Error(), "3304") {
		t.Errorf("err = %v", err)
	}
}

func TestPlanPortsSkipsPortsReservedForOtherInstances(t *testing.T) {
	e, _, inst := portsFixture(t, apiPorts(), 3303)
	if err := e.portStore.Save(ports.State{"/elsewhere": {"api|3000/tcp": 3304}}); err != nil {
		t.Fatal(err)
	}
	plan, _ := e.PlanPorts(context.Background(), inst, nil, true)
	if m := mapping(t, plan, "api"); m.Effective != 3305 {
		t.Errorf("effective = %d, want 3305 (3304 is reserved)", m.Effective)
	}
}

func TestApplyInstanceAppliesSettingsAndMasksSecrets(t *testing.T) {
	svcs := map[string]compose.Service{
		"api": {},
		"db":  {ContainerName: "fixed-db"},
	}
	e, c, inst := portsFixture(t, svcs)
	str := func(v string) *string { return &v }
	// What Compose resolves with the generated file: no container_name, and
	// the variables set with `theo env`.
	c.resolved = &compose.Project{Services: map[string]compose.Service{
		"api": {Environment: map[string]*string{"TOKEN": str("s3cret"), "MODE": str("x y")}},
		"db":  {Environment: map[string]*string{"MODE": str("x y")}},
	}}
	c.text = "services:\n  api:\n    environment:\n      TOKEN: s3cret\n"
	ctx := context.Background()

	if err := e.SetEnv(inst, "TOKEN", "s3cret", true, []string{"api"}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetEnv(inst, "MODE", "x y", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.SetNeutralizeContainerNames(inst, true); err != nil {
		t.Fatal(err)
	}
	if err := e.SetEnv(inst, "1BAD", "v", false, nil); err == nil {
		t.Error("an invalid variable name must be refused")
	}
	if err := e.SetEnv(inst, "OK", "v", false, []string{"nope"}); err == nil || !strings.Contains(err.Error(), "api, db") {
		t.Errorf("an unknown service must be refused, listing the real ones: %v", err)
	}

	if _, err := e.ApplyInstance(ctx, inst, nil, false); err != nil {
		t.Fatal(err)
	}
	files := inst.GeneratedFiles()
	if len(files) != 2 {
		t.Fatalf("generated = %v", files)
	}
	overlay, _ := os.ReadFile(files[0])
	for _, want := range []string{`"db":`, "container_name: !reset null", `"TOKEN": "s3cret"`, `"MODE": "x y"`} {
		if !strings.Contains(string(overlay), want) {
			t.Errorf("overlay lacks %q:\n%s", want, overlay)
		}
	}
	// TOKEN only goes to api, MODE to every service.
	if strings.Count(string(overlay), `"MODE"`) != 2 || strings.Count(string(overlay), `"TOKEN"`) != 1 {
		t.Errorf("overlay scoping:\n%s", overlay)
	}
	envFile, _ := os.ReadFile(files[1])
	if !strings.Contains(string(envFile), "MODE='x y'") {
		t.Errorf("env file:\n%s", envFile)
	}
	if info, err := os.Stat(files[0]); err != nil || info.Mode().Perm()&0o077 != 0 && filepath.Separator == '/' {
		t.Errorf("the generated files must be private: %v %v", info, err)
	}
	// The env file comes last, so it wins over the project's.
	if got := inst.target().EnvFiles; len(got) == 0 || got[len(got)-1] != files[1] {
		t.Errorf("env files = %v", got)
	}

	text, err := e.ResolvedConfig(ctx, inst, nil, false)
	if err != nil || strings.Contains(text, "s3cret") || !strings.Contains(text, Mask) {
		t.Errorf("resolved config must mask secrets: %v\n%s", err, text)
	}

	// Removing every setting goes back to the project's own files.
	e.UnsetEnv(inst, "TOKEN")
	e.UnsetEnv(inst, "MODE")
	e.SetNeutralizeContainerNames(inst, false)
	if _, err := e.ApplyInstance(ctx, inst, nil, false); err != nil {
		t.Fatal(err)
	}
	if len(inst.GeneratedFiles()) != 0 || len(inst.target().Files) != 0 {
		t.Errorf("files still in use: %v %v", inst.GeneratedFiles(), inst.target().Files)
	}
	if _, err := os.Stat(files[0]); err == nil {
		t.Errorf("the stale overlay was not removed")
	}
}

func TestHistoryRecordsOperations(t *testing.T) {
	e, c, inst := portsFixture(t, apiPorts())
	ctx := context.Background()
	_ = c
	if err := e.Up(ctx, inst, compose.UpOptions{}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := e.Down(ctx, inst, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	all, err := e.History(ctx, "app", "", 0)
	if err != nil || len(all) != 2 || all[0].Operation != "up" || all[1].Operation != "down" || all[0].Result != "ok" {
		t.Fatalf("history = %+v, %v", all, err)
	}
	if got, _ := e.History(ctx, "app", "main", 0); len(got) != 0 {
		t.Errorf("the main worktree has no operations: %+v", got)
	}
	if got, _ := e.History(ctx, "app", "feature/login", 1); len(got) != 1 || got[0].Operation != "down" {
		t.Errorf("limit: %+v", got)
	}
}

func TestPlanPortsSkipsServicesThatShareANetwork(t *testing.T) {
	svcs := apiPorts()
	svcs["host"] = compose.Service{NetworkMode: "host", Ports: []compose.Port{{Target: 80, Published: "3303", Protocol: "tcp"}}}
	svcs["side"] = compose.Service{NetworkMode: "service:api", Ports: []compose.Port{{Target: 81, Published: "3304", Protocol: "tcp"}}}
	e, _, inst := portsFixture(t, svcs, 3303)
	plan, err := e.PlanPorts(context.Background(), inst, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Mappings) != 1 || plan.Mappings[0].Service != "api" || len(plan.changed) != 1 {
		t.Errorf("only api publishes ports through Docker: %+v", plan.Mappings)
	}
}
