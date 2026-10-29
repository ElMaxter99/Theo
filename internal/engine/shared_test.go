package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/instances"
)

func sharedServices() map[string]compose.Service {
	dep := func(names ...string) map[string]json.RawMessage {
		m := map[string]json.RawMessage{}
		for _, n := range names {
			m[n] = json.RawMessage(`{"condition":"service_started","required":true}`)
		}
		return m
	}
	return map[string]compose.Service{
		"api":   {Image: "api:1", DependsOn: dep("db", "cache"), Networks: map[string]json.RawMessage{"default": nil}},
		"db":    {Image: "mongo:7", Ports: []compose.Port{{Target: 27017, Published: "27017", Protocol: "tcp"}}, ContainerName: "fixed-db", Networks: map[string]json.RawMessage{"default": nil}},
		"cache": {Image: "redis:7"},
	}
}

func TestSharedAddValidatesAndKeepsOneDefiningWorktree(t *testing.T) {
	e, c, inst := portsFixture(t, sharedServices())
	c.configs[root] = &compose.Project{Name: "app", Services: sharedServices()}
	ctx := context.Background()
	if _, err := e.SharedAdd(ctx, "app", []string{"nope"}, "feature/login", false); err == nil || !strings.Contains(err.Error(), "api, cache, db") {
		t.Errorf("an unknown service must be refused, listing the real ones: %v", err)
	}
	set, err := e.SharedAdd(ctx, "app", []string{"db"}, "feature/login", false)
	if err != nil || set.From != inst.Workspace || set.Network != "theo-app-shared" || len(set.Services) != 1 {
		t.Fatalf("set = %+v, %v", set, err)
	}
	if set, err = e.SharedAdd(ctx, "app", []string{"cache"}, "", false); err != nil || len(set.Services) != 2 {
		t.Errorf("adding more services keeps the same worktree: %+v %v", set, err)
	}
	if _, err := e.SharedAdd(ctx, "app", []string{"db"}, "main", false); err == nil || !strings.Contains(err.Error(), "defined from") {
		t.Errorf("another defining worktree must be refused: %v", err)
	}
}

func TestSharedUseAddsTheConsumerOverlay(t *testing.T) {
	e, c, inst := portsFixture(t, sharedServices())
	ctx := context.Background()
	c.resolved = &compose.Project{Services: sharedServices()}
	if _, err := e.SharedAdd(ctx, "app", []string{"db"}, "feature/login", false); err != nil {
		t.Fatal(err)
	}
	if err := e.SharedUse(inst, []string{"cache"}, nil); err == nil {
		t.Errorf("a service that is not shared cannot be used")
	}
	if err := e.SharedUse(inst, []string{"db"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApplyInstance(ctx, inst, nil, false); err != nil {
		t.Fatal(err)
	}
	overlay, err := os.ReadFile(inst.GeneratedFiles()[0])
	if err != nil {
		t.Fatal(err)
	}
	got := string(overlay)
	for _, want := range []string{
		`name: "theo-app-shared"`,
		`"db":`, `profiles: !override`, `- "theo-shared"`, // its own copy never starts
		`"api":`, `depends_on: !override`, `"cache": {"condition":"service_started","required":true}`, // only the shared dependency goes
		`"default": {}`, `"theo_shared": {}`, // joins the shared network without leaving its own
	} {
		if !strings.Contains(got, want) {
			t.Errorf("overlay lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"db": {"condition"`) {
		t.Errorf("api must lose its dependency on the shared service:\n%s", got)
	}

	// Unusing goes back to the project's own files.
	if err := e.SharedUnuse(inst, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApplyInstance(ctx, inst, nil, false); err != nil {
		t.Fatal(err)
	}
	if len(inst.GeneratedFiles()) != 0 {
		t.Errorf("files still in use: %v", inst.GeneratedFiles())
	}
}

func TestSharedUpDoesNotPublishPortsAndStartsOnlyTheSharedServices(t *testing.T) {
	e, c, _ := portsFixture(t, sharedServices())
	ctx := context.Background()
	c.resolved = &compose.Project{Services: sharedServices()}
	e.SharedAdd(ctx, "app", []string{"db"}, "feature/login", false)
	if err := e.SharedUp(ctx, "app", io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(c.networks) != 1 || c.networks[0] != "theo-app-shared" {
		t.Errorf("networks = %v", c.networks)
	}
	if !strings.Contains(strings.Join(c.calls, "\n"), "up app-shared") {
		t.Errorf("calls = %q", c.calls)
	}
	files, _ := os.ReadFile(e.instanceFile(&Instance{Workspace: feature, Compose: "app-shared"}, ".yaml"))
	for _, want := range []string{"ports: !override []", "container_name: !reset null", `"theo_shared": {}`} {
		if !strings.Contains(string(files), want) {
			t.Errorf("provider overlay lacks %q:\n%s", want, files)
		}
	}
}

func TestSharedProtections(t *testing.T) {
	e, c, inst := portsFixture(t, sharedServices())
	ctx := context.Background()
	c.resolved = &compose.Project{Services: sharedServices()}
	e.SharedAdd(ctx, "app", []string{"db"}, "feature/login", false)
	if err := e.SharedUse(inst, []string{"db"}, nil); err != nil {
		t.Fatal(err)
	}
	// The consumer has run (history) and is running (stack).
	e.settings.Append(instances.Event{Project: "app", Workspace: inst.Workspace, Compose: inst.Compose, Operation: "up", Result: "ok"})
	c.stacks = []compose.Stack{{Name: inst.Compose, Status: "running(2)"}}

	var inUse *SharedInUseError
	if err := e.SharedDown(ctx, "app", "", io.Discard, io.Discard); !errors.As(err, &inUse) || len(inUse.Consumers) != 1 || !inUse.Consumers[0].Active || inUse.Confirm != "app-shared" {
		t.Fatalf("down with a running consumer: %v", err)
	}
	if err := e.SharedDown(ctx, "app", "wrong", io.Discard, io.Discard); !errors.As(err, &inUse) {
		t.Errorf("a wrong confirmation must be refused: %v", err)
	}
	if err := e.SharedRemove(ctx, "app", nil); !errors.As(err, &inUse) {
		t.Errorf("remove with a linked instance: %v", err)
	}
	if _, err := e.RemoveWorktree(ctx, "app", "feature/login", RemoveWorktreeOptions{}); err == nil || !strings.Contains(err.Error(), "defines the shared services") {
		t.Errorf("the worktree that defines the shared services cannot be removed: %v", err)
	}
	if err := e.SharedDown(ctx, "app", "app-shared", io.Discard, io.Discard); err != nil {
		t.Errorf("confirmed down: %v", err)
	}

	// Once nobody is linked, removing works and takes the network along.
	e.SharedUnuse(inst, nil)
	if err := e.SharedRemove(ctx, "app", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.SharedSet("app"); ok {
		t.Errorf("the set is still declared")
	}
	if len(c.removedNetworks) != 1 || c.removedNetworks[0] != "theo-app-shared" {
		t.Errorf("removed networks = %v", c.removedNetworks)
	}
}

func TestEnsureSharedRefusesUnlessAskedToStart(t *testing.T) {
	e, c, inst := portsFixture(t, sharedServices())
	ctx := context.Background()
	c.resolved = &compose.Project{Services: sharedServices()}
	e.SharedAdd(ctx, "app", []string{"db"}, "feature/login", false)
	e.SharedUse(inst, []string{"db"}, nil)

	if _, err := e.EnsureShared(ctx, inst, false, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "--start-shared") {
		t.Fatalf("stopped provider must be refused with a hint: %v", err)
	}
	if strings.Contains(strings.Join(c.calls, "\n"), "up app-shared") {
		t.Fatalf("it started the shared services in silence")
	}
	if _, err := e.EnsureShared(ctx, inst, true, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(c.calls, "\n"), "up app-shared") {
		t.Errorf("calls = %q", c.calls)
	}
}

func TestReconcileReportsBrokenSharedServices(t *testing.T) {
	e, _, inst := portsFixture(t, sharedServices())
	ctx := context.Background()
	e.SharedAdd(ctx, "app", []string{"db"}, "feature/login", false)
	e.SharedUse(inst, []string{"db"}, nil)
	all, _ := e.settings.LoadShared()
	set := all["app"]
	set.Services = []string{"cache"} // db is no longer shared but the instance still links to it
	all["app"] = set
	e.settings.SaveShared(all)
	r, err := e.Reconcile(ctx, "app")
	if err != nil || !has(r, FindSharedBroken) {
		t.Errorf("findings = %v, %v", kinds(r), err)
	}
}

func TestPortsOfServicesTakenFromTheSharedOnesAreNotPlanned(t *testing.T) {
	e, _, inst := portsFixture(t, sharedServices(), 27017)
	ctx := context.Background()
	e.SharedAdd(ctx, "app", []string{"db"}, "feature/login", false)
	plan, _ := e.PlanPorts(ctx, inst, nil, false)
	if len(plan.Conflicts()) != 1 {
		t.Fatalf("before using the shared db its port conflicts: %+v", plan.Mappings)
	}
	e.SharedUse(inst, []string{"db"}, nil)
	plan, _ = e.PlanPorts(ctx, inst, nil, false)
	if len(plan.Mappings) != 0 {
		t.Errorf("the instance does not run db, so its port is not planned: %+v", plan.Mappings)
	}
}
