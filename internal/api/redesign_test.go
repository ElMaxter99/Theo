package api_test

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// controlCompose is fakeCompose with per-service control (stop, start,
// restart), recording each call.
type controlCompose struct {
	*fakeCompose
	mu    sync.Mutex
	calls []string
}

func (c *controlCompose) Control(_ context.Context, t compose.Target, verb string, services []string, _, _ io.Writer) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, verb+" "+t.Name+" "+strings.Join(services, ","))
	if verb == "stop" && len(services) == 0 {
		c.fakeCompose.set(t.Name)
	}
	return nil
}

// Config and ConfigFor give the db service the profile "data".
func (c *controlCompose) Config(ctx context.Context, dir string, files []string) (*compose.Project, error) {
	return withDataProfile(c.fakeCompose.Config(ctx, dir, files))
}

func (c *controlCompose) ConfigFor(ctx context.Context, t compose.Target) (*compose.Project, error) {
	return withDataProfile(c.fakeCompose.ConfigFor(ctx, t))
}

func withDataProfile(p *compose.Project, err error) (*compose.Project, error) {
	if err == nil {
		db := p.Services["db"]
		db.Profiles = []string{"data"}
		p.Services["db"] = db
	}
	return p, err
}

func (c *controlCompose) seen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

func newControlEnv(t *testing.T) (*env, *controlCompose) {
	t.Helper()
	main, linked := testutil.NewRepo(t, map[string]string{"compose.yaml": "services:\n  web:\n    image: scratch-ub\n  db:\n    image: scratch-db\n    profiles: [data]\n"})
	dir := testutil.TempDir(t)
	fc := &fakeCompose{}
	cc := &controlCompose{fakeCompose: fc}
	eng := engine.New(registry.Open(dir), git.Git{}, cc).WithDataDir(dir)
	if _, err := eng.AddProject(context.Background(), main, "ubapp", ""); err != nil {
		t.Fatal(err)
	}
	return startEnv(t, eng, fc, main, linked, dir), cc
}

func TestServicesOnLifecycleOperations(t *testing.T) {
	e, cc := newControlEnv(t)
	id := e.wtID(e.main)
	base := "/api/v1/projects/ubapp/worktrees/" + id
	var op struct{ ID string }
	for _, kind := range []string{"stop", "start", "restart"} {
		if st := e.c.call("POST", base+"/"+kind, map[string]any{"services": []string{"web"}}, &op); st != 202 {
			t.Fatalf("%s = %d", kind, st)
		}
		if f := e.waitOp(op.ID); f.State != "succeeded" || f.Kind != kind {
			t.Fatalf("%s op = %+v", kind, f)
		}
	}
	calls := strings.Join(cc.seen(), "\n")
	for _, want := range []string{"stop ", "start ", "restart "} {
		if !strings.Contains(calls, want) || !strings.Contains(calls, " web") {
			t.Errorf("calls = %q, want %q with web", calls, want)
		}
	}
	if st := e.c.call("POST", base+"/recreate", map[string]any{"services": []string{"web"}, "build": true}, &op); st != 202 {
		t.Fatalf("recreate = %d", st)
	}
	if f := e.waitOp(op.ID); f.State != "succeeded" {
		t.Fatalf("recreate op = %+v", f)
	}

	var ae apiErr
	if st := e.c.call("POST", base+"/down", map[string]any{"services": []string{"web"}}, &ae); st != 400 {
		t.Errorf("down with services = %d %+v", st, ae)
	}
	if st := e.c.call("POST", base+"/stop", map[string]any{"build": true}, &ae); st != 400 {
		t.Errorf("stop with build = %d", st)
	}
	if st := e.c.call("POST", base+"/start", map[string]any{"services": []string{"--all"}}, &ae); st != 400 {
		t.Errorf("start with a flag as service = %d", st)
	}
}

func TestStatusAndGlobalHistory(t *testing.T) {
	e := newEnv(t)
	var report struct {
		Projects []struct {
			Name      string `json:"name"`
			Worktrees []struct {
				ID string `json:"id"`
			} `json:"worktrees"`
		} `json:"projects"`
	}
	if st := e.c.call("GET", "/api/v1/status", nil, &report); st != 200 || len(report.Projects) != 1 || report.Projects[0].Name != "ubapp" || len(report.Projects[0].Worktrees) != 2 {
		t.Fatalf("status = %d %+v", st, report)
	}
	if report.Projects[0].Worktrees[0].ID != e.wtID(e.main) {
		t.Errorf("status ids = %+v", report.Projects[0].Worktrees)
	}
	id := e.wtID(e.main)
	var op struct{ ID string }
	e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+id+"/up", nil, &op)
	e.waitOp(op.ID)
	st, body, hdr := e.c.raw("GET", "/api/v1/history?limit=10", nil, nil)
	if st != 200 || !strings.Contains(string(body), "ubapp") || hdr.Get("Theo-History-Total") == "" {
		t.Errorf("history = %d %s %v", st, body, hdr)
	}
	if st, _, _ := e.c.raw("GET", "/api/v1/history?limit=-1", nil, nil); st != 400 {
		t.Errorf("history with a bad limit = %d", st)
	}
	st, body, hdr = e.c.raw("GET", "/api/v1/projects/ubapp/history?limit=10", nil, nil)
	if st != 200 || !strings.Contains(string(body), `"up"`) || hdr.Get("Theo-History-Total") == "" {
		t.Errorf("project history = %d %s %v", st, body, hdr)
	}
	if st, _, _ := e.c.raw("GET", "/api/v1/projects/nope/history", nil, nil); st != 404 {
		t.Errorf("history of an unknown project = %d", st)
	}
}

func TestStopAll(t *testing.T) {
	e, cc := newControlEnv(t)
	id := e.wtID(e.main)
	var op struct{ ID string }
	e.c.call("POST", "/api/v1/projects/ubapp/worktrees/"+id+"/up", nil, &op)
	e.waitOp(op.ID)
	if st := e.c.call("POST", "/api/v1/stop-all", map[string]string{"project": "ubapp"}, &op); st != 202 {
		t.Fatalf("stop-all = %d", st)
	}
	if f := e.waitOp(op.ID); f.State != "succeeded" || f.Kind != "stop-all" {
		t.Fatalf("stop-all op = %+v", f)
	}
	if calls := cc.seen(); len(calls) != 1 || !strings.HasPrefix(calls[0], "stop ") {
		t.Errorf("calls = %v", calls)
	}
	var ae apiErr
	if st := e.c.call("POST", "/api/v1/stop-all", map[string]string{"project": "nope"}, &ae); st != 404 {
		t.Errorf("stop-all of an unknown project = %d", st)
	}
}

func TestMCPInstallDocument(t *testing.T) {
	e := newEnv(t)
	var doc struct {
		Args    []string `json:"args"`
		Snippet string   `json:"snippet"`
		Command string   `json:"claude_code_command"`
	}
	if st := e.c.call("GET", "/api/v1/mcp/install?owner=agent-1&read_only=1", nil, &doc); st != 200 {
		t.Fatalf("mcp install = %d", st)
	}
	if strings.Join(doc.Args, " ") != "mcp --read-only --owner agent-1" || !strings.Contains(doc.Snippet, "read-only") || !strings.Contains(doc.Command, "claude mcp add theo --") {
		t.Errorf("doc = %+v", doc)
	}
	if st, _, _ := e.c.raw("GET", "/api/v1/mcp/install?owner=bad%01owner", nil, nil); st != 400 {
		t.Errorf("bad owner = %d", st)
	}
	if st, _, _ := e.c.raw("GET", "/api/v1/mcp/install?scope=global", nil, nil); st != 400 {
		t.Errorf("bad scope = %d", st)
	}
}

func TestPatchProjectComposeFilesAndCopyOnAdd(t *testing.T) {
	e := newEnv(t)
	var p struct {
		ComposeFiles []string `json:"compose_files"`
		CopyOnAdd    []string `json:"copy_on_add"`
	}
	if st := e.c.call("PATCH", "/api/v1/projects/ubapp", map[string]any{"compose_files": []string{"compose.yaml"}, "copy_on_add": []string{".env"}}, &p); st != 200 {
		t.Fatalf("patch = %d %+v", st, p)
	}
	if len(p.ComposeFiles) != 1 || len(p.CopyOnAdd) != 1 || p.CopyOnAdd[0] != ".env" {
		t.Errorf("project = %+v", p)
	}
	if st := e.c.call("PATCH", "/api/v1/projects/ubapp", map[string]any{"compose_files": []string{"missing.yaml"}}, nil); st < 400 {
		t.Errorf("a compose file that does not exist = %d", st)
	}
	if st := e.c.call("PATCH", "/api/v1/projects/ubapp", map[string]any{"copy_on_add": "x"}, nil); st != 400 {
		t.Errorf("copy_on_add as a string = %d", st)
	}
	var reset struct {
		ComposeFiles []string `json:"compose_files"`
		CopyOnAdd    []string `json:"copy_on_add"`
	}
	if st := e.c.call("PATCH", "/api/v1/projects/ubapp", map[string]any{"compose_files": nil, "copy_on_add": nil}, &reset); st != 200 || len(reset.ComposeFiles) != 0 || len(reset.CopyOnAdd) != 0 {
		t.Errorf("reset = %d %+v", st, reset)
	}
}

func TestSettingsOwnerAndProfiles(t *testing.T) {
	e, _ := newControlEnv(t)
	id := e.wtID(e.linked)
	path := "/api/v1/projects/ubapp/worktrees/" + id + "/settings"
	var s struct {
		Owner         string   `json:"owner"`
		Profiles      []string `json:"profiles"`
		KnownProfiles []string `json:"known_profiles"`
	}
	if st := e.c.call("PATCH", path, map[string]any{"owner": "agent-1", "profiles": []string{"data"}}, &s); st != 200 {
		t.Fatalf("patch = %d", st)
	}
	if s.Owner != "agent-1" || strings.Join(s.Profiles, ",") != "data" || strings.Join(s.KnownProfiles, ",") != "data" {
		t.Errorf("settings = %+v", s)
	}
	if st := e.c.call("GET", path, nil, &s); st != 200 || s.Owner != "agent-1" {
		t.Errorf("get = %d %+v", st, s)
	}
	if st := e.c.call("PATCH", path, map[string]any{"profiles": []string{"nope"}}, nil); st != 400 {
		t.Errorf("unknown profile = %d", st)
	}
	if st := e.c.call("PATCH", path, map[string]any{"owner": "", "profiles": []string{}}, &s); st != 200 || s.Owner != "" || len(s.Profiles) != 0 {
		t.Errorf("clear = %d %+v", st, s)
	}
}

func TestCreateWorktreeWithOwner(t *testing.T) {
	e := newEnv(t)
	var out struct {
		ID    string `json:"id"`
		Owner string `json:"owner"`
	}
	if st := e.c.call("POST", "/api/v1/projects/ubapp/worktrees", map[string]any{"branch": "fix-1", "new": true, "owner": "agent-2"}, &out); st != 201 {
		t.Fatalf("create = %d", st)
	}
	var wt struct {
		Owner string `json:"owner"`
	}
	e.c.call("GET", "/api/v1/projects/ubapp/worktrees/"+out.ID, nil, &wt)
	if wt.Owner != "agent-2" {
		t.Errorf("owner = %q", wt.Owner)
	}
	if st := e.c.call("POST", "/api/v1/projects/ubapp/worktrees", map[string]any{"branch": "fix-2", "new": true, "owner": "bad\u0001owner"}, nil); st != 400 {
		t.Errorf("bad owner = %d", st)
	}
}

func TestPruneNeedsTheProjectName(t *testing.T) {
	e := newEnv(t)
	var report struct {
		Project string `json:"project"`
		Applied bool   `json:"applied"`
		Merged  []any  `json:"merged"`
	}
	if st := e.c.call("GET", "/api/v1/projects/ubapp/prune", nil, &report); st != 200 || report.Applied || report.Merged == nil {
		t.Fatalf("prune preview = %d %+v", st, report)
	}
	var ae apiErr
	if st := e.c.call("POST", "/api/v1/projects/ubapp/prune", map[string]string{"confirm": "wrong"}, &ae); st != 409 || ae.Error.Code != "confirmation_required" {
		t.Errorf("prune without the name = %d %+v", st, ae)
	}
	if st := e.c.call("POST", "/api/v1/projects/ubapp/prune", map[string]string{"confirm": "ubapp"}, &report); st != 200 || !report.Applied {
		t.Errorf("prune = %d %+v", st, report)
	}
}

func TestRenameProject(t *testing.T) {
	e := newEnv(t)
	var res struct{ Old, New string }
	if st := e.c.call("POST", "/api/v1/projects/ubapp/rename", map[string]any{"name": "shop", "yes": true}, &res); st != 200 || res.New != "shop" {
		t.Fatalf("rename = %d %+v", st, res)
	}
	if st := e.c.call("GET", "/api/v1/projects/shop/worktrees", nil, nil); st != 200 {
		t.Errorf("renamed project = %d", st)
	}
	if st := e.c.call("POST", "/api/v1/projects/shop/rename", map[string]any{"name": ""}, nil); st != 400 {
		t.Errorf("empty name = %d", st)
	}
}

func TestVolumeOperationsAreChecked(t *testing.T) {
	e := newEnv(t)
	id := e.wtID(e.main)
	other := e.wtID(e.linked)
	base := "/api/v1/projects/ubapp/worktrees/" + id + "/volumes"
	var list []any
	if st := e.c.call("GET", base+"/backups", nil, &list); st != 200 || len(list) != 0 {
		t.Errorf("backups = %d %v", st, list)
	}
	for _, body := range []map[string]any{{"backup": "../x.tar.gz", "confirm": "x"}, {"backup": "/etc/passwd"}, {}} {
		if st := e.c.call("POST", base+"/restore", body, nil); st != 400 {
			t.Errorf("restore %v = %d", body, st)
		}
	}
	var ae apiErr
	if st := e.c.call("POST", base+"/restore", map[string]any{"backup": "a.tar.gz", "confirm": "wrong"}, &ae); st != 409 || ae.Error.Code != "confirmation_required" {
		t.Errorf("restore without the name = %d %+v", st, ae)
	}
	if st := e.c.call("POST", base+"/clone", map[string]any{"from": id}, nil); st != 400 {
		t.Errorf("clone from itself = %d", st)
	}
	if st := e.c.call("POST", base+"/clone", map[string]any{"from": other, "confirm": "wrong"}, &ae); st != 409 {
		t.Errorf("clone without the name = %d", st)
	}
}
