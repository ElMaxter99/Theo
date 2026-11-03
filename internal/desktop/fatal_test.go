package desktop

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestWebView2Message(t *testing.T) {
	err := fmt.Errorf("window: %w", ErrNoWebview)
	msg := WebView2Message(err)
	for _, want := range []string{"WebView2", "Evergreen Bootstrapper", "theo ui", "Detalle: window: cannot create the system web view"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
}

func TestReportFatalWritesStderr(t *testing.T) {
	var b bytes.Buffer
	ReportFatal(&b, fmt.Errorf("wrapped: %w", ErrNoWebview))
	if !strings.Contains(b.String(), "theo-desktop: wrapped: cannot create the system web view") {
		t.Errorf("stderr: %q", b.String())
	}
}
