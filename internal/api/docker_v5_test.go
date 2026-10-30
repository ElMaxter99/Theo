package api_test

import (
	"context"
	"fmt"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// Real Docker tests of the V5 and V6 resources. Every Docker name starts
// with "ub5" (this track's prefix).

type sharedDockerEnv struct {
	*env
	project string
	shared  string // Compose project of the shared services
	names   []string
}

func newSharedDockerEnv(t *testing.T) *sharedDockerEnv {
	t.Helper()
	testutil.RequireDaemon(t)
	name := fmt.Sprintf("ub5s%06d", rand.Intn(1000000))
	main, linked := testutil.NewRepo(t, map[string]string{
		".gitignore": "server\nDockerfile\n",
		"compose.yaml": fmt.Sprintf(`name: %s
services:
  db:
    build: .
    volumes: [data:/data]
  api:
    build: .
    depends_on: [db]
volumes:
  data: {}
`, name),
	})
	testutil.WriteServerImage(t, main)
	testutil.WriteServerImage(t, linked)
	home := testutil.TempDir(t)
	t.Setenv("THEO_HOME", home)
	eng, err := engine.Open()
	if err != nil {
		t.Fatal(err)
	}
	project := name + "p"
	if _, err := eng.AddProject(context.Background(), main, project, ""); err != nil {
		t.Fatal(err)
	}
	d := &sharedDockerEnv{project: project, shared: project + "-shared", names: []string{name, project + "-app-feature", project + "-shared"}}
	t.Cleanup(func() {
		for _, n := range d.names {
			exec.Command("docker", "compose", "-p", n, "down", "-v", "--remove-orphans", "--timeout", "1").Run()
		}
		exec.Command("docker", "network", "rm", "theo-"+project+"-shared").Run()
	})
	d.env = startEnv(t, eng, nil, main, linked, home)
	return d
}

func (d *sharedDockerEnv) wt(path string) string {
	var list []struct{ ID, Path string }
	d.c.call("GET", "/api/v1/projects/"+d.project+"/worktrees", nil, &list)
	for _, w := range list {
		if sameDir(w.Path, path) {
			return "/api/v1/projects/" + d.project + "/worktrees/" + w.ID
		}
	}
	d.t.Fatalf("worktree %s not found", path)
	return ""
}

func (d *sharedDockerEnv) op(method, path string, body any) opState {
	d.t.Helper()
	var op opState
	if st := d.c.call(method, path, body, &op); st != 202 {
		d.t.Fatalf("%s %s = %d", method, path, st)
	}
	f := (&dockerEnv{env: d.env}).waitOpLong(op.ID)
	if f.State != "succeeded" {
		(&dockerEnv{env: d.env}).dumpOp(op.ID)
		d.t.Fatalf("%s %s = %+v err %+v", method, path, f, f.Error)
	}
	return f
}

func TestDockerSharedServicesFromTheAPI(t *testing.T) {
	d := newSharedDockerEnv(t)
	p := "/api/v1/projects/" + d.project
	mainWT, linkedWT := d.wt(d.main), d.wt(d.linked)

	var er apiErr
	if st := d.c.call("GET", p+"/shared", nil, &er); st != 404 || er.Error.Code != "shared_not_configured" {
		t.Fatalf("before add = %d %+v", st, er.Error)
	}
	var st sharedStatus
	if code := d.c.call("POST", p+"/shared", map[string]any{"services": []string{"db"}}, &st); code != 201 || st.Compose != d.shared {
		t.Fatalf("add = %d %+v", code, st)
	}
	// Declaring starts nothing.
	if got := dockerPS(t, d.shared); got != "" {
		t.Fatalf("declaring started containers: %s", got)
	}
	d.op("POST", p+"/shared/up", nil)
	if got := dockerPS(t, d.shared); !strings.Contains(got, "running") || strings.Count(got, "\n") != 0 {
		t.Fatalf("shared project = %q, want only db, running", got)
	}
	for _, w := range []string{mainWT, linkedWT} {
		if code := d.c.call("PUT", w+"/shared", map[string]any{"use": []string{"db"}}, nil); code != 200 {
			t.Fatalf("use = %d", code)
		}
		d.op("POST", w+"/up", map[string]any{"auto_ports": true})
	}
	// Each instance runs only api; db runs once, in the shared project.
	if got := dockerPS(t, d.names[0]); strings.Contains(got, "db") || !strings.Contains(got, "api") {
		t.Errorf("main instance runs %q", got)
	}

	d.c.call("GET", p+"/shared", nil, &st)
	if len(st.Consumers) != 2 || !st.Consumers[0].Active || !st.Consumers[1].Active || st.Services[0].State != "running" {
		t.Fatalf("status = %+v", st)
	}
	// The API answers what the CLI prints.
	var apiSt, cliSt any = apiJSON(t, d.c, p+"/shared"), cliJSON(t, "shared", "status", d.project, "--json")
	if !reflect.DeepEqual(apiSt, cliSt) {
		t.Errorf("shared status: API and CLI differ\napi %v\ncli %v", apiSt, cliSt)
	}

	// Down with two running consumers: 409, both listed, nothing stopped.
	if code := d.c.call("POST", p+"/shared/down", nil, &er); code != 409 || er.Error.Code != "confirmation_required" || er.Error.Level != 2 ||
		len(er.Error.Affected) != 2 || er.Error.Confirm.Expected != d.shared {
		t.Fatalf("down = %d %+v", code, er.Error)
	}
	if got := dockerPS(t, d.shared); !strings.Contains(got, "running") {
		t.Errorf("a refused down stopped the shared services: %q", got)
	}
	// Purge and remove with consumers linked.
	if code := d.c.call("POST", p+"/shared/purge", map[string]any{"confirm": d.shared}, &er); code != 409 || er.Error.Code != "shared_in_use" {
		t.Errorf("purge = %d %+v", code, er.Error)
	}
	if code := d.c.call("DELETE", p+"/shared/services/db", nil, &er); code != 409 || er.Error.Code != "shared_in_use" {
		t.Errorf("remove = %d %+v", code, er.Error)
	}
	// One failing request affects nobody else: the instances keep running.
	if got := dockerPS(t, d.names[0]); !strings.Contains(got, "running") {
		t.Errorf("main instance after refusals: %q", got)
	}

	// Confirmed down: the provider stops, the consumers keep their containers,
	// and the data is kept.
	d.op("POST", p+"/shared/down", map[string]any{"confirm": d.shared})
	if got := dockerPS(t, d.shared); got != "" {
		t.Errorf("shared containers after down: %q", got)
	}
	if out := testutil.Docker(t, "volume", "ls", "-q", "--filter", "label=com.docker.compose.project="+d.shared); !strings.Contains(out, "data") {
		t.Errorf("the shared volume was not kept: %q", out)
	}
	for _, w := range []string{mainWT, linkedWT} {
		d.op("POST", w+"/down", nil)
		if code := d.c.call("DELETE", w+"/shared", nil, nil); code != 200 {
			t.Fatalf("unuse = %d", code)
		}
	}
	// Purge: the text first, then the data goes.
	if code := d.c.call("POST", p+"/shared/purge", map[string]any{}, &er); code != 409 || er.Error.Code != "confirmation_required" || er.Error.Level != 3 || er.Error.Confirm.Expected != d.shared {
		t.Fatalf("purge needs the text = %d %+v", code, er.Error)
	}
	if code := d.c.call("POST", p+"/shared/purge", map[string]any{"confirm": "nope"}, &er); code != 409 {
		t.Errorf("purge wrong text = %d", code)
	}
	var done struct{ Deleted []struct{ Name string } }
	if code := d.c.call("POST", p+"/shared/purge", map[string]any{"confirm": d.shared}, &done); code != 200 || len(done.Deleted) != 1 {
		t.Fatalf("purge = %d %+v", code, done)
	}
	if out := testutil.Docker(t, "volume", "ls", "-q", "--filter", "label=com.docker.compose.project="+d.shared); strings.TrimSpace(out) != "" {
		t.Errorf("the shared volume survived the purge: %q", out)
	}
	if code := d.c.call("DELETE", p+"/shared/services/db", nil, nil); code != 200 {
		t.Errorf("remove = %d", code)
	}
	if code := d.c.call("GET", p+"/shared", nil, &er); code != 404 {
		t.Errorf("after remove = %d", code)
	}
}

// Reconcile against real Docker: a stale record is forgotten, containers and
// volumes that exist are never touched, and the report equals the CLI's.
func TestDockerReconcileForgetsOnlyTheosRecords(t *testing.T) {
	d := newDockerEnv(t)
	p := d.project
	base := d.base(d.linked)

	// An instance with a container and a volume, started by Theo...
	d.c.call("PUT", base+"/env/UB5_X", map[string]any{"value": "1"}, nil)
	var op opState
	d.c.call("POST", base+"/up", map[string]any{"auto_ports": true}, &op)
	if f := (&dockerEnv{env: d.env}).waitOpLong(op.ID); f.State != "succeeded" {
		t.Fatalf("up = %+v", f)
	}
	linkedName := p + "-app-feature"
	// ...whose worktree is then deleted behind Theo's back. The directory is
	// gone, the containers and the volume are not.
	d.c.call("POST", base+"/down", nil, &op)
	(&dockerEnv{env: d.env}).waitOpLong(op.ID)
	volBefore := testutil.Docker(t, "volume", "ls", "-q", "--filter", "label=com.docker.compose.project="+linkedName)
	if strings.TrimSpace(volBefore) == "" {
		t.Fatal("the instance has no volume to protect")
	}
	d.c.call("POST", base+"/up", map[string]any{"auto_ports": true}, &op)
	(&dockerEnv{env: d.env}).waitOpLong(op.ID)
	if out, err := exec.Command("git", "-C", d.main, "worktree", "remove", "--force", d.linked).CombinedOutput(); err != nil {
		t.Fatalf("worktree remove: %v %s", err, out)
	}

	var rep reconcileOut
	if code := d.c.call("GET", "/api/v1/reconcile?project="+p, nil, &rep); code != 200 {
		t.Fatalf("reconcile = %d", code)
	}
	kinds := map[string]bool{}
	var forgettable []string
	for _, f := range rep.Findings {
		kinds[f.Kind] = true
		if f.Forgettable {
			forgettable = append(forgettable, f.ID)
		}
	}
	for _, want := range []string{engine.FindStaleSettings, engine.FindOrphanStack} {
		if !kinds[want] {
			t.Errorf("no %q finding in %+v", want, rep.Findings)
		}
	}
	for _, f := range rep.Findings {
		if (f.Kind == engine.FindOrphanStack || f.Kind == engine.FindOrphanVolumes) && f.Forgettable {
			t.Errorf("containers or volumes are forgettable: %+v", f)
		}
	}
	// The API and the CLI see the same report.
	apiRep, cliRep := apiJSON(t, d.c, "/api/v1/reconcile?project="+p), cliJSON(t, "reconcile", p, "--json")
	if !reflect.DeepEqual(apiRep, cliRep) {
		t.Errorf("reconcile: API and CLI differ\napi %v\ncli %v", apiRep, cliRep)
	}

	d.c.call("POST", "/api/v1/reconcile/apply", map[string]any{"project": p, "ids": forgettable}, &rep)
	if rep.Forgotten != len(forgettable) || rep.Forgotten == 0 {
		t.Fatalf("forgotten %d of %d", rep.Forgotten, len(forgettable))
	}
	// What exists stays: the container and the volume.
	if got := dockerPS(t, linkedName); !strings.Contains(got, "running") {
		t.Errorf("the orphan container was touched: %q", got)
	}
	if after := testutil.Docker(t, "volume", "ls", "-q", "--filter", "label=com.docker.compose.project="+linkedName); after != volBefore {
		t.Errorf("volumes changed: %q -> %q", volBefore, after)
	}
	d.c.call("GET", "/api/v1/reconcile?project="+p, nil, &rep)
	for _, f := range rep.Findings {
		if f.Kind == engine.FindStaleSettings {
			t.Errorf("stale settings still reported: %+v", f)
		}
	}
	if !strings.Contains(strings.Join(func() (k []string) {
		for _, f := range rep.Findings {
			k = append(k, f.Kind)
		}
		return
	}(), ","), engine.FindOrphanStack) {
		t.Errorf("the orphan containers vanished from the report: %+v", rep.Findings)
	}
}

// With Docker unreachable the report says so and the registry findings are
// still there (the engine under test talks to a daemon that does not exist).
func TestDockerReconcileWithTheDaemonUnreachable(t *testing.T) {
	d := newDockerEnv(t)
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/ub5-docker.sock")
	var rep reconcileOut
	if code := d.c.call("GET", "/api/v1/reconcile", nil, &rep); code != 200 || rep.DockerError == "" {
		t.Fatalf("reconcile = %d %+v", code, rep)
	}
	var h struct{ Status string }
	d.c.call("GET", "/api/v1/health", nil, &h)
	if h.Status != "docker_unavailable" {
		t.Errorf("health = %+v", h)
	}
	var er apiErr
	if code := d.c.call("GET", d.base(d.main)+"/volumes", nil, &er); code == 200 {
		t.Log("volumes answered without Docker (no volumes to look up)")
	}
	// The registry views keep working.
	var projects []any
	if code := d.c.call("GET", "/api/v1/projects", nil, &projects); code != 200 || len(projects) != 1 {
		t.Errorf("projects = %d", code)
	}
}

// History pages on real data equal the CLI's output.
func TestDockerHistoryPagesEqualTheCLI(t *testing.T) {
	d := newDockerEnv(t)
	base := d.base(d.linked)
	var op opState
	for i := 0; i < 2; i++ {
		d.c.call("POST", base+"/up", map[string]any{"auto_ports": true}, &op)
		(&dockerEnv{env: d.env}).waitOpLong(op.ID)
		d.c.call("POST", base+"/down", nil, &op)
		(&dockerEnv{env: d.env}).waitOpLong(op.ID)
	}
	same := func(name string, a, b any) {
		t.Helper()
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: API and CLI differ\napi %v\ncli %v", name, a, b)
		}
	}
	same("history limit", apiJSON(t, d.c, base+"/history?limit=3"), cliJSON(t, "history", d.project, "-w", "feature/x", "--limit", "3", "--json"))
	same("history", apiJSON(t, d.c, base+"/history"), cliJSON(t, "history", d.project, "-w", "feature/x", "--json"))
	same("project history", apiJSON(t, d.c, "/api/v1/projects/"+d.project+"/history"), cliJSON(t, "history", d.project, "--json"))
}
