package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/api"
	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// Tests of the V5 and V6 resources (shared services, reconcile, health,
// diagnostics, paginated history) over a fake Compose. The critical ones also
// run against real Docker (docker_v5_test.go).

const sharedName = "ubapp-shared" // the Compose project of the shared services of "ubapp"

type sharedStatus struct {
	Project   string `json:"project"`
	Compose   string `json:"compose_project"`
	From      string `json:"from"`
	Network   string `json:"network"`
	Publish   bool   `json:"publish_ports"`
	Services  []struct{ Name, Image, State string }
	Consumers []struct {
		Workspace string
		Branch    string
		Services  []string
		Active    bool
	}
	Warnings []string
}

func TestSharedNotConfigured(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/shared"}, {"POST", "/shared/up"}, {"POST", "/shared/down"}, {"POST", "/shared/purge"}, {"DELETE", "/shared/services/db"},
	} {
		var er apiErr
		if st := e.c.call(c.method, "/api/v1/projects/ubapp"+c.path, nil, &er); st != 404 || er.Error.Code != "shared_not_configured" {
			t.Errorf("%s %s = %d %+v", c.method, c.path, st, er.Error)
		}
	}
	var er apiErr
	if st := e.c.call("GET", "/api/v1/projects/nope/shared", nil, &er); st != 404 || er.Error.Code != "project_not_found" {
		t.Errorf("unknown project = %d %+v", st, er.Error)
	}
}

func TestSharedAddUseUnuse(t *testing.T) {
	e := newEnv(t)
	p := "/api/v1/projects/ubapp"
	var er apiErr
	if st := e.c.call("POST", p+"/shared", map[string]any{}, &er); st != 400 {
		t.Errorf("no services = %d", st)
	}
	if st := e.c.call("POST", p+"/shared", map[string]any{"services": []string{"nope"}}, &er); st != 422 {
		t.Errorf("unknown service = %d %+v", st, er.Error)
	}
	var added sharedStatus
	if st := e.c.call("POST", p+"/shared", map[string]any{"services": []string{"db"}}, &added); st != 201 {
		t.Fatalf("add = %d", st)
	}
	if added.Compose != sharedName || added.Network != "theo-ubapp-shared" || len(added.Services) != 1 || added.Services[0].State != "not created" || added.Publish {
		t.Errorf("added = %+v", added)
	}
	var got sharedStatus
	if st := e.c.call("GET", p+"/shared", nil, &got); st != 200 || got.Compose != sharedName || len(got.Consumers) != 0 {
		t.Errorf("status = %d %+v", st, got)
	}

	wt := p + "/worktrees/" + e.wtID(e.linked)
	var use struct {
		Shared, Join []string
		AppliesOn    string `json:"applies_on"`
	}
	if st := e.c.call("PUT", wt+"/shared", map[string]any{}, &er); st != 400 {
		t.Errorf("use without services = %d", st)
	}
	if st := e.c.call("PUT", wt+"/shared", map[string]any{"use": []string{"web"}}, &er); st != 422 {
		t.Errorf("use of a service that is not shared = %d %+v", st, er.Error)
	}
	if st := e.c.call("PUT", wt+"/shared", map[string]any{"use": []string{"db"}}, &use); st != 200 || len(use.Shared) != 1 || use.AppliesOn != "next_up" {
		t.Fatalf("use = %d %+v", st, use)
	}
	e.c.call("GET", p+"/shared", nil, &got)
	if len(got.Consumers) != 1 || got.Consumers[0].Services[0] != "db" || got.Consumers[0].Active || got.Consumers[0].Branch != "feature/x" {
		t.Errorf("consumers = %+v", got.Consumers)
	}
	// The instance's own settings show it.
	var settings struct{ Shared []string }
	e.c.call("GET", wt+"/settings", nil, &settings)
	if len(settings.Shared) != 1 {
		t.Errorf("settings = %+v", settings)
	}
	// Remove is refused while linked, and says who.
	if st := e.c.call("DELETE", p+"/shared/services/db", nil, &er); st != 409 || er.Error.Code != "shared_in_use" || len(er.Error.Affected) != 1 || er.Error.Confirm != nil {
		t.Errorf("remove linked = %d %+v", st, er.Error)
	}
	if st := e.c.call("DELETE", wt+"/shared/db", nil, &use); st != 200 || len(use.Shared) != 0 {
		t.Errorf("unuse = %d %+v", st, use)
	}
	if st := e.c.call("PUT", wt+"/shared", map[string]any{"use": []string{"db"}}, &use); st != 200 {
		t.Fatalf("use again = %d", st)
	}
	if st := e.c.call("DELETE", wt+"/shared", nil, &use); st != 200 || len(use.Shared) != 0 {
		t.Errorf("unuse all = %d %+v", st, use)
	}
	var removed struct {
		Removed     bool
		Shared      bool
		VolumesKept bool `json:"volumes_kept"`
	}
	if st := e.c.call("DELETE", p+"/shared/services/db", nil, &removed); st != 200 || !removed.Removed {
		t.Errorf("remove = %d %+v", st, removed)
	}
	if st := e.c.call("GET", p+"/shared", nil, &er); st != 404 || er.Error.Code != "shared_not_configured" {
		t.Errorf("after remove = %d", st)
	}
}

func TestSharedUpDownAndPurgeProtections(t *testing.T) {
	e := newEnv(t)
	p := "/api/v1/projects/ubapp"
	wt := p + "/worktrees/" + e.wtID(e.linked)
	if st := e.c.call("POST", p+"/shared", map[string]any{"services": []string{"db"}}, nil); st != 201 {
		t.Fatalf("add = %d", st)
	}
	var op opState
	if st := e.c.call("POST", p+"/shared/up", nil, &op); st != 202 || op.Kind != "shared.up" {
		t.Fatalf("shared up = %d %+v", st, op)
	}
	if f := e.waitOp(op.ID); f.State != "succeeded" {
		t.Fatalf("shared up = %+v err %+v", f, f.Error)
	}
	e.c.call("PUT", wt+"/shared", map[string]any{"use": []string{"db"}}, nil)
	e.c.call("POST", wt+"/up", nil, &op)
	if f := e.waitOp(op.ID); f.State != "succeeded" {
		t.Fatalf("up = %+v", f)
	}
	var st sharedStatus
	e.c.call("GET", p+"/shared", nil, &st)
	if len(st.Consumers) != 1 || !st.Consumers[0].Active || st.Services[0].State != "running" {
		t.Fatalf("status = %+v", st)
	}

	// Purge with a consumer linked is refused, whatever the text says.
	var er apiErr
	for _, body := range []map[string]any{{}, {"confirm": sharedName}} {
		if code := e.c.call("POST", p+"/shared/purge", body, &er); code != 409 || er.Error.Code != "shared_in_use" || len(er.Error.Affected) != 1 {
			t.Errorf("purge with consumers %v = %d %+v", body, code, er.Error)
		}
	}
	// Down with a running consumer: level 2, nothing starts, consumers listed.
	var before []opState
	e.c.call("GET", "/api/v1/operations", nil, &before)
	if code := e.c.call("POST", p+"/shared/down", nil, &er); code != 409 || er.Error.Code != "confirmation_required" || er.Error.Level != 2 ||
		er.Error.Confirm == nil || er.Error.Confirm.Expected != sharedName || len(er.Error.Affected) != 1 || er.Error.Affected[0].Kind != "instance" {
		t.Fatalf("down = %d %+v", code, er.Error)
	}
	if code := e.c.call("POST", p+"/shared/down", map[string]any{"confirm": "wrong"}, &er); code != 409 || er.Error.Code != "confirmation_required" {
		t.Errorf("down wrong text = %d %+v", code, er.Error)
	}
	var after []opState
	e.c.call("GET", "/api/v1/operations", nil, &after)
	if len(after) != len(before) {
		t.Errorf("a refused down started an operation: %d -> %d", len(before), len(after))
	}
	if n := len(e.fc.containers[sharedName]); n == 0 {
		t.Errorf("a refused down stopped the shared services")
	}
	if code := e.c.call("POST", p+"/shared/down", map[string]any{"confirm": sharedName}, &op); code != 202 || op.Kind != "shared.down" {
		t.Fatalf("down confirmed = %d %+v", code, op)
	}
	if f := e.waitOp(op.ID); f.State != "succeeded" {
		t.Fatalf("down = %+v", f)
	}
	if n := len(e.fc.containers[sharedName]); n != 0 {
		t.Errorf("shared containers after down: %d", n)
	}

	// Without a running consumer down needs no text.
	e.c.call("POST", wt+"/down", nil, &op)
	e.waitOp(op.ID)
	e.c.call("POST", p+"/shared/up", nil, &op)
	e.waitOp(op.ID)
	if code := e.c.call("POST", p+"/shared/down", nil, &op); code != 202 {
		t.Errorf("down without active consumers = %d", code)
	}
	e.waitOp(op.ID)

	// Remove with containers is refused with its own code; the instance is
	// still linked so the linked check fires first.
	e.fc.set(sharedName, running("db", sharedName))
	e.c.call("DELETE", wt+"/shared", nil, nil)
	if code := e.c.call("DELETE", p+"/shared/services/db", nil, &er); code != 409 || er.Error.Code != "shared_running" {
		t.Errorf("remove with containers = %d %+v", code, er.Error)
	}
	// Purge: stopped is required, then the text of the engine.
	e.fc.volumes = map[string][]compose.DockerVolume{sharedName: {{Name: sharedName + "_data", Labels: map[string]string{compose.LabelVolume: "data"}}}}
	if code := e.c.call("POST", p+"/shared/purge", map[string]any{"confirm": sharedName}, &er); code != 409 || er.Error.Code != "instance_has_containers" {
		t.Errorf("purge with containers = %d %+v", code, er.Error)
	}
	e.fc.set(sharedName)
	if code := e.c.call("POST", p+"/shared/purge", map[string]any{}, &er); code != 409 || er.Error.Code != "confirmation_required" || er.Error.Level != 3 ||
		er.Error.Confirm.Expected != sharedName || len(er.Error.Affected) != 1 {
		t.Errorf("purge needs the text = %d %+v", code, er.Error)
	}
	var done struct {
		Compose string `json:"compose_project"`
		Deleted []struct{ Name string }
	}
	if code := e.c.call("POST", p+"/shared/purge", map[string]any{"confirm": sharedName}, &done); code != 200 || len(done.Deleted) != 1 || done.Compose != sharedName {
		t.Errorf("purge = %d %+v", code, done)
	}
}

func TestSharedOperationsAreSerialised(t *testing.T) {
	e := newEnv(t)
	p := "/api/v1/projects/ubapp"
	e.c.call("POST", p+"/shared", map[string]any{"services": []string{"db"}}, nil)
	release := make(chan struct{})
	e.fc.upHook = func(ctx context.Context, _ compose.Target, _, _ io.Writer) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}
	var op opState
	e.c.call("POST", p+"/shared/up", nil, &op)
	var er apiErr
	if code := e.c.call("POST", p+"/shared/up", nil, &er); code != 409 || er.Error.Code != "busy" || er.Error.Operation == nil || er.Error.Operation.ID != op.ID {
		t.Errorf("second up = %d %+v", code, er.Error)
	}
	if code := e.c.call("DELETE", p+"/shared/services/db", nil, &er); code != 409 || er.Error.Code != "busy" {
		t.Errorf("remove while busy = %d %+v", code, er.Error)
	}
	close(release)
	e.waitOp(op.ID)
}

// --- reconcile ---

type reconcileOut struct {
	Findings []struct {
		ID, Kind, Project, Subject, Detail, Fix string
		Forgettable                             bool
	}
	DockerError string `json:"docker_error"`
	Forgotten   int
	Applied     []string
	Skipped     []string
}

// staleWorktree gives the linked worktree a Theo record (a variable) and
// then deletes the worktree behind Theo's back.
func staleWorktree(t *testing.T, e *env) {
	t.Helper()
	wt := "/api/v1/projects/ubapp/worktrees/" + e.wtID(e.linked)
	if st := e.c.call("PUT", wt+"/env/UB_X", map[string]any{"value": "1"}, nil); st != 200 {
		t.Fatalf("env = %d", st)
	}
	if out, err := exec.Command("git", "-C", e.main, "worktree", "remove", "--force", e.linked).CombinedOutput(); err != nil {
		t.Fatalf("worktree remove: %v %s", err, out)
	}
}

func TestReconcileReportAndApply(t *testing.T) {
	e := newEnv(t)
	var rep reconcileOut
	var raw map[string]json.RawMessage
	e.c.call("GET", "/api/v1/reconcile", nil, &raw)
	if string(raw["findings"]) != "[]" {
		t.Fatalf("clean report = %s", raw["findings"])
	}
	staleWorktree(t, e)
	if st := e.c.call("GET", "/api/v1/reconcile?project=ubapp", nil, &rep); st != 200 {
		t.Fatalf("reconcile = %d", st)
	}
	var stale string
	for _, f := range rep.Findings {
		if f.Kind == engine.FindStaleSettings {
			stale = f.ID
			if !f.Forgettable || f.Fix == "" || f.Project != "ubapp" || !strings.HasPrefix(f.ID, "fd_") {
				t.Errorf("finding = %+v", f)
			}
		}
	}
	if stale == "" {
		t.Fatalf("no stale settings finding: %+v", rep.Findings)
	}
	var er apiErr
	if st := e.c.call("GET", "/api/v1/reconcile?project=nope", nil, &er); st != 404 || er.Error.Code != "project_not_found" {
		t.Errorf("unknown project = %d %+v", st, er.Error)
	}
	// GET changes nothing.
	e.c.call("GET", "/api/v1/reconcile", nil, &rep)
	if len(rep.Findings) == 0 || rep.Forgotten != 0 {
		t.Fatalf("GET changed something: %+v", rep)
	}
	if st := e.c.call("POST", "/api/v1/reconcile/apply", map[string]any{}, &er); st != 400 {
		t.Errorf("apply without ids = %d", st)
	}
	if st := e.c.call("POST", "/api/v1/reconcile/apply", map[string]any{"ids": []string{"x"}, "all": true}, &er); st != 400 {
		t.Errorf("apply ids and all = %d", st)
	}
	// An id the report does not have is skipped, not an error.
	if st := e.c.call("POST", "/api/v1/reconcile/apply", map[string]any{"ids": []string{"fd_nope"}}, &rep); st != 200 || rep.Forgotten != 0 || len(rep.Skipped) != 1 {
		t.Errorf("unknown id = %d %+v", st, rep)
	}
	if st := e.c.call("POST", "/api/v1/reconcile/apply", map[string]any{"ids": []string{stale}}, &rep); st != 200 || rep.Forgotten != 1 || len(rep.Applied) != 1 || rep.Applied[0] != stale {
		t.Fatalf("apply = %d %+v", st, rep)
	}
	e.c.call("GET", "/api/v1/reconcile", nil, &rep)
	for _, f := range rep.Findings {
		if f.Kind == engine.FindStaleSettings {
			t.Errorf("still reported: %+v", f)
		}
	}
	// Nothing of the user's was touched: the repository is intact.
	if _, err := os.Stat(e.main); err != nil {
		t.Errorf("main worktree: %v", err)
	}
}

func TestReconcileNeverForgetsWhatIsNotTheosOwn(t *testing.T) {
	e := newEnv(t)
	// A project whose directory is gone is a finding, but not forgettable.
	main2, _ := testutil.NewRepo(t, map[string]string{"compose.yaml": "services:\n  web:\n    image: scratch-ub\n"})
	if _, err := e.eng.AddProject(context.Background(), main2, "ubgone", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(main2); err != nil {
		t.Fatal(err)
	}
	var rep reconcileOut
	e.c.call("GET", "/api/v1/reconcile", nil, &rep)
	var id string
	for _, f := range rep.Findings {
		if f.Kind == engine.FindProjectMissing {
			id = f.ID
			if f.Forgettable {
				t.Errorf("a missing project is forgettable: %+v", f)
			}
		}
	}
	if id == "" {
		t.Fatalf("no finding for the missing project: %+v", rep.Findings)
	}
	if st := e.c.call("POST", "/api/v1/reconcile/apply", map[string]any{"ids": []string{id}}, &rep); st != 200 || rep.Forgotten != 0 || len(rep.Skipped) != 1 {
		t.Errorf("apply = %d %+v", st, rep)
	}
	if st := e.c.call("POST", "/api/v1/reconcile/apply", map[string]any{"all": true}, &rep); st != 200 || rep.Forgotten != 0 {
		t.Errorf("apply all = %d %+v", st, rep)
	}
	if _, err := e.eng.Project("ubgone"); err != nil {
		t.Errorf("the project record was forgotten: %v", err)
	}
}

func TestReconcileWithDockerDown(t *testing.T) {
	e := newEnv(t)
	e.fc.psErr = errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?")
	var rep reconcileOut
	if st := e.c.call("GET", "/api/v1/reconcile", nil, &rep); st != 200 || !strings.Contains(rep.DockerError, "Cannot connect") {
		t.Fatalf("reconcile = %d %+v", st, rep)
	}
	var h api.Health
	if st := e.c.call("GET", "/api/v1/health", nil, &h); st != 200 || h.Status != "docker_unavailable" || h.DockerError == "" {
		t.Errorf("health = %d %+v", st, h)
	}
}

func TestHealthCountsFindings(t *testing.T) {
	e := newEnv(t)
	staleWorktree(t, e)
	var h api.Health
	e.c.call("GET", "/api/v1/health", nil, &h)
	if h.Status != "attention" || h.Findings == 0 || h.Forgettable == 0 {
		t.Fatalf("health = %+v", h)
	}
	// Forgetting refreshes the indicator at once (the answer is cached 5 s).
	e.c.call("POST", "/api/v1/reconcile/apply", map[string]any{"all": true}, nil)
	e.c.call("GET", "/api/v1/health", nil, &h)
	if h.Forgettable != 0 {
		t.Errorf("health after apply = %+v", h)
	}
}

func TestHealthIsOkWhenNothingDiffers(t *testing.T) {
	e := newEnv(t)
	var h api.Health
	if st := e.c.call("GET", "/api/v1/health", nil, &h); st != 200 || h.Status != "ok" || h.Findings != 0 {
		t.Errorf("health = %d %+v", st, h)
	}
}

// --- diagnostics ---

func TestDiagnosticsAndMetaVersions(t *testing.T) {
	e := newEnv(t)
	var meta api.Meta
	e.c.call("GET", "/api/v1/meta", nil, &meta)
	if meta.Compose.MinVersion != compose.MinVersion.String() || meta.Git.MinVersion != engine.MinGitVersion.String() {
		t.Errorf("min versions = %+v %+v", meta.Compose, meta.Git)
	}
	var d api.Diagnostics
	if st := e.c.call("GET", "/api/v1/diagnostics", nil, &d); st != 200 {
		t.Fatalf("diagnostics = %d", st)
	}
	if d.Meta.TheoVersion == "" || d.Meta.DataDir != e.dataDir || d.Projects != 1 || len(d.Doctor.Checks) == 0 {
		t.Errorf("diagnostics = %+v", d)
	}
	for _, want := range []string{"Theo " + engine.Version, "Data directory: " + e.dataDir, "Docker Compose", "Checks:"} {
		if !strings.Contains(d.Report, want) {
			t.Errorf("report lacks %q:\n%s", want, d.Report)
		}
	}
	// No secrets: a variable set on an instance never reaches the report.
	e.c.call("PUT", "/api/v1/projects/ubapp/worktrees/"+e.wtID(e.linked)+"/env/UB_TOKEN", map[string]any{"value": "hunter2-ub", "secret": true}, nil)
	_, body, _ := e.c.raw("GET", "/api/v1/diagnostics", nil, nil)
	if strings.Contains(string(body), "hunter2-ub") {
		t.Error("the diagnostics contain a secret")
	}
}

// --- history ---

func TestHistoryPagination(t *testing.T) {
	e := newEnv(t)
	wt := "/api/v1/projects/ubapp/worktrees/" + e.wtID(e.linked)
	var op opState
	for i := 0; i < 3; i++ { // six events: up, down, up, down...
		e.c.call("POST", wt+"/up", nil, &op)
		e.waitOp(op.ID)
		e.c.call("POST", wt+"/down", nil, &op)
		e.waitOp(op.ID)
	}
	type ev struct{ Time, Operation string }
	page := func(q string) ([]ev, http.Header) {
		var out []ev
		st, body, hdr := e.c.raw("GET", wt+"/history"+q, nil, nil)
		if st != 200 {
			t.Fatalf("history%s = %d %s", q, st, body)
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatal(err)
		}
		return out, hdr
	}
	all, hdr := page("")
	if len(all) != 6 || hdr.Get("Theo-History-Total") != "6" || hdr.Get("Theo-History-Next") != "" {
		t.Fatalf("all = %d %v", len(all), hdr)
	}
	p1, h1 := page("?limit=4")
	if len(p1) != 4 || h1.Get("Theo-History-Next") != "2" || h1.Get("Theo-History-Total") != "6" {
		t.Fatalf("page 1 = %d %v", len(p1), h1)
	}
	if p1[3] != all[5] || p1[0] != all[2] {
		t.Errorf("page 1 is not the newest four, oldest first")
	}
	p2, h2 := page("?limit=4&before=" + h1.Get("Theo-History-Next"))
	if len(p2) != 2 || h2.Get("Theo-History-Next") != "" || p2[0] != all[0] || p2[1] != all[1] {
		t.Errorf("page 2 = %+v %v", p2, h2)
	}
	if p, _ := page("?limit=2&before=100"); len(p) != 2 || p[1] != all[5] {
		t.Errorf("a cursor past the end = %+v", p)
	}
	// The project-wide history is the same data.
	var proj []ev
	st, body, ph := e.c.raw("GET", "/api/v1/projects/ubapp/history?limit=3", nil, nil)
	json.Unmarshal(body, &proj)
	if st != 200 || len(proj) != 3 || ph.Get("Theo-History-Total") != "6" || ph.Get("Theo-History-Next") != strconv.Itoa(3) {
		t.Errorf("project history = %d %+v %v", st, proj, ph)
	}
	var er apiErr
	for _, q := range []string{"?limit=-1", "?limit=x", "?before=-3", "?before=x"} {
		if st := e.c.call("GET", wt+"/history"+q, nil, &er); st != 400 {
			t.Errorf("history%s = %d", q, st)
		}
	}
	if st := e.c.call("GET", "/api/v1/projects/nope/history", nil, &er); st != 404 {
		t.Errorf("unknown project = %d", st)
	}
}

var _ = fmt.Sprint
