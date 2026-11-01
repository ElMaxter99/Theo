//go:build desktop

package desktop

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	webview "github.com/webview/webview_go"
)

// The shim runs in the real system web view. On Linux this needs a display
// (xvfb-run -a go test -tags desktop ./internal/desktop); without one it is
// skipped. It serves a page with every kind of link, "clicks" them from
// script, and checks which ones reached Go and where the window ended up.
func TestShimInRealWebview(t *testing.T) {
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" {
		t.Skip("no display: run under xvfb-run")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	const page = `<!doctype html><title>t</title>
<a id="ext" href="http://localhost:9/container">container</a>
<a id="extblank" href="http://localhost:9/blank" target="_blank" rel="noopener">container blank</a>
<a id="js" href="javascript:void(0)">js</a>
<a id="inside" href="/inside">inside</a>
<a id="insideblank" href="/insideblank" target="_blank">inside blank</a>
<form id="f" action="http://localhost:9/post" method="post"><button id="sub">go</button></form>`

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// The server's own policy (internal/api/security.go): the injected
		// shim and the test driver are not page scripts and must still run.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		fmt.Fprint(w, page)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	origin := srv.URL

	var mu sync.Mutex
	var called []string
	reported := make(chan string, 4)

	w := webview.New(false)
	if w == nil {
		t.Skip("cannot create a web view here")
	}
	defer w.Destroy()
	if err := w.Bind(BindName, func(raw string) {
		mu.Lock()
		called = append(called, raw)
		mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Bind("report", func(s string) {
		reported <- s
	}); err != nil {
		t.Fatal(err)
	}
	w.Init(Shim(origin))
	// After the page loads, drive it and report; own script, registered after
	// the shim, runs in the same page.
	w.Init(`window.addEventListener("load", function () {
	  if (location.pathname === "/start") {
	    ["ext", "extblank", "js"].forEach(function (i) { document.getElementById(i).click(); });
	    window.open("http://localhost:9/opened");
	    window.open("javascript:void(0)");
	    var notPrevented = document.getElementById("f").dispatchEvent(new Event("submit", {cancelable: true, bubbles: true}));
	    window.report(JSON.stringify({phase: 1, href: location.href, submitNotPrevented: notPrevented}));
	    document.getElementById("insideblank").click();
	  } else {
	    window.report(JSON.stringify({phase: 2, href: location.href}));
	  }
	});`)
	w.Navigate(origin + "/start")

	var got []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		timeout := time.After(30 * time.Second)
		for len(got) < 2 {
			select {
			case r := <-reported:
				got = append(got, r)
			case <-timeout:
				w.Terminate()
				return
			}
		}
		time.Sleep(300 * time.Millisecond) // let a wrong navigation, if any, happen
		w.Terminate()
	}()
	w.Run()
	<-done

	mu.Lock()
	defer mu.Unlock()
	want := []string{"http://localhost:9/container", "http://localhost:9/blank", "http://localhost:9/opened"}
	if strings.Join(called, "|") != strings.Join(want, "|") {
		t.Errorf("addresses sent to the system browser = %v, want %v", called, want)
	}
	if len(got) != 2 {
		t.Fatalf("page reports = %v", got)
	}
	var p1, p2 struct {
		Href               string `json:"href"`
		SubmitNotPrevented bool   `json:"submitNotPrevented"`
	}
	json.Unmarshal([]byte(got[0]), &p1)
	json.Unmarshal([]byte(got[1]), &p2)
	if p1.Href != origin+"/start" || p1.SubmitNotPrevented {
		t.Errorf("after the external clicks: %+v (the window must stay and the cross-origin form must be stopped)", p1)
	}
	if p2.Href != origin+"/insideblank" {
		t.Errorf("a same-origin target=_blank link should load in the window, got %q", p2.Href)
	}
}
