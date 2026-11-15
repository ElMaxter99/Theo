// Package staticserve serves a folder of files under a base path, the way a
// build that only generates files (an AngularJS or other single-page app)
// expects to be found: "/portal/" is the folder, "/" sends you there, and
// anything else is not found. Each instance of the application is served on
// its own port, so every one keeps the same base path.
package staticserve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config says what to serve.
type Config struct {
	Dir  string // folder of files
	Base string // "/portal"; "" or "/" for the root
	SPA  bool   // a path without a file or an extension gets index.html
	// Proxy sends the paths under each prefix to another server, keeping the
	// path ("/portal/api/x" to http://localhost:4000/portal/api/x).
	Proxy map[string]string
}

// Handler builds the handler for cfg.
func Handler(cfg Config) (http.Handler, error) {
	base := strings.TrimRight(cfg.Base, "/")
	if base != "" {
		if base[0] != '/' || strings.Contains(base, `\`) || (len(base) > 1 && (base[1] == '/' || base[1] == '\\')) {
			return nil, fmt.Errorf("base %q must be a path starting with a single /", cfg.Base)
		}
	}
	type route struct {
		prefix string
		proxy  *httputil.ReverseProxy
	}
	var routes []route
	for prefix, target := range cfg.Proxy {
		u, err := url.Parse(target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("proxy target %q is not an http address", target)
		}
		rp := httputil.NewSingleHostReverseProxy(u)
		rp.Director = func(orig func(*http.Request)) func(*http.Request) {
			return func(r *http.Request) {
				orig(r)
				r.Host = u.Host
			}
		}(rp.Director)
		routes = append(routes, route{strings.TrimRight(prefix, "/"), rp})
	}
	// The longest prefix wins.
	sort.Slice(routes, func(i, j int) bool { return len(routes[i].prefix) > len(routes[j].prefix) })

	files := http.FileServer(http.Dir(cfg.Dir))
	index := func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Open(filepath.Join(cfg.Dir, "index.html"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "index.html", st.ModTime(), f)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		for _, rt := range routes {
			if p == rt.prefix || strings.HasPrefix(p, rt.prefix+"/") {
				rt.proxy.ServeHTTP(w, r)
				return
			}
		}
		if base != "" {
			switch {
			case p == "/" || p == base:
				http.Redirect(w, r, base+"/", http.StatusFound)
				return
			case !strings.HasPrefix(p, base+"/"):
				http.NotFound(w, r)
				return
			}
		}
		rel := strings.TrimPrefix(p, base)
		if rel == "" {
			rel = "/"
		}
		if cfg.SPA && path.Ext(rel) == "" && !fileExists(cfg.Dir, rel) {
			index(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = rel
		files.ServeHTTP(w, r2)
	}), nil
}

// Server is a running instance.
type Server struct {
	Port int
	base string
	srv  *http.Server
	done chan struct{}
	err  error
	once sync.Once
}

// Start listens on 127.0.0.1 (port 0 asks for a free one) and serves cfg.
func Start(cfg Config, port int) (*Server, error) {
	h, err := Handler(cfg)
	if err != nil {
		return nil, err
	}
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return nil, err
	}
	s := &Server{
		Port: l.Addr().(*net.TCPAddr).Port,
		base: strings.TrimRight(cfg.Base, "/"),
		srv:  &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second},
		done: make(chan struct{}),
	}
	go func() {
		if err := s.srv.Serve(l); !errors.Is(err, http.ErrServerClosed) {
			s.err = err
		}
		close(s.done)
	}()
	return s, nil
}

// URL is where the application starts: the origin plus its base path.
func (s *Server) URL() string { return "http://127.0.0.1:" + strconv.Itoa(s.Port) + s.base + "/" }

// Done is closed when the server ended; Err says why, if it was not Stop.
func (s *Server) Done() <-chan struct{} { return s.done }
func (s *Server) Err() error            { return s.err }

// Stop ends the server.
func (s *Server) Stop() {
	s.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if s.srv.Shutdown(ctx) != nil {
			s.srv.Close()
		}
		<-s.done
	})
}
