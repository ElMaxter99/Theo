package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/proxy"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// getVia fetches path through the proxy at addr with the given Host header,
// until it answers 200 (the web service may still be starting).
func getVia(t *testing.T, addr, host, path string) string {
	t.Helper()
	var last error
	for i := 0; i < 40; i++ {
		req, _ := http.NewRequest("GET", "http://"+addr+path, nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return string(body)
			}
			last = fmt.Errorf("status %d: %s", resp.StatusCode, body)
		} else {
			last = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("GET %s via the proxy (Host %s): %v", path, host, last)
	return ""
}

// The demo app, two instances, reached by name through the proxy.
func TestProxyDemo(t *testing.T) {
	testutil.RequireDaemon(t)
	requireDemoImages(t)

	const (
		proj   = "pxyt-demo"
		second = "pxyt-demo-app-feature" // Compose project of the linked worktree
	)
	port := testutil.FreePort(t)
	main, _ := testutil.NewRepo(t, demoFiles(t, proj, port))
	home := t.TempDir()
	t.Setenv(registry.EnvHome, home)
	t.Cleanup(func() {
		for _, p := range []string{proj, second} {
			exec.Command("docker", "compose", "-p", p, "--profile", "worker", "down", "--volumes", "--remove-orphans").Run()
		}
		exec.Command("docker", "image", "rm", proj+"-web", second+"-web").Run()
	})

	must(t, "project", "add", main, "--name", proj)
	testutil.WriteFile(t, main, ".env", "GREETING=Hello through the proxy\n")
	must(t, "up", proj, "--build")
	must(t, "instance", "set", proj, "-w", "feature/x", "--neutralize-container-names")
	t.Setenv("BRANCH", "feature-x")
	must(t, "up", proj, "-w", "feature/x", "--auto-ports", "--build")

	// The proxy, on a free port, as `theo proxy` runs it.
	e, err := engine.Open()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	pport := ln.Addr().(*net.TCPAddr).Port
	h := &proxy.Handler{Source: proxy.EngineSource(e.EnableConfigCache(5*time.Second), ""), Port: pport}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- proxy.Serve(ctx, ln, h) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	}()
	remove, err := proxy.WriteState(home, proxy.State{Addr: "127.0.0.1", Port: pport, PID: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	addr := ln.Addr().String()

	contains(t, "main short form", getVia(t, addr, fmt.Sprintf("main.%s.localhost:%d", proj, pport), "/"), "greeting: Hello through the proxy", "instance: main")
	contains(t, "main service form", getVia(t, addr, "web.main."+proj+".localhost", "/"), "greeting: Hello through the proxy")
	contains(t, "linked instance", getVia(t, addr, "app-feature."+proj+".localhost", "/"), "branch:   feature-x")
	contains(t, "branch alias", getVia(t, addr, "web.feature-x."+proj+".localhost", "/"), "branch:   feature-x")

	// routes and status know the running proxy.
	out := must(t, "proxy", "routes", proj, "--json")
	var doc struct {
		Port    int           `json:"port"`
		Running bool          `json:"proxy_running"`
		Routes  []proxy.Route `json:"routes"`
	}
	if err := json.Unmarshal([]byte(out[:strings.LastIndex(out, "}")+1]), &doc); err != nil || doc.Port != pport || !doc.Running || len(doc.Routes) != 4 {
		t.Errorf("proxy routes --json: %v\n%s", err, out)
	}
	for _, r := range doc.Routes {
		if !r.Running || r.Service != "web" {
			t.Errorf("route %+v", r)
		}
	}
	contains(t, "status", must(t, "status", proj), fmt.Sprintf("proxy main: http://main.%s.localhost:%d", proj, pport))
	contains(t, "hosts", must(t, "proxy", "routes", "--hosts"), "127.0.0.1\tmain."+proj+".localhost")

	// After down the route says the instance is not running.
	must(t, "down", proj, "-w", "feature/x")
	time.Sleep(600 * time.Millisecond) // let a miss read the instances again
	req, _ := http.NewRequest("GET", "http://"+addr+"/", nil)
	req.Host = "app-feature." + proj + ".localhost"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "theo up "+proj) {
		t.Errorf("stopped instance through the proxy: %d %s", resp.StatusCode, body)
	}
	must(t, "down", proj)
}
