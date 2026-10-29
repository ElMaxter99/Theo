package acceptance

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/cli"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// demoImages are pulled or built from by examples/demo. The test is skipped
// when they are neither present nor reachable (offline, registry limits).
var demoImages = []string{"golang:1.24-alpine", "alpine:3.20", "redis:7-alpine"}

func requireDemoImages(t *testing.T) {
	t.Helper()
	for _, img := range demoImages {
		if exec.Command("docker", "image", "inspect", img).Run() == nil {
			continue
		}
		if out, err := exec.Command("docker", "pull", "-q", img).CombinedOutput(); err != nil {
			t.Skipf("cannot get image %s: %v\n%s", img, err, out)
		}
	}
}

// demoFiles reads examples/demo (without any .env) as the files of a
// repository, renaming what would collide with other tests or the host: the
// Compose project, the fixed host port and the fixed container name.
func demoFiles(t *testing.T, project string, port int) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..", "examples", "demo")
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == ".env" || d.Name() == "README.md" {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c, ok := files["compose.yaml"]
	if !ok {
		t.Fatal("examples/demo/compose.yaml not found")
	}
	for old, repl := range map[string]string{
		"name: theo-demo":       "name: " + project,
		`"8080:8080"`:           fmt.Sprintf(`"%d:8080"`, port),
		"container_name: cache": "container_name: " + project + "-cache",
	} {
		if !strings.Contains(c, old) {
			t.Fatalf("examples/demo/compose.yaml lacks %q", old)
		}
		c = strings.Replace(c, old, repl, 1)
	}
	files["compose.yaml"] = c
	return files
}

// get fetches url until it answers (the web service may still be starting).
func get(t *testing.T, url string) string {
	t.Helper()
	var last error
	for i := 0; i < 40; i++ {
		resp, err := http.Get(url)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return string(body)
			}
			last = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			last = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("GET %s: %v", url, last)
	return ""
}

// The controlled test guide of examples/demo (the parts that do not need
// shared services or volume backups), driven through the command line.
func TestDemoGuide(t *testing.T) {
	testutil.RequireDaemon(t)
	requireDemoImages(t)

	const (
		proj   = "t7demo"
		second = "t7demo-app-feature" // Compose project of the linked worktree
	)
	port := testutil.FreePort(t)
	main, linked := testutil.NewRepo(t, demoFiles(t, proj, port))
	t.Setenv(registry.EnvHome, t.TempDir())
	t.Cleanup(func() {
		for _, p := range []string{proj, second} {
			exec.Command("docker", "compose", "-p", p, "--profile", "worker", "down", "--volumes", "--remove-orphans").Run()
		}
		exec.Command("docker", "image", "rm", proj+"-web", proj+"-worker", second+"-web", second+"-worker").Run()
	})

	// 1. Register. The required GREETING is missing until .env exists, and
	// Theo says so instead of starting something half configured.
	must(t, "project", "add", main, "--name", proj)
	if _, stderr, code := theo("services", proj); code == cli.ExitOK || !strings.Contains(stderr, "GREETING") {
		t.Errorf("services without .env must fail naming GREETING: code %d, %s", code, stderr)
	}
	testutil.WriteFile(t, main, ".env", "GREETING=Hello from Theo\n")

	// 2. Services and ports.
	out := must(t, "services", proj)
	contains(t, "services", out, "web", "cache", "worker", "bind", "cachedata", "depends on", "worker")
	contains(t, "ports", must(t, "ports", proj), fmt.Sprint(port), "free")

	// 3. Up the main worktree.
	must(t, "up", proj, "--build")
	url := fmt.Sprintf("http://localhost:%d/", port)
	body := get(t, url)
	contains(t, "main page", body, "greeting: Hello from Theo", "instance: main", "visits:   1", "bind mount")
	contains(t, "main page again", get(t, url), "visits:   2")
	testutil.Docker(t, "exec", proj+"-cache", "redis-cli", "ping")

	// 4. The profile service.
	must(t, "up", proj, "--profile", "worker")
	if logs := must(t, "logs", proj, "--tail", "5", "worker"); !strings.Contains(logs, "worker tick") {
		// The first tick is immediate; give a slow machine a moment.
		time.Sleep(3 * time.Second)
		contains(t, "worker logs", must(t, "logs", proj, "--tail", "5", "worker"), "worker tick")
	}

	// 5. A second worktree: the .env is used by reference, and starting it
	// shows both conflicts. It is not started by force.
	must(t, "worktree", "add", proj, "fix/t7", "--new", "--from", "main")
	contains(t, "worktrees", must(t, "worktrees", proj), "feature/x", "fix/t7")
	_, stderr, code := theo("up", proj, "-w", "feature/x", "--build")
	if code == cli.ExitOK {
		t.Fatal("the second instance started although its port and container name are taken")
	}
	contains(t, "conflict warnings", stderr, "already in use", "container_name")
	// --auto-ports alone does not solve the fixed container name.
	if _, _, code := theo("up", proj, "-w", "feature/x", "--auto-ports"); code == cli.ExitOK {
		t.Fatal("the fixed container_name must still stop the second instance")
	}
	must(t, "instance", "set", proj, "-w", "feature/x", "--neutralize-container-names")
	t.Setenv("BRANCH", "feature-x")
	must(t, "up", proj, "-w", "feature/x", "--auto-ports", "--build")
	url2 := fmt.Sprintf("http://localhost:%d/", port+1)
	contains(t, "second page", get(t, url2), "branch:   feature-x", "visits:   1") // its own cache, not main's
	contains(t, "ports", must(t, "ports", proj, "-w", "feature/x"), fmt.Sprint(port+1))

	// 6. Per-instance variables, one of them secret.
	must(t, "env", "set", proj, "-w", "feature/x", "INSTANCE_NAME=second")
	must(t, "env", "set", proj, "-w", "feature/x", "API_TOKEN=t7-secret-value", "--secret")
	list := must(t, "env", "list", proj, "-w", "feature/x")
	contains(t, "env list", list, "INSTANCE_NAME", "API_TOKEN", "********")
	if strings.Contains(list, "t7-secret-value") {
		t.Errorf("env list leaked the secret:\n%s", list)
	}
	must(t, "up", proj, "-w", "feature/x", "--auto-ports")
	contains(t, "second page", get(t, url2), "instance: second")
	if cfg := must(t, "instance", "config", proj, "-w", "feature/x"); strings.Contains(cfg, "t7-secret-value") {
		t.Errorf("instance config leaked the secret:\n%s", cfg)
	}

	// 7. ps, history.
	contains(t, "ps", must(t, "ps", proj), second, "running")
	contains(t, "history", must(t, "history", proj), "up", second)

	// 8. Volumes: kept after down, purge needs the confirmation.
	must(t, "down", proj, "-w", "feature/x")
	contains(t, "volumes", must(t, "volumes", proj, "-w", "feature/x"), "kept", "cachedata", "data")
	if _, _, code := theo("volumes", "purge", proj, "-w", "feature/x"); code == cli.ExitOK {
		t.Error("purge without --confirm succeeded")
	}
	vols := testutil.Docker(t, "volume", "ls", "--format", "{{.Name}}")
	contains(t, "volumes after refused purge", vols, second+"_data", proj+"_data")
	must(t, "volumes", "purge", proj, "-w", "feature/x", "--confirm", second)
	vols = testutil.Docker(t, "volume", "ls", "--format", "{{.Name}}")
	if strings.Contains(vols, second+"_") || !strings.Contains(vols, proj+"_cachedata") {
		t.Errorf("purge removed the wrong volumes:\n%s", vols)
	}

	// 9. Reconcile after deleting a worktree by hand.
	if err := os.RemoveAll(linked); err != nil {
		t.Fatal(err)
	}
	contains(t, "reconcile", must(t, "reconcile", proj), "worktree missing", "stale settings")
	contains(t, "reconcile --apply", must(t, "reconcile", proj, "--apply"), "Forgot")
	if out := must(t, "reconcile", proj); strings.Contains(out, "stale settings") {
		t.Errorf("stale settings remain after --apply:\n%s", out)
	}

	// 10. Cleanup: down keeps the main volumes; the repository stays clean.
	must(t, "down", proj)
	contains(t, "volumes", must(t, "volumes", proj), "kept")
	testutil.GitClean(t, main)
}
