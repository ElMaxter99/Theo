package engine

import (
	"context"
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

	plan, err := e.ApplyPorts(context.Background(), inst, nil)
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
	if _, err := e.ApplyPorts(context.Background(), inst, nil); err == nil || !strings.Contains(err.Error(), "3304") {
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
