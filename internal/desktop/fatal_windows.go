//go:build windows

package desktop

import (
	"syscall"
	"unsafe"
)

const (
	mbOK          = 0x0
	mbYesNo       = 0x4
	mbIconError   = 0x10
	mbIconWarning = 0x30
	idYes         = 6
)

func messageBox(msg string, flags uintptr) uintptr {
	text, err := syscall.UTF16PtrFromString(msg)
	if err != nil {
		return 0
	}
	title, _ := syscall.UTF16PtrFromString(Title)
	r, _, _ := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0,
		uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), flags)
	return r
}

// showFatal puts the message in a dialog. The Windows build is a GUI program
// without a console, so stderr goes nowhere and a failure would otherwise be
// silent.
func showFatal(msg string) { messageBox(msg, mbOK|mbIconError) }

// showMissingWebView2 asks, in a native Yes/No dialog, whether to open the
// WebView2 download page. It reports whether the user chose Yes.
func showMissingWebView2(msg string) bool { return messageBox(msg, mbYesNo|mbIconWarning) == idYes }
