package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestUpdateRealBinary builds the real theo program, installs it in a folder
// of its own, and lets `theo update` replace it with a release served by a
// local server: the same steps the app takes, in a real process.
func TestUpdateRealBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the archive of this test is a .tar.gz; Windows uses a .zip")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "theo")
	build := exec.Command("go", "build", "-ldflags", "-X github.com/ElMaxter99/Theo/internal/engine.Version=0.11.0", "-o", bin, "github.com/ElMaxter99/Theo/cmd/theo")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	// The "new version" is a small program that says so, packed like a release.
	newProgram := []byte("#!/bin/sh\necho theo v0.11.1 replaced\n")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	arch := "theo_v0.11.1_" + runtime.GOOS + "_" + runtime.GOARCH
	_ = tw.WriteHeader(&tar.Header{Name: arch + "/theo", Mode: 0o755, Size: int64(len(newProgram)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(newProgram)
	_ = tw.Close()
	_ = gz.Close()
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	name := arch + ".tar.gz"

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases"):
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"tag_name": "v0.11.1", "html_url": srv.URL + "/notes", "assets": []map[string]any{
					{"name": name, "browser_download_url": srv.URL + "/dl/" + name, "size": len(archive)},
					{"name": "checksums.txt", "browser_download_url": srv.URL + "/dl/checksums.txt"},
				}},
				{"tag_name": "v0.12.0-rc.1", "prerelease": true, "html_url": srv.URL + "/rc"},
				{"tag_name": "v0.11.0"},
			})
		case strings.HasSuffix(r.URL.Path, "/"+name):
			_, _ = w.Write(archive)
		case strings.HasSuffix(r.URL.Path, "/checksums.txt"):
			_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + name + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	run := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "THEO_HOME="+t.TempDir(), "THEO_UPDATE_API="+srv.URL+"/releases")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	out, err := run("update", "--check")
	if err != nil || !strings.Contains(out, "A new version exists: 0.11.1") || strings.Contains(out, "rc.1") {
		t.Fatalf("--check (stable channel): %v\n%s", err, out)
	}
	if got, _ := run("version"); !strings.HasPrefix(got, "theo 0.11.0") && !strings.HasPrefix(got, "theo v0.11.0") {
		t.Fatalf("version before: %q", got)
	}
	out, err = run("update")
	if err != nil || !strings.Contains(out, "Theo is now 0.11.1") {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(bin); string(b) != string(newProgram) {
		t.Fatalf("the program was not replaced: %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.old")); len(left) != 0 {
		t.Errorf("leftovers: %v", left)
	}
}
