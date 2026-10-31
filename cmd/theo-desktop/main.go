//go:build desktop

// Command theo-desktop shows the Theo web interface in a native window
// instead of the browser. It starts the same server as `theo serve` inside the
// process (or opens a window onto the one already running) and stops it when
// the window closes. See docs/cli/desktop.md.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"

	"github.com/ElMaxter99/Theo/internal/desktop"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/ui"
)

// envRequestLog names a file that receives one line per request of the
// server this app starts ("POST /session": method and route pattern only,
// never paths, queries or bodies). It exists for the smoke test and for
// troubleshooting.
const envRequestLog = "THEO_DESKTOP_REQUEST_LOG"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version":
			fmt.Println(engine.VersionLine())
			return
		default:
			fmt.Fprintln(os.Stderr, "usage: theo-desktop   (no arguments; 'theo-desktop version' prints the version)")
			os.Exit(2)
		}
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "theo-desktop: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Opened from Finder the app has a bare PATH: find git and docker anyway.
	home, _ := os.UserHomeDir()
	os.Setenv("PATH", desktop.AugmentPath(os.Getenv("PATH"), runtime.GOOS, home))

	dir, err := registry.DefaultDir()
	if err != nil {
		return err
	}
	e, err := engine.Open()
	if err != nil {
		return err
	}
	var reqLog *log.Logger
	if p := os.Getenv(envRequestLog); p != "" {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		reqLog = log.New(f, "", 0)
	}
	return desktop.Run(context.Background(), desktop.Options{
		Engine:     e,
		DataDir:    dir,
		Assets:     ui.Assets(),
		NewWindow:  desktop.NewWebviewWindow,
		RequestLog: reqLog,
		Stderr:     os.Stderr,
	})
}
