//go:build windows

package desktop

import (
	"syscall"
	"unsafe"
)

// showFatal puts the message in a dialog. The Windows build is a GUI program
// without a console, so stderr goes nowhere and a failure (for example a
// missing WebView2 runtime) would otherwise be silent.
func showFatal(msg string) {
	text, err := syscall.UTF16PtrFromString(msg)
	if err != nil {
		return
	}
	title, _ := syscall.UTF16PtrFromString(Title)
	const mbOK, mbIconError = 0x0, 0x10
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0,
		uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbOK|mbIconError)
}
