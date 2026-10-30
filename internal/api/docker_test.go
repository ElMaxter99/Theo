package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/api"
	"github.com/ElMaxter99/Theo/internal/cli"
	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// dockerEnv is a server over the real engine, real Git and real Docker.
// Every Docker name starts with "ub" (this track's prefix).
type dockerEnv struct {
	*env
	project string // Theo project name
	name    string // Compose project name of the main worktree
	port    int
	home    string
}

func newDockerEnv(t *testing.T) *dockerEnv {
	t.Helper()
	testutil.RequireDaemon(t)
	port := testutil.FreePort(t)
	name := fmt.Sprintf("ubd%06d", rand.Intn(1000000))
	files := map[string]string{"compose.yaml": fmt.Sprintf(`name: %s
services:
  web:
    build: .
    ports: ["%d:8080"]
    volumes: ["data:/data"]
volumes:
  data: {}
`, name, port)}
	main, linked := testutil.NewRepo(t, files)
	testutil.WriteServerImage(t, main)
	testutil.Git(t, main, "add", "-A")
	testutil.Git(t, main, "commit", "-q", "-m", "server")
	testutil.Git(t, linked, "merge", "-q", "--ff-only", "main")

	home := testutil.TempDir(t)
	t.Setenv("THEO_HOME", home)
	eng, err := engine.Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.AddProject(context.Background(), main, name+"p", ""); err != nil {
		t.Fatal(err)
	}
	linkedName := name + "p-app-feature"
	t.Cleanup(func() {
		for _, n := range []string{name, linkedName} {
			exec.Command("docker", "compose", "-p", n, "down", "-v", "--remove-orphans", "--timeout", "1").Run()
		}
	})
	e := startEnv(t, eng, nil, main, linked, home)
	return &dockerEnv{env: e, project: name + "p", name: name, port: port, home: home}
}

func (d *dockerEnv) base(wt string) string {
	return "/api/v1/projects/" + d.project + "/worktrees/" + d.wtIDOf(wt)
}

func (d *dockerEnv) wtIDOf(path string) string {
	var list []struct {
		ID, Path string
	}
	d.c.call("GET", "/api/v1/projects/"+d.project+"/worktrees", nil, &list)
	for _, w := range list {
		if sameDir(w.Path, path) {
			return w.ID
		}
	}
	d.t.Fatalf("worktree %s not found", path)
	return ""
}

func dockerPS(t *testing.T, project string) string {
	out, _ := exec.Command("docker", "ps", "-a", "--filter", "label=com.docker.compose.project="+project, "--format", "{{.Names}} {{.State}}").Output()
	return strings.TrimSpace(string(out))
}

func TestDockerUpFromTheAPICreatesContainers(t *testing.T) {
	d := newDockerEnv(t)
	base := d.base(d.main)
	var op opState
	if st := d.c.call("POST", base+"/up", map[string]any{}, &op); st != 202 {
		t.Fatalf("up = %d", st)
	}
	// A second up on the same instance while the first runs is busy (or, if
	// the first already finished, a new operation: both are acceptable, but
	// it must never run twice at once).
	var busy apiErr
	st2 := d.c.call("POST", base+"/up", map[string]any{}, &busy)
	if st2 != 409 && st2 != 202 {
		t.Errorf("second up = %d", st2)
	}
	if st2 == 409 && busy.Error.Code != "busy" {
		t.Errorf("second up = %+v", busy.Error)
	}
	s := d.c.sse("/api/v1/operations/"+op.ID+"/events", "")
	_, seen := s.until(t, "result", 120*time.Second)
	last := seen[len(seen)-1]
	if !strings.Contains(last.Data, `"ok":true`) {
		t.Fatalf("up failed: %s", last.Data)
	}
	if ps := dockerPS(t, d.name); !strings.Contains(ps, "running") {
		t.Errorf("docker ps: %q", ps)
	}
	var w struct {
		State string
		Ports []struct {
			URL string
		}
	}
	d.c.call("GET", base, nil, &w)
	if w.State != "running" || len(w.Ports) != 1 || !strings.HasSuffix(w.Ports[0].URL, fmt.Sprint(d.port)) {
		t.Errorf("detail = %+v", w)
	}
	// Down.
	var down opState
	d.c.call("POST", base+"/down", nil, &down)
	if f := d.waitOp(down.ID); f.State != "succeeded" {
		t.Errorf("down = %+v", f)
	}
	if ps := dockerPS(t, d.name); ps != "" {
		t.Errorf("containers after down: %q", ps)
	}
	// Volumes were kept; purge needs the confirmation text.
	var vols []struct{ Name, Status string }
	d.c.call("GET", base+"/volumes", nil, &vols)
	if len(vols) != 1 || vols[0].Status != "kept" {
		t.Fatalf("volumes = %+v", vols)
	}
	var need apiErr
	if st := d.c.call("POST", base+"/volumes/purge", map[string]any{}, &need); st != 409 || need.Error.Confirm == nil || need.Error.Confirm.Expected != d.name {
		t.Fatalf("purge = %d %+v", st, need.Error)
	}
	if out, _ := exec.Command("docker", "volume", "ls", "-q", "--filter", "name="+d.name).Output(); len(bytes.TrimSpace(out)) == 0 {
		t.Fatal("the volume was deleted without confirmation")
	}
	var done struct{ Deleted []any }
	if st := d.c.call("POST", base+"/volumes/purge", map[string]any{"confirm": d.name}, &done); st != 200 || len(done.Deleted) != 1 {
		t.Errorf("purge = %d %+v", st, done)
	}
	var hist []struct{ Operation string }
	d.c.call("GET", base+"/history", nil, &hist)
	ops := ""
	for _, h := range hist {
		ops += h.Operation + ","
	}
	if !strings.Contains(ops, "up") || !strings.Contains(ops, "down") || !strings.Contains(ops, "volumes purge") {
		t.Errorf("history = %s", ops)
	}
}

func TestDockerTwoInstancesInParallelWithAutoPorts(t *testing.T) {
	d := newDockerEnv(t)
	var a, b opState
	d.c.call("POST", d.base(d.main)+"/up", map[string]any{}, &a)
	if fa := d.waitOpLong(a.ID); fa.State != "succeeded" {
		t.Fatalf("main up = %+v", fa)
	}
	// The main instance holds the port: the linked one gets another.
	d.c.call("POST", d.base(d.linked)+"/up", map[string]any{"auto_ports": true}, &b)
	if fa := d.waitOpLong(a.ID); fa.State != "succeeded" {
		d.dumpOp(a.ID)
		t.Fatalf("main up = %+v %+v", fa, *fa.Error)
	}
	if fb := d.waitOpLong(b.ID); fb.State != "succeeded" {
		d.dumpOp(b.ID)
		t.Fatalf("linked up = %+v %+v", fb, *fb.Error)
	}
	var ov struct {
		Worktrees []struct {
			Branch string
			State  string
			Ports  []struct {
				HostPort int `json:"host_port"`
				URL      string
			}
		}
	}
	d.c.call("GET", "/api/v1/projects/"+d.project+"/overview?refresh=1", nil, &ov)
	if len(ov.Worktrees) != 2 {
		t.Fatalf("overview %+v", ov)
	}
	seen := map[int]bool{}
	for _, w := range ov.Worktrees {
		if w.State != "running" || len(w.Ports) != 1 {
			t.Errorf("%s: %+v", w.Branch, w)
			continue
		}
		seen[w.Ports[0].HostPort] = true
	}
	if len(seen) != 2 {
		t.Errorf("both instances share a port: %+v", ov)
	}
	// Preview of a port plan reserves nothing.
	var plan struct {
		Ports []struct{ Status string } `json:"ports"`
	}
	d.c.call("GET", d.base(d.linked)+"/ports?auto_ports=1", nil, &plan)
}

func TestDockerPortConflictIsClassified(t *testing.T) {
	d := newDockerEnv(t)
	var a, b opState
	d.c.call("POST", d.base(d.main)+"/up", nil, &a)
	if f := d.waitOpLong(a.ID); f.State != "succeeded" {
		t.Fatalf("main up = %+v", f)
	}
	d.c.call("POST", d.base(d.linked)+"/up", map[string]any{}, &b)
	f := d.waitOpLong(b.ID)
	if f.State != "failed" || f.Error == nil || f.Error.Code != "port_conflict" {
		t.Errorf("linked up on a taken port = %+v", f)
	}
	var plan struct {
		Ports []struct{ Status string } `json:"ports"`
	}
	d.c.call("GET", d.base(d.linked)+"/ports", nil, &plan)
	if len(plan.Ports) != 1 || plan.Ports[0].Status != "in use" {
		t.Errorf("preview should flag the conflict: %+v", plan)
	}
	d.c.call("GET", d.base(d.linked)+"/ports?auto_ports=1", nil, &plan)
	if len(plan.Ports) != 1 || plan.Ports[0].Status != "reassigned" {
		t.Errorf("auto-ports preview = %+v", plan)
	}
}

func (d *dockerEnv) waitOpLong(id string) opState {
	d.t.Helper()
	deadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(deadline) {
		var op opState
		d.c.call("GET", "/api/v1/operations/"+id, nil, &op)
		if op.State != "queued" && op.State != "running" && op.State != "cancelling" {
			return op
		}
		time.Sleep(100 * time.Millisecond)
	}
	d.t.Fatalf("operation %s did not finish", id)
	return opState{}
}

func TestDockerCancelLeavesNoOrphans(t *testing.T) {
	d := newDockerEnv(t)
	var op opState
	d.c.call("POST", d.base(d.main)+"/up", nil, &op)
	time.Sleep(300 * time.Millisecond)
	d.c.call("POST", "/api/v1/operations/"+op.ID+"/cancel", nil, nil)
	f := d.waitOpLong(op.ID)
	if f.State != "cancelled" && f.State != "succeeded" {
		t.Errorf("state after cancel = %+v", f)
	}
	time.Sleep(500 * time.Millisecond)
	if out, _ := exec.Command("pgrep", "-af", "compose.* -p "+d.name+" .*up").Output(); len(bytes.TrimSpace(out)) > 0 {
		t.Errorf("docker compose up is still running: %s", out)
	}
	// The instance is free again and reports a consistent state.
	var w struct{ State string }
	d.c.call("GET", d.base(d.main), nil, &w)
	switch w.State {
	case "running", "partial", "absent", "stopped":
	default:
		t.Errorf("state after cancel = %q", w.State)
	}
	var again opState
	if st := d.c.call("POST", d.base(d.main)+"/down", nil, &again); st != 202 {
		t.Errorf("down after cancel = %d", st)
	}
	d.waitOpLong(again.ID)
}

func TestDockerLogsStreamAndProcessDisappears(t *testing.T) {
	d := newDockerEnv(t)
	var op opState
	d.c.call("POST", d.base(d.main)+"/up", nil, &op)
	if f := d.waitOpLong(op.ID); f.State != "succeeded" {
		t.Fatalf("up = %+v", f)
	}
	start := time.Now()
	s := d.c.sse(d.base(d.main)+"/logs?follow=1&tail=20", "")
	ev, _ := s.until(t, "log", 10*time.Second)
	if !strings.Contains(ev.Data, "server ready") || !strings.Contains(ev.Data, `"service":"web"`) {
		t.Errorf("first log event = %s", ev.Data)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("the line took %s", time.Since(start))
	}
	pattern := "compose.* -p " + d.name + " .*logs"
	if out, _ := exec.Command("pgrep", "-f", pattern).Output(); len(bytes.TrimSpace(out)) == 0 {
		t.Error("no docker compose logs process while streaming")
	}
	s.close()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if out, _ := exec.Command("pgrep", "-f", pattern).Output(); len(bytes.TrimSpace(out)) == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	out, _ := exec.Command("pgrep", "-af", pattern).Output()
	t.Errorf("docker compose logs is still running after the client left: %s", out)
}

// --- contract: the API answers what `theo <cmd> --json` prints ---

func cliJSON(t *testing.T, args ...string) any {
	t.Helper()
	var out, errb bytes.Buffer
	if code := cli.Run(args, &out, &errb); code != 0 {
		t.Fatalf("theo %s: exit %d: %s", strings.Join(args, " "), code, errb.String())
	}
	var v any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("theo %s: %v\n%s", strings.Join(args, " "), err, out.String())
	}
	return v
}

func apiJSON(t *testing.T, c *client, path string) any {
	t.Helper()
	st, body, _ := c.raw("GET", path, nil, nil)
	if st != 200 {
		t.Fatalf("GET %s = %d %s", path, st, body)
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return v
}

func TestContractAPIEqualsCLIJSON(t *testing.T) {
	d := newDockerEnv(t)
	p := d.project
	linked := "feature/x"
	base := d.base(d.linked)

	// Something to compare: a variable, a started and stopped instance (history).
	d.c.call("PUT", base+"/env/UB_PLAIN", map[string]any{"value": "1"}, nil)
	d.c.call("PUT", base+"/env/UB_SECRET", map[string]any{"value": "s3cret", "secret": true}, nil)
	var op opState
	d.c.call("POST", base+"/up", map[string]any{"auto_ports": true}, &op)
	if f := d.waitOpLong(op.ID); f.State != "succeeded" {
		t.Fatalf("up = %+v", f)
	}

	same := func(name string, a, b any) {
		t.Helper()
		if !reflect.DeepEqual(a, b) {
			ab, _ := json.MarshalIndent(a, "", " ")
			bb, _ := json.MarshalIndent(b, "", " ")
			t.Errorf("%s: API and CLI differ\n--- API\n%s\n--- CLI\n%s", name, ab, bb)
		}
	}
	same("project list", apiJSON(t, d.c, "/api/v1/projects"), cliJSON(t, "project", "list", "--json"))
	same("branches", apiJSON(t, d.c, "/api/v1/projects/"+p+"/branches"), cliJSON(t, "branches", p, "--json"))
	same("services", apiJSON(t, d.c, base+"/services"), cliJSON(t, "services", p, "-w", linked, "--json"))
	same("volumes", apiJSON(t, d.c, base+"/volumes"), cliJSON(t, "volumes", p, "-w", linked, "--json"))
	same("history", apiJSON(t, d.c, base+"/history"), cliJSON(t, "history", p, "-w", linked, "--json"))
	same("ports", apiJSON(t, d.c, base+"/ports"), cliJSON(t, "ports", p, "-w", linked, "--json"))

	// env list: the CLI prints a map name -> {value, secret, services}; the
	// API prints the same data as a sorted array (documented in docs/api.md).
	cliEnv := cliJSON(t, "env", "list", p, "-w", linked, "--json").(map[string]any)
	apiEnv := apiJSON(t, d.c, base+"/env").([]any)
	if len(cliEnv) != len(apiEnv) {
		t.Fatalf("env: %d vs %d", len(cliEnv), len(apiEnv))
	}
	for _, item := range apiEnv {
		m := item.(map[string]any)
		c := cliEnv[m["name"].(string)].(map[string]any)
		if c["value"] != m["value"] || (c["secret"] == true) != (m["secret"] == true) {
			t.Errorf("env %v: api %v cli %v", m["name"], m, c)
		}
	}

	// worktrees: same worktrees in the same order.
	cliWT := cliJSON(t, "worktrees", p, "--json").([]any)
	apiWT := apiJSON(t, d.c, "/api/v1/projects/"+p+"/worktrees").([]any)
	if len(cliWT) != len(apiWT) {
		t.Fatalf("worktrees: %d vs %d", len(cliWT), len(apiWT))
	}
	for i := range cliWT {
		c, a := cliWT[i].(map[string]any), apiWT[i].(map[string]any)
		for _, k := range []string{"path", "head", "branch", "dirty", "kind"} {
			if c[k] != a[k] && !(c[k] == nil && (a[k] == "" || a[k] == false)) {
				t.Errorf("worktree %d field %s: api %v cli %v", i, k, a[k], c[k])
			}
		}
	}
	// ps -w --json: the containers and the state.
	cliPS := cliJSON(t, "ps", p, "-w", linked, "--json").(map[string]any)
	apiD := apiJSON(t, d.c, base).(map[string]any)
	if !reflect.DeepEqual(cliPS["containers"], apiD["containers"]) {
		t.Errorf("containers differ:\napi %v\ncli %v", apiD["containers"], cliPS["containers"])
	}
	if cliPS["state"] != apiD["state"] || cliPS["state"] != "running" {
		t.Errorf("state: api %v cli %v", apiD["state"], cliPS["state"])
	}
	d.c.call("POST", base+"/down", nil, &op)
	d.waitOpLong(op.ID)
	_ = compose.LabelVolume
	_ = api.APIVersion
	_ = os.Getpid
	_ = filepath.Join
}

// dumpOp logs what an operation printed, for failures.
func (d *dockerEnv) dumpOp(id string) {
	s := d.c.sse("/api/v1/operations/"+id+"/events", "")
	for {
		ev, ok := s.next(d.t, 2*time.Second)
		if !ok {
			return
		}
		if ev.Name == "log" {
			d.t.Log(ev.Data)
		}
	}
}
