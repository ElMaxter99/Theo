package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// serve --print-launch-url goes through the real nonce exchange: there is no
// back door for tests.
func TestServePrintLaunchURL(t *testing.T) {
	main, _ := testutil.NewRepo(t, nil)
	home := t.TempDir()
	t.Setenv(registry.EnvHome, home)
	if _, stderr, code := run("project", "add", main, "--name", "ubserve"); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}

	pr, pw := io.Pipe()
	done := make(chan int, 1)
	var errb bytes.Buffer
	go func() {
		done <- Run([]string{"serve", "--print-launch-url", "--idle-timeout", "3s"}, pw, &errb)
		pw.Close()
	}()
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatalf("no launch line: %v (%s)", err, errb.String())
	}
	var l struct {
		URL  string `json:"url"`
		Port int    `json:"port"`
		PID  int    `json:"pid"`
	}
	if err := json.Unmarshal([]byte(line), &l); err != nil || l.Port == 0 || l.PID != os.Getpid() {
		t.Fatalf("launch line %q: %v", line, err)
	}
	u, _ := url.Parse(l.URL)
	if u.Host != "127.0.0.1:"+itoaPort(l.Port) || !strings.HasPrefix(u.Fragment, "n=") || u.RawQuery != "" {
		t.Fatalf("launch URL %q", l.URL)
	}

	// Exchange the nonce, then read the projects through the API.
	base := "http://127.0.0.1:" + itoaPort(l.Port)
	hc := &http.Client{Timeout: 5 * time.Second}
	body, _ := json.Marshal(map[string]string{"nonce": strings.TrimPrefix(u.Fragment, "n=")})
	req, _ := http.NewRequest("POST", base+"/api/v1/session", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("exchange: %v %v", resp, err)
	}
	cookies := resp.Cookies()
	resp.Body.Close()
	get := func(path string) (int, string) {
		r, _ := http.NewRequest("GET", base+path, nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		rs, err := hc.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer rs.Body.Close()
		b, _ := io.ReadAll(rs.Body)
		return rs.StatusCode, string(b)
	}
	if st, b := get("/api/v1/projects"); st != 200 || !strings.Contains(b, `"ubserve"`) {
		t.Errorf("projects = %d %s", st, b)
	}
	if st, b := get("/"); st != 200 || !strings.Contains(b, "<") {
		t.Errorf("shell = %d", st)
	}
	// The nonce is spent.
	req2, _ := http.NewRequest("POST", base+"/api/v1/session", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	if r2, err := hc.Do(req2); err != nil || r2.StatusCode != 401 {
		t.Errorf("nonce reuse: %v %v", r2, err)
	}

	// A second start finds ui.lock and reuses the running server.
	var out2, err2 bytes.Buffer
	if code := Run([]string{"serve", "--print-launch-url"}, &out2, &err2); code != ExitOK {
		t.Fatalf("second serve: %d %s", code, err2.String())
	}
	var l2 struct {
		URL  string `json:"url"`
		Port int    `json:"port"`
		PID  int    `json:"pid"`
	}
	json.Unmarshal(out2.Bytes(), &l2)
	if l2.Port != l.Port || l2.PID != l.PID || l2.URL == l.URL || !strings.Contains(l2.URL, "#n=") {
		t.Errorf("second serve = %+v, first %+v", l2, l)
	}

	// With no stream and no operation the server stops by itself, and takes
	// ui.lock with it.
	select {
	case code := <-done:
		if code != ExitOK {
			t.Errorf("serve exited %d: %s", code, errb.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serve did not stop after the idle timeout")
	}
	if _, err := os.Stat(home + "/ui.lock"); err == nil {
		t.Error("ui.lock was left behind")
	}
}

func itoaPort(n int) string { b, _ := json.Marshal(n); return string(b) }
