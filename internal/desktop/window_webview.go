//go:build desktop

package desktop

import (
	webview "github.com/webview/webview_go"
)

// Default and minimum size of the window, in pixels.
const (
	DefaultWidth  = 1280
	DefaultHeight = 820
	MinWidth      = 900
	MinHeight     = 600
)

// webviewWindow is the Window backed by the system web view: WebView2 on
// Windows, WKWebView on macOS, WebKitGTK on Linux. Developer tools are off.
type webviewWindow struct {
	w webview.WebView
}

// NewWebviewWindow opens the one native window of the app. It must be called
// on the main thread (webview_go locks it in its init), and returns an error
// instead of panicking when the system web view cannot be created.
func NewWebviewWindow() (Window, error) {
	w := webview.New(false)
	if w == nil {
		return nil, ErrNoWebview
	}
	w.SetTitle(Title)
	w.SetSize(DefaultWidth, DefaultHeight, webview.HintNone)
	w.SetSize(MinWidth, MinHeight, webview.HintMin)
	setWindowIcon(w.Window())
	return &webviewWindow{w: w}, nil
}

func (v *webviewWindow) Init(js string) { v.w.Init(js) }

func (v *webviewWindow) Bind(name string, fn func(string)) error {
	return v.w.Bind(name, fn)
}

func (v *webviewWindow) Navigate(raw string) { v.w.Navigate(raw) }
func (v *webviewWindow) Run()                { v.w.Run() }
func (v *webviewWindow) Terminate()          { v.w.Terminate() }
func (v *webviewWindow) Destroy()            { v.w.Destroy() }
