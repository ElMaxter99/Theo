package cli

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/agents"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/opener"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
	"github.com/ElMaxter99/Theo/internal/update"
)

func fakeTools(t *testing.T, installed ...string) *[]string {
	t.Helper()
	origLook, origStart := opener.Look, opener.Start
	t.Cleanup(func() { opener.Look, opener.Start = origLook, origStart })
	var started []string
	opener.Look = func(name string) (string, error) {
		for _, n := range installed {
			if n == name {
				return "/fake/" + name, nil
			}
		}
		return "", exec.ErrNotFound
	}
	opener.Start = func(dir, name string, args ...string) error {
		started = append(started, dir+"|"+name+" "+strings.Join(args, " "))
		return nil
	}
	return &started
}

func TestOpenInEditorAndConfig(t *testing.T) {
	main, _ := testutil.NewRepo(t, map[string]string{"compose.yaml": "services:\n  web:\n    image: alpine:3\n"})
	t.Setenv(registry.EnvHome, t.TempDir())
	if _, stderr, code := run("project", "add", main, "--name", "app"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}

	started := fakeTools(t) // nothing installed
	_, stderr, code := run("open", "app", "--in", "editor")
	if code != ExitError || !strings.Contains(stderr, "Editor not found. Set one with: theo config set open.editor <command>") {
		t.Errorf("no editor: %d %q", code, stderr)
	}

	if _, stderr, code := run("config", "set", "open.editor", "myedit --wait"); code != ExitOK {
		t.Fatalf("config set: %s", stderr)
	}
	if _, stderr, code := run("open", "app", "--in", "editor"); code != ExitError || !strings.Contains(stderr, "Editor not found") {
		t.Errorf("configured editor missing from PATH: %d %q", code, stderr)
	}

	started = fakeTools(t, "myedit")
	stdout, stderr, code := run("open", "app", "--in", "editor")
	if code != ExitOK || !strings.Contains(stdout, "Opened main in editor (myedit)") {
		t.Fatalf("open: %d %q %q", code, stdout, stderr)
	}
	if len(*started) != 1 || !strings.Contains((*started)[0], "|myedit --wait ") || !strings.HasSuffix((*started)[0], main) {
		t.Errorf("started %v", *started)
	}
	if _, _, code := run("open", "app", "--in", "toaster"); code != ExitUsage {
		t.Errorf("unknown target: %d", code)
	}
	if out, _, _ := run("config", "list"); !strings.Contains(out, "open.editor    myedit --wait") {
		t.Errorf("config list = %q", out)
	}
	if _, _, code := run("config", "set", "update-check", "maybe"); code == ExitOK {
		t.Error("update-check accepts only on or off")
	}
	if _, _, code := run("config", "set", "nope", "x"); code == ExitOK {
		t.Error("unknown key accepted")
	}
}

func TestUpdateNotice(t *testing.T) {
	t.Setenv(registry.EnvHome, t.TempDir())
	e, err := engine.Open()
	if err != nil {
		t.Fatal(err)
	}
	origFetch, origVersion := listReleases, engine.Version
	t.Cleanup(func() { listReleases, engine.Version = origFetch, origVersion })
	engine.Version = "0.5.0"
	calls := 0
	listReleases = func(context.Context) ([]update.Release, error) {
		calls++
		return []update.Release{{Tag: "v0.5.1"}, {Tag: "v0.6.0-rc.1", Prerelease: true}}, nil
	}
	now := time.Now()

	if got := updateNotice(context.Background(), e, now); got != nil || calls != 0 {
		t.Fatalf("off by default: %v, %d calls", got, calls)
	}
	if err := e.Prefs().Set("update-check", "on"); err != nil {
		t.Fatal(err)
	}
	got := updateNotice(context.Background(), e, now)
	if len(got) != 3 || !strings.Contains(got[0], "0.5.1 (you have 0.5.0)") || calls != 1 {
		t.Fatalf("notice = %v, %d calls", got, calls)
	}
	updateNotice(context.Background(), e, now.Add(time.Hour))
	if calls != 1 {
		t.Errorf("asked again within the interval: %d calls", calls)
	}
	updateNotice(context.Background(), e, now.Add(7*time.Hour))
	if calls != 2 {
		t.Errorf("did not ask after the interval: %d calls", calls)
	}
	// Release candidates only with the rc channel.
	if err := e.Prefs().Set("update-channel", "rc"); err != nil {
		t.Fatal(err)
	}
	got = updateNotice(context.Background(), e, now.Add(14*time.Hour))
	if len(got) != 3 || !strings.Contains(got[0], "0.6.0-rc.1") || !strings.Contains(got[0], "test version") {
		t.Errorf("rc notice = %v", got)
	}
	_ = e.Prefs().Set("update-channel", "stable")
	listReleases = func(context.Context) ([]update.Release, error) { return []update.Release{{Tag: "v0.5.0"}}, nil }
	if got := updateNotice(context.Background(), e, now.Add(60*time.Hour)); got != nil {
		t.Errorf("same version: %v", got)
	}
	engine.Version = "0.0.0-dev"
	if got := updateNotice(context.Background(), e, now.Add(90*time.Hour)); got != nil {
		t.Errorf("a dev build never looks: %v", got)
	}
}

func TestAgentTextAndSummaryLine(t *testing.T) {
	if agentText(nil) != "-" || agentText(&agents.State{State: "waiting", Source: agents.SourceHook}) != "waiting" ||
		agentText(&agents.State{State: "working", Source: agents.SourceProcess}) != "working (estimated)" {
		t.Error("agentText")
	}
	var b strings.Builder
	printAgents(&b, []engine.ProjectStatus{{Name: "shop", Worktrees: []engine.WorktreeStatus{
		{Branch: "fix", Path: "/p/fix", Agent: &agents.State{State: agents.Working}},
		{Branch: "docs", Path: "/p/docs", Agent: &agents.State{State: agents.Waiting}},
	}}}, false)
	out := b.String()
	if !strings.Contains(out, "Agents: 1 working, 1 waiting") || !strings.Contains(out, "shop  docs waits for you") {
		t.Errorf("summary = %q", out)
	}
	b.Reset()
	printAgents(&b, nil, false)
	if b.Len() != 0 {
		t.Errorf("no agents, no line: %q", b.String())
	}
}
