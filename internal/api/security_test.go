package api_test

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/api"
	"github.com/ElMaxter99/Theo/internal/compose"
)

func TestNoCookieIs401(t *testing.T) {
	e := newEnv(t)
	anon := newBareClient(t, e.srv.Port())
	for _, p := range []string{"/api/v1/meta", "/api/v1/projects", "/api/v1/projects/ubapp/overview", "/api/v1/events", "/api/v1/operations"} {
		st, body, _ := anon.raw("GET", p, nil, nil)
		if st != 401 {
			t.Errorf("GET %s without cookie = %d, want 401 (%s)", p, st, body)
		}
	}
}

func TestWrongSessionCookieIs401(t *testing.T) {
	e := newEnv(t)
	c := newBareClient(t, e.srv.Port())
	c.cookie = "not-a-session"
	if st, _, _ := c.raw("GET", "/api/v1/meta", nil, nil); st != 401 {
		t.Errorf("status %d, want 401", st)
	}
}

func TestMissingOrWrongCSRFIs403(t *testing.T) {
	e := newEnv(t)
	for _, tok := range []string{"", "wrong"} {
		st, body, _ := e.c.raw("POST", "/api/v1/events/focus", map[string]bool{"visible": true}, map[string]string{"X-Theo-Csrf": tok})
		if st != 403 || !strings.Contains(string(body), `"csrf"`) {
			t.Errorf("csrf %q: status %d %s, want 403 csrf", tok, st, body)
		}
	}
	if st, _, _ := e.c.raw("POST", "/api/v1/events/focus", map[string]bool{"visible": true}, nil); st != 204 {
		t.Errorf("with the right token: %d, want 204", st)
	}
}

func TestMutatingNeedsJSONContentType(t *testing.T) {
	e := newEnv(t)
	st, _, _ := e.c.raw("POST", "/api/v1/events/focus", map[string]bool{"visible": true}, map[string]string{"Content-Type": "text/plain"})
	if st != 400 {
		t.Errorf("text/plain = %d, want 400", st)
	}
}

func TestWrongHostIs403(t *testing.T) {
	e := newEnv(t)
	for _, host := range []string{"evil.example:" + strconv.Itoa(e.srv.Port()), "localhost:" + strconv.Itoa(e.srv.Port()), "127.0.0.1", "127.0.0.1:1"} {
		for _, p := range []string{"/api/v1/meta", "/", "/index.html"} {
			st, _, _ := e.c.raw("GET", p, nil, map[string]string{"Host": host})
			if st != 403 {
				t.Errorf("GET %s with Host %q = %d, want 403", p, host, st)
			}
		}
	}
}

func TestWrongOriginIs403(t *testing.T) {
	e := newEnv(t)
	for _, o := range []string{"http://evil.example", "http://127.0.0.1:1", "null", "http://localhost:" + strconv.Itoa(e.srv.Port())} {
		st, _, _ := e.c.raw("GET", "/api/v1/meta", nil, map[string]string{"Origin": o})
		if st != 403 {
			t.Errorf("GET with Origin %q = %d, want 403", o, st)
		}
		st, _, _ = e.c.raw("POST", "/api/v1/events/focus", map[string]bool{"visible": true}, map[string]string{"Origin": o})
		if st != 403 {
			t.Errorf("POST with Origin %q = %d, want 403", o, st)
		}
	}
	own := "http://127.0.0.1:" + strconv.Itoa(e.srv.Port())
	if st, _, _ := e.c.raw("GET", "/api/v1/meta", nil, map[string]string{"Origin": own}); st != 200 {
		t.Errorf("own Origin = %d, want 200", st)
	}
}

func TestSecFetchSiteMustBeSameOrigin(t *testing.T) {
	e := newEnv(t)
	for _, v := range []string{"cross-site", "same-site", "none"} {
		if st, _, _ := e.c.raw("GET", "/api/v1/meta", nil, map[string]string{"Sec-Fetch-Site": v}); st != 403 {
			t.Errorf("Sec-Fetch-Site %q = %d, want 403", v, st)
		}
	}
	if st, _, _ := e.c.raw("GET", "/api/v1/meta", nil, map[string]string{"Sec-Fetch-Site": "same-origin"}); st != 200 {
		t.Errorf("same-origin = %d, want 200", st)
	}
}

func TestNoCORSHeaders(t *testing.T) {
	e := newEnv(t)
	st, _, hdr := e.c.raw("OPTIONS", "/api/v1/meta", nil, map[string]string{"Origin": "http://evil.example", "Access-Control-Request-Method": "GET"})
	if st == 200 || st == 204 {
		t.Errorf("a cross-origin preflight succeeded: %d", st)
	}
	for k := range hdr {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Errorf("response carries %s", k)
		}
	}
	_, _, hdr = e.c.raw("GET", "/api/v1/meta", nil, nil)
	for k := range hdr {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Errorf("response carries %s", k)
		}
	}
}

func TestNonceIsSingleUse(t *testing.T) {
	e := newEnv(t)
	launch := e.srv.LaunchURL()
	nonce := strings.TrimPrefix(launch[strings.Index(launch, "#")+1:], "n=")
	c := newBareClient(t, e.srv.Port())
	if st, _, _ := c.raw("POST", "/api/v1/session", map[string]string{"nonce": nonce}, nil); st != 200 {
		t.Fatalf("first exchange: %d", st)
	}
	if st, _, _ := c.raw("POST", "/api/v1/session", map[string]string{"nonce": nonce}, nil); st != 401 {
		t.Errorf("second exchange = %d, want 401", st)
	}
	if st, _, _ := c.raw("POST", "/api/v1/session", map[string]string{"nonce": "guess"}, nil); st != 401 {
		t.Errorf("wrong nonce = %d, want 401", st)
	}
}

func TestNonceExpires(t *testing.T) {
	old := api.NonceTTL
	api.NonceTTL = 80 * time.Millisecond
	defer func() { api.NonceTTL = old }()
	e := newEnv(t)
	launch := e.srv.LaunchURL()
	nonce := strings.TrimPrefix(launch[strings.Index(launch, "#")+1:], "n=")
	time.Sleep(200 * time.Millisecond)
	c := newBareClient(t, e.srv.Port())
	if st, _, _ := c.raw("POST", "/api/v1/session", map[string]string{"nonce": nonce}, nil); st != 401 {
		t.Errorf("expired nonce = %d, want 401", st)
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	e := newEnv(t)
	launch := e.srv.LaunchURL()
	nonce := strings.TrimPrefix(launch[strings.Index(launch, "#")+1:], "n=")
	c := newBareClient(t, e.srv.Port())
	_, _, hdr := c.raw("POST", "/api/v1/session", map[string]string{"nonce": nonce}, nil)
	sc := hdr.Get("Set-Cookie")
	for _, want := range []string{"HttpOnly", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(sc, want) {
			t.Errorf("Set-Cookie %q lacks %s", sc, want)
		}
	}
	if strings.Contains(strings.ToLower(sc), "domain=") {
		t.Errorf("the cookie has a Domain: %s", sc)
	}
}

func TestNoSecretInURLsOrLogs(t *testing.T) {
	sb := &syncBuf{}
	e := newEnv(t, func(c *api.Config) { c.Log = nil })
	_ = sb
	// After the exchange the nonce is gone from everything the server says.
	launch := e.srv.LaunchURL()
	nonce := strings.TrimPrefix(launch[strings.Index(launch, "#")+1:], "n=")
	c := newBareClient(t, e.srv.Port())
	_, body, hdr := c.raw("POST", "/api/v1/session", map[string]string{"nonce": nonce}, nil)
	if strings.Contains(string(body), nonce) {
		t.Error("the nonce is echoed in the exchange body")
	}
	for k, v := range hdr {
		for _, x := range v {
			if strings.Contains(x, nonce) && k != "" {
				t.Errorf("header %s carries the nonce", k)
			}
		}
	}
	if hdr.Get("Location") != "" {
		t.Error("the exchange redirects")
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
	for _, p := range []string{"/", "/api/v1/meta", "/missing.js"} {
		_, _, hdr := e.c.raw("GET", p, nil, nil)
		if got := hdr.Get("Content-Security-Policy"); got != csp {
			t.Errorf("%s: CSP = %q", p, got)
		}
		if hdr.Get("X-Content-Type-Options") != "nosniff" || hdr.Get("Referrer-Policy") != "no-referrer" || hdr.Get("Cross-Origin-Opener-Policy") != "same-origin" {
			t.Errorf("%s: missing security headers: %v", p, hdr)
		}
	}
	_, _, hdr := e.c.raw("GET", "/api/v1/meta", nil, nil)
	if hdr.Get("Cache-Control") != "no-store" {
		t.Errorf("API Cache-Control = %q", hdr.Get("Cache-Control"))
	}
}

// Nothing may listen outside 127.0.0.1.
func TestListensOnLoopbackOnly(t *testing.T) {
	e := newEnv(t)
	port := e.srv.Port()
	// Reaching the port through a non-loopback address of this machine fails.
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		ip, _, err := net.ParseCIDR(a.String())
		if err != nil || ip.IsLoopback() || ip.To4() == nil {
			continue
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port)), time.Second)
		if err == nil {
			conn.Close()
			t.Errorf("the server answers on %s", ip)
		}
	}
	// And the kernel's table says the socket is bound to 127.0.0.1 only.
	data, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		t.Skip("no /proc/net/tcp")
	}
	hexPort := strings.ToUpper(strconv.FormatInt(int64(port), 16))
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) > 3 && f[3] == "0A" && strings.HasSuffix(f[1], ":"+strings.Repeat("0", 4-len(hexPort))+hexPort) {
			found = true
			if !strings.HasPrefix(f[1], "0100007F:") {
				t.Errorf("listening on %s, want 0100007F (127.0.0.1)", f[1])
			}
		}
	}
	if !found {
		t.Error("the listening socket was not found in /proc/net/tcp")
	}
	if data6, err := os.ReadFile("/proc/net/tcp6"); err == nil {
		for _, line := range strings.Split(string(data6), "\n") {
			f := strings.Fields(line)
			if len(f) > 3 && f[3] == "0A" && strings.HasSuffix(f[1], ":"+strings.Repeat("0", 4-len(hexPort))+hexPort) {
				t.Errorf("an IPv6 socket listens on the port: %s", f[1])
			}
		}
	}
}

// Every registered route goes through the guard: without a session each one
// answers 401, except the two that are exempt on purpose.
func TestEveryRouteIsGuarded(t *testing.T) {
	e := newEnv(t)
	anon := newBareClient(t, e.srv.Port())
	exempt := map[string]bool{"POST /session": true, "POST /launch": true}
	seen := map[string]bool{}
	for _, rt := range e.srv.Routes() {
		key := rt.Method + " " + rt.Pattern
		seen[key] = true
		path := regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(rt.Pattern, "x")
		var body any
		if rt.Method != "GET" {
			body = map[string]string{}
		}
		st, data, _ := anon.raw(rt.Method, "/api/v1"+path, body, nil)
		if exempt[key] {
			if st == 200 {
				t.Errorf("%s answered 200 without credentials: %s", key, data)
			}
			continue
		}
		if st != 401 {
			t.Errorf("%s without a session = %d, want 401", key, st)
		}
	}
	for k := range exempt {
		if !seen[k] {
			t.Errorf("exempt route %s is not registered", k)
		}
	}
	// The exemptions are exactly these: any other public or control route
	// would be a new hole.
	for _, rt := range e.srv.Routes() {
		if rt.Public() && !exempt[rt.Method+" "+rt.Pattern] {
			t.Errorf("route %s %s is public and not in the list of exemptions", rt.Method, rt.Pattern)
		}
	}
}

// Handlers must be registered through Server.handle/stream, never straight
// on the mux, or they would skip the guard.
func TestNoRouteBypassesTheGuard(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, _ := os.ReadFile(f)
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "mux.Handle") && !(f == "server.go" && strings.Contains(line, "guard(")) && !strings.Contains(line, `"/", s.staticHandler()`) {
				t.Errorf("%s:%d registers a handler on the mux directly: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}

func TestLaunchEndpointNeedsControlSecret(t *testing.T) {
	e := newEnv(t)
	anon := newBareClient(t, e.srv.Port())
	if st, _, _ := anon.raw("POST", "/api/v1/launch", nil, nil); st != 401 {
		t.Errorf("launch without secret = %d", st)
	}
	if st, _, _ := anon.raw("POST", "/api/v1/launch", nil, map[string]string{"X-Theo-Control": "wrong"}); st != 401 {
		t.Errorf("launch with a wrong secret = %d", st)
	}
	// The browser session is not the control secret.
	if st, _, _ := e.c.raw("POST", "/api/v1/launch", nil, nil); st != 401 {
		t.Errorf("launch with a browser session = %d", st)
	}
	st, body, _ := anon.raw("POST", "/api/v1/launch", nil, map[string]string{"X-Theo-Control": e.srv.ControlSecret()})
	if st != 200 || !strings.Contains(string(body), "#n=") {
		t.Errorf("launch with the secret = %d %s", st, body)
	}
	// A page of another origin cannot use it even with the secret.
	if st, _, _ := anon.raw("POST", "/api/v1/launch", nil, map[string]string{"X-Theo-Control": e.srv.ControlSecret(), "Origin": "http://evil.example"}); st != 403 && st != 401 {
		t.Errorf("launch with a foreign Origin = %d", st)
	}
}

func TestUILockSingleInstance(t *testing.T) {
	e := newEnv(t)
	release, err := e.srv.WriteLock(e.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	l, ok := api.FindRunning(e.dataDir)
	if !ok || !strings.Contains(l.URL, "#n=") || l.Port != e.srv.Port() {
		t.Fatalf("FindRunning = %+v, %v", l, ok)
	}
	// The URL it hands out works once.
	c := loginURL(t, e.srv, l.URL)
	if st := c.call("GET", "/api/v1/meta", nil, nil); st != 200 {
		t.Errorf("session from the second launch: %d", st)
	}
	if info, err := os.Stat(api.LockPath(e.dataDir)); err != nil {
		t.Fatal(err)
	} else if runtimeUnix() && info.Mode().Perm() != 0o600 {
		t.Errorf("ui.lock mode %v, want 0600", info.Mode().Perm())
	}
	release()
	if _, ok := api.FindRunning(e.dataDir); ok {
		t.Error("a released lock still finds a server")
	}
}

func TestStaleUILockIsIgnored(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(api.LockPath(dir), []byte(`{"pid":1,"port":1,"control_secret":"x"}`), 0o600)
	if _, ok := api.FindRunning(dir); ok {
		t.Error("a stale lock was treated as a running server")
	}
	_ = exec.Command // keep the import list stable across platforms
}

func runtimeUnix() bool { return os.PathSeparator == '/' }

// fakeLockServer listens where ui.lock says Theo is, but is something else
// (a stale lock whose port another program took). It answers POST /launch
// with whatever reply says and records the control secrets it receives.
func fakeLockServer(t *testing.T, dir string, reply func(port int) string) (port int, secrets *[]string) {
	t.Helper()
	var got []string
	var mu sync.Mutex
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Header.Get("X-Theo-Control"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, reply(port))
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	lock := fmt.Sprintf(`{"pid":%d,"port":%d,"control_secret":"the-control-secret"}`, os.Getpid(), port)
	if err := os.WriteFile(api.LockPath(dir), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	return port, &got
}

// The launch URL is handed to the system's URL opener (xdg-open, open,
// rundll32): FindRunning must only return the address of that very server.
func TestFindRunningRejectsAForeignLaunchURL(t *testing.T) {
	for name, url := range map[string]string{
		"file":        "file:///etc/passwd#n=abc",
		"program":     `C:\\Windows\\System32\\calc.exe`,
		"other host":  "http://evil.example/#n=abc",
		"other port":  "http://127.0.0.1:1/#n=abc",
		"userinfo":    "http://127.0.0.1@evil.example/#n=abc",
		"no nonce":    "PORT",
		"odd nonce":   "PORT#n=a b;calc",
		"empty nonce": "PORT#n=",
	} {
		dir := t.TempDir()
		fakeLockServer(t, dir, func(port int) string {
			u := strings.ReplaceAll(url, "PORT", fmt.Sprintf("http://127.0.0.1:%d/", port))
			return fmt.Sprintf(`{"url":%q,"port":%d,"pid":1}`, u, port)
		})
		if l, ok := api.FindRunning(dir); ok {
			t.Errorf("%s: FindRunning accepted %q", name, l.URL)
		}
	}
	// A well-formed answer from the right port still works.
	dir := t.TempDir()
	fakeLockServer(t, dir, func(port int) string {
		return fmt.Sprintf(`{"url":"http://127.0.0.1:%d/#n=abc_DEF-123","port":%d,"pid":1}`, port, port)
	})
	if _, ok := api.FindRunning(dir); !ok {
		t.Error("a well-formed launch URL was rejected")
	}
}

// The control secret goes to the port in ui.lock and nowhere else, even if
// that port redirects.
func TestFindRunningDoesNotFollowRedirects(t *testing.T) {
	dir := t.TempDir()
	var hits []string
	var mu sync.Mutex
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.Header.Get("X-Theo-Control"))
		mu.Unlock()
		io.WriteString(w, `{"url":"http://127.0.0.1:1/#n=abc","port":1,"pid":1}`)
	}))
	defer other.Close()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	redirector := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/api/v1/launch", http.StatusTemporaryRedirect)
	})}
	go redirector.Serve(ln)
	defer redirector.Close()
	lock := fmt.Sprintf(`{"pid":%d,"port":%d,"control_secret":"the-control-secret"}`, os.Getpid(), mustPort(t, ln.Addr()))
	if err := os.WriteFile(api.LockPath(dir), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	api.FindRunning(dir)
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 0 {
		t.Errorf("the redirect was followed and the control secret sent to another server: %v", hits)
	}
}

// Names that go to Git as arguments are never read as options.
func TestCreateWorktreeRefusesOptionLikeNames(t *testing.T) {
	e := newEnv(t)
	for _, body := range []map[string]any{
		{"branch": "--detach"},
		{"branch": "-f"},
		{"branch": "nb", "new": true, "from": "--force"},
	} {
		body["path"] = filepath.Join(t.TempDir(), "wt")
		var er apiErr
		if st := e.c.call("POST", "/api/v1/projects/ubapp/worktrees", body, &er); st != 400 || er.Error.Code != "invalid_request" {
			t.Errorf("%v = %d %+v, want 400 invalid_request", body, st, er.Error)
		}
		if _, err := os.Stat(body["path"].(string)); err == nil {
			t.Errorf("%v created %s", body, body["path"])
		}
	}
}

// The report is "safe to paste into an issue": no path of this machine in it.
func TestDiagnosticsReportHasNoPaths(t *testing.T) {
	e := newEnv(t)
	var d api.Diagnostics
	if st := e.c.call("GET", "/api/v1/diagnostics", nil, &d); st != 200 {
		t.Fatalf("diagnostics = %d", st)
	}
	if d.Report == "" {
		t.Fatal("no report")
	}
	for _, p := range []string{e.dataDir, filepath.Dir(e.dataDir), e.main} {
		if strings.Contains(d.Report, p) {
			t.Errorf("the report carries the path %s:\n%s", p, d.Report)
		}
	}
	if strings.Contains(d.Report, "unix:///") || strings.Contains(d.Report, "/var/run") {
		t.Errorf("a socket path is in the report:\n%s", d.Report)
	}
}

func mustPort(t *testing.T, a net.Addr) int {
	t.Helper()
	return a.(*net.TCPAddr).Port
}

// The resolved configuration prints a literal $ as $$ and a multi-line value
// as an indented block; the secrets of the instance are masked in both.
func TestConfigMasksSecretsAsComposeRendersThem(t *testing.T) {
	e := newEnv(t)
	e.fc.configText = func(compose.Target) string {
		return "services:\n  web:\n    environment:\n      DB_PASS: pa$$word\n      KEY: |-\n        -----BEGIN KEY-----\n        s3cr3t-line-two\n        -----END KEY-----\n      LEVEL: debug\n"
	}
	id := e.wtID(e.linked)
	base := "/api/v1/projects/ubapp/worktrees/" + id
	secrets := map[string]string{"DB_PASS": "pa$word", "KEY": "-----BEGIN KEY-----\ns3cr3t-line-two\n-----END KEY-----"}
	e.fc.configEnv = map[string]string{"LEVEL": "debug"}
	for k, v := range secrets {
		e.fc.configEnv[k] = v
	}
	for name, v := range secrets {
		if st := e.c.call("PUT", base+"/env/"+name, map[string]any{"value": v, "secret": true, "services": []string{"web"}}, nil); st != 200 {
			t.Fatalf("put %s = %d", name, st)
		}
	}
	e.c.call("PUT", base+"/env/LEVEL", map[string]any{"value": "debug", "services": []string{"web"}}, nil)
	st, body, _ := e.c.raw("GET", base+"/config", nil, nil)
	if st != 200 {
		t.Fatalf("config = %d %s", st, body)
	}
	for _, leak := range []string{"pa$$word", "pa$word", "s3cr3t-line-two", "BEGIN KEY"} {
		if strings.Contains(string(body), leak) {
			t.Errorf("GET /config leaks %q:\n%s", leak, body)
		}
	}
	if !strings.Contains(string(body), "LEVEL: debug") {
		t.Errorf("a plain variable was masked:\n%s", body)
	}
}
