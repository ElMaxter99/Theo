package desktop

import (
	"bytes"
	"errors"
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

// fakeDialogs replaces the native dialogs and the browser for one test.
func fakeDialogs(t *testing.T, answerYes bool) (fatals, asks, opened *[]string) {
	t.Helper()
	oldFatal, oldAsk, oldOpen := fatalDialog, askMissingWebView2, openURL
	t.Cleanup(func() { fatalDialog, askMissingWebView2, openURL = oldFatal, oldAsk, oldOpen })
	fatals, asks, opened = &[]string{}, &[]string{}, &[]string{}
	fatalDialog = func(msg string) { *fatals = append(*fatals, msg) }
	askMissingWebView2 = func(msg string) bool { *asks = append(*asks, msg); return answerYes }
	openURL = func(u string) error { *opened = append(*opened, u); return nil }
	return
}

func TestReportFatalWritesStderr(t *testing.T) {
	fakeDialogs(t, false)
	var b bytes.Buffer
	ReportFatal(&b, fmt.Errorf("wrapped: %w", ErrNoWebview))
	if !strings.Contains(b.String(), "theo-desktop: wrapped: cannot create the system web view") {
		t.Errorf("stderr: %q", b.String())
	}
}

func TestReportFatalMissingWebView2(t *testing.T) {
	for _, yes := range []bool{true, false} {
		fatals, asks, opened := fakeDialogs(t, yes)
		ReportFatal(&bytes.Buffer{}, fmt.Errorf("window: %w", ErrNoWebview))
		if len(*asks) != 1 || len(*fatals) != 0 {
			t.Fatalf("yes=%v: asked %d times, fatal dialogs %d", yes, len(*asks), len(*fatals))
		}
		if want := 0; yes {
			want = 1
			if len(*opened) != want || (*opened)[0] != WebView2DownloadURL {
				t.Errorf("Yes opened %v", *opened)
			}
		} else if len(*opened) != want {
			t.Errorf("No opened %v", *opened)
		}
	}
}

func TestReportFatalOtherErrors(t *testing.T) {
	fatals, asks, _ := fakeDialogs(t, true)
	ReportFatal(&bytes.Buffer{}, errors.New("boom"))
	if len(*asks) != 0 || len(*fatals) != 1 || (*fatals)[0] != "theo-desktop: boom" {
		t.Errorf("asks %v, fatals %v", *asks, *fatals)
	}
}
