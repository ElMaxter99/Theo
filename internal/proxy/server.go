package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultTTL is how long the proxy trusts what it read of the instances
// before it reads again.
const DefaultTTL = 2 * time.Second

// minRefresh limits how often a host that is not in the table (or an
// instance that is not running) makes the proxy read again before the TTL.
const minRefresh = 500 * time.Millisecond

// Handler is the reverse proxy: it routes each request by its Host header.
type Handler struct {
	// Source lists what the instances publish. It is called at most once at
	// a time.
	Source func(context.Context) ([]Target, error)
	// Port is the proxy's own port, for the URLs it shows.
	Port int
	// HTTPSPort is the port it also serves HTTPS on, for the URLs it shows
	// (0 when it does not).
	HTTPSPort int
	// TTL is how long a table is used before it is read again (DefaultTTL
	// when zero).
	TTL time.Duration
	// ErrorLog receives proxy errors; nil discards them.
	ErrorLog *log.Logger

	mu      sync.Mutex
	table   *Table
	readErr error
	at      time.Time

	once sync.Once
	rp   *httputil.ReverseProxy
}

type routeKey struct{}

// Table returns the routing table, read again when it is older than the TTL
// (or older than minRefresh when force is set).
func (h *Handler) Table(ctx context.Context, force bool) (*Table, error) {
	ttl := h.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	age := time.Since(h.at)
	if h.table != nil && (age < ttl && !force || force && age < minRefresh) {
		return h.table, h.readErr
	}
	targets, err := h.Source(ctx)
	h.at = time.Now()
	h.readErr = err
	if err != nil {
		if h.table == nil {
			h.table = BuildTable(nil, h.Port)
			h.table.SetHTTPS(h.HTTPSPort)
		}
		return h.table, err // keep serving what was known
	}
	h.table = BuildTable(targets, h.Port)
	h.table.SetHTTPS(h.HTTPSPort)
	return h.table, nil
}

func (h *Handler) proxy() *httputil.ReverseProxy {
	h.once.Do(func() {
		h.rp = &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				rt := pr.In.Context().Value(routeKey{}).(*Route)
				pr.SetURL(&url.URL{Scheme: "http", Host: rt.Target})
				pr.Out.Host = pr.In.Host // the application sees the name it is served under
				pr.SetXForwarded()       // X-Forwarded-For, -Host and -Proto
			},
			ErrorLog: h.ErrorLog,
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				rt := r.Context().Value(routeKey{}).(*Route)
				if h.ErrorLog != nil {
					h.ErrorLog.Printf("%s -> %s: %v", r.Host, rt.Target, err)
				}
				writePage(w, http.StatusBadGateway, "Bad gateway",
					fmt.Sprintf("%s (%s of %s, instance %s) did not answer on %s: %v. %s",
						rt.Host, rt.Service, rt.Project, rt.Instance, rt.Target, err, startHint(rt)), nil, false)
			},
		}
		if h.ErrorLog == nil {
			h.rp.ErrorLog = log.New(io.Discard, "", 0)
		}
	})
	return h.rp
}

func startHint(rt *Route) string {
	w := rt.Branch
	if w == "" {
		w = rt.Worktree
	}
	return fmt.Sprintf("Is it running? Start it with `theo up %s -w %s`.", rt.Project, w)
}

// ServeHTTP routes the request by its Host header.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tbl, err := h.Table(r.Context(), false)
	rt, ok := tbl.Lookup(r.Host)
	if !ok || !rt.Running {
		// A name or an instance that just appeared: read again.
		tbl, err = h.Table(r.Context(), true)
		rt, ok = tbl.Lookup(r.Host)
	}
	if !ok {
		status, title := http.StatusNotFound, "No such route"
		msg := fmt.Sprintf("Theo has no instance at %q.", NormalizeHost(r.Host))
		if !IsRouteShaped(r.Host) {
			msg = "Theo serves each instance under <service>.<instance>.<project>.localhost."
			if name := NormalizeHost(r.Host); name == Suffix || name == "127.0.0.1" || name == "::1" {
				status, title = http.StatusOK, "Theo proxy"
			}
		}
		if err != nil {
			msg += " Theo could not read the instances: " + firstLine(err.Error())
		}
		writePage(w, status, title, msg, tbl.Routes, r.TLS != nil)
		return
	}
	if !rt.Running {
		writePage(w, http.StatusBadGateway, "Not running",
			fmt.Sprintf("%s goes to %s of %s (instance %s), which is not running. %s",
				rt.Host, rt.Service, rt.Project, rt.Instance, startHint(rt)), nil, false)
		return
	}
	h.proxy().ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), routeKey{}, rt)))
}

// writePage answers with a small page that lists the routes: HTML for a
// browser, plain text for curl and scripts.
func writePage(w http.ResponseWriter, status int, title, msg string, routes []Route, secure bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	var b strings.Builder
	if routes == nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		fmt.Fprintf(&b, "%s\n\n%s\n", title, msg)
		io.WriteString(w, b.String())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(&b, "<!doctype html>\n<meta charset=\"utf-8\"><title>%s · Theo proxy</title>\n", html.EscapeString(title))
	b.WriteString("<style>body{font:14px/1.5 system-ui,sans-serif;margin:2rem;color:#222;background:#fff}td,th{padding:.2rem .8rem;text-align:left}" +
		"code{font-size:13px}.off{color:#888}@media(prefers-color-scheme:dark){body{color:#ddd;background:#161616}a{color:#8ab4f8}}</style>\n")
	fmt.Fprintf(&b, "<h1>%s</h1>\n<p>%s</p>\n", html.EscapeString(title), html.EscapeString(msg))
	if len(routes) == 0 {
		b.WriteString("<p>No instance publishes a TCP port. Start one with <code>theo up &lt;project&gt;</code>.</p>\n")
	} else {
		b.WriteString("<table>\n<tr><th>Route</th><th>Project</th><th>Instance</th><th>Service</th><th>Target</th><th>State</th></tr>\n")
		for _, r := range routes {
			state, class := "running", ""
			if !r.Running {
				state, class = "not running", " class=\"off\""
			}
			link := r.URL
			if secure && r.HTTPSURL != "" {
				link = r.HTTPSURL
			}
			fmt.Fprintf(&b, "<tr%s><td><a href=\"%s/\">%s</a></td><td>%s</td><td>%s</td><td>%s</td><td><code>%s</code></td><td>%s</td></tr>\n",
				class, html.EscapeString(link), html.EscapeString(r.Host), html.EscapeString(r.Project), html.EscapeString(r.Instance),
				html.EscapeString(r.Service), html.EscapeString(r.Target), state)
		}
		b.WriteString("</table>\n")
	}
	b.WriteString("<p class=\"off\">theo proxy routes lists the same table.</p>\n")
	io.WriteString(w, b.String())
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// CheckAddr validates the address the proxy listens on: a loopback address
// unless allowRemote. "localhost" is taken as 127.0.0.1.
func CheckAddr(addr string, allowRemote bool) (string, error) {
	if addr == "" || strings.EqualFold(addr, "localhost") {
		return "127.0.0.1", nil
	}
	ip := net.ParseIP(strings.Trim(addr, "[]"))
	if ip == nil {
		return "", fmt.Errorf("--addr %q is not an IP address", addr)
	}
	if !ip.IsLoopback() && !allowRemote {
		return "", fmt.Errorf("--addr %s is not a loopback address: other machines could reach your instances through it. Pass --allow-remote if that is what you want", addr)
	}
	return ip.String(), nil
}

// ServeTLS runs the proxy over HTTPS on ln, with the certificates of ca,
// until ctx is done.
func ServeTLS(ctx context.Context, ln net.Listener, h *Handler, ca *CA) error {
	return Serve(ctx, tls.NewListener(ln, ca.TLSConfig()), h)
}

// Serve runs the proxy on ln until ctx is done, then stops it, waiting a
// moment for requests in flight.
func Serve(ctx context.Context, ln net.Listener, h *Handler) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, ErrorLog: h.ErrorLog}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := srv.Shutdown(sctx)
	if errors.Is(err, context.DeadlineExceeded) {
		err = srv.Close()
	}
	if e := <-done; !errors.Is(e, http.ErrServerClosed) && err == nil {
		err = e
	}
	return err
}
