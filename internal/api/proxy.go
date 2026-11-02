package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/ElMaxter99/Theo/internal/filelock"
	"github.com/ElMaxter99/Theo/internal/proxy"
)

// The interface can run `theo proxy` itself, in its own process, so that the
// local domains work while it is open without a second terminal. It serves
// on loopback only, writes proxy.json like the command does (so `theo status`
// and MCP see it), and remembers in proxy-ui.json whether it was on, to turn
// it on again next time. A proxy started with `theo proxy` is shown but never
// stopped from here.

// proxyPrefsFile remembers the switch of the interface.
const proxyPrefsFile = "proxy-ui.json"

type proxyPrefs struct {
	Autostart bool `json:"autostart"`
	Port      int  `json:"port,omitempty"`
}

// ProxyStatus is the answer of GET /proxy.
type ProxyStatus struct {
	// Running is true while a proxy listens: the interface's own, or one
	// started with `theo proxy`.
	Running bool `json:"running"`
	// Managed is true when the running proxy is the interface's own, which
	// it can stop.
	Managed bool   `json:"managed"`
	Port    int    `json:"port"`
	URL     string `json:"url,omitempty"` // the page that lists the routes
	PID     int    `json:"pid,omitempty"`
	// Autostart says whether the interface starts its proxy when it starts.
	Autostart bool   `json:"autostart"`
	Error     string `json:"error,omitempty"` // why the last start failed
}

type proxyManager struct {
	s *Server

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	port    int
	lastErr string
}

func (m *proxyManager) prefsPath() string {
	if m.s.cfg.DataDir == "" {
		return ""
	}
	return filepath.Join(m.s.cfg.DataDir, proxyPrefsFile)
}

func (m *proxyManager) prefs() proxyPrefs {
	var p proxyPrefs
	if path := m.prefsPath(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &p)
		}
	}
	return p
}

func (m *proxyManager) savePrefs(p proxyPrefs) {
	path := m.prefsPath()
	if path == "" {
		return
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = filelock.WriteFile(path, append(data, '\n'), 0o600)
}

// managed reports whether the interface's own proxy runs.
func (m *proxyManager) managed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancel != nil
}

// status describes the proxy the interface knows about.
func (m *proxyManager) status() ProxyStatus {
	m.mu.Lock()
	st := ProxyStatus{Autostart: m.prefs().Autostart, Error: m.lastErr, Port: proxy.DefaultPort}
	if m.cancel != nil {
		st.Running, st.Managed, st.Port, st.PID = true, true, m.port, os.Getpid()
	}
	m.mu.Unlock()
	if !st.Running && m.s.cfg.DataDir != "" {
		if rec, ok := proxy.Running(m.s.cfg.DataDir); ok {
			st.Running, st.Port, st.PID = true, rec.Port, rec.PID
		}
	}
	if p := m.prefs().Port; !st.Running && p > 0 {
		st.Port = p
	}
	if st.Running {
		st.URL = fmt.Sprintf("http://localhost:%d/", st.Port)
	}
	return st
}

// runningPort is the port of whichever proxy runs, for the links.
func (m *proxyManager) runningPort() (int, bool) {
	m.mu.Lock()
	if m.cancel != nil {
		defer m.mu.Unlock()
		return m.port, true
	}
	m.mu.Unlock()
	if m.s.cfg.DataDir == "" {
		return 0, false
	}
	return proxy.RunningPort(m.s.cfg.DataDir)
}

var errProxyRunsElsewhere = errors.New("a proxy started with `theo proxy` is already running")

// start runs the interface's proxy on 127.0.0.1:port.
func (m *proxyManager) start(port int) error {
	if port <= 0 {
		port = proxy.DefaultPort
	}
	if port > 65535 {
		return badRequest("port must be between 1 and 65535")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		if m.port == port {
			return nil
		}
		return errf(http.StatusConflict, "proxy_running", fmt.Sprintf("the interface's proxy already runs on port %d; switch it off first", m.port))
	}
	if m.s.cfg.DataDir != "" {
		if rec, ok := proxy.Running(m.s.cfg.DataDir); ok {
			e := errf(http.StatusConflict, "proxy_running", fmt.Sprintf("%s on port %d (pid %d); the interface uses it as it is", errProxyRunsElsewhere, rec.Port, rec.PID))
			e.Hint = "stop it with Ctrl+C in its terminal to let the interface run its own"
			return e
		}
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		m.lastErr = fmt.Sprintf("cannot listen on %s: %v", addr, err)
		e := errf(http.StatusConflict, "port_in_use", m.lastErr)
		e.Hint = "another program uses that port; choose another one"
		return e
	}
	m.lastErr = ""
	h := &proxy.Handler{Source: proxy.EngineSource(m.s.eng, ""), Port: port, ErrorLog: log.New(m.s.log.Writer(), "proxy: ", 0)}
	var remove func()
	if m.s.cfg.DataDir != "" {
		remove, _ = proxy.WriteState(m.s.cfg.DataDir, proxy.State{Addr: "127.0.0.1", Port: port, PID: os.Getpid(), Started: time.Now().UTC()})
	}
	ctx, cancel := context.WithCancel(m.s.workCtx)
	done := make(chan struct{})
	m.cancel, m.done, m.port = cancel, done, port
	go func() {
		defer close(done)
		if err := proxy.Serve(ctx, ln, h); err != nil {
			m.s.log.Printf("proxy stopped: %v", err)
		}
		if remove != nil {
			remove()
		}
		m.mu.Lock()
		if m.done == done {
			m.cancel, m.done = nil, nil
		}
		m.mu.Unlock()
	}()
	return nil
}

// stop stops the interface's proxy and waits for it.
func (m *proxyManager) stop() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel, m.done = nil, nil
	m.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// autostart turns the proxy on when the switch was left on.
func (m *proxyManager) autostart() {
	p := m.prefs()
	if !p.Autostart {
		return
	}
	if err := m.start(p.Port); err != nil {
		var ae *Error
		if errors.As(err, &ae) && ae.Code == "proxy_running" {
			return // `theo proxy` serves the names already
		}
		m.s.log.Printf("proxy not started: %v", err)
	}
}

func (s *Server) proxyChanged() {
	s.hub.publish("state.changed", map[string]string{})
}

func (s *Server) handleProxyStatus(w http.ResponseWriter, r *http.Request, _ *session) error {
	return writeJSON(w, http.StatusOK, s.proxy.status())
}

func (s *Server) handleProxyStart(w http.ResponseWriter, r *http.Request, _ *session) error {
	var req struct {
		Port int `json:"port"`
	}
	if err := decodeOptional(r, &req); err != nil {
		return err
	}
	if req.Port < 0 {
		return badRequest("port must be between 1 and 65535")
	}
	if err := s.proxy.start(req.Port); err != nil {
		return err
	}
	s.proxy.savePrefs(proxyPrefs{Autostart: true, Port: s.proxy.status().Port})
	s.proxyChanged()
	return writeJSON(w, http.StatusOK, s.proxy.status())
}

func (s *Server) handleProxyStop(w http.ResponseWriter, r *http.Request, _ *session) error {
	if !s.proxy.managed() {
		if st := s.proxy.status(); st.Running {
			e := errf(http.StatusConflict, "proxy_external", fmt.Sprintf("the proxy on port %d was started with `theo proxy` (pid %d); stop it with Ctrl+C in its terminal", st.Port, st.PID))
			return e
		}
	}
	s.proxy.stop()
	p := s.proxy.prefs()
	p.Autostart = false
	s.proxy.savePrefs(p)
	s.proxyChanged()
	return writeJSON(w, http.StatusOK, s.proxy.status())
}

// withProxyLinks adds the proxy addresses of each worktree while a proxy
// runs. It works on a copy: the cached overview stays without them, since a
// proxy can start or stop at any time.
func (s *Server) withProxyLinks(ov *Overview) *Overview {
	port, running := s.proxy.runningPort()
	if !running {
		return ov
	}
	var targets []proxy.Target
	for _, v := range ov.Worktrees {
		targets = append(targets, proxy.InstanceTargets(proxy.Instance{
			Project: ov.Project, Worktree: v.Path, Branch: v.Branch, Main: v.Main, ComposeProject: v.ComposeProject,
		}, v.Ports)...)
	}
	links := proxy.Links(targets, port)
	c := *ov
	c.Worktrees = append([]WorktreeView(nil), ov.Worktrees...)
	for i := range c.Worktrees {
		c.Worktrees[i].ProxyURLs = links[c.Worktrees[i].Path]
	}
	return &c
}
