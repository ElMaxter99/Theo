package acceptance

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/cli"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

var visits = regexp.MustCompile(`visits:\s*(\d+)`)

func visitCount(t *testing.T, url string) int {
	t.Helper()
	body := get(t, url)
	m := visits.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no visit counter in %s:\n%s", url, body)
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// Steps 9 and 10 of the demo guide: a shared cache seen by two instances, and
// backup, restore and clone of volumes, driven through the command line with
// the real demo app.
func TestDemoGuideSharedAndData(t *testing.T) {
	testutil.RequireDaemon(t)
	requireDemoImages(t)

	const proj = "t7shd"
	second := proj + "-app-feature" // Compose project of the linked worktree
	port := testutil.FreePort(t)
	main, linked := testutil.NewRepo(t, demoFiles(t, proj, port))
	testutil.WriteFile(t, main, ".env", "GREETING=Hello from Theo\n")
	home := t.TempDir()
	t.Setenv(registry.EnvHome, home)
	t.Cleanup(func() {
		for _, p := range []string{proj, second, proj + "-shared"} {
			exec.Command("docker", "compose", "-p", p, "--profile", "worker", "down", "--volumes", "--remove-orphans").Run()
		}
		exec.Command("docker", "network", "rm", "theo-"+proj+"-shared").Run()
		exec.Command("docker", "image", "rm", proj+"-web", proj+"-worker", second+"-web", second+"-worker", proj+"-shared-web", proj+"-shared-worker").Run()
	})
	_ = linked
	must(t, "project", "add", main, "--name", proj)

	// 9. Share the cache. Nothing runs yet, so there is no leftover copy.
	must(t, "shared", "add", proj, "cache")
	must(t, "shared", "use", proj, "-w", "main", "cache")
	must(t, "shared", "use", proj, "-w", "feature/x", "cache")
	must(t, "up", proj, "--start-shared", "--build")
	must(t, "up", proj, "-w", "feature/x", "--auto-ports", "--start-shared", "--build")
	second1, second2 := fmt.Sprintf("http://localhost:%d/", port), fmt.Sprintf("http://localhost:%d/", port+1)

	// The counter lives in the shared cache: it grows across both instances.
	a := visitCount(t, second1)
	b := visitCount(t, second2)
	c := visitCount(t, second1)
	if !(b > a && c > b) {
		t.Errorf("the counter should keep growing across instances: %d, %d, %d", a, b, c)
	}
	contains(t, "status", must(t, "shared", "status", proj), "running", "Used by 2 instance(s)")
	if got := testutil.Docker(t, "ps", "--filter", "label=com.docker.compose.project="+proj+"-shared", "--format", "{{.Names}}"); len(strings.Fields(got)) != 1 {
		t.Errorf("the cache should run once, got %q", got)
	}
	_, stderr, code := theo("shared", "down", proj)
	if code == cli.ExitOK {
		t.Fatalf("shared down with running instances succeeded")
	}
	contains(t, "refusal", stderr, "feature/x", "--confirm")

	// Both instances stop, then the shared cache; the data is kept.
	must(t, "down", proj)
	must(t, "down", proj, "-w", "feature/x")
	must(t, "shared", "down", proj)
	if !strings.Contains(testutil.Docker(t, "volume", "ls", "--format", "{{.Name}}"), proj+"-shared_cachedata") {
		t.Errorf("the shared data was not kept")
	}

	// 10. Backup, restore and clone of the instances' own volumes (their data).
	backups := t.TempDir()
	out := must(t, "volumes", "backup", proj, "-w", "main", "--volume", "data", "--out", backups)
	contains(t, "backup", out, ".tar.gz")
	archives, _ := filepath.Glob(filepath.Join(backups, "*data*.tar.gz"))
	if len(archives) != 1 {
		t.Fatalf("backups in %s: %v\n%s", backups, archives, out)
	}
	// Restoring over a volume that has data needs --replace and the confirmation.
	if _, _, code := theo("volumes", "restore", proj, "-w", "main", "--from", archives[0], "--confirm", proj); code == cli.ExitOK {
		t.Errorf("restore over a non-empty volume without --replace succeeded")
	}
	must(t, "volumes", "restore", proj, "-w", "main", "--from", archives[0], "--replace", "--confirm", proj)
	must(t, "volumes", "clone", proj, "--from", "main", "-w", "feature/x", "--volume", "data", "--replace", "--confirm", second)
	if _, _, code := theo("volumes", "clone", proj, "--from", "main", "-w", "feature/x", "--volume", "data"); code == cli.ExitOK {
		t.Errorf("clone without --confirm succeeded")
	}

	// Data and declaration are removed as separate, explicit steps.
	// Purging is refused while instances are linked to the shared services.
	if _, _, code := theo("shared", "purge", proj, "--confirm", proj+"-shared"); code == cli.ExitOK {
		t.Errorf("purge with linked instances succeeded")
	}
	must(t, "shared", "unuse", proj, "-w", "main")
	must(t, "shared", "unuse", proj, "-w", "feature/x")
	must(t, "shared", "purge", proj, "--confirm", proj+"-shared")
	must(t, "shared", "remove", proj)
	testutil.GitClean(t, main)
}
