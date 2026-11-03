//go:build !windows

package desktop

func showFatal(string) {}

// showMissingWebView2 only exists on Windows; elsewhere stderr is enough.
func showMissingWebView2(string) bool { return false }
