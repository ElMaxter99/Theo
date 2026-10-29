// Package testutil holds helpers shared by tests that use the real git and
// docker tools.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// RequireGit skips the test when git is not installed.
func RequireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// RequireCompose skips the test when docker compose is not installed. It
// does not need a running Docker daemon.
func RequireCompose(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not installed")
	}
}

// TempDir returns a temporary directory with symbolic links and short
// names resolved, so it matches the paths that git reports.
func TempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// Git runs git in dir and fails the test if it fails.
func Git(t *testing.T, dir string, args ...string) {
	t.Helper()
	args = append([]string{"-c", "user.name=Theo Test", "-c", "user.email=test@example.invalid"}, args...)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// WriteFile writes content to dir/name, creating parent directories.
func WriteFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// NewRepo creates a Git repository called "app" with one commit containing
// files, and a linked worktree "app-feature" on branch "feature/x". It
// returns the paths of both worktrees.
func NewRepo(t *testing.T, files map[string]string) (main, linked string) {
	t.Helper()
	RequireGit(t)
	root := TempDir(t)
	main = filepath.Join(root, "app")
	linked = filepath.Join(root, "app-feature")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatal(err)
	}
	Git(t, main, "init", "-q", "-b", "main")
	WriteFile(t, main, "README.md", "test\n")
	for name, content := range files {
		WriteFile(t, main, name, content)
	}
	Git(t, main, "add", "-A")
	Git(t, main, "commit", "-q", "-m", "init")
	Git(t, main, "worktree", "add", "-q", "-b", "feature/x", linked)
	return main, linked
}
