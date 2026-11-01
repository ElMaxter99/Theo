// Package desktop is the logic of the Theo desktop app (cmd/theo-desktop):
// which addresses may load inside the window, how an address is handed to the
// system browser, the script that enforces it in the page, and how the app
// starts (or reuses) the local server and closes it again.
//
// Nothing here needs cgo or a graphical session: the native window sits
// behind the Window interface and is implemented in window_webview.go, which
// only builds with the "desktop" tag. That keeps this package, and its tests,
// part of the default `go vet ./...` and `go test ./...`.
package desktop

import (
	"net/url"
	"strings"
)

// Decision says what to do with an address the page wants to go to.
type Decision int

const (
	// Stay: it is the Theo server itself; the window may load it.
	Stay Decision = iota
	// External: a web address of another origin (typically a container at
	// http://localhost:PORT); it opens in the system browser, never in the
	// window.
	External
	// Refuse: not a plain http(s) address (javascript:, file:, data:, ...),
	// or one that is malformed or hides its real host. Nothing happens.
	Refuse
)

func (d Decision) String() string {
	switch d {
	case Stay:
		return "stay"
	case External:
		return "external"
	default:
		return "refuse"
	}
}

// maxURLLen bounds what is parsed and handed to the operating system.
const maxURLLen = 4096

// Classify decides what to do with raw. appOrigin is the origin of the Theo
// server, "http://127.0.0.1:PORT". Only an exact match of scheme, host and
// port stays in the window: another port of 127.0.0.1 is another application.
func Classify(raw, appOrigin string) Decision {
	if raw == "" || len(raw) > maxURLLen || hasControl(raw) {
		return Refuse
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Refuse
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return Refuse
	}
	// "http://127.0.0.1:1234@evil.example/" displays like the app and goes
	// elsewhere; credentials in a link have no use here.
	if u.User != nil || u.Hostname() == "" || u.Opaque != "" {
		return Refuse
	}
	if sameOrigin(u, appOrigin) {
		return Stay
	}
	return External
}

func sameOrigin(u *url.URL, appOrigin string) bool {
	a, err := url.Parse(appOrigin)
	if err != nil || a.Scheme == "" || a.Host == "" {
		return false
	}
	return strings.EqualFold(u.Scheme, a.Scheme) && strings.EqualFold(u.Host, a.Host)
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
