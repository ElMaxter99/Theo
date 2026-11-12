package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/ElMaxter99/Theo/internal/agents"
	"github.com/ElMaxter99/Theo/internal/api"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/notify"
)

// Title is the title of the window.
const Title = "Theo"

// Window is the native window. It is implemented with a system web view in
// window_webview.go (build tag "desktop"); tests use a fake.
type Window interface {
	// Init registers a script that runs in every page before its own scripts.
	Init(js string)
	// Bind exposes fn to the page as a global function taking one string.
	Bind(name string, fn func(string)) error
	// Navigate loads raw in the window.
	Navigate(raw string)
	// Run shows the window and blocks until it is closed or Terminate is
	// called. It must be called from the thread that created the window.
	Run()
	// Terminate ends Run; it is safe to call from another goroutine.
	Terminate()
	// Destroy releases the window.
	Destroy()
}

// Options are the dependencies of Run. Only Engine, DataDir and NewWindow are
// required.
type Options struct {
	Engine  *engine.Engine
	DataDir string
	Assets  fs.FS
	// NewWindow creates the window. It is called once, after the server is
	// known, so that a failure to start a server never opens an empty window.
	NewWindow func() (Window, error)
	// Find looks for a running server of this data directory; nil uses
	// api.FindRunning.
	Find func(dataDir string) (*api.Launch, bool)
	// Open opens an address in the system browser; nil uses OpenExternal.
	Open func(raw, appOrigin string) error
	// RequestLog receives one line per request of the server this app starts
	// (method and route pattern only). Nil discards it.
	RequestLog *log.Logger
	// Stderr receives the app's own messages. Nil discards.
	Stderr io.Writer
	// Notify shows a desktop notification; nil uses notify.Send.
	Notify func(notify.Message) error
}

// Backend is the server the window shows.
type Backend struct {
	// URL is the launch address: Theo origin plus a single-use nonce.
	URL string
	// Origin is "http://127.0.0.1:PORT".
	Origin string
	// Reused is true when the server was already running (started by
	// `theo ui` or another desktop app); the app then never stops it.
	Reused bool
	// Stop shuts down the server this app started. It does nothing for a
	// reused one. Safe to call more than once.
	Stop func()
}

// ReuseExisting is the decision of whether to open a window onto a server that
// is already running instead of starting another: only when it answered with a
// usable launch address. A stale ui.lock, or a server that does not answer,
// means "start our own".
func ReuseExisting(l *api.Launch, found bool) bool {
	return found && l != nil && l.URL != "" && l.Port > 0
}

// Start obtains a server for the window: the running one if there is any,
// otherwise a new one in this process, registered in ui.lock so that
// `theo ui` and other desktop windows find it. The launch address is
// requested from the server directly, in memory; it is never passed through a
// command line.
func Start(ctx context.Context, o Options) (*Backend, error) {
	if o.Engine == nil || o.DataDir == "" {
		return nil, errors.New("desktop: engine and data directory are required")
	}
	find := o.Find
	if find == nil {
		find = api.FindRunning
	}
	unguard, err := api.GuardStart(o.DataDir)
	if err != nil {
		return nil, err
	}
	defer unguard()

	if l, ok := find(o.DataDir); ReuseExisting(l, ok) {
		origin, err := originOf(l.URL)
		if err != nil {
			return nil, err
		}
		return &Backend{URL: l.URL, Origin: origin, Reused: true, Stop: func() {}}, nil
	}

	// The window's lifetime governs the server: no idle timeout.
	srv, err := api.New(api.Config{Engine: o.Engine, DataDir: o.DataDir, Assets: o.Assets, IdleTimeout: -1, Log: o.RequestLog})
	if err != nil {
		return nil, err
	}
	release, err := srv.WriteLock(o.DataDir)
	if err != nil {
		srv.Close()
		return nil, err
	}
	sctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(sctx) }()
	launch := srv.LaunchURL()
	origin, err := originOf(launch)
	if err != nil {
		cancel()
		<-done
		release()
		return nil, err
	}
	stopped := false
	return &Backend{URL: launch, Origin: origin, Stop: func() {
		if stopped {
			return
		}
		stopped = true
		release() // new starts must not find a server that is going away
		cancel()
		<-done
	}}, nil
}

func originOf(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("desktop: unusable server address")
	}
	return u.Scheme + "://" + u.Host, nil
}

// Run starts or reuses the server, opens one window onto it, and returns when
// the window is closed, after stopping the server it started.
func Run(ctx context.Context, o Options) error {
	if o.NewWindow == nil {
		return errors.New("desktop: no window factory")
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	open := o.Open
	if open == nil {
		open = OpenExternal
	}
	be, err := Start(ctx, o)
	if err != nil {
		return err
	}
	defer be.Stop()
	if be.Reused {
		fmt.Fprintln(o.Stderr, "theo-desktop: using the Theo server that is already running; it stays running when the window closes")
	}

	w, err := o.NewWindow()
	if err != nil {
		return err
	}
	defer w.Destroy()
	origin := be.Origin
	w.Init(Shim(origin))
	if err := w.Bind(BindName, func(raw string) {
		// The page is not trusted to have filtered: decide again here.
		if err := open(raw, origin); err != nil {
			fmt.Fprintf(o.Stderr, "theo-desktop: %v\n", err)
		}
	}); err != nil {
		return err
	}

	// Whether the window is in front, reported by the page, for the notices.
	var inFront atomic.Bool
	if err := w.Bind(FocusBindName, func(raw string) { inFront.Store(raw == "1") }); err != nil {
		return err
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	quit := make(chan struct{})
	defer close(quit)
	go func() {
		select {
		case <-sig:
			w.Terminate()
		case <-ctx.Done():
			w.Terminate()
		case <-quit:
		}
	}()

	// Desktop notifications when an agent waits or finishes (off unless the
	// person set notify.agent).
	ctxN, stopN := context.WithCancel(ctx)
	defer stopN()
	go watchAgents(ctxN, o, inFront.Load)

	w.Navigate(be.URL)
	w.Run()
	return nil
}

// watchAgents runs the notifications of agents until ctx ends.
func watchAgents(ctx context.Context, o Options, inFront func() bool) {
	send := o.Notify
	if send == nil {
		send = notify.Send
	}
	lang := strings.ToLower(os.Getenv("LC_ALL") + os.Getenv("LC_MESSAGES") + os.Getenv("LANG"))
	reported := false
	notify.Watch(ctx, notify.Deps{
		Mode:   func() string { return o.Engine.Prefs().Get("notify.agent") },
		States: func() map[string]agents.State { return agents.Store{Dir: o.DataDir}.All() },
		Label:  func(path string) string { return worktreeLabel(ctx, o.Engine, path) },
		Send:   send,
		// Not while the person looks at the window, if they asked for that.
		Skip: func() bool { return inFront() && o.Engine.Prefs().Get("notify.skip-focused") == "on" },
		// Spanish when the system says so; the interface has its own language setting.
		Spanish: strings.HasPrefix(lang, "es"),
		OnError: func(err error) {
			if !reported { // one line, not one per notification
				reported = true
				fmt.Fprintf(o.Stderr, "theo-desktop: notifications: %v\n", err)
			}
		},
	})
}

// worktreeLabel names a worktree for a notification: "project / branch".
func worktreeLabel(ctx context.Context, e *engine.Engine, path string) string {
	projects, err := e.Projects()
	if err == nil {
		for _, p := range projects {
			list, err := e.Worktrees(ctx, p.Name)
			if err != nil {
				continue
			}
			for _, wt := range list {
				if sameDir(wt.Path, path) {
					name := wt.Branch
					if name == "" {
						name = wt.Path[strings.LastIndexAny(wt.Path, `/\`)+1:]
					}
					return p.Name + " / " + name
				}
			}
		}
	}
	return path[strings.LastIndexAny(path, `/\`)+1:]
}

// sameDir says whether two paths are the same folder, as the agent store
// compares them: cleaned, with symbolic links resolved, slashes forward.
func sameDir(a, b string) bool {
	canon := func(p string) string {
		p = filepath.Clean(p)
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		return filepath.ToSlash(p)
	}
	return canon(a) == canon(b)
}
