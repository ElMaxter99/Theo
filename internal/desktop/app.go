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
	"syscall"

	"github.com/ElMaxter99/Theo/internal/api"
	"github.com/ElMaxter99/Theo/internal/engine"
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

	w.Navigate(be.URL)
	w.Run()
	return nil
}
