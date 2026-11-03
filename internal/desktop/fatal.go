package desktop

import (
	"errors"
	"fmt"
	"io"
)

// ErrNoWebview is returned when the system web view cannot be created.
var ErrNoWebview = errors.New("cannot create the system web view (on Linux it needs a display and WebKitGTK; on Windows the WebView2 runtime)")

// WebView2DownloadURL is Microsoft's download page for the WebView2 runtime.
const WebView2DownloadURL = "https://developer.microsoft.com/microsoft-edge/webview2/"

// ReportFatal tells the user why the app could not start: on stderr, and on
// Windows also in a dialog because that build has no console. A missing
// WebView2 runtime gets its own dialog (design board V-12), which offers to
// open the download page.
func ReportFatal(w io.Writer, err error) {
	msg := fmt.Sprintf("theo-desktop: %v", err)
	fmt.Fprintln(w, msg)
	if errors.Is(err, ErrNoWebview) {
		if askMissingWebView2(WebView2Message(err)) {
			if oerr := openURL(WebView2DownloadURL); oerr != nil {
				fmt.Fprintf(w, "theo-desktop: %v\n", oerr)
			}
		}
		return
	}
	fatalDialog(msg)
}

// The dialogs and the browser, replaced in tests: a real dialog would wait
// for a click that never comes.
var (
	fatalDialog        = showFatal
	askMissingWebView2 = showMissingWebView2
	openURL            = systemOpen
)

// WebView2Message is the text of the missing-WebView2 dialog, in Spanish like
// the interface (Q-029). The technical detail goes last, for bug reports.
func WebView2Message(err error) string {
	return "Theo necesita Microsoft Edge WebView2\n\n" +
		"La ventana de Theo usa WebView2, un componente gratuito de Microsoft que viene con Windows 11 y con Windows 10 actualizado. En este equipo no está instalado.\n\n" +
		"¿Abrir la página de descarga de Microsoft? Elige «Evergreen Bootstrapper» e instálalo; luego vuelve a abrir Theo.\n\n" +
		"Mientras tanto puedes usar Theo en el navegador: theo ui\n\n" +
		"Detalle: " + err.Error()
}
