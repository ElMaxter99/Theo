package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func TestCompletionScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		stdout, stderr, code := run("completion", shell)
		if code != ExitOK || stderr != "" || !strings.Contains(stdout, "__complete suggest") {
			t.Errorf("completion %s: code %d, stderr %q\n%s", shell, code, stderr, stdout)
		}
	}
	if _, stderr, code := run("completion", "tcsh"); code != ExitUsage || !strings.Contains(stderr, "bash, zsh, fish, powershell") {
		t.Errorf("completion tcsh: code %d, stderr %s", code, stderr)
	}
	if _, _, code := run("completion"); code != ExitUsage {
		t.Errorf("completion without a shell: code %d", code)
	}
}

func suggest(t *testing.T, cur string, before ...string) []string {
	t.Helper()
	stdout, stderr, code := run(append([]string{"__complete", "suggest", "--cur=" + cur, "--"}, before...)...)
	if code != ExitOK || stderr != "" {
		t.Fatalf("__complete suggest %q %v: code %d, stderr %q", cur, before, code, stderr)
	}
	return lines(stdout)
}

func TestCompletionStatic(t *testing.T) {
	t.Setenv(registry.EnvHome, t.TempDir())
	cmds := suggest(t, "")
	for _, want := range []string{"up", "down", "stop", "start", "restart", "recreate", "exec", "shell", "status", "completion", "project", "worktree", "volumes", "reconcile", "version"} {
		found := false
		for _, c := range cmds {
			found = found || c == want
		}
		if !found {
			t.Errorf("commands lack %q: %v", want, cmds)
		}
	}
	for _, c := range cmds {
		if strings.HasPrefix(c, "__") {
			t.Errorf("the hidden command is offered: %v", cmds)
		}
	}
	if got := suggest(t, "re"); !reflect.DeepEqual(got, []string{"reconcile", "recreate", "restart"}) {
		t.Errorf("re = %v", got)
	}
	if got := suggest(t, "", "completion"); !reflect.DeepEqual(got, []string{"bash", "zsh", "fish", "powershell"}) {
		t.Errorf("completion = %v", got)
	}
	if got := suggest(t, "--", "--explain"); !reflect.DeepEqual(got, []string{"--explain", "--help", "--version"}) {
		t.Errorf("global flags = %v", got)
	}
	if got := suggest(t, "p", "--explain"); !reflect.DeepEqual(got, []string{"ports", "project", "ps"}) {
		t.Errorf("commands after --explain = %v", got)
	}
	if got := suggest(t, "--f", "up", "app"); !reflect.DeepEqual(got, []string{"--force-recreate"}) {
		t.Errorf("up --f = %v", got)
	}
	if got := suggest(t, "--s", "up", "app"); !reflect.DeepEqual(got, []string{"--service"}) {
		t.Errorf("up --s = %v", got)
	}
	if got := suggest(t, "", "project"); !reflect.DeepEqual(got, []string{"add", "set", "list", "remove"}) {
		t.Errorf("project = %v", got)
	}
	if got := suggest(t, "--", "project", "set", "app"); !strings.Contains(strings.Join(got, " "), "--worktree-dir") {
		t.Errorf("project set flags = %v", got)
	}
	if got := suggest(t, "", "volumes"); len(got) != 1 || got[0] != "purge" {
		t.Errorf("volumes (no projects registered) = %v", got)
	}
	if got := suggest(t, "", "project", "add"); !reflect.DeepEqual(got, []string{":files"}) {
		t.Errorf("project add = %v, want the files marker", got)
	}
	if got := suggest(t, "", "up", "app", "--env-file"); !reflect.DeepEqual(got, []string{":files"}) {
		t.Errorf("--env-file value = %v", got)
	}
	if got := suggest(t, "", "nope"); len(got) != 0 {
		t.Errorf("unknown command = %v", got)
	}
	// Dynamic kinds never fail, even without projects.
	for _, args := range [][]string{{"projects"}, {"worktrees", "nope"}, {"services", "nope"}, {"services", "nope", "x"}, {"bogus"}, {}} {
		if stdout, stderr, code := run(append([]string{"__complete"}, args...)...); code != ExitOK || stdout != "" || stderr != "" {
			t.Errorf("__complete %v: code %d, stdout %q, stderr %q", args, code, stdout, stderr)
		}
	}
}

func TestCompletionDynamic(t *testing.T) {
	testutil.RequireCompose(t)
	main, _ := testutil.NewRepo(t, map[string]string{"compose.yaml": "name: t5-comp\nservices:\n  api:\n    image: alpine:3\n  db:\n    image: alpine:3\n  worker:\n    image: alpine:3\n    profiles: [jobs]\n"})
	t.Setenv(registry.EnvHome, t.TempDir())
	if _, stderr, code := run("project", "add", main, "--name", "t5comp"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	other, _ := testutil.NewRepo(t, nil)
	if _, stderr, code := run("project", "add", other, "--name", "t5other"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}

	if stdout, _, _ := run("__complete", "projects"); !reflect.DeepEqual(lines(stdout), []string{"t5comp", "t5other"}) {
		t.Errorf("projects = %q", stdout)
	}
	if stdout, _, _ := run("__complete", "worktrees", "t5comp"); !reflect.DeepEqual(lines(stdout), []string{"main", "feature/x"}) {
		t.Errorf("worktrees = %q", stdout)
	}
	if stdout, _, _ := run("__complete", "services", "t5comp"); !reflect.DeepEqual(lines(stdout), []string{"api", "db", "worker"}) {
		t.Errorf("services = %q", stdout)
	}
	if stdout, _, _ := run("__complete", "services", "t5comp", "feature/x"); !reflect.DeepEqual(lines(stdout), []string{"api", "db", "worker"}) {
		t.Errorf("services of a worktree = %q", stdout)
	}

	tests := []struct {
		cur    string
		before []string
		want   []string
	}{
		{"", []string{"up"}, []string{"t5comp", "t5other"}},
		{"t5c", []string{"up"}, []string{"t5comp"}},
		{"", []string{"ps", "t5comp", "-w"}, []string{"main", "feature/x"}},
		{"f", []string{"ps", "t5comp", "--worktree"}, []string{"feature/x"}},
		{"", []string{"up", "t5comp", "--service"}, []string{"api", "db", "worker"}},
		{"d", []string{"up", "-w", "feature/x", "t5comp", "--service"}, []string{"db"}},
		{"", []string{"stop", "t5comp"}, []string{"api", "db", "worker"}},
		{"", []string{"stop", "t5comp", "api"}, []string{"api", "db", "worker"}}, // repeats
		{"", []string{"exec", "t5comp"}, []string{"api", "db", "worker"}},
		{"", []string{"exec", "t5comp", "api"}, nil}, // the command is up to the user
		{"", []string{"logs", "-w", "main", "t5comp"}, []string{"api", "db", "worker"}},
		{"", []string{"worktree", "remove", "t5comp"}, []string{"main", "feature/x"}},
		{"", []string{"worktree", "add", "t5comp"}, []string{"feature/x", "main"}},
		{"", []string{"worktree"}, []string{"add", "remove"}},
		{"", []string{"env", "list"}, []string{"t5comp", "t5other"}},
		{"", []string{"volumes"}, []string{"purge", "t5comp", "t5other"}},
		{"", []string{"volumes", "purge"}, []string{"t5comp", "t5other"}},
		{"", []string{"status"}, []string{"t5comp", "t5other"}},
	}
	for _, tt := range tests {
		got := suggest(t, tt.cur, tt.before...)
		if len(tt.want) == 0 && len(got) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%v + %q = %v, want %v", tt.before, tt.cur, got, tt.want)
		}
	}
}

// TestCompletionBashScript runs the generated bash script in a real bash
// against a real theo binary: it is the part that no Go unit test sees.
func TestCompletionBashScript(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("bash not available")
	}
	testutil.RequireCompose(t)
	main, _ := testutil.NewRepo(t, map[string]string{"compose.yaml": "name: t5-bash\nservices:\n  api:\n    image: alpine:3\n  db:\n    image: alpine:3\n"})
	home := t.TempDir()
	t.Setenv(registry.EnvHome, home)
	if _, stderr, code := run("project", "add", main, "--name", "t5bash"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	bin := filepath.Join(t.TempDir(), "theo")
	build := exec.Command("go", "build", "-o", bin, "github.com/ElMaxter99/Theo/cmd/theo")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	complete := func(words ...string) []string {
		t.Helper()
		// words[0] is the command as typed; the last word is the one being completed.
		script := `source <("$THEO_BIN" completion bash); COMP_WORDS=("$THEO_BIN" "$@"); COMP_CWORD=$#; _theo; printf '%s\n' "${COMPREPLY[@]}"`
		cmd := exec.Command(bash, "-c", script, "bash")
		cmd.Args = append(cmd.Args, words...)
		cmd.Env = append(os.Environ(), "THEO_BIN="+bin, registry.EnvHome+"="+home)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bash: %v\n%s", err, out)
		}
		return lines(string(out))
	}
	if got := complete("sta"); !reflect.DeepEqual(got, []string{"start", "status"}) {
		t.Errorf("sta = %v", got)
	}
	if got := complete("stop", ""); !reflect.DeepEqual(got, []string{"t5bash"}) {
		t.Errorf("stop <tab> = %v", got)
	}
	if got := complete("stop", "t5bash", ""); !reflect.DeepEqual(got, []string{"api", "db"}) {
		t.Errorf("stop t5bash <tab> = %v", got)
	}
	if got := complete("up", "t5bash", "-w", "f"); !reflect.DeepEqual(got, []string{"feature/x"}) {
		t.Errorf("up t5bash -w f<tab> = %v", got)
	}
	if got := complete("up", "t5bash", "--se"); !reflect.DeepEqual(got, []string{"--service"}) {
		t.Errorf("up t5bash --se<tab> = %v", got)
	}
}
