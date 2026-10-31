// Package api is the local HTTP interface of Theo: it translates JSON over
// HTTP into calls of the engine and streams long operations and logs with
// server-sent events. It holds no domain logic and relaxes no protection of
// the engine. The security model (loopback bind, launch nonce, session
// cookie, CSRF token, Host and Origin checks, CSP) is described in
// docs/cli/ui.md; the resources are described in docs/api.md.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"path"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/execx"
)

// APIVersion is the version of the HTTP interface (GET /meta).
const APIVersion = "1"

// DefaultIdleTimeout is how long the server runs with no SSE client and no
// operation before it stops by itself.
const DefaultIdleTimeout = 15 * time.Minute

// Config configures a Server.
type Config struct {
	Engine *engine.Engine
	// DataDir is Theo's data directory (ui.lock lives there; GET /meta
	// reports it).
	DataDir string
	// Assets is the web interface, rooted at index.html. Nil serves a
	// placeholder page.
	Assets fs.FS
	// IdleTimeout stops the server after that long without SSE clients and
	// operations; zero means DefaultIdleTimeout, negative means never.
	IdleTimeout time.Duration
	// MaxOperations is the number of operations that run at once (default 4).
	MaxOperations int
	// PollInterval is how often the server looks for changes made outside
	// Theo while a visible tab is connected (default 5 s).
	PollInterval time.Duration
	// CommandTimeout kills any external command of an overview, health or
	// reconcile that runs longer (default 15 s). A command that times out
	// also pauses new Docker commands for a few seconds, so a stuck daemon
	// costs one timeout, not one per project.
	CommandTimeout time.Duration
	// WorkTimeout bounds one whole shared computation (default 60 s).
	WorkTimeout time.Duration
	// MaxProcesses is how many read commands run at once in the process
	// (default 8).
	MaxProcesses int
	// ConfigCacheTTL is how long a `docker compose config` answer is reused for
	// the panel's reads (overview, health, reconcile) while its files do not
	// change (default 5 min; negative turns it off).
	ConfigCacheTTL time.Duration
	// Log receives one line per request (method and route pattern only, never
	// paths, queries or bodies). Nil discards.
	Log *log.Logger
}

// Auth levels of a route.
type authLevel int

const (
	// authSession needs the session cookie (and the CSRF header for
	// changes). It is the default: a route must ask to be exempt.
	authSession authLevel = iota
	// authPublic needs nothing but passes the Host and Origin checks.
	authPublic
	// authControl needs the control secret of ui.lock.
	authControl
)

// Route is a registered route; the list is what the reflection test walks.
type Route struct {
	Method  string
	Pattern string // path below /api/v1
	Auth    authLevel
	Stream  bool
}

type handlerFunc func(w http.ResponseWriter, r *http.Request, sess *session) error

// Server is the HTTP interface.
type Server struct {
	cfg    Config
	eng    *engine.Engine
	ln     net.Listener
	port   int
	srv    *http.Server
	mux    *http.ServeMux
	routes []Route
	auth   *auth
	ops    *operations
	hub    *hub
	log    *log.Logger
	assets fs.FS

	cache  *overviewCache
	meta   *metaCache
	health *healthCache

	// workCtx is the parent of every shared computation (see work); it ends
	// with the server. limits bound the commands they start.
	workCtx    context.Context
	cancelWork context.CancelFunc
	limits     execx.Limits
	snaps      snapCache
	polled     pollMemo

	ovFlight     flightGroup[*Overview]
	snapFlight   flightGroup[*snapResult]
	healthFlight flightGroup[Health]
	repFlight    flightGroup[*engine.Report]

	started time.Time
	// sse counts open SSE streams; lastBusy is when there last was one, or
	// an operation. Both feed the idle timer.
	sse      atomic.Int64
	lastBusy atomic.Int64 // unix nanoseconds

	stopOnce sync.Once
	stopped  chan struct{}
	requests atomic.Int64
}

// New binds 127.0.0.1 on a random port and returns a server that is not yet
// serving; call Serve.
func New(cfg Config) (*Server, error) {
	if cfg.Engine == nil {
		return nil, errors.New("api: no engine")
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0") // never 0.0.0.0, never "localhost"
	if err != nil {
		return nil, fmt.Errorf("cannot listen on 127.0.0.1: %w", err)
	}
	if cfg.MaxOperations <= 0 {
		cfg.MaxOperations = 4
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = DefaultIdleTimeout
	}
	if cfg.CommandTimeout <= 0 {
		cfg.CommandTimeout = defaultCommandTimeout
	}
	if cfg.WorkTimeout <= 0 {
		cfg.WorkTimeout = defaultWorkTimeout
	}
	if cfg.ConfigCacheTTL == 0 {
		cfg.ConfigCacheTTL = defaultConfigCacheTTL
	}
	s := &Server{cfg: cfg, eng: cfg.Engine, ln: ln, port: ln.Addr().(*net.TCPAddr).Port, mux: http.NewServeMux(),
		auth: newAuth(), log: cfg.Log, assets: cfg.Assets, started: time.Now(), stopped: make(chan struct{})}
	if s.log == nil {
		s.log = log.New(io.Discard, "", 0)
	}
	s.workCtx, s.cancelWork = context.WithCancel(context.Background())
	s.limits = execx.Limits{Timeout: cfg.CommandTimeout, Breaker: execx.NewBreaker(defaultBreakerPause)}
	execx.SetMaxConcurrent(cfg.MaxProcesses)
	s.eng.EnableConfigCache(cfg.ConfigCacheTTL)
	s.hub = newHub()
	s.ops = newOperations(s)
	s.cache = newOverviewCache(2 * time.Second)
	s.meta = &metaCache{}
	s.health = &healthCache{}
	s.touch()
	s.routesTable()
	s.srv = &http.Server{Handler: s.mux, ReadHeaderTimeout: 10 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	return s, nil
}

// Port is the port the server listens on.
func (s *Server) Port() int { return s.port }

// ControlSecret is the secret that goes in ui.lock.
func (s *Server) ControlSecret() string { return s.auth.control }

// LaunchURL returns a URL that opens the interface once: it carries a
// single-use nonce, valid for 60 seconds, in the fragment.
func (s *Server) LaunchURL() string { return s.origin() + "/#n=" + s.auth.newNonce() }

// Routes returns the registered routes (for tests).
func (s *Server) Routes() []Route { return append([]Route(nil), s.routes...) }

func (s *Server) touch() { s.lastBusy.Store(time.Now().UnixNano()) }

// maxStreams is how many server-sent event streams can be open at once. A
// browser keeps a handful (it opens six connections per host at most); each
// stream holds a goroutine, and a log stream a `docker compose logs` process.
const maxStreams = 32

// openStream counts a stream and returns the function that closes it, or a
// 429 when there are too many. Call it before anything is spawned or written.
func (s *Server) openStream() (func(), error) {
	if s.sse.Add(1) > maxStreams {
		s.sse.Add(-1)
		e := errf(http.StatusTooManyRequests, "too_many_streams", "too many live streams are open; close a tab or wait")
		e.Hint = "each tab keeps a few open"
		return nil, e
	}
	return func() { s.sse.Add(-1); s.touch() }, nil
}

// Serve serves until ctx is cancelled, the idle timeout passes, or Close is
// called. It shuts down gracefully: open streams are closed, and operations
// are cancelled only if the server is told to stop, never by idling.
func (s *Server) Serve(ctx context.Context) error {
	errc := make(chan error, 1)
	go func() { errc <- s.srv.Serve(s.ln) }()
	go s.hub.pollLoop(s)
	idle := time.NewTicker(idleCheckEvery(s.cfg.IdleTimeout))
	defer idle.Stop()
	for {
		select {
		case err := <-errc:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ctx.Done():
			return s.shutdown()
		case <-s.stopped:
			return s.shutdown()
		case <-idle.C:
			if s.cfg.IdleTimeout > 0 && s.idleFor() >= s.cfg.IdleTimeout {
				s.log.Printf("idle for %s: stopping", s.cfg.IdleTimeout)
				return s.shutdown()
			}
		}
	}
}

func idleCheckEvery(d time.Duration) time.Duration {
	if d <= 0 || d > time.Minute {
		return 10 * time.Second
	}
	if d < 200*time.Millisecond {
		return d / 2
	}
	return 100 * time.Millisecond
}

// idleFor is how long the server has had no SSE client and no operation.
func (s *Server) idleFor() time.Duration {
	if s.sse.Load() > 0 || s.ops.running() > 0 {
		s.touch()
		return 0
	}
	return time.Since(time.Unix(0, s.lastBusy.Load()))
}

// Close asks Serve to stop.
func (s *Server) Close() { s.stopOnce.Do(func() { close(s.stopped) }) }

func (s *Server) shutdown() error {
	s.cancelWork()
	s.hub.close()
	s.ops.cancelAll()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := s.srv.Shutdown(ctx)
	if err != nil {
		s.srv.Close()
	}
	return nil
}

// handle registers a route. Every route goes through guard, so a handler
// cannot be reached without the checks of its auth level.
func (s *Server) handle(method, pattern string, level authLevel, h handlerFunc) {
	s.register(Route{Method: method, Pattern: pattern, Auth: level}, h)
}

func (s *Server) stream(method, pattern string, h handlerFunc) {
	s.register(Route{Method: method, Pattern: pattern, Auth: authSession, Stream: true}, h)
}

func (s *Server) register(rt Route, h handlerFunc) {
	s.routes = append(s.routes, rt)
	s.mux.HandleFunc(rt.Method+" "+apiBase+rt.Pattern, s.guard(rt, h))
}

// guard applies, in this order: panic recovery, Host check, Origin and
// Sec-Fetch-Site checks, session (401), CSRF (403) and content type.
func (s *Server) guard(rt Route, h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		reqID := randomToken()[:12]
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Printf("panic in %s %s: %v\n%s", r.Method, r.Pattern, rec, debug.Stack())
				writeError(w, &Error{Status: http.StatusInternalServerError, Code: "internal", Message: "internal error", RequestID: reqID})
			}
		}()
		setCommonHeaders(w.Header())
		w.Header().Set("Cache-Control", "no-store")

		if err := s.checkOrigin(r); err != nil {
			writeError(w, err)
			return
		}
		var sess *session
		switch rt.Auth {
		case authSession:
			if sess = s.auth.lookup(r); sess == nil {
				writeError(w, errf(http.StatusUnauthorized, "unauthorized", "no valid session; open the launch URL that `theo ui` printed"))
				return
			}
			if mutating(r.Method) {
				if !equal(r.Header.Get(csrfHeader), sess.csrf) {
					writeError(w, errf(http.StatusForbidden, "csrf", "missing or wrong "+csrfHeader+" header"))
					return
				}
			}
		case authControl:
			if r.Header.Get("Origin") != "" || !s.auth.controlOK(r) {
				writeError(w, errf(http.StatusUnauthorized, "unauthorized", "missing or wrong control secret"))
				return
			}
		}
		if mutating(r.Method) && rt.Auth != authControl {
			if ct := r.Header.Get("Content-Type"); !isJSON(ct) {
				writeError(w, badRequest("changes must be sent with Content-Type: application/json"))
				return
			}
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		s.touch()
		if err := h(w, r, sess); err != nil {
			var ae *Error
			if !errors.As(err, &ae) {
				ae = fromEngine(err, nil)
			}
			if ae.Status >= 500 {
				ae.RequestID = reqID
			}
			writeError(w, ae)
		}
		s.log.Printf("%s %s", r.Method, r.Pattern)
	}
}

func isJSON(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	return ct == contentJSON || strings.HasPrefix(ct, contentJSON+";")
}

// checkOrigin enforces Host, Origin and Sec-Fetch-Site. It applies to every
// request, including the static ones (Host) and the SSE streams.
func (s *Server) checkOrigin(r *http.Request) *Error {
	if r.Host != s.hostPort() {
		return errf(http.StatusForbidden, "forbidden_origin", "unexpected Host header")
	}
	if o := r.Header.Get("Origin"); o != "" && o != s.origin() {
		return errf(http.StatusForbidden, "forbidden_origin", "unexpected Origin header")
	}
	if sf := r.Header.Get("Sec-Fetch-Site"); sf != "" && sf != "same-origin" {
		return errf(http.StatusForbidden, "forbidden_origin", "the request does not come from this page")
	}
	return nil
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", contentJSON+"; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func writeError(w http.ResponseWriter, e *Error) {
	if w.Header().Get("Content-Type") == "" || strings.HasPrefix(w.Header().Get("Content-Type"), contentJSON) {
		writeJSON(w, e.Status, struct {
			Error *Error `json:"error"`
		}{e})
	}
}

// decode reads a JSON body into v, rejecting unknown fields.
func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return badRequest("the request needs a JSON body")
		}
		if strings.HasPrefix(err.Error(), "json: unknown field") {
			return errf(http.StatusBadRequest, "unknown_field", err.Error())
		}
		return badRequest("invalid JSON: " + err.Error())
	}
	if dec.More() {
		return badRequest("unexpected data after the JSON body")
	}
	return nil
}

// decodeOptional is decode for a body that may be empty.
func decodeOptional(r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return nil
	}
	if err := decode(r, v); err != nil {
		var ae *Error
		if errors.As(err, &ae) && ae.Code == "invalid_request" && strings.Contains(ae.Message, "needs a JSON body") {
			return nil
		}
		return err
	}
	return nil
}

// staticHandler serves the embedded interface. It needs no session (the page
// must load before it can exchange its nonce) and contains no data; the Host
// check still applies.
func (s *Server) staticHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCommonHeaders(w.Header())
		if r.Host != s.hostPort() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if strings.HasPrefix(r.URL.Path, apiBase+"/") || r.URL.Path == apiBase {
			writeError(w, errf(http.StatusNotFound, "not_found", "no such API route"))
			return
		}
		if s.assets == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, "<!doctype html><meta charset=utf-8><title>Theo</title><p>The web interface is not built into this binary.")
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		f, err := s.assets.Open(name)
		if err == nil {
			if st, serr := f.Stat(); serr != nil || st.IsDir() {
				f.Close()
				err = errors.New("directory")
			}
		}
		if err != nil {
			if path.Ext(name) != "" { // a missing asset is a 404, not the app
				http.NotFound(w, r)
				return
			}
			name = "index.html"
			if f, err = s.assets.Open(name); err != nil {
				http.NotFound(w, r)
				return
			}
		}
		defer f.Close()
		if rs, ok := f.(io.ReadSeeker); ok {
			st, _ := f.Stat()
			if path.Ext(name) == ".html" || name == "index.html" {
				w.Header().Set("Cache-Control", "no-store")
			}
			http.ServeContent(w, r, name, st.ModTime(), rs)
			return
		}
		data, err := io.ReadAll(f)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if ct := mimeFor(name); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.Write(data)
	}
}

func mimeFor(name string) string {
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".json":
		return "application/json"
	case ".png":
		return "image/png"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	}
	return ""
}

// Public reports whether the route is exempt from the session (its own
// credentials, if any, are the nonce or the control secret).
func (r Route) Public() bool { return r.Auth != authSession }
