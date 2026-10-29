package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// snapshot describes everything `theo --explain` must leave alone: the
// containers, volumes and networks of the test project in Docker, the
// branches and worktrees in Git, the files of the worktrees, and Theo's own
// data directory.
func snapshot(t *testing.T, project, repo, data string) string {
	t.Helper()
	var b strings.Builder
	add := func(title, text string) { b.WriteString("== " + title + "\n" + text + "\n") }
	label := "label=com.docker.compose.project=" + project
	add("containers", testutil.Docker(t, "ps", "--all", "--no-trunc", "--filter", label, "--format", "{{.ID}} {{.State}} {{.Names}}"))
	add("volumes", testutil.Docker(t, "volume", "ls", "--filter", label, "--format", "{{.Name}}"))
	add("networks", testutil.Docker(t, "network", "ls", "--filter", label, "--format", "{{.Name}}"))
	for _, args := range [][]string{{"worktree", "list", "--porcelain"}, {"branch", "-a"}, {"status", "--porcelain"}, {"stash", "list"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		add("git "+strings.Join(args, " "), string(out))
	}
	// Theo's own files and the directories next to the repository.
	for _, root := range []string{data, filepath.Dir(repo)} {
		var files []string
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || strings.Contains(path, string(filepath.Separator)+".git"+string(filepath.Separator)) || filepath.Base(path) == ".git" {
				return nil
			}
			sum := ""
			if !d.IsDir() {
				if data, err := os.ReadFile(path); err == nil {
					h := sha256.Sum256(data)
					sum = hex.EncodeToString(h[:6])
				}
			}
			files = append(files, path+" "+sum)
			return nil
		})
		sort.Strings(files)
		add("files "+root, strings.Join(files, "\n"))
	}
	return b.String()
}

// TestExplainChangesNothing walks the commands that change something and
// runs each with --explain against a running stack: the listing says what
// would run, and Docker, Git and Theo's own files are exactly as before.
func TestExplainChangesNothing(t *testing.T) {
	testutil.RequireDaemon(t)
	const project = "t5-exp"
	main, linked := testutil.NewRepo(t, map[string]string{"compose.yaml": strings.Replace(t5Compose, "%[1]s", project, 1)})
	otherRepo, _ := testutil.NewRepo(t, nil)
	home := t.TempDir()
	t.Setenv(registry.EnvHome, home)
	if _, stderr, code := run("project", "add", main, "--name", "t5exp"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	cleanupProject(t, project, "t5exp")
	if stdout, stderr, code := run("up", "t5exp"); code != ExitOK {
		t.Fatalf("up: %d\n%s\n%s", code, stdout, stderr)
	}
	// Some data in the volume and one setting, so there is something to lose.
	stdin = strings.NewReader("")
	t.Cleanup(func() { stdin = os.Stdin })
	run("exec", "t5exp", "-T", "tools", "--", "sh", "-c", "echo keep > /data/marker")
	if _, stderr, code := run("env", "set", "t5exp", "KEEP=1"); code != ExitOK {
		t.Fatalf("env set: %s", stderr)
	}

	tests := []struct {
		args []string
		want []string // substrings of the listing
	}{
		{[]string{"up", "t5exp"}, []string{"change " + main + " $ docker compose -p t5-exp", "up --detach"}},
		{[]string{"up", "t5exp", "--service", "api"}, []string{"up --detach api"}},
		{[]string{"up", "t5exp", "--force-recreate", "--auto-ports"}, []string{"up --detach --force-recreate"}},
		{[]string{"up", "t5exp", "-w", "feature/x"}, []string{"change " + linked + " $ docker compose -p t5exp-app-feature", "up --detach"}},
		{[]string{"down", "t5exp"}, []string{" down"}},
		{[]string{"down", "t5exp", "-w", "feature/x"}, []string{" down"}},
		{[]string{"restart", "t5exp"}, []string{" restart"}},
		{[]string{"restart", "t5exp", "api", "db"}, []string{" restart api db"}},
		{[]string{"stop", "t5exp"}, []string{" stop"}},
		{[]string{"stop", "t5exp", "api"}, []string{" stop api"}},
		{[]string{"start", "t5exp", "api"}, []string{" start api"}},
		{[]string{"recreate", "t5exp"}, []string{"up --detach --force-recreate"}},
		{[]string{"recreate", "t5exp", "tools"}, []string{"up --detach --force-recreate --no-deps tools"}},
		{[]string{"exec", "t5exp", "-T", "tools", "--", "rm", "-rf", "/data/marker"}, []string{"exec -T tools rm -rf /data/marker"}},
		{[]string{"shell", "t5exp", "tools"}, []string{"exec"}},
		{[]string{"worktree", "add", "t5exp", "feature/y", "--new"}, []string{"git worktree add -b feature/y "}},
		{[]string{"worktree", "remove", "t5exp", "feature/x"}, []string{"change " + main + " $ git worktree remove -- " + linked}},
		{[]string{"project", "add", otherRepo, "--name", "second"}, nil},
		{[]string{"project", "remove", "t5exp"}, nil},
		{[]string{"project", "set", "t5exp", "--worktree-dir", filepath.Join(home, "elsewhere")}, nil},
		{[]string{"env", "set", "t5exp", "NEW=2"}, nil},
		{[]string{"env", "unset", "t5exp", "KEEP"}, nil},
		{[]string{"instance", "set", "t5exp", "--neutralize-container-names"}, nil},
		{[]string{"reconcile", "--apply"}, nil},
		{[]string{"status"}, nil},
		{[]string{"ps", "t5exp"}, nil},
		{[]string{"ports", "t5exp", "--auto-ports"}, nil},
		{[]string{"instance", "config", "t5exp", "--auto-ports"}, []string{" config"}},
		{[]string{"volumes", "t5exp"}, nil},
		{[]string{"logs", "t5exp", "--tail", "1"}, []string{" logs --tail 1"}},
	}

	before := snapshot(t, project, main, home)
	for _, tt := range tests {
		args := append([]string{"--explain"}, tt.args...)
		stdout, stderr, code := run(args...)
		if code != ExitOK {
			t.Errorf("%v: code %d\n%s\n%s", args, code, stdout, stderr)
			continue
		}
		if !strings.HasPrefix(stdout, "# theo --explain") || !strings.Contains(stdout, "Nothing was changed.") {
			t.Errorf("%v: not an explain listing:\n%s", args, stdout)
		}
		for _, want := range tt.want {
			if !strings.Contains(stdout, want) {
				t.Errorf("%v: the listing lacks %q:\n%s", args, want, stdout)
			}
		}
		if len(tt.want) > 0 && tt.args[0] != "instance" && tt.args[0] != "logs" && !strings.Contains(stdout, ". change ") {
			t.Errorf("%v: no change line in the listing:\n%s", args, stdout)
		}
		for _, lie := range []string{"Started ", "Stopped ", "Created worktree", "Removed worktree", "Registered project", "Recreated "} {
			if strings.Contains(stdout, lie) {
				t.Errorf("%v: --explain printed %q as if it had happened:\n%s", args, lie, stdout)
			}
		}
		if after := snapshot(t, project, main, home); after != before {
			t.Fatalf("%v changed something:\n--- before\n%s\n--- after\n%s", args, before, after)
		}
	}

	// The data survived all of it, and the real commands still work.
	if got := testutil.Docker(t, "run", "--rm", "-v", project+"_data:/data", "alpine:3", "cat", "/data/marker"); got != "keep" {
		t.Errorf("volume content = %q", got)
	}

	// A purge needs the instance down; explain lists the removal and keeps the volume.
	if _, stderr, code := run("down", "t5exp"); code != ExitOK {
		t.Fatalf("down: %s", stderr)
	}
	before = snapshot(t, project, main, home)
	stdout, stderr, code := run("--explain", "volumes", "purge", "t5exp", "--confirm", project)
	if code != ExitOK {
		t.Fatalf("explain purge: %d\n%s\n%s", code, stdout, stderr)
	}
	checkOutput(t, "explain purge", stdout, "docker volume rm -- "+project+"_data")
	if after := snapshot(t, project, main, home); after != before {
		t.Fatalf("explain purge changed something:\n--- before\n%s\n--- after\n%s", before, after)
	}
	if !strings.Contains(testutil.Docker(t, "volume", "ls", "--format", "{{.Name}}"), project+"_data") {
		t.Error("the volume is gone")
	}
}

func TestExplainListsReadsInOrderWithDirectories(t *testing.T) {
	testutil.RequireCompose(t)
	main, _ := testutil.NewRepo(t, map[string]string{"compose.yaml": strings.Replace(t5Compose, "%[1]s", "t5-order", 1)})
	t.Setenv(registry.EnvHome, t.TempDir())
	if _, stderr, code := run("project", "add", main, "--name", "t5order"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	stdout, stderr, code := run("--explain", "worktree", "add", "t5order", "feature/z", "--new")
	if code != ExitOK {
		t.Fatalf("explain: %d %s", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	var cmds []string
	for _, l := range lines {
		if !strings.HasPrefix(l, "#") {
			cmds = append(cmds, l)
		}
	}
	// Reads first (list worktrees, branches), then the change, in main's
	// directory, then the read that looks at the result.
	if len(cmds) != 4 || !strings.HasPrefix(cmds[0], "1. read   "+main+" $ git worktree list --porcelain") ||
		!strings.HasPrefix(cmds[1], "2. read   ") || !strings.Contains(cmds[2], "3. change "+main+" $ git worktree add -b feature/z ") ||
		!strings.HasPrefix(cmds[3], "4. read   ") {
		t.Errorf("listing:\n%s", stdout)
	}
	if _, _, code := run("--explain"); code != ExitUsage {
		t.Errorf("--explain alone: code %d, want usage", code)
	}
	// Errors still go to stderr with a non-zero code.
	stdout, stderr, code = run("--explain", "up", "nope")
	if code != ExitError || !strings.Contains(stderr, `project "nope"`) || strings.Contains(stdout, "Started") {
		t.Errorf("explain with an unknown project: code %d\n%s\n%s", code, stdout, stderr)
	}
}
