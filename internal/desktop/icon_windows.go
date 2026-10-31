//go:build windows

package desktop

import (
	"os"
	"syscall"
	"unsafe"
)

// setWindowIcon gives the window the icon embedded in the executable (the
// first icon of the file, put there by the release build). The web view
// library registers its window class with a stock icon, so without this the
// title bar and the Alt+Tab list would show the generic one. Best effort: a
// failure leaves the default icon.
func setWindowIcon(hwnd unsafe.Pointer) {
	if hwnd == nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	name, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return
	}
	shell32 := syscall.NewLazyDLL("shell32.dll")
	user32 := syscall.NewLazyDLL("user32.dll")
	extract := shell32.NewProc("ExtractIconW")
	send := user32.NewProc("SendMessageW")
	hInst, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	icon, _, _ := extract.Call(hInst, uintptr(unsafe.Pointer(name)), 0)
	if icon <= 1 { // 0: no icon, 1: not an executable
		return
	}
	const wmSetIcon, iconSmall, iconBig = 0x0080, 0, 1
	send.Call(uintptr(hwnd), wmSetIcon, iconBig, icon)
	send.Call(uintptr(hwnd), wmSetIcon, iconSmall, icon)
}
