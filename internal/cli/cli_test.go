package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

func TestRun(t *testing.T) {
	t.Setenv(registry.EnvHome, t.TempDir())
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"no arguments", nil, ExitUsage, "", "Usage:"},
		{"version", []string{"version"}, ExitOK, "theo " + engine.Version, ""},
		{"version flag", []string{"--version"}, ExitOK, "theo " + engine.Version, ""},
		{"help", []string{"help"}, ExitOK, "Usage:", ""},
		{"help flag", []string{"-h"}, ExitOK, "Usage:", ""},
		{"unknown command", []string{"bogus"}, ExitUsage, "", `unknown command "bogus"`},
		{"project without subcommand", []string{"project"}, ExitUsage, "", "needs a subcommand"},
		{"project add without path", []string{"project", "add"}, ExitUsage, "", "exactly one path"},
		{"unknown flag", []string{"project", "list", "--nope"}, ExitUsage, "", "flag provided but not defined"},
		{"empty registry", []string{"project", "list"}, ExitOK, "No projects registered", ""},
		{"unknown project", []string{"worktrees", "nope"}, ExitError, "", `project "nope": not found`},
		{"services without project", []string{"services"}, ExitUsage, "", "exactly one project"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, code := run(tt.args...)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			checkOutput(t, "stdout", stdout, tt.wantStdout)
			checkOutput(t, "stderr", stderr, tt.wantStderr)
		})
	}
}

func TestProjectAndWorktreesWithRealGit(t *testing.T) {
	main, linked := testutil.NewRepo(t, nil)
	t.Setenv(registry.EnvHome, t.TempDir())

	// Registering from a linked worktree registers the main one, and
	// flags may follow the path.
	stdout, stderr, code := run("project", "add", linked, "--name", "demo")
	if code != ExitOK {
		t.Fatalf("project add: code %d, stderr %s", code, stderr)
	}
	checkOutput(t, "stdout", stdout, `Registered project "demo" at `+main)

	if _, stderr, code := run("project", "add", main); code != ExitError || !strings.Contains(stderr, "already registered") {
		t.Errorf("second project add: code %d, stderr %s", code, stderr)
	}

	stdout, _, _ = run("project", "list")
	checkOutput(t, "stdout", stdout, "demo")

	testutil.WriteFile(t, linked, "wip.txt", "x")
	stdout, stderr, code = run("worktrees", "demo")
	if code != ExitOK {
		t.Fatalf("worktrees: code %d, stderr %s", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 || !strings.Contains(lines[1], "main") || !strings.Contains(lines[1], "clean") ||
		!strings.Contains(lines[2], "feature/x") || !strings.Contains(lines[2], "uncommitted") {
		t.Errorf("worktrees output:\n%s", stdout)
	}

	stdout, _, _ = run("worktrees", "--json", "demo")
	var list []engine.Worktree
	if err := json.Unmarshal([]byte(stdout), &list); err != nil || len(list) != 2 || list[1].Path != linked {
		t.Errorf("worktrees --json = %s (%v)", stdout, err)
	}

	stdout, _, code = run("project", "remove", "demo")
	if code != ExitOK {
		t.Fatalf("project remove: code %d", code)
	}
	checkOutput(t, "stdout", stdout, "were not touched")
}

func TestServicesWithRealCompose(t *testing.T) {
	testutil.RequireCompose(t)
	main, linked := testutil.NewRepo(t, map[string]string{
		"compose.yaml": `services:
  web:
    image: nginx:alpine
    ports: ["8080:80", "3000"]
    volumes: ["./html:/usr/share/nginx/html:ro", "../shared:/shared"]
`,
	})
	t.Setenv(registry.EnvHome, t.TempDir())
	if _, stderr, code := run("project", "add", main); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}

	stdout, stderr, code := run("services", "app", "-w", "feature/x")
	if code != ExitOK {
		t.Fatalf("services: code %d, stderr %s", code, stderr)
	}
	for _, want := range []string{
		"Worktree feature/x · " + linked,
		"nginx:alpine",
		"8080 -> 80/tcp",
		"3000/tcp (random host port)",
		"bind ." + string(filepath.Separator) + "html -> /usr/share/nginx/html (read-only)",
		"[outside the worktree]",
	} {
		checkOutput(t, "stdout", stdout, want)
	}
}

func run(args ...string) (stdout, stderr string, code int) {
	var out, errOut bytes.Buffer
	code = Run(args, &out, &errOut)
	return out.String(), errOut.String(), code
}

func checkOutput(t *testing.T, name, got, want string) {
	t.Helper()
	if want == "" {
		if got != "" {
			t.Errorf("%s = %q, want empty", name, got)
		}
		return
	}
	if !strings.Contains(got, want) {
		t.Errorf("%s = %q, want it to contain %q", name, got, want)
	}
}
