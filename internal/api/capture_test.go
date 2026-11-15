package api_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureStateWithoutInventory(t *testing.T) {
	e := newEnv(t)
	var v struct {
		NoInv   bool             `json:"no_inventory"`
		Sets    []map[string]any `json:"sets"`
		Reports []map[string]any `json:"reports"`
	}
	if st := e.c.call("GET", "/api/v1/projects/ubapp/capture", nil, &v); st != 200 || !v.NoInv || v.Sets == nil || v.Reports == nil {
		t.Fatalf("%d %+v", st, v)
	}
	var nf apiErr
	if st := e.c.call("GET", "/api/v1/projects/nope/capture", nil, &nf); st != 404 {
		t.Errorf("unknown project = %d", st)
	}
}

func TestCaptureCompareValidatesTheRequest(t *testing.T) {
	e := newEnv(t)
	var er apiErr
	if st := e.c.call("POST", "/api/v1/projects/ubapp/capture/compare", map[string]any{"a": "x"}, &er); st != 400 {
		t.Errorf("missing b = %d", st)
	}
	if st := e.c.call("POST", "/api/v1/projects/ubapp/capture/compare", map[string]any{"a": "x", "b": "y", "lang": "fr"}, &er); st != 400 {
		t.Errorf("bad lang = %d", st)
	}
	if st := e.c.call("POST", "/api/v1/projects/ubapp/capture/compare", map[string]any{"a": "x", "b": "y", "mode": "wild"}, &er); st != 400 {
		t.Errorf("bad mode = %d", st)
	}
}

func TestCaptureReportFilesAreServedSafely(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(e.main, ".theo", "capturas", "informes", "2026-11-15-a-vs-b")
	if err := os.MkdirAll(filepath.Join(dir, "composiciones"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "resumen.md"), []byte("# hi"), 0o644)
	os.WriteFile(filepath.Join(dir, "composiciones.html"), []byte("<p>x</p>"), 0o644)
	os.WriteFile(filepath.Join(dir, "composiciones", "a.png"), []byte("png"), 0o644)
	os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("no"), 0o644)
	os.WriteFile(filepath.Join(e.main, ".theo", "capturas", "secret.md"), []byte("no"), 0o644)
	base := "/api/v1/projects/ubapp/capture/reports/2026-11-15-a-vs-b/"
	st, body, h := e.c.raw("GET", base+"composiciones.html", nil, nil)
	if st != 200 || string(body) != "<p>x</p>" || !strings.Contains(h.Get("Content-Security-Policy"), "connect-src 'none'") || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("html %d %q %v", st, body, h)
	}
	if st, _, h := e.c.raw("GET", base+"composiciones/a.png", nil, nil); st != 200 || h.Get("Content-Type") != "image/png" {
		t.Errorf("png %d %v", st, h)
	}
	for _, bad := range []string{"secret.txt", "../secret.md", "..%2fsecret.md", "composiciones/..%2f..%2fsecret.md", "nothing.html"} {
		if st, _, _ := e.c.raw("GET", base+bad, nil, nil); st == 200 {
			t.Errorf("%s was served", bad)
		}
	}
	if st, _, _ := e.c.raw("GET", "/api/v1/projects/ubapp/capture/reports/..%2f..%2f/resumen.md", nil, nil); st == 200 {
		t.Error("a report name outside the folder was served")
	}
}
