package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// requireHelper skips the test when the helper image is not available and
// cannot be pulled.
func requireHelper(t *testing.T, image string) {
	t.Helper()
	if exec.Command("docker", "image", "inspect", image).Run() == nil {
		return
	}
	if out, err := exec.Command("docker", "pull", "--quiet", image).CombinedOutput(); err != nil {
		t.Skipf("helper image %s cannot be pulled: %v\n%s", image, err, out)
	}
}

// volumeDigest describes the files of a volume as a helper container sees
// them: content hashes, mode and owner.
func volumeDigest(t *testing.T, volume string) string {
	t.Helper()
	return testutil.Docker(t, "run", "--rm", "--network", "none", "-v", volume+":/data:ro", "alpine:3.20", "sh", "-c",
		"cd /data && sha256sum a.txt sub/b.bin .hidden && stat -c '%n %a %u' a.txt sub/b.bin .hidden")
}

// TestVolumeBackupRestoreClone runs the data-safety commands with a real
// Docker daemon: backup while running, restore into a fresh instance,
// clone between two worktrees, the refusals, and purge --backup.
func TestVolumeBackupRestoreClone(t *testing.T) {
	testutil.RequireDaemon(t)
	requireHelper(t, "alpine:3.20")
	main, linked := testutil.NewRepo(t, map[string]string{
		".gitignore": "server\nDockerfile\n",
		"compose.yaml": `name: t6vd
services:
  web:
    build: .
    volumes: [data:/data, ext:/ext]
volumes:
  data: {}
  ext:
    external: true
    name: t6-vd-ext
`,
	})
	testutil.WriteServerImage(t, main)
	testutil.WriteServerImage(t, linked)
	testutil.Docker(t, "volume", "create", "t6-vd-ext")
	home := t.TempDir()
	t.Setenv(registry.EnvHome, home)
	t.Cleanup(func() {
		run("down", "t6app")
		run("down", "t6app", "-w", "feature/x")
		testutil.Docker(t, "volume", "rm", "--force", "t6vd_data", "t6app-app-feature_data", "t6-vd-ext")
		testutil.Docker(t, "image", "rm", "--force", "t6vd-web", "t6app-app-feature-web")
	})
	must := func(args ...string) (string, string) {
		t.Helper()
		stdout, stderr, code := run(args...)
		if code != ExitOK {
			t.Fatalf("theo %s: exit %d\n%s\n%s", strings.Join(args, " "), code, stdout, stderr)
		}
		return stdout, stderr
	}
	refused := func(want string, args ...string) {
		t.Helper()
		stdout, stderr, code := run(args...)
		if code == ExitOK {
			t.Fatalf("theo %s succeeded, want a refusal containing %q\n%s", strings.Join(args, " "), want, stdout)
		}
		checkOutput(t, "theo "+strings.Join(args, " "), stderr, want)
	}

	must("project", "add", main, "--name", "t6app")
	must("up", "t6app")
	// Files with a hidden name, a binary, a mode and a subdirectory.
	testutil.Docker(t, "run", "--rm", "-v", "t6vd_data:/data", "alpine:3.20", "sh", "-c",
		"cd /data && echo hello > a.txt && mkdir sub && head -c 70000 /dev/urandom > sub/b.bin && chmod 640 sub/b.bin && chown 1000 sub/b.bin && echo hidden > .hidden")
	want := volumeDigest(t, "t6vd_data")

	// Backup while the instance runs: works, warns.
	out := filepath.Join(t.TempDir(), "backups")
	stdout, stderr := must("volumes", "backup", "t6app", "--out", out)
	checkOutput(t, "backup warning", stderr, "inconsistent")
	checkOutput(t, "backup output", stdout, "Backed up data")
	entries, _ := os.ReadDir(out)
	var archive, manifest string
	for _, en := range entries {
		switch {
		case strings.HasSuffix(en.Name(), ".tar.gz"):
			archive = filepath.Join(out, en.Name())
		case strings.HasSuffix(en.Name(), ".json"):
			manifest = filepath.Join(out, en.Name())
		}
	}
	if archive == "" || manifest == "" || len(entries) != 2 {
		t.Fatalf("backup files = %v", entries)
	}
	for _, part := range []string{"t6app", "t6vd", "data"} {
		checkOutput(t, "file name", filepath.Base(archive), part)
	}
	var m map[string]any
	raw, _ := os.ReadFile(manifest)
	if err := json.Unmarshal(raw, &m); err != nil || m["volume"] != "data" || m["instance"] != "t6vd" || m["helper_image"] != "alpine:3.20" || m["sha256"] == "" || m["size"] == nil {
		t.Fatalf("manifest = %s (%v)", raw, err)
	}
	// The default directory is under the Theo data dir.
	must("down", "t6app")
	must("volumes", "backup", "t6app")
	if m, _ := filepath.Glob(filepath.Join(home, "backups", "t6app", "*.tar.gz")); len(m) != 1 {
		t.Errorf("default backup directory has %v", m)
	}

	// External volumes and unknown ones are refused.
	refused("external", "volumes", "backup", "t6app", "--volume", "ext", "--out", out)
	refused("has no volume", "volumes", "backup", "t6app", "--volume", "nope", "--out", out)

	// Restore into the other worktree's fresh instance.
	feat := "t6app-app-feature"
	refused("--confirm "+feat, "volumes", "restore", "t6app", "-w", "feature/x", "--from", archive)
	if strings.Contains(testutil.Docker(t, "volume", "ls", "--format", "{{.Name}}"), feat+"_data") {
		t.Fatal("restore without confirmation created the volume")
	}
	must("volumes", "restore", "t6app", "-w", "feature/x", "--from", archive, "--confirm", feat)
	if got := volumeDigest(t, feat+"_data"); got != want {
		t.Errorf("restored data differs:\n%s\nwant\n%s", got, want)
	}
	labels := testutil.Docker(t, "volume", "inspect", "--format", "{{index .Labels \"com.docker.compose.project\"}}/{{index .Labels \"com.docker.compose.volume\"}}", feat+"_data")
	if labels != feat+"/data" {
		t.Errorf("labels = %q", labels)
	}

	// Compose adopts the restored volume without warnings.
	_, stderr = must("up", "t6app", "-w", "feature/x")
	if strings.Contains(stderr, "not created by") || strings.Contains(stderr, "already exists") {
		t.Errorf("compose complained about the restored volume:\n%s", stderr)
	}
	stdout, _ = must("volumes", "t6app", "-w", "feature/x")
	checkOutput(t, "volumes", stdout, "in use")

	// Refused while the instance has containers.
	refused("theo down", "volumes", "restore", "t6app", "-w", "feature/x", "--from", archive, "--replace", "--confirm", feat)
	refused("theo down", "volumes", "clone", "t6app", "--from", "main", "-w", "feature/x", "--replace", "--confirm", feat)
	must("down", "t6app", "-w", "feature/x")

	// Non-empty: needs --replace. Change the data to see it replaced.
	testutil.Docker(t, "run", "--rm", "-v", feat+"_data:/data", "alpine:3.20", "sh", "-c", "echo changed > /data/a.txt")
	refused("--replace", "volumes", "restore", "t6app", "-w", "feature/x", "--from", archive, "--confirm", feat)
	refused("--replace", "volumes", "clone", "t6app", "--from", "main", "-w", "feature/x", "--confirm", feat)
	if !strings.Contains(testutil.Docker(t, "run", "--rm", "-v", feat+"_data:/data:ro", "alpine:3.20", "cat", "/data/a.txt"), "changed") {
		t.Fatal("a refused restore or clone changed the data")
	}
	// A tampered archive is refused.
	bad := filepath.Join(t.TempDir(), filepath.Base(archive))
	data, _ := os.ReadFile(archive)
	data[len(data)/2] ^= 0xff
	os.WriteFile(bad, data, 0o600)
	os.WriteFile(strings.TrimSuffix(bad, ".tar.gz")+".json", raw, 0o600)
	refused("sha256", "volumes", "restore", "t6app", "-w", "feature/x", "--from", bad, "--replace", "--confirm", feat)

	// Clone main -> feature over the changed data.
	refused("external", "volumes", "clone", "t6app", "--from", "main", "-w", "feature/x", "--volume", "ext", "--replace", "--confirm", feat)
	refused("same instance", "volumes", "clone", "t6app", "--from", "feature/x", "-w", "feature/x", "--confirm", feat)
	must("volumes", "clone", "t6app", "--from", "main", "-w", "feature/x", "--replace", "--confirm", feat)
	if got := volumeDigest(t, feat+"_data"); got != want {
		t.Errorf("cloned data differs:\n%s\nwant\n%s", got, want)
	}
	if got := volumeDigest(t, "t6vd_data"); got != want {
		t.Errorf("the clone changed the source:\n%s", got)
	}

	// purge --backup: nothing is deleted when refused; the backup is taken first.
	pout := filepath.Join(t.TempDir(), "purge")
	refused("for good", "volumes", "purge", "t6app", "-w", "feature/x", "--backup", "--out", pout)
	if _, err := os.Stat(pout); err == nil {
		t.Error("a refused purge took a backup")
	}
	must("volumes", "purge", "t6app", "-w", "feature/x", "--backup", "--out", pout, "--confirm", feat)
	if strings.Contains(testutil.Docker(t, "volume", "ls", "--format", "{{.Name}}"), feat+"_data") {
		t.Error("the volume is still there after the purge")
	}
	purged, _ := filepath.Glob(filepath.Join(pout, "*.tar.gz"))
	if len(purged) != 1 {
		t.Fatalf("purge backups = %v", purged)
	}
	// And that backup brings the data back.
	must("volumes", "restore", "t6app", "-w", "feature/x", "--from", purged[0], "--confirm", feat)
	if got := volumeDigest(t, feat+"_data"); got != want {
		t.Errorf("data restored from the purge backup differs:\n%s", got)
	}

	stdout, _ = must("history", "t6app", "-w", "feature/x")
	for _, op := range []string{"volumes restore", "volumes clone", "volumes backup", "volumes purge"} {
		checkOutput(t, "history", stdout, op)
	}
	testutil.GitClean(t, main)
	testutil.GitClean(t, linked)
}
