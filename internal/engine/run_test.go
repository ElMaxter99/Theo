package engine

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// TestHelperServe is not a test: the test binary runs it as the application a
// run section starts (an HTTP server on $PORT) when HELPER_SERVE is set.
func TestHelperServe(t *testing.T) {
	if os.Getenv("HELPER_SERVE") != "1" {
		return
	}
	http.ListenAndServe("127.0.0.1:"+os.Getenv("PORT"), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello " + os.Getenv("GREETING")))
	}))
}

func runProject(t *testing.T, config string) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteFile(t, dir, ".theo.json", config)
	e, _, g, _ := newEngine()
	g.worktrees = []git.Worktree{{Path: dir, Branch: "main"}}
	if _, err := e.AddProject(context.Background(), dir, "app", ""); err != nil {
		t.Fatal(err)
	}
	return e, dir
}

func serveConfig() string {
	cmd := `\"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `\" -test.run=TestHelperServe`
	return `{"run":{"start":"` + cmd + `","env":{"HELPER_SERVE":"1","GREETING":"on ${PORT}"},"ready":{"timeout_s":30}}}`
}

func TestStartScriptNeedsApprovalThenRemembersIt(t *testing.T) {
	e, dir := runProject(t, serveConfig())
	ctx := context.Background()
	plan, err := e.PlanRun("app", dir)
	if err != nil || plan.Spec == nil || plan.Trusted {
		t.Fatalf("a new section must not be trusted: %+v %v", plan, err)
	}
	var out, errb bytes.Buffer
	if _, err := e.StartScript(ctx, plan, false, &out, &errb); err != ErrRunNotApproved {
		t.Fatalf("without approval: %v", err)
	}

	p, err := e.StartScript(ctx, plan, true, &out, &errb)
	if err != nil {
		t.Fatalf("%v\n%s", err, errb.String())
	}
	resp, err := http.Get(p.URL())
	if err != nil {
		t.Fatal(err)
	}
	body := new(bytes.Buffer)
	body.ReadFrom(resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(body.String(), "hello on ") || strings.Contains(body.String(), "${PORT}") {
		t.Errorf("${PORT} must be replaced in env values: %q", body.String())
	}
	p.Stop()

	// The approval was remembered for exactly these commands...
	plan, _ = e.PlanRun("app", dir)
	if !plan.Trusted {
		t.Error("the approval must be remembered")
	}
	// ...and a changed section asks again.
	testutil.WriteFile(t, dir, ".theo.json", strings.Replace(serveConfig(), "GREETING", "OTHER", 1))
	plan, _ = e.PlanRun("app", dir)
	if plan.Trusted {
		t.Error("a changed environment must ask again")
	}
}

func TestStartScriptRefusesWhatIsNotAScript(t *testing.T) {
	e, dir := runProject(t, `{"run":{"kind":"compose"}}`)
	plan, _ := e.PlanRun("app", dir)
	if _, err := e.StartScript(context.Background(), plan, true, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "theo up") {
		t.Errorf("compose: %v", err)
	}
	e, dir = runProject(t, `{"copy":["x"]}`)
	plan, _ = e.PlanRun("app", dir)
	if plan.Spec != nil {
		t.Error("no run section, no spec")
	}
	if _, err := e.StartScript(context.Background(), plan, true, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Error("no run section must be an error")
	}
}

func TestStartScriptInstallFailureStops(t *testing.T) {
	e, dir := runProject(t, `{"run":{"install":["exit 3"],"start":"echo never"}}`)
	plan, _ := e.PlanRun("app", dir)
	var errb bytes.Buffer
	if _, err := e.StartScript(context.Background(), plan, true, &bytes.Buffer{}, &errb); err == nil || !strings.Contains(err.Error(), "install command") {
		t.Errorf("a failing install must stop before start: %v", err)
	}
	if strings.Contains(errb.String(), "start:") {
		t.Error("start must not run after a failed install")
	}
}
