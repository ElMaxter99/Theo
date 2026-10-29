// Package testutil holds helpers shared by tests that use the real git and
// docker tools.
package testutil

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

// RequireDaemon skips the test unless a Docker daemon that runs Linux
// containers is reachable.
func RequireDaemon(t *testing.T) {
	t.Helper()
	RequireCompose(t)
	out, err := exec.Command("docker", "info", "--format", "{{.OSType}}").Output()
	if err != nil || strings.TrimSpace(string(out)) != "linux" {
		t.Skip("no Docker daemon running Linux containers")
	}
}

// serverSource is a tiny HTTP server used as a container in tests. It is
// built into an image FROM scratch, so tests never pull images.
const serverSource = `package main

import (
	"fmt"
	"net/http"
)

func main() {
	fmt.Println("server ready")
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	http.ListenAndServe(":8080", nil)
}
`

// WriteServerImage writes to dir a Dockerfile and a static Linux binary of a
// tiny HTTP server listening on port 8080, so that a Compose file in dir can
// use `build: .`.
func WriteServerImage(t *testing.T, dir string) {
	t.Helper()
	src := TempDir(t)
	WriteFile(t, src, "go.mod", "module server\n\ngo 1.24\n")
	WriteFile(t, src, "main.go", serverSource)
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "server"), ".")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the test server: %v\n%s", err, out)
	}
	WriteFile(t, dir, "Dockerfile", "FROM scratch\nCOPY server /server\nENTRYPOINT [\"/server\"]\n")
}

// Docker runs docker and returns its trimmed standard output, failing the
// test if it fails.
func Docker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// FreePort returns a TCP port that is free now and whose next port is free
// too, so a test can use one fixed port and know the next is available.
func FreePort(t *testing.T) int {
	t.Helper()
	for i := 0; i < 20; i++ {
		l, err := net.Listen("tcp", ":0")
		if err != nil {
			t.Fatal(err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		l.Close()
		if port >= 65000 {
			continue
		}
		next, err := net.Listen("tcp", ":"+strconv.Itoa(port+1))
		if err != nil {
			continue
		}
		next.Close()
		return port
	}
	t.Fatal("no free pair of consecutive ports")
	return 0
}
