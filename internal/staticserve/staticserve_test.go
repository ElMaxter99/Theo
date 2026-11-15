package staticserve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func site(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>portal</h1>"), 0o644)
	os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log(1)"), 0o644)
	os.MkdirAll(filepath.Join(dir, "css"), 0o755)
	os.WriteFile(filepath.Join(dir, "css", "a.css"), []byte("a{}"), 0o644)
	os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.txt"), []byte("secret"), 0o644)
	return dir
}

func get(t *testing.T, h http.Handler, p string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.Path = p
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServesUnderTheBasePath(t *testing.T) {
	h, err := Handler(Config{Dir: site(t), Base: "/portal/"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path     string
		code     int
		location string
		body     string
	}{
		{"/", 302, "/portal/", ""},
		{"/portal", 302, "/portal/", ""},
		{"/portal/", 200, "", "<h1>portal</h1>"},
		{"/portal/app.js", 200, "", "console.log(1)"},
		{"/portal/css/a.css", 200, "", "a{}"},
		{"/portal/missing.js", 404, "", ""},
		{"/portal/nope", 404, "", ""}, // no SPA: not found
		{"/other", 404, "", ""},
		{"/portalx/app.js", 404, "", ""}, // a prefix of the name is not the base
		{"/app.js", 404, "", ""},         // outside the base
	} {
		rec := get(t, h, c.path)
		if rec.Code != c.code || (c.location != "" && rec.Header().Get("Location") != c.location) || (c.body != "" && !strings.Contains(rec.Body.String(), c.body)) {
			t.Errorf("GET %s: %d location %q body %q; want %d %q %q", c.path, rec.Code, rec.Header().Get("Location"), rec.Body.String(), c.code, c.location, c.body)
		}
	}
}

func TestRootBaseAndNoEscape(t *testing.T) {
	h, _ := Handler(Config{Dir: site(t), Base: "/"})
	if rec := get(t, h, "/app.js"); rec.Code != 200 {
		t.Errorf("root base: %d", rec.Code)
	}
	for _, p := range []string{"/../secret.txt", "/css/../../secret.txt", "/..%2fsecret.txt"} {
		if rec := get(t, h, p); rec.Code == 200 && strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("%s escaped the folder", p)
		}
	}
}

func TestSPAFallback(t *testing.T) {
	h, _ := Handler(Config{Dir: site(t), Base: "/portal", SPA: true})
	if rec := get(t, h, "/portal/clientes/42"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "portal") {
		t.Errorf("a route without a file gets index.html: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get(t, h, "/portal/missing.js"); rec.Code != 404 {
		t.Errorf("a missing asset is not index.html: %d", rec.Code)
	}
	if rec := get(t, h, "/portal/app.js"); !strings.Contains(rec.Body.String(), "console.log") {
		t.Errorf("an existing file wins over the fallback: %q", rec.Body.String())
	}
}

func TestProxyKeepsThePath(t *testing.T) {
	var gotPath, gotHost string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotHost = r.URL.RequestURI(), r.Host
		w.Write([]byte("api says hi"))
	}))
	defer backend.Close()
	s, err := Start(Config{Dir: site(t), Base: "/portal", Proxy: map[string]string{"/portal/api": backend.URL}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	if !strings.HasSuffix(s.URL(), "/portal/") {
		t.Errorf("URL carries the base: %s", s.URL())
	}
	resp, err := http.Get(strings.TrimSuffix(s.URL(), "/") + "/api/clientes?x=1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "api says hi" || gotPath != "/portal/api/clientes?x=1" || gotHost != strings.TrimPrefix(backend.URL, "http://") {
		t.Errorf("proxy: body %q path %q host %q", b, gotPath, gotHost)
	}
	resp, _ = http.Get(s.URL())
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("files still served: %d", resp.StatusCode)
	}
}

func TestTwoInstancesKeepTheSameBase(t *testing.T) {
	a, _ := Start(Config{Dir: site(t), Base: "/portal"}, 0)
	b, _ := Start(Config{Dir: site(t), Base: "/portal"}, 0)
	defer a.Stop()
	defer b.Stop()
	if a.Port == b.Port || !strings.HasSuffix(a.URL(), "/portal/") || !strings.HasSuffix(b.URL(), "/portal/") {
		t.Errorf("two instances, two ports, one base: %s %s", a.URL(), b.URL())
	}
	a.Stop()
	if _, err := http.Get(a.URL()); err == nil {
		t.Error("a stopped instance must not answer")
	}
	if resp, err := http.Get(b.URL()); err != nil || resp.StatusCode != 200 {
		t.Errorf("the other keeps answering: %v", err)
	}
}

func TestBadConfig(t *testing.T) {
	if _, err := Handler(Config{Dir: t.TempDir(), Base: "portal"}); err == nil {
		t.Error("a base without / must be refused")
	}
	if _, err := Handler(Config{Dir: t.TempDir(), Base: "/p", Proxy: map[string]string{"/a": "ftp://x"}}); err == nil {
		t.Error("a proxy target that is not http must be refused")
	}
}

func TestBaseCannotBeAnotherHost(t *testing.T) {
	for _, base := range []string{"//evil.example", `/\evil.example`, "portal"} {
		if _, err := Handler(Config{Dir: t.TempDir(), Base: base}); err == nil {
			t.Errorf("base %q was accepted", base)
		}
	}
}
