package proxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// backend is a fake application: it echoes what it received, and answers
// /ws with a protocol upgrade after which it echoes lines in upper case.
func backend(t *testing.T) (*httptest.Server, int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || !strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
				http.Error(w, "no upgrade", http.StatusBadRequest)
				return
			}
			conn, rw, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: fake\r\n\r\n")
			rw.Flush()
			for {
				line, err := rw.ReadString('\n')
				if err != nil {
					return
				}
				rw.WriteString(strings.ToUpper(line))
				rw.Flush()
			}
		}
		fmt.Fprintf(w, "path=%s host=%s xfh=%s xfp=%s xff=%t", r.URL.Path, r.Host,
			r.Header.Get("X-Forwarded-Host"), r.Header.Get("X-Forwarded-Proto"), r.Header.Get("X-Forwarded-For") != "")
	}))
	t.Cleanup(srv.Close)
	_, p, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(p)
	return srv, port
}

type fakeSource struct {
	mu      sync.Mutex
	targets []Target
	err     error
	calls   atomic.Int32
}

func (f *fakeSource) read(context.Context) ([]Target, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Target(nil), f.targets...), f.err
}

func (f *fakeSource) set(ts []Target) {
	f.mu.Lock()
	f.targets = ts
	f.mu.Unlock()
}

func startProxy(t *testing.T, src *fakeSource) (string, *Handler) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	h := &Handler{Source: src.read, Port: port, TTL: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, h) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return ln.Addr().String(), h
}

// request sends a request to the proxy at addr with the given Host header.
func request(t *testing.T, addr, host, path string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", "http://"+addr+path, nil)
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestProxyRoutesByHost(t *testing.T) {
	_, port := backend(t)
	src := &fakeSource{targets: []Target{
		{Project: "shop", Worktree: "/src/shop", Branch: "main", Main: true, ComposeProject: "shop", Service: "web", ContainerPort: 80, HostPort: port, Running: true},
		{Project: "shop", Worktree: "/wt/x", Branch: "x", ComposeProject: "shop-x", Service: "web", ContainerPort: 80, HostPort: 1, Running: false},
	}}
	addr, _ := startProxy(t, src)
	_, pport, _ := net.SplitHostPort(addr)

	host := "main.shop.localhost:" + pport
	code, body := request(t, addr, host, "/hello?x=1")
	want := "path=/hello host=" + host + " xfh=" + host + " xfp=http xff=true"
	if code != 200 || body != want {
		t.Errorf("short form: %d %q, want %q", code, body, want)
	}
	if code, body := request(t, addr, "WEB.main.shop.localhost", "/"); code != 200 || !strings.Contains(body, "path=/ ") {
		t.Errorf("service form: %d %q", code, body)
	}

	// Not running: a page that says how to start it.
	if code, body := request(t, addr, "x.shop.localhost", "/"); code != http.StatusBadGateway || !strings.Contains(body, "theo up shop -w x") {
		t.Errorf("stopped instance: %d %q", code, body)
	}
	// Unknown route: 404 listing the routes, escaped.
	code, body = request(t, addr, "nope.shop.localhost", "/")
	if code != 404 || !strings.Contains(body, "web.main.shop.localhost") || !strings.Contains(body, "nope.shop.localhost") {
		t.Errorf("unknown route: %d %q", code, body)
	}
	if code, body := request(t, addr, "<script>.shop.localhost", "/"); code != 404 || strings.Contains(body, "<script>") {
		t.Errorf("host not escaped: %d %q", code, body)
	}
	// The bare name is an index.
	if code, body := request(t, addr, "localhost:"+pport, "/"); code != 200 || !strings.Contains(body, "main.shop.localhost") {
		t.Errorf("index: %d %q", code, body)
	}
	if code, _ := request(t, addr, "evil.example.com", "/"); code != 404 {
		t.Errorf("foreign host: %d", code)
	}
}

func TestProxyRefreshesOnMiss(t *testing.T) {
	_, port := backend(t)
	src := &fakeSource{}
	addr, h := startProxy(t, src)
	if code, _ := request(t, addr, "main.app.localhost", "/"); code != 404 {
		t.Fatalf("empty table: %d", code)
	}
	// An instance starts: a miss reads again (no faster than minRefresh),
	// although the TTL (an hour here) has not passed.
	src.set([]Target{{Project: "app", Worktree: "/a", Main: true, Service: "api", ContainerPort: 3000, HostPort: port, Running: true}})
	time.Sleep(minRefresh + 50*time.Millisecond)
	if code, body := request(t, addr, "main.app.localhost", "/"); code != 200 {
		t.Fatalf("after start: %d %q", code, body)
	}
	calls := src.calls.Load()
	for i := 0; i < 5; i++ {
		request(t, addr, "main.app.localhost", "/")
	}
	if got := src.calls.Load(); got != calls {
		t.Errorf("hits read the instances again: %d -> %d calls", calls, got)
	}
	// Misses in a burst read at most once.
	for i := 0; i < 5; i++ {
		request(t, addr, "missing.app.localhost", "/")
	}
	if got := src.calls.Load(); got > calls+1 {
		t.Errorf("a burst of misses read %d times", got-calls)
	}
	// A read that fails keeps the last table.
	src.mu.Lock()
	src.err = errors.New("docker is gone")
	src.mu.Unlock()
	h.mu.Lock()
	h.at = time.Time{}
	h.mu.Unlock()
	if code, _ := request(t, addr, "main.app.localhost", "/"); code != 200 {
		t.Errorf("failed read dropped the table: %d", code)
	}
	if code, body := request(t, addr, "other.app.localhost", "/"); code != 404 || !strings.Contains(body, "docker is gone") {
		t.Errorf("read error not shown: %d %q", code, body)
	}
}

func TestProxyWebSocketUpgrade(t *testing.T) {
	_, port := backend(t)
	src := &fakeSource{targets: []Target{{Project: "chat", Worktree: "/c", Main: true, Service: "web", ContainerPort: 80, HostPort: port, Running: true}}}
	addr, _ := startProxy(t, src)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	fmt.Fprint(conn, "GET /ws HTTP/1.1\r\nHost: main.chat.localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols || !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		t.Fatalf("upgrade answer: %s %v", resp.Status, resp.Header)
	}
	for _, msg := range []string{"hello\n", "second frame\n"} {
		if _, err := io.WriteString(conn, msg); err != nil {
			t.Fatal(err)
		}
		line, err := br.ReadString('\n')
		if err != nil || line != strings.ToUpper(msg) {
			t.Fatalf("echo through the upgraded connection: %q, %v", line, err)
		}
	}
}

func TestProxyBackendDown(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // nothing listens there now
	src := &fakeSource{targets: []Target{{Project: "p", Worktree: "/p", Main: true, Branch: "main", Service: "web", ContainerPort: 80, HostPort: port, Running: true}}}
	addr, _ := startProxy(t, src)
	if code, body := request(t, addr, "main.p.localhost", "/"); code != http.StatusBadGateway || !strings.Contains(body, "theo up p -w main") {
		t.Errorf("backend down: %d %q", code, body)
	}
}

func TestServeStops(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, &Handler{Source: (&fakeSource{}).read}) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not stop")
	}
	if c, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second); err == nil {
		c.Close()
		t.Error("still listening after stop")
	}
}

func TestState(t *testing.T) {
	dir := t.TempDir()
	if _, ok := Running(dir); ok {
		t.Fatal("running without a record")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	remove, err := WriteState(dir, State{Addr: "127.0.0.1", Port: port, PID: 42})
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := Running(dir); !ok || s.Port != port {
		t.Errorf("Running: %+v %v", s, ok)
	}
	ln.Close()
	if _, ok := Running(dir); ok {
		t.Error("a record without a listener counts as running")
	}
	remove()
	if _, ok := readState(dir); ok {
		t.Error("record not removed")
	}
}
