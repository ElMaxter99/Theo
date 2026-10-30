package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/ElMaxter99/Theo/internal/api"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/ui"
)

func init() {
	registerCommand("serve", func(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
		return runServe(e, "serve", args, stdout, stderr)
	})
	registerCommand("ui", func(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
		return runServe(e, "ui", args, stdout, stderr)
	})
	registerHelp(45, `
  ui [--no-open] [--idle-timeout 15m]          Start the local web interface and open it in the default
                                               browser. Listens on 127.0.0.1 only; a second 'theo ui'
                                               opens another tab on the running server
  serve [--print-launch-url] [--no-open]       The same server without opening a browser.
        [--idle-timeout 15m]                   --print-launch-url prints one JSON line with the launch
                                               URL, the port and the pid
`)
}

func runServe(e *engine.Engine, cmd string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	var printURL, noOpen bool
	var idle time.Duration
	fs.BoolVar(&printURL, "print-launch-url", false, "print {\"url\",\"port\",\"pid\"} as one JSON line and do not open a browser")
	fs.BoolVar(&noOpen, "no-open", cmd == "serve", "do not open a browser; print the launch URL")
	fs.DurationVar(&idle, "idle-timeout", api.DefaultIdleTimeout, "stop after this long without clients and operations (0 never)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return usageErrorf("%s takes no arguments", cmd)
	}
	dir, err := registry.DefaultDir()
	if err != nil {
		return err
	}
	if idle == 0 {
		idle = -1
	}

	unguard, err := api.GuardStart(dir)
	if err != nil {
		return err
	}
	guarded := true
	defer func() {
		if guarded {
			unguard()
		}
	}()

	// One server per data directory: a second start asks the first for a
	// launch URL instead of starting another.
	if l, ok := api.FindRunning(dir); ok {
		return announce(l, printURL, noOpen, true, stdout, stderr)
	}

	srv, err := api.New(api.Config{Engine: e, DataDir: dir, Assets: ui.Assets(), IdleTimeout: idle})
	if err != nil {
		return err
	}
	release, err := srv.WriteLock(dir)
	if err != nil {
		srv.Close()
		return err
	}
	defer release()
	unguard()
	guarded = false

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	l := &api.Launch{URL: srv.LaunchURL(), Port: srv.Port(), PID: os.Getpid()}
	if err := announce(l, printURL, noOpen, false, stdout, stderr); err != nil {
		stop()
		<-done
		return err
	}
	return <-done
}

// announce tells the user (or the test harness) where the interface is, and
// opens the browser when asked to.
func announce(l *api.Launch, printURL, noOpen, existing bool, stdout, stderr io.Writer) error {
	if printURL {
		return json.NewEncoder(stdout).Encode(l)
	}
	if noOpen {
		if existing {
			fmt.Fprintf(stderr, "theo: the interface is already running (pid %d)\n", l.PID)
		}
		fmt.Fprintf(stdout, "%s\n", l.URL)
		fmt.Fprintln(stderr, "theo: open that address in your browser within 60 seconds. It works once: do not share it.")
		fmt.Fprintln(stderr, "theo: the server listens on 127.0.0.1 only and stops by itself after a while without use. Ctrl+C stops it now.")
		return nil
	}
	if existing {
		fmt.Fprintf(stderr, "theo: the interface is already running (pid %d); opening another tab\n", l.PID)
	} else {
		fmt.Fprintf(stderr, "theo: interface on http://127.0.0.1:%d/ (Ctrl+C stops it)\n", l.Port)
	}
	if err := openBrowser(l.URL); err != nil {
		fmt.Fprintf(stderr, "theo: could not open a browser (%v). Open this address yourself within 60 seconds:\n%s\n", err, l.URL)
	}
	return nil
}

// openBrowser asks the system to open url in the default browser. No shell
// is involved.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
