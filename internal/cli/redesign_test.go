package cli

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

func TestInferProjectFromTheCurrentFolder(t *testing.T) {
	main, linked := testutil.NewRepo(t, nil)
	t.Setenv(registry.EnvHome, t.TempDir())
	if _, stderr, code := run("project", "add", main, "--name", "demo"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	t.Chdir(filepath.Join(linked))
	cases := []struct {
		in, want []string
	}{
		{[]string{"up"}, []string{"up", "demo", "-w", linked}},
		{[]string{"up", "--build"}, []string{"up", "demo", "--build", "-w", linked}},
		{[]string{"logs", ".", "web", "-f"}, []string{"logs", "demo", "web", "-f", "-w", linked}},
		{[]string{"env", "list"}, []string{"env", "list", "demo", "-w", linked}},
		{[]string{"ps"}, []string{"ps", "demo"}},
		{[]string{"up", ".", "-w", "main"}, []string{"up", "demo", "-w", "main"}},
		// An explicit project keeps meaning the main worktree.
		{[]string{"up", "demo"}, []string{"up", "demo"}},
		{[]string{"project", "list"}, []string{"project", "list"}},
	}
	for _, c := range cases {
		var stderr bytes.Buffer
		got := inferProject(c.in, &stderr)
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("%v -> %v, want %v", c.in, got, c.want)
		}
		if len(c.in) > 1 && c.in[1] == "demo" || c.in[0] == "project" {
			if stderr.Len() != 0 {
				t.Errorf("%v wrote %q", c.in, stderr.String())
			}
			continue
		}
		if !strings.Contains(stderr.String(), "theo: demo") || !strings.Contains(stderr.String(), "(from the current folder)") {
			t.Errorf("%v: stderr %q", c.in, stderr.String())
		}
	}
	t.Chdir(t.TempDir())
	var stderr bytes.Buffer
	if got := inferProject([]string{"up"}, &stderr); len(got) != 1 || stderr.Len() != 0 {
		t.Errorf("outside a project: %v %q", got, stderr.String())
	}
	if got := inferProject([]string{"up", "."}, &stderr); len(got) != 2 || !strings.Contains(stderr.String(), "not in a registered project") {
		t.Errorf("dot outside a project: %v %q", got, stderr.String())
	}
}

func TestSummary(t *testing.T) {
	inst := func(state string, running, total int) *engine.InstanceStatus {
		return &engine.InstanceStatus{InstanceState: state, Counts: engine.ServiceCounts{Running: running, Total: total}}
	}
	wt := func(branch, kind string, i *engine.InstanceStatus, warns ...engine.StatusWarning) engine.WorktreeStatus {
		return engine.WorktreeStatus{Path: "/code/shop/" + branch, Branch: branch, Kind: kind, Instance: i, Warnings: warns}
	}
	running := inst(engine.InstRunning, 3, 3)
	running.ProxyURLs = []string{"http://main.shop.localhost:7080"}
	report := &engine.StatusReport{Projects: []engine.ProjectStatus{{Name: "shop", Path: "/code/shop", Worktrees: []engine.WorktreeStatus{
		wt("feature-x", "linked", inst(engine.InstStopped, 0, 3)),
		wt("main", engine.KindMain, running),
		wt("fix-123", "linked", inst(engine.InstPartial, 2, 3), engine.StatusWarning{Kind: "port-conflict", Message: "port 6379 in use", Fix: "theo up shop -w fix-123 --auto-ports"}),
	}}}}
	var out bytes.Buffer
	if err := printSummary(&out, report, "/code/shop/fix-123/src", false); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"shop  /code/shop  (this folder: fix-123)", "▶ main", "running", "3/3  http://main.shop.localhost:7080",
		"◐ fix-123", "port 6379 in use", "→ theo up shop -w fix-123 --auto-ports", "■ feature-x", "1 warning: theo status"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "main") > strings.Index(got, "feature-x") {
		t.Errorf("running worktrees come first:\n%s", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("colors without a terminal:\n%s", got)
	}
	out.Reset()
	printSummary(&out, &engine.StatusReport{}, "", true)
	if !strings.Contains(out.String(), "theo project add .") {
		t.Errorf("empty summary = %q", out.String())
	}
}

func TestGlobalColorFlag(t *testing.T) {
	defer func(m string) { colorMode = m }(colorMode)
	rest, explain, err := globalFlags([]string{"--color", "always", "--explain", "up"})
	if err != nil || !explain || len(rest) != 1 || colorMode != "always" {
		t.Errorf("flags = %v %v %v %s", rest, explain, err, colorMode)
	}
	if _, _, err := globalFlags([]string{"--color=sometimes"}); err == nil {
		t.Error("a bad --color was accepted")
	}
	colorMode = "auto"
	t.Setenv("NO_COLOR", "1")
	if useColor(&bytes.Buffer{}) {
		t.Error("color with NO_COLOR")
	}
	if _, stderr, code := run("--color", "never", "bogus"); code != ExitUsage || !strings.Contains(stderr, "→ theo help") {
		t.Errorf("usage error = %d %q", code, stderr)
	}
}

func TestErrorsEndWithTheNextStep(t *testing.T) {
	var out bytes.Buffer
	printError(&out, errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock"))
	if !strings.Contains(out.String(), "  → start Docker, then run `theo doctor`") {
		t.Errorf("docker error = %q", out.String())
	}
	t.Setenv(registry.EnvHome, t.TempDir())
	if _, stderr, code := run("up", "ghost"); code != ExitError || !strings.Contains(stderr, "→ theo project list") {
		t.Errorf("unknown project = %d %q", code, stderr)
	}
	if text, ok := helpFor("stop"); !ok || !strings.Contains(text, "stop --all") {
		t.Errorf("help stop = %q", text)
	}
}
