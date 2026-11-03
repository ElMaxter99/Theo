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
	"strings"
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
	HTTPS     bool `json:"https,omitempty"`
	HTTPSPort int  `json:"https_port,omitempty"`
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
	// HTTPS is true while the running proxy also serves HTTPS (or, when no
	// proxy runs, when the switch asks for it).
	HTTPS     bool `json:"https"`
	HTTPSPort int  `json:"https_port"`
	// Authority is Theo's local certificate authority, once it exists.
	Authority *AuthorityStatus `json:"authority,omitempty"`
}

// AuthorityStatus describes Theo's local certificate authority (ADR-0015).
type AuthorityStatus struct {
	Path        string            `json:"path"` // the certificate, the file to import in Firefox
	Created     time.Time         `json:"created"`
	Fingerprint string            `json:"fingerprint"`
	Trust       proxy.TrustStatus `json:"trust"`
	Trusted     bool              `json:"trusted"`
	// CanTrustHere is true where the interface can trust it itself (Windows
	// and macOS); elsewhere it shows Command.
	CanTrustHere bool   `json:"can_trust_here"`
	Command      string `json:"command"`
}

type proxyManager struct {
	s *Server

	mu        sync.Mutex
	cancel    context.CancelFunc
	done      chan struct{}
	port      int
	httpsPort int
	lastErr   string
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
	prefs := m.prefs()
	m.mu.Lock()
	st := ProxyStatus{Autostart: prefs.Autostart, Error: m.lastErr, Port: proxy.DefaultPort, HTTPSPort: proxy.DefaultHTTPSPort}
	if m.cancel != nil {
		st.Running, st.Managed, st.Port, st.PID = true, true, m.port, os.Getpid()
		st.HTTPS, st.HTTPSPort = m.httpsPort > 0, m.httpsPort
	}
	m.mu.Unlock()
	if !st.Running && m.s.cfg.DataDir != "" {
		if rec, ok := proxy.Running(m.s.cfg.DataDir); ok {
			st.Running, st.Port, st.PID = true, rec.Port, rec.PID
			st.HTTPS, st.HTTPSPort = rec.HTTPSPort > 0, rec.HTTPSPort
		}
	}
	if !st.Running {
		if prefs.Port > 0 {
			st.Port = prefs.Port
		}
		st.HTTPS = prefs.HTTPS
		if prefs.HTTPSPort > 0 {
			st.HTTPSPort = prefs.HTTPSPort
		}
	}
	if !st.HTTPS && st.HTTPSPort == 0 {
		st.HTTPSPort = proxy.DefaultHTTPSPort
	}
	if st.Running {
		st.URL = fmt.Sprintf("http://localhost:%d/", st.Port)
	}
	st.Authority = m.authority()
	return st
}

// authority describes the local certificate authority, nil while there is
// none.
func (m *proxyManager) authority() *AuthorityStatus {
	if m.s.cfg.DataDir == "" {
		return nil
	}
	ca, err := proxy.LoadCA(m.s.cfg.DataDir)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	trust := ca.Trust(ctx)
	return &AuthorityStatus{
		Path: ca.CertPath(), Created: ca.Cert.NotBefore.Add(time.Hour).UTC(), Fingerprint: ca.Fingerprint(),
		Trust: trust, Trusted: trust.Trusted(), CanTrustHere: proxy.CanTrustWithoutTerminal(), Command: "theo proxy trust",
	}
}

// runningPorts are the ports of whichever proxy runs, for the links
// (httpsPort is 0 when it serves only HTTP).
func (m *proxyManager) runningPorts() (port, httpsPort int, running bool) {
	m.mu.Lock()
	if m.cancel != nil {
		defer m.mu.Unlock()
		return m.port, m.httpsPort, true
	}
	m.mu.Unlock()
	if m.s.cfg.DataDir == "" {
		return 0, 0, false
	}
	rec, ok := proxy.Running(m.s.cfg.DataDir)
	return rec.Port, rec.HTTPSPort, ok
}

var errProxyRunsElsewhere = errors.New("a proxy started with `theo proxy` is already running")

// start runs the interface's proxy on 127.0.0.1:port, and over HTTPS on
// 127.0.0.1:httpsPort when httpsPort is not 0.
func (m *proxyManager) start(port, httpsPort int) error {
	if port <= 0 {
		port = proxy.DefaultPort
	}
	if port > 65535 || httpsPort < 0 || httpsPort > 65535 {
		return badRequest("port must be between 1 and 65535")
	}
	if httpsPort == port {
		return badRequest("HTTP and HTTPS need different ports")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		if m.port == port && m.httpsPort == httpsPort {
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
	var tlsLn net.Listener
	var ca *proxy.CA
	if httpsPort > 0 {
		if m.s.cfg.DataDir == "" {
			ln.Close()
			return errf(http.StatusConflict, "no_data_dir", "HTTPS needs Theo's data directory, to keep its local authority")
		}
		if ca, _, err = proxy.EnsureCA(m.s.cfg.DataDir); err != nil {
			ln.Close()
			m.lastErr = "cannot prepare the local authority: " + err.Error()
			return errf(http.StatusInternalServerError, "authority", m.lastErr)
		}
		taddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(httpsPort))
		if tlsLn, err = net.Listen("tcp4", taddr); err != nil {
			ln.Close()
			m.lastErr = fmt.Sprintf("cannot listen on %s: %v", taddr, err)
			e := errf(http.StatusConflict, "port_in_use", m.lastErr)
			e.Hint = "another program uses that port; choose another one"
			return e
		}
	}
	m.lastErr = ""
	h := &proxy.Handler{Source: proxy.EngineSource(m.s.eng, ""), Port: port, HTTPSPort: httpsPort, ErrorLog: log.New(m.s.log.Writer(), "proxy: ", 0)}
	var remove func()
	if m.s.cfg.DataDir != "" {
		remove, _ = proxy.WriteState(m.s.cfg.DataDir, proxy.State{Addr: "127.0.0.1", Port: port, HTTPSPort: httpsPort, PID: os.Getpid(), Started: time.Now().UTC()})
	}
	ctx, cancel := context.WithCancel(m.s.workCtx)
	done := make(chan struct{})
	m.cancel, m.done, m.port, m.httpsPort = cancel, done, port, httpsPort
	go func() {
		defer close(done)
		var tlsDone chan struct{}
		if tlsLn != nil {
			tlsDone = make(chan struct{})
			go func() {
				defer close(tlsDone)
				if err := proxy.ServeTLS(ctx, tlsLn, h, ca); err != nil {
					m.s.log.Printf("proxy (HTTPS) stopped: %v", err)
				}
			}()
		}
		if err := proxy.Serve(ctx, ln, h); err != nil {
			m.s.log.Printf("proxy stopped: %v", err)
		}
		if tlsDone != nil {
			<-tlsDone
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
	https := 0
	if p.HTTPS {
		https = p.HTTPSPort
		if https <= 0 {
			https = proxy.DefaultHTTPSPort
		}
	}
	if err := m.start(p.Port, https); err != nil {
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
		Port      int  `json:"port"`
		HTTPS     bool `json:"https"`
		HTTPSPort int  `json:"https_port"`
	}
	if err := decodeOptional(r, &req); err != nil {
		return err
	}
	if req.Port < 0 || req.HTTPSPort < 0 {
		return badRequest("port must be between 1 and 65535")
	}
	https := 0
	if req.HTTPS {
		https = req.HTTPSPort
		if https == 0 {
			https = proxy.DefaultHTTPSPort
		}
	}
	if err := s.proxy.start(req.Port, https); err != nil {
		return err
	}
	st := s.proxy.status()
	s.proxy.savePrefs(proxyPrefs{Autostart: true, Port: st.Port, HTTPS: st.HTTPS, HTTPSPort: st.HTTPSPort})
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

// handleProxyTrust adds Theo's local authority to the system's store (or
// removes it, when untrust). Windows and macOS show their own confirmation;
// on Linux it needs sudo, so the interface only shows the command.
func (s *Server) handleProxyTrust(untrust bool) func(http.ResponseWriter, *http.Request, *session) error {
	return func(w http.ResponseWriter, r *http.Request, _ *session) error {
		cmd := "theo proxy trust"
		if untrust {
			cmd = "theo proxy untrust"
		}
		if !proxy.CanTrustWithoutTerminal() {
			e := errf(http.StatusConflict, "needs_terminal", "on this system trusting the local authority needs administrator rights (sudo)")
			e.Hint = "run `" + cmd + "` in a terminal"
			return e
		}
		if s.cfg.DataDir == "" {
			return errf(http.StatusConflict, "no_data_dir", "Theo has no data directory")
		}
		ca, _, err := proxy.EnsureCA(s.cfg.DataDir)
		if err != nil {
			return err
		}
		// The system's dialog waits for the person: give it time.
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		var out strings.Builder
		if untrust {
			err = ca.RemoveTrust(ctx, nil, &out)
		} else {
			err = ca.AddTrust(ctx, nil, &out)
		}
		if err != nil {
			e := errf(http.StatusConflict, "trust_failed", err.Error())
			e.Hint = "try `" + cmd + "` in a terminal to see what the system says"
			return e
		}
		s.proxyChanged()
		return writeJSON(w, http.StatusOK, s.proxy.status())
	}
}

// withProxyLinks adds the proxy addresses of each worktree while a proxy
// runs. It works on a copy: the cached overview stays without them, since a
// proxy can start or stop at any time.
func (s *Server) withProxyLinks(ov *Overview) *Overview {
	port, httpsPort, running := s.proxy.runningPorts()
	if !running {
		return ov
	}
	var targets []proxy.Target
	for _, v := range ov.Worktrees {
		targets = append(targets, proxy.InstanceTargets(proxy.Instance{
			Project: ov.Project, Worktree: v.Path, Branch: v.Branch, Main: v.Main, ComposeProject: v.ComposeProject,
		}, v.Ports)...)
	}
	links := proxy.Links(targets, port, httpsPort)
	c := *ov
	c.Worktrees = append([]WorktreeView(nil), ov.Worktrees...)
	for i := range c.Worktrees {
		c.Worktrees[i].ProxyURLs = links[c.Worktrees[i].Path]
	}
	return &c
}
