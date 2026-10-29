package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// TestJSONOutputs runs every command that prints data with --json and checks
// the contract of docs/cli/output.md: exit 0, valid JSON on stdout, nothing
// on stderr, and a list is [] when it is empty, never null.
func TestJSONOutputs(t *testing.T) {
	testutil.RequireDaemon(t)
	main, _ := testutil.NewRepo(t, map[string]string{"compose.yaml": "name: t5-out\nservices:\n  web:\n    image: alpine:3\n    ports: [\"8080\"]\n"})
	home := t.TempDir()
	t.Setenv(registry.EnvHome, home)

	// An empty registry first.
	for _, args := range [][]string{{"project", "list", "--json"}, {"status", "--json"}, {"reconcile", "--json"}, {"version", "--json"}} {
		checkJSON(t, args)
	}
	if stdout, _, _ := run("project", "list", "--json"); strings.TrimSpace(stdout) != "[]" {
		t.Errorf("project list --json with no projects = %q, want []", stdout)
	}

	for _, args := range [][]string{
		{"project", "add", main, "--name", "t5out", "--json"},
		{"project", "list", "--json"},
		{"project", "set", "t5out", "--worktree-dir", t.TempDir(), "--json"},
		{"worktrees", "t5out", "--json"},
		{"branches", "t5out", "--json"},
		{"services", "t5out", "--json"},
		{"ps", "t5out", "--json"},
		{"ps", "t5out", "-w", "main", "--json"},
		{"ports", "t5out", "--json"},
		{"volumes", "t5out", "--json"},
		{"volumes", "purge", "t5out", "--json"},
		{"env", "list", "t5out", "--json"},
		{"instance", "show", "t5out", "--json"},
		{"instance", "config", "t5out", "--json"},
		{"history", "t5out", "--json"},
		{"reconcile", "t5out", "--json"},
		{"status", "t5out", "--json"},
		{"worktree", "add", "t5out", "t5-out-branch", "--new", "--json"},
		{"worktree", "remove", "t5out", "t5-out-branch", "--json"},
		{"project", "remove", "t5out", "--json"},
	} {
		checkJSON(t, args)
	}
}

func checkJSON(t *testing.T, args []string) {
	stdout, stderr, code := run(args...)
	t.Helper()
	if code != ExitOK || stderr != "" {
		t.Errorf("%v: code %d, stderr %q\n%s", args, code, stderr, stdout)
		return
	}
	var v any
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Errorf("%v: stdout is not JSON: %v\n%s", args, err, stdout)
		return
	}
	if v == nil {
		t.Errorf("%v: stdout is null", args)
	}
	if path := findNull(v, ""); path != "" {
		t.Errorf("%v: %s is null (lists must be []):\n%s", args, path, stdout)
	}
}

// findNull returns the path of the first null in a decoded JSON document.
func findNull(v any, path string) string {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if e == nil {
				return path + "." + k
			}
			if p := findNull(e, path+"."+k); p != "" {
				return p
			}
		}
	case []any:
		for i, e := range x {
			if p := findNull(e, fmt.Sprintf("%s[%d]", path, i)); p != "" {
				return p
			}
		}
	}
	return ""
}

// TestErrorsGoToStderr checks the contract for failures: nothing on stdout,
// the message on stderr, exit 1 for failures and 2 for wrong usage.
func TestErrorsGoToStderr(t *testing.T) {
	t.Setenv(registry.EnvHome, t.TempDir())
	tests := []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"nope"}, ExitUsage, "unknown command"},
		{[]string{"up"}, ExitUsage, "needs exactly one project"},
		{[]string{"up", "a", "b"}, ExitUsage, "needs exactly one project"},
		{[]string{"up", "ghost"}, ExitError, `project "ghost"`},
		{[]string{"down", "ghost"}, ExitError, `project "ghost"`},
		{[]string{"stop", "ghost"}, ExitError, `project "ghost"`},
		{[]string{"start"}, ExitUsage, "needs a project"},
		{[]string{"recreate", "ghost"}, ExitError, `project "ghost"`},
		{[]string{"exec", "ghost", "svc"}, ExitUsage, "needs a project, a service and a command"},
		{[]string{"exec", "ghost", "svc", "--", "ls"}, ExitError, `project "ghost"`},
		{[]string{"shell", "ghost"}, ExitUsage, "needs a project and a service"},
		{[]string{"status", "ghost"}, ExitError, `project "ghost"`},
		{[]string{"status", "a", "b"}, ExitUsage, "at most one"},
		{[]string{"status", "--bogus"}, ExitUsage, "flag provided but not defined"},
		{[]string{"completion", "tcsh"}, ExitUsage, "unknown shell"},
		{[]string{"ps", "ghost", "--json"}, ExitError, `project "ghost"`},
		{[]string{"worktrees", "ghost", "--json"}, ExitError, `project "ghost"`},
		{[]string{"project", "add", "/no/such/dir", "--json"}, ExitError, "theo:"},
		{[]string{"--explain"}, ExitUsage, "Usage:"},
		{[]string{"--explain", "up", "ghost"}, ExitError, `project "ghost"`},
	}
	for _, tt := range tests {
		stdout, stderr, code := run(tt.args...)
		if code != tt.code || !strings.Contains(stderr, tt.msg) {
			t.Errorf("%v: code %d (want %d), stderr %q (want %q)", tt.args, code, tt.code, stderr, tt.msg)
		}
		// Only --explain may print on stdout, and only its listing.
		if stdout != "" && !strings.HasPrefix(stdout, "# theo --explain") {
			t.Errorf("%v: stdout %q on a failure", tt.args, stdout)
		}
	}
}
