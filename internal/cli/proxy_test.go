package cli

import (
	"encoding/json"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/proxy"
	"github.com/ElMaxter99/Theo/internal/registry"
)

func TestProxyUsage(t *testing.T) {
	t.Setenv(registry.EnvHome, t.TempDir())
	if out, _, _ := run("help", "all"); !strings.Contains(out, "proxy routes [project]") || !strings.Contains(out, "<service>.<instance>.<project>.localhost") {
		t.Errorf("help lacks the proxy:\n%s", out)
	}
	for _, args := range [][]string{
		{"proxy", "extra"},
		{"proxy", "--port", "0"},
		{"proxy", "--port", "70000"},
		{"proxy", "--bogus"},
		{"proxy", "routes", "a", "b"},
		{"proxy", "routes", "--json", "--hosts"},
	} {
		if _, _, code := run(args...); code != ExitUsage {
			t.Errorf("%v: code %d, want %d", args, code, ExitUsage)
		}
	}
	// A non-loopback address is refused unless asked for, before listening.
	for _, addr := range []string{"0.0.0.0", "192.0.2.1", "::"} {
		_, stderr, code := run("proxy", "--addr", addr)
		if code != ExitError || !strings.Contains(stderr, "--allow-remote") {
			t.Errorf("--addr %s: code %d, %s", addr, code, stderr)
		}
	}
	// A port that is taken is a clear error.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	if _, stderr, code := run("proxy", "--port", port); code != ExitError || !strings.Contains(stderr, "--port") {
		t.Errorf("taken port: code %d, %s", code, stderr)
	}
}

func TestProxyRoutesEmpty(t *testing.T) {
	t.Setenv(registry.EnvHome, t.TempDir())
	out, stderr, code := run("proxy", "routes")
	if code != ExitOK || !strings.Contains(out, "No routes") || !strings.Contains(stderr, "no proxy is running on port 7080") {
		t.Errorf("routes: %d %q %q", code, out, stderr)
	}
	out, _, code = run("proxy", "routes", "--json", "--port", "7999")
	var doc proxyRoutes
	if code != ExitOK || json.Unmarshal([]byte(out), &doc) != nil || doc.Port != 7999 || doc.Running || doc.Routes == nil {
		t.Errorf("routes --json: %d %s", code, out)
	}
	if !strings.Contains(out, `"routes": []`) {
		t.Errorf("an empty list must be [], got %s", out)
	}
	out, _, code = run("proxy", "routes", "--hosts")
	if code != ExitOK || !strings.HasPrefix(out, "# Theo proxy") {
		t.Errorf("routes --hosts: %d %q", code, out)
	}
	if _, stderr, code := run("proxy", "routes", "nope"); code != ExitError || !strings.Contains(stderr, "nope") {
		t.Errorf("unknown project: %d %s", code, stderr)
	}
}

func TestProxyRunningState(t *testing.T) {
	home := t.TempDir()
	t.Setenv(registry.EnvHome, home)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	remove, err := proxy.WriteState(home, proxy.State{Addr: "127.0.0.1", Port: port, PID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	out, stderr, code := run("proxy", "routes", "--json")
	var doc proxyRoutes
	if code != ExitOK || json.Unmarshal([]byte(out), &doc) != nil || doc.Port != port || !doc.Running {
		t.Errorf("routes with a running proxy: %d %s %s", code, out, stderr)
	}

	r := &engine.StatusReport{Projects: []engine.ProjectStatus{{Name: "shop", Worktrees: []engine.WorktreeStatus{{
		Path: "/s", Branch: "main", Kind: engine.KindMain, Instance: &engine.InstanceStatus{ComposeProject: "shop",
			Ports: []engine.PortStatus{{Service: "web", ContainerPort: 80, Protocol: "tcp", HostPort: 8080, URL: "http://localhost:8080", Status: engine.PortRunning}}},
	}}}}}
	addProxyURLs(r)
	want := []string{"http://main.shop.localhost:" + strconv.Itoa(port), "http://web.main.shop.localhost:" + strconv.Itoa(port)}
	if got := r.Projects[0].Worktrees[0].Instance.ProxyURLs; !reflect.DeepEqual(got, want) {
		t.Errorf("proxy urls: %v", got)
	}
	var b strings.Builder
	if err := printStatus(&b, r); err != nil || !strings.Contains(b.String(), "proxy main: "+want[0]) {
		t.Errorf("status text:\n%s", b.String())
	}
}

func TestProxyCompletion(t *testing.T) {
	t.Setenv(registry.EnvHome, t.TempDir())
	if got := suggest(t, "", "proxy"); !reflect.DeepEqual(got, []string{"routes", "trust", "untrust"}) {
		t.Errorf("proxy = %v", got)
	}
	if got := suggest(t, "--", "proxy"); !strings.Contains(strings.Join(got, " "), "--allow-remote") || !strings.Contains(strings.Join(got, " "), "--https-port") {
		t.Errorf("proxy flags = %v", got)
	}
	if got := suggest(t, "--ho", "proxy", "routes"); !reflect.DeepEqual(got, []string{"--hosts"}) {
		t.Errorf("proxy routes --ho = %v", got)
	}
}
