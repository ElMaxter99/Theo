package api_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/api"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/update"
)

type updStatus struct {
	Current    string `json:"current"`
	Latest     string `json:"latest"`
	Available  bool   `json:"available"`
	Prerelease bool   `json:"prerelease"`
	Enabled    bool   `json:"enabled"`
	Channel    string `json:"channel"`
	CanInstall bool   `json:"can_install"`
	WhyNot     string `json:"why_not"`
	State      string `json:"state"`
	Installed  string `json:"installed"`
	ErrorCode  string `json:"error_code"`
	Desktop    bool   `json:"desktop"`
}

func withUpdates(list update.Lister, target *update.Target, desktop bool, restart func()) func(*api.Config) {
	return func(c *api.Config) {
		c.UpdateList, c.UpdateTarget, c.Desktop, c.Restart = list, target, desktop, restart
	}
}

func TestUpdateCheckChannelAndDismiss(t *testing.T) {
	old := engine.Version
	engine.Version = "0.11.0"
	t.Cleanup(func() { engine.Version = old })
	rels := []update.Release{{Tag: "v0.11.0"}, {Tag: "v0.12.0-rc.1", Prerelease: true}}
	calls := 0
	e := newEnv(t, withUpdates(func(context.Context) ([]update.Release, error) { calls++; return rels, nil }, nil, false, nil))

	var st updStatus
	if c := e.c.call("GET", "/api/v1/update", nil, &st); c != 200 || st.Current != "0.11.0" || st.Enabled || st.Channel != "stable" || st.Available {
		t.Fatalf("initial = %d %+v", c, st)
	}
	if calls != 0 {
		t.Error("GET never asks the network")
	}
	// Candidates only after opting in.
	e.c.call("POST", "/api/v1/update/check", nil, &st)
	if st.Available || calls != 1 {
		t.Fatalf("stable: %+v calls=%d", st, calls)
	}
	if c := e.c.call("POST", "/api/v1/update/settings", map[string]any{"channel": "rc"}, &st); c != 200 || st.Channel != "rc" {
		t.Fatalf("settings = %d %+v", c, st)
	}
	e.c.call("POST", "/api/v1/update/check", nil, &st)
	if !st.Available || st.Latest != "0.12.0-rc.1" || !st.Prerelease {
		t.Fatalf("rc: %+v", st)
	}
	var bad apiErr
	if c := e.c.call("POST", "/api/v1/update/settings", map[string]any{"channel": "nightly"}, &bad); c != 400 {
		t.Errorf("bad channel = %d", c)
	}
	if c := e.c.call("POST", "/api/v1/update/settings", map[string]any{}, &bad); c != 400 {
		t.Errorf("empty = %d", c)
	}
	e.c.call("POST", "/api/v1/update/settings", map[string]any{"auto_check": true, "channel": "stable"}, &st)
	if !st.Enabled || st.Available || st.Channel != "stable" {
		t.Errorf("back to stable hides the candidate: %+v", st)
	}
	if v := e.eng.Prefs().Get("update-check"); v != "on" {
		t.Errorf("stored %q", v)
	}
	if c := e.c.call("POST", "/api/v1/update/install", nil, &bad); c != 409 {
		t.Errorf("install with nothing new = %d", c)
	}
	if c := e.c.call("POST", "/api/v1/update/restart", nil, &bad); c != 409 {
		t.Errorf("restart in the browser = %d", c)
	}
}

func TestUpdateCheckOffline(t *testing.T) {
	old := engine.Version
	engine.Version = "0.11.0"
	t.Cleanup(func() { engine.Version = old })
	e := newEnv(t, withUpdates(func(context.Context) ([]update.Release, error) { return nil, context.DeadlineExceeded }, nil, false, nil))
	var st updStatus
	if c := e.c.call("POST", "/api/v1/update/check", nil, &st); c != 200 || st.ErrorCode != "check_failed" {
		t.Fatalf("%d %+v", c, st)
	}
}

func tgz(name, content string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "theo/" + name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(content))
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestUpdateInstallAndRestart(t *testing.T) {
	old := engine.Version
	engine.Version = "0.11.0"
	t.Cleanup(func() { engine.Version = old })
	archive := tgz("theo-desktop", "new desktop")
	sum := sha256.Sum256(archive)
	name := "theo-desktop_v0.11.1_linux_amd64.tar.gz"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case name:
			_, _ = w.Write(archive)
		case "checksums.txt":
			_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + name + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv(update.EnvAPI, srv.URL)
	rel := update.Release{Tag: "v0.11.1", Assets: []update.Asset{
		{Name: name, URL: srv.URL + "/dl/" + name}, {Name: "checksums.txt", URL: srv.URL + "/dl/checksums.txt"}}}

	dir := t.TempDir()
	exe := filepath.Join(dir, "theo-desktop")
	if err := os.WriteFile(exe, []byte("old desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	restarted := make(chan struct{}, 1)
	target := &update.Target{Exe: exe, GOOS: "linux", GOARCH: "amd64"}
	e := newEnv(t, withUpdates(func(context.Context) ([]update.Release, error) { return []update.Release{rel}, nil }, target, true, func() { restarted <- struct{}{} }))

	var st updStatus
	e.c.call("GET", "/api/v1/update", nil, &st)
	if !st.Enabled || !st.CanInstall || !st.Desktop {
		t.Fatalf("desktop default: %+v", st)
	}
	var bad apiErr
	if c := e.c.call("POST", "/api/v1/update/restart", nil, &bad); c != 409 {
		t.Errorf("restart before installing = %d", c)
	}
	e.c.call("POST", "/api/v1/update/check", nil, &st)
	if !st.Available || st.Latest != "0.11.1" {
		t.Fatalf("check: %+v", st)
	}
	if c := e.c.call("POST", "/api/v1/update/install", nil, &st); c != 202 {
		t.Fatalf("install = %d", c)
	}
	deadline := time.Now().Add(10 * time.Second)
	for st.State != "installed" && st.State != "error" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		e.c.call("GET", "/api/v1/update", nil, &st)
	}
	if st.State != "installed" || st.Installed != "v0.11.1" {
		t.Fatalf("state: %+v", st)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new desktop" {
		t.Errorf("the program was not replaced: %q", b)
	}
	if c := e.c.call("POST", "/api/v1/update/restart", nil, nil); c != 204 {
		t.Fatalf("restart = %d", c)
	}
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Error("the desktop app was not asked to restart")
	}
}

func TestUpdateInstallRefusedDownloadAndUnmanaged(t *testing.T) {
	old := engine.Version
	engine.Version = "0.11.0"
	t.Cleanup(func() { engine.Version = old })
	rel := update.Release{Tag: "v0.11.1", Assets: []update.Asset{{Name: "checksums.txt", URL: "https://evil.example/c"}}}
	// A binary that is not an installed Theo cannot update itself.
	e := newEnv(t, withUpdates(func(context.Context) ([]update.Release, error) { return []update.Release{rel}, nil }, &update.Target{Exe: "/tmp/x.test", GOOS: "linux", GOARCH: "amd64"}, false, nil))
	var st updStatus
	e.c.call("GET", "/api/v1/update", nil, &st)
	if st.CanInstall || st.WhyNot != "unmanaged" {
		t.Fatalf("%+v", st)
	}
	e.c.call("POST", "/api/v1/update/check", nil, &st)
	e.c.call("POST", "/api/v1/update/install", nil, &st)
	deadline := time.Now().Add(5 * time.Second)
	for st.State != "error" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		e.c.call("GET", "/api/v1/update", nil, &st)
	}
	if st.State != "error" || st.ErrorCode != "unmanaged" {
		t.Errorf("%+v", st)
	}
}
