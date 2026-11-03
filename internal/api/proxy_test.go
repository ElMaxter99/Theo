package api_test

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/api"
	"github.com/ElMaxter99/Theo/internal/proxy"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

type proxyStatus struct {
	Running   bool   `json:"running"`
	Managed   bool   `json:"managed"`
	Port      int    `json:"port"`
	URL       string `json:"url"`
	Autostart bool   `json:"autostart"`
}

func TestInterfaceRunsTheProxy(t *testing.T) {
	e := newEnv(t)
	var st proxyStatus
	if code := e.c.call("GET", "/api/v1/proxy", nil, &st); code != 200 || st.Running || st.Autostart {
		t.Fatalf("GET /proxy before = %d %+v", code, st)
	}
	port := freePort(t)
	if code := e.c.call("POST", "/api/v1/proxy/start", map[string]int{"port": port}, &st); code != 200 || !st.Running || !st.Managed || st.Port != port {
		t.Fatalf("start = %d %+v", code, st)
	}
	t.Cleanup(func() { e.c.call("POST", "/api/v1/proxy/stop", map[string]any{}, nil) })

	// Like `theo proxy`, it records itself, so `theo status` and MCP see it.
	if rec, ok := proxy.Running(e.dataDir); !ok || rec.Port != port {
		t.Errorf("proxy.json = %+v %v", rec, ok)
	}
	if data, err := os.ReadFile(filepath.Join(e.dataDir, "proxy-ui.json")); err != nil || !strings.Contains(string(data), `"autostart": true`) {
		t.Errorf("proxy-ui.json = %s %v", data, err)
	}

	// The overview carries the local domains while it runs.
	var ov struct {
		Worktrees []struct {
			Main      bool
			ProxyURLs []struct {
				Service, URL string
				Default      bool
			} `json:"proxy_urls"`
		}
	}
	e.c.call("GET", "/api/v1/projects/ubapp/overview", nil, &ov)
	want := "http://main.ubapp.localhost:" + strconv.Itoa(port)
	if len(ov.Worktrees) != 2 || len(ov.Worktrees[0].ProxyURLs) == 0 || ov.Worktrees[0].ProxyURLs[0].URL != want || !ov.Worktrees[0].ProxyURLs[0].Default {
		t.Errorf("overview proxy_urls = %+v, want %s first", ov.Worktrees, want)
	}

	// It answers: an unknown name gets the list of routes.
	req, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/", nil)
	req.Host = "nope.ubapp.localhost"
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("unknown name = %d, want 404", resp.StatusCode)
	}

	if code := e.c.call("POST", "/api/v1/proxy/stop", map[string]any{}, &st); code != 200 || st.Running || st.Autostart {
		t.Fatalf("stop = %d %+v", code, st)
	}
	if _, ok := proxy.Running(e.dataDir); ok {
		t.Error("proxy.json still says a proxy runs")
	}
	ov.Worktrees = nil
	e.c.call("GET", "/api/v1/projects/ubapp/overview", nil, &ov)
	if len(ov.Worktrees[0].ProxyURLs) != 0 {
		t.Errorf("proxy_urls after stop = %+v", ov.Worktrees[0].ProxyURLs)
	}
}

func TestInterfaceLeavesAnExternalProxyAlone(t *testing.T) {
	e := newEnv(t)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	remove, err := proxy.WriteState(e.dataDir, proxy.State{Addr: "127.0.0.1", Port: port, PID: 4242})
	if err != nil {
		t.Fatal(err)
	}
	defer remove()

	var st proxyStatus
	if code := e.c.call("GET", "/api/v1/proxy", nil, &st); code != 200 || !st.Running || st.Managed || st.Port != port {
		t.Fatalf("GET /proxy = %d %+v", code, st)
	}
	var ae apiErr
	if code := e.c.call("POST", "/api/v1/proxy/start", map[string]any{}, &ae); code != 409 || ae.Error.Code != "proxy_running" {
		t.Errorf("start with `theo proxy` running = %d %+v", code, ae.Error)
	}
	if code := e.c.call("POST", "/api/v1/proxy/stop", map[string]any{}, &ae); code != 409 || ae.Error.Code != "proxy_external" {
		t.Errorf("stop of `theo proxy` = %d %+v", code, ae.Error)
	}
}

func TestInterfaceProxyPortInUse(t *testing.T) {
	e := newEnv(t)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var ae apiErr
	if code := e.c.call("POST", "/api/v1/proxy/start", map[string]int{"port": ln.Addr().(*net.TCPAddr).Port}, &ae); code != 409 || ae.Error.Code != "port_in_use" {
		t.Errorf("start on a taken port = %d %+v", code, ae.Error)
	}
}

func TestInterfaceProxyAutostart(t *testing.T) {
	port := freePort(t)
	e := newEnv(t, func(c *api.Config) {
		os.WriteFile(filepath.Join(c.DataDir, "proxy-ui.json"), []byte(`{"autostart": true, "port": `+strconv.Itoa(port)+`}`), 0o600)
	})
	var st proxyStatus
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.c.call("GET", "/api/v1/proxy", nil, &st)
		if st.Running || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !st.Running || !st.Managed || st.Port != port || !st.Autostart {
		t.Fatalf("after a start with the switch on: %+v", st)
	}
	e.c.call("POST", "/api/v1/proxy/stop", map[string]any{}, nil)
}

func TestInterfaceProxyServesHTTPS(t *testing.T) {
	e := newEnv(t)
	port, httpsPort := freePort(t), freePort(t)
	var st struct {
		proxyStatus
		HTTPS     bool `json:"https"`
		HTTPSPort int  `json:"https_port"`
		Authority *struct {
			Path         string `json:"path"`
			Trusted      bool   `json:"trusted"`
			CanTrustHere bool   `json:"can_trust_here"`
			Command      string `json:"command"`
		} `json:"authority"`
	}
	if code := e.c.call("POST", "/api/v1/proxy/start", map[string]any{"port": port, "https": true, "https_port": httpsPort}, &st); code != 200 || !st.HTTPS || st.HTTPSPort != httpsPort {
		t.Fatalf("start with HTTPS = %d %+v", code, st)
	}
	t.Cleanup(func() { e.c.call("POST", "/api/v1/proxy/stop", map[string]any{}, nil) })
	if st.Authority == nil || st.Authority.Path != filepath.Join(e.dataDir, "proxy", "ca.pem") || st.Authority.Command != "theo proxy trust" {
		t.Fatalf("authority = %+v", st.Authority)
	}
	if rec, ok := proxy.Running(e.dataDir); !ok || rec.HTTPSPort != httpsPort {
		t.Errorf("proxy.json = %+v %v", rec, ok)
	}
	if data, _ := os.ReadFile(filepath.Join(e.dataDir, "proxy-ui.json")); !strings.Contains(string(data), `"https": true`) {
		t.Errorf("proxy-ui.json does not remember HTTPS: %s", data)
	}

	var ov struct {
		Worktrees []struct {
			ProxyURLs []struct {
				HTTPSURL string `json:"https_url"`
			} `json:"proxy_urls"`
		}
	}
	e.c.call("GET", "/api/v1/projects/ubapp/overview", nil, &ov)
	if want := "https://main.ubapp.localhost:" + strconv.Itoa(httpsPort); len(ov.Worktrees) == 0 || len(ov.Worktrees[0].ProxyURLs) == 0 || ov.Worktrees[0].ProxyURLs[0].HTTPSURL != want {
		t.Errorf("overview proxy_urls = %+v, want %s", ov.Worktrees, want)
	}

	// A browser that trusts the authority gets a valid certificate.
	ca, err := proxy.LoadCA(e.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	conn, err := tls.Dial("tcp", "127.0.0.1:"+strconv.Itoa(httpsPort), &tls.Config{ServerName: "main.ubapp.localhost", RootCAs: roots})
	if err != nil {
		t.Fatalf("TLS handshake: %v", err)
	}
	conn.Close()

	if runtime.GOOS == "linux" {
		var ae struct {
			Error struct{ Code, Hint string }
		}
		if code := e.c.call("POST", "/api/v1/proxy/trust", map[string]any{}, &ae); code != 409 || ae.Error.Code != "needs_terminal" || !strings.Contains(ae.Error.Hint, "theo proxy trust") {
			t.Errorf("trust on Linux = %d %+v", code, ae)
		}
	}
}
