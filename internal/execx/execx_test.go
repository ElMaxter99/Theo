package execx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestHelperProcess is not a test: the tests below run the test binary again
// as a command that echoes its input and exits with a chosen status.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("THEO_EXECX_HELPER") != "1" {
		return
	}
	var buf bytes.Buffer
	buf.ReadFrom(os.Stdin)
	os.Stdout.WriteString("got:" + buf.String())
	os.Stderr.WriteString("err-output")
	if os.Getenv("THEO_EXECX_EXIT") == "3" {
		os.Exit(3)
	}
	os.Exit(0)
}

func helper(t *testing.T, exit string) (string, []string) {
	t.Helper()
	t.Setenv("THEO_EXECX_HELPER", "1")
	t.Setenv("THEO_EXECX_EXIT", exit)
	return os.Args[0], []string{"-test.run=TestHelperProcess"}
}

func TestStreamInteractivePassesStdinAndExitCode(t *testing.T) {
	name, args := helper(t, "3")
	var out, errOut bytes.Buffer
	err := StreamInteractive(context.Background(), "", strings.NewReader("hello"), &out, &errOut, name, args...)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 3 {
		t.Fatalf("err = %v, want an ExitError with code 3", err)
	}
	if !strings.Contains(out.String(), "got:hello") || errOut.String() != "err-output" {
		t.Errorf("stdout %q, stderr %q", out.String(), errOut.String())
	}

	name, args = helper(t, "0")
	if err := StreamInteractive(context.Background(), "", strings.NewReader("x"), &out, &errOut, name, args...); err != nil {
		t.Errorf("exit 0: %v", err)
	}
}

func TestExplainListsAndSkipsChanges(t *testing.T) {
	var listing bytes.Buffer
	done := Explain(&listing)
	defer Explain(nil)

	dir := t.TempDir()
	if !Explaining() {
		t.Fatal("Explaining() = false")
	}
	// A read runs: git is needed by the rest of the tests anyway.
	if _, err := exec.LookPath("git"); err == nil {
		if _, err := Run(context.Background(), dir, "git", "--version"); err != nil {
			t.Errorf("a read must run: %v", err)
		}
	}
	// Changes are listed and not executed, even for a tool that does not exist.
	if out, err := RunChange(context.Background(), dir, "theo-no-such-tool", "make", "a mess"); err != nil || out != nil {
		t.Errorf("RunChange under --explain = %q, %v; want nothing", out, err)
	}
	if err := StreamChange(context.Background(), dir, nil, nil, "theo-no-such-tool", "up"); err != nil {
		t.Errorf("StreamChange under --explain: %v", err)
	}
	if err := StreamInteractive(context.Background(), dir, nil, nil, nil, "theo-no-such-tool", "exec"); err != nil {
		t.Errorf("StreamInteractive under --explain: %v", err)
	}
	// A mutating verb through Run is caught too.
	if out, err := Run(context.Background(), dir, "theo-no-such-tool-git", "worktree", "list"); err == nil {
		t.Errorf("a read of a missing tool should fail, got %q", out)
	}
	if _, err := Run(context.Background(), dir, "git", "worktree", "add", "x"); err != nil {
		t.Errorf("a mutating verb through Run must be skipped, not run: %v", err)
	}
	done()

	got := listing.String()
	for _, want := range []string{
		"1. read   ", "git --version",
		"change " + dir + " $ theo-no-such-tool make 'a mess'",
		"change " + dir + " $ theo-no-such-tool up",
		"change " + dir + " $ theo-no-such-tool exec",
		"change " + dir + " $ git worktree add x",
		"command(s): ", "Nothing was changed.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("listing lacks %q:\n%s", want, got)
		}
	}
	if Explaining() {
		t.Error("done() must turn explain mode off")
	}
}

func TestNotExplainingRunsEverything(t *testing.T) {
	if _, err := RunChange(context.Background(), "", "theo-no-such-tool"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestLooksMutating(t *testing.T) {
	tests := []struct {
		name string
		args string
		want bool
	}{
		{"git", "worktree list --porcelain", false},
		{"git", "worktree add -b x /p", true},
		{"git", "worktree remove -- /p", true},
		{"git", "status --porcelain", false},
		{"git", "rev-parse --show-toplevel", false},
		{"git", "for-each-ref --format=%(refname) refs/heads", false},
		{"git", "ls-files --others", false},
		{"git", "clone url dir", true},
		{"git", "checkout main", true},
		{"git", "branch --list", false},
		{"git", "branch feature", true},
		{"git", "branch -D feature", true},
		{"docker", "compose -p x --profile * ps --all --format json", false},
		{"docker", "compose -p x -f a.yaml --env-file e config --format json", false},
		{"docker", "compose ls --all --format json", false},
		{"docker", "compose -p x up --detach", true},
		{"docker", "compose -p x --profile * down", true},
		{"docker", "compose -p x exec -T web ls", true},
		{"docker", "compose -p x logs --follow", false},
		{"docker", "volume ls --format x", false},
		{"docker", "volume rm -- v", true},
		{"docker", "network create n", true},
		{"docker", "ps --all --filter volume=v", false},
		{"docker", "rm c", true},
		{"docker", "run --rm alpine", true},
		{"docker", "info", false},
	}
	for _, tt := range tests {
		if got := looksMutating(tt.name, strings.Fields(tt.args)); got != tt.want {
			t.Errorf("looksMutating(%s %s) = %v, want %v", tt.name, tt.args, got, tt.want)
		}
	}
}

func TestCommandLineQuoting(t *testing.T) {
	got := CommandLine("docker", []string{"compose", "-p", "my app", "--env-file", "/a b/.env", "it's", "", "*", "{{json .}}"})
	want := `docker compose -p 'my app' --env-file '/a b/.env' 'it'\''s' '' '*' '{{json .}}'`
	if got != want {
		t.Errorf("CommandLine() = %s\nwant %s", got, want)
	}
}
