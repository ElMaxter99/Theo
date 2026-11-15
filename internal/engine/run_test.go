package engine

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"strconv"
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

// TestHelperBuild is not a test: the test binary runs it as the build command
// of a static run section when HELPER_BUILD is set. HELPER_BUILD=ok leaves
// dist/ with an index.html (its <base href> is HELPER_BASE), "nodir" leaves
// nothing, "noindex" leaves an empty dist/ and "fail" prints and fails.
func TestHelperBuild(t *testing.T) {
	switch os.Getenv("HELPER_BUILD") {
	case "":
		return
	case "ok":
		os.MkdirAll("dist", 0o755)
		os.WriteFile("dist/index.html", []byte(`<html><base href="`+os.Getenv("HELPER_BASE")+`">home</html>`), 0o644)
		os.WriteFile("dist/app.js", []byte("js"), 0o644)
	case "noindex":
		os.MkdirAll("dist", 0o755)
	case "fail":
		os.Stderr.WriteString("Error: Cannot find module 'gulp-sass'\n")
		os.Exit(1)
	}
}

func staticConfig(mode, base string) string {
	cmd := `\"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `\" -test.run=TestHelperBuild`
	return `{"run":{"kind":"static","build":["` + cmd + `"],"dir":"dist","base":"/portal","spa":true,` +
		`"env":{"HELPER_BUILD":"` + mode + `","HELPER_BASE":"` + base + `"}}}`
}

func TestStartRunStaticServesTheBuildUnderItsBase(t *testing.T) {
	e, dir := runProject(t, staticConfig("ok", "/portal/"))
	plan, err := e.PlanRun("app", dir)
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	r, err := e.StartRun(context.Background(), plan, true, &out, &errb)
	if err != nil {
		t.Fatalf("%v\n%s", err, errb.String())
	}
	defer r.Stop()
	if !strings.HasSuffix(r.URL(), "/portal/") {
		t.Fatalf("url %q", r.URL())
	}
	get := func(p string) int {
		c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := c.Get("http://127.0.0.1:" + itoa(r.ListenPort()) + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if get("/portal/app.js") != 200 || get("/") != 302 || get("/otra-cosa") != 404 || get("/portal/clientes/42") != 200 {
		t.Fatal("the static server does not answer as designed")
	}
	log := errb.String()
	if !strings.Contains(log, "theo: run: build: ") || !strings.Contains(log, "theo: run: serve: dist/ under /portal\n") {
		t.Fatalf("log:\n%s", log)
	}
	if strings.Contains(log, "warning") {
		t.Fatalf("a relative base href must not warn:\n%s", log)
	}
}

func TestStartRunStaticWarnsAboutAnAbsoluteBaseHref(t *testing.T) {
	e, dir := runProject(t, staticConfig("ok", "https://pre.example.com/portal/"))
	plan, _ := e.PlanRun("app", dir)
	var errb bytes.Buffer
	r, err := e.StartRun(context.Background(), plan, true, &bytes.Buffer{}, &errb)
	if err != nil {
		t.Fatal(err)
	}
	r.Stop()
	if !strings.Contains(errb.String(), `theo: warning: dist/index.html has <base href="https://pre.example.com/portal/">:`) {
		t.Fatalf("log:\n%s", errb.String())
	}
}

func TestStartRunStaticErrors(t *testing.T) {
	cases := map[string]string{
		"fail":    "last lines it printed:\n  Error: Cannot find module 'gulp-sass'",
		"nodir":   "the build did not leave the folder dist/ (run.dir) in ",
		"noindex": "dist/ has no index.html: the app cannot start under /portal/",
	}
	for mode, want := range cases {
		e, dir := runProject(t, staticConfig(mode, ""))
		plan, _ := e.PlanRun("app", dir)
		_, err := e.StartRun(context.Background(), plan, true, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", mode, err, want)
		}
	}
}

func TestStartRunStaticNeedsApproval(t *testing.T) {
	e, dir := runProject(t, staticConfig("ok", ""))
	plan, _ := e.PlanRun("app", dir)
	if _, err := e.StartRun(context.Background(), plan, false, &bytes.Buffer{}, &bytes.Buffer{}); err != ErrRunNotApproved {
		t.Fatalf("%v", err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
