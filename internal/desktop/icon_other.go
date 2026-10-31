//go:build !windows

package desktop

import "unsafe"

// setWindowIcon is a no-op outside Windows: macOS takes the icon from the
// .app bundle and Linux from the .desktop file (see docs/cli/desktop.md).
func setWindowIcon(unsafe.Pointer) {}
