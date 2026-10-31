package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/api"
	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/registry"
)

func TestReuseExisting(t *testing.T) {
	good := &api.Launch{URL: "http://127.0.0.1:5000/#n=x", Port: 5000, PID: 1}
	cases := []struct {
		name  string
		l     *api.Launch
		found bool
		want  bool
	}{
		{"running server answered", good, true, true},
		{"nothing found", nil, false, false},
		{"found but nil", nil, true, false},
		{"stale: answered without url", &api.Launch{Port: 5000}, true, false},
		{"stale: answered without port", &api.Launch{URL: good.URL}, true, false},
		{"not found despite a launch value", good, false, false},
	}
	for _, c := range cases {
		if got := ReuseExisting(c.l, c.found); got != c.want {
			t.Errorf("%s: ReuseExisting = %v, want %v", c.name, got, c.want)
		}
	}
}

func testEngine(t *testing.T, dir string) *engine.Engine {
	t.Helper()
	return engine.New(registry.Open(dir), git.Git{}, compose.Compose{}).WithDataDir(dir)
}

func TestShimCarriesTheOriginAndTheBinding(t *testing.T) {
	js := Shim(app)
	if !strings.Contains(js, `"`+app+`"`) || !strings.Contains(js, "window."+BindName+"(") {
		t.Fatalf("shim lacks the origin or the binding:\n%s", js)
	}
	if strings.Contains(js, "__ORIGIN__") || strings.Contains(js, "__BIND__") {
		t.Fatal("placeholders left in the shim")
	}
	// An origin is data, never code: a hostile value stays a JS string.
	hostile := Shim(`http://x"+alert(1)+"`)
	if strings.Contains(hostile, `"+alert(1)+"`) {
		t.Fatal("origin was not escaped")
	}
}

// fakeWindow records what Run does with the window and lets the test play the
// part of the page and of the user closing it.
type fakeWindow struct {
	mu      sync.Mutex
	init    string
	bound   map[string]func(string)
	nav     string
	destroy bool
	closeFn func(w *fakeWindow) // called inside Run: the "user"
	quit    chan struct{}
}

func newFakeWindow(closeFn func(w *fakeWindow)) *fakeWindow {
	return &fakeWindow{bound: map[string]func(string){}, closeFn: closeFn, quit: make(chan struct{}, 1)}
}
func (f *fakeWindow) Init(js string) { f.init = js }
func (f *fakeWindow) Bind(n string, fn func(string)) error {
	f.bound[n] = fn
	return nil
}
func (f *fakeWindow) Navigate(u string) { f.nav = u }
func (f *fakeWindow) Run() {
	if f.closeFn != nil {
		go f.closeFn(f)
	}
	<-f.quit
}
func (f *fakeWindow) Terminate() {
	select {
	case f.quit <- struct{}{}:
	default:
	}
}
func (f *fakeWindow) Destroy() { f.destroy = true }

// session performs the same nonce exchange as the page does.
func session(t *testing.T, launch string) int {
	t.Helper()
	u, err := url.Parse(launch)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"nonce": strings.TrimPrefix(u.Fragment, "n=")})
	req, _ := http.NewRequest("POST", u.Scheme+"://"+u.Host+"/api/v1/session", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func listening(addr string) bool {
	c := &http.Client{Timeout: time.Second}
	resp, err := c.Get("http://" + addr + "/api/v1/meta")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// A window that starts its own server: the launch URL goes straight to the
// window, the nonce works there, and closing the window stops the server and
// removes ui.lock.
func TestRunStartsServerAndStopsItWhenTheWindowCloses(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(registry.EnvHome, dir)
	var opened []string
	var win *fakeWindow
	var addr string
	win = newFakeWindow(func(w *fakeWindow) {
		w.mu.Lock()
		launch := w.nav
		w.mu.Unlock()
		if st := session(t, launch); st != http.StatusOK {
			t.Errorf("session exchange = %d", st)
		}
		u, _ := url.Parse(launch)
		addr = u.Host
		// The page asks for a container link and for something hostile.
		w.bound[BindName]("http://localhost:8080/")
		w.bound[BindName]("javascript:alert(1)")
		w.bound[BindName]("file:///etc/passwd")
		w.bound[BindName](launch)
		w.Terminate() // the user closes the window
	})
	var stderr bytes.Buffer
	err := Run(context.Background(), Options{
		Engine: testEngine(t, dir), DataDir: dir,
		NewWindow: func() (Window, error) { return win, nil },
		Open: func(raw, origin string) error {
			// Same filter as the real opener, without starting a browser.
			if Classify(raw, origin) != External {
				return fmt.Errorf("refusing %q", raw)
			}
			opened = append(opened, raw)
			return nil
		},
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(win.nav, "http://127.0.0.1:") || !strings.Contains(win.nav, "/#n=") {
		t.Errorf("window navigated to %q", win.nav)
	}
	if !strings.Contains(win.init, `"http://`+addr+`"`) {
		t.Errorf("shim was not given the server origin")
	}
	if !win.destroy {
		t.Error("window not destroyed")
	}
	if len(opened) != 1 || opened[0] != "http://localhost:8080/" {
		t.Errorf("opened %v, want only the container link", opened)
	}
	if strings.Count(stderr.String(), "refusing") != 3 {
		t.Errorf("expected 3 refusals (javascript, file, the app's own address), got: %q", stderr.String())
	}
	if _, err := os.Stat(api.LockPath(dir)); !os.IsNotExist(err) {
		t.Errorf("ui.lock still there: %v", err)
	}
	if listening(addr) {
		t.Error("the server still answers after the window closed")
	}
}

// With a server already running the window opens onto it and leaves it alone.
func TestRunReusesARunningServerAndLeavesItRunning(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(registry.EnvHome, dir)
	first, err := Start(context.Background(), Options{Engine: testEngine(t, dir), DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Stop()
	if first.Reused {
		t.Fatal("the first start reused something")
	}
	lockBefore, _ := os.ReadFile(api.LockPath(dir))
	addr := strings.TrimPrefix(first.Origin, "http://")
	if !listening(addr) {
		t.Fatal("first server not up")
	}

	win := newFakeWindow(func(w *fakeWindow) {
		w.mu.Lock()
		launch := w.nav
		w.mu.Unlock()
		if !strings.HasPrefix(launch, first.Origin+"/#n=") {
			t.Errorf("window not on the running server: %q", launch)
		}
		if st := session(t, launch); st != http.StatusOK {
			t.Errorf("session exchange on the reused server = %d", st)
		}
		w.Terminate()
	})
	var stderr bytes.Buffer
	if err := Run(context.Background(), Options{
		Engine: testEngine(t, dir), DataDir: dir, Stderr: &stderr,
		NewWindow: func() (Window, error) { return win, nil },
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "already running") {
		t.Errorf("no notice about reuse: %q", stderr.String())
	}
	if !listening(addr) {
		t.Fatal("closing a reusing window stopped the shared server")
	}
	if lockAfter, _ := os.ReadFile(api.LockPath(dir)); !bytes.Equal(lockBefore, lockAfter) {
		t.Error("ui.lock changed")
	}
}

// A ui.lock whose owner is gone is replaced, not trusted.
func TestStartReplacesAStaleLock(t *testing.T) {
	dir := t.TempDir()
	stale := `{"pid":999999,"port":1,"control_secret":"x","started_at":"2020-01-01T00:00:00Z"}`
	if err := os.WriteFile(api.LockPath(dir), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	be, err := Start(context.Background(), Options{Engine: testEngine(t, dir), DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if be.Reused {
		t.Fatal("reused a stale server")
	}
	be.Stop()
	be.Stop() // idempotent
}

func TestRunNeedsAWindowFactory(t *testing.T) {
	if err := Run(context.Background(), Options{}); err == nil {
		t.Fatal("want an error")
	}
}
