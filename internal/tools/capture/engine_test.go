package capture

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// appHandler serves a tiny single-page app under /portal: its list comes from
// GET /api/items (which a mock answers) and its colour is the "brand".
func appHandler(color string, extraCall bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/portal/", func(w http.ResponseWriter, r *http.Request) {
		extra := ""
		if extraCall {
			extra = `fetch('/api/secret').catch(function(){});`
		}
		fmt.Fprintf(w, `<!doctype html><html><body style="margin:0;font-family:sans-serif"><div style="background:%s;color:#fff;padding:20px;height:120px"><h1>Portal</h1></div>
<ul id="l"></ul><button id="b" onclick="document.getElementById('x').hidden=false">Filtros</button><div id="x" hidden style="padding:10px;background:#eee">Panel de filtros</div>
<span data-testid="reloj" id="clock"></span>
<script>document.getElementById('clock').textContent=String(Date.now()%%100000);
fetch('/api/items').then(function(r){return r.json()}).then(function(items){document.getElementById('l').innerHTML=items.map(function(i){return '<li>'+i+'</li>'}).join('')});%s</script></body></html>`, color, extra)
	})
	return mux
}

func testEngine(t *testing.T) *Engine {
	t.Helper()
	e := &Engine{Dir: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := e.Check(ctx); err != nil {
		t.Skipf("no capture engine here: %v", err)
	}
	return e
}

func inventoryFor(t *testing.T, root string) *Inventory {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "mocks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mocks", "items.json"), []byte(`["uno","dos","tres"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	inv, err := ParseInventory([]byte(`{"version":1,"app":"portal","viewports":[{"id":"desktop","width":600,"height":400},{"id":"mobile","width":320,"height":500,"mobile":true,"scale":2}],
 "mocks":{"modo":"mock","carpeta":"mocks/"},
 "pantallas":[{"id":"inicio","ruta":"/","ignorar":[{"selector":"[data-testid=reloj]"}],"estados":[
   {"id":"con-datos","datos":{"GET /api/items":"mocks/items.json"},"acciones":[{"esperar":"tres"}]},
   {"id":"filtros","datos":{"GET /api/items":{"status":200,"json":["a"]}},"acciones":[{"click":{"rol":"button","nombre":"Filtros"}},{"esperar":"Panel de filtros"}]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if errs := inv.Validate(); len(errs) != 0 {
		t.Fatal(errs)
	}
	return inv
}

func TestCaptureAndCompareReal(t *testing.T) {
	e := testEngine(t)
	root := t.TempDir()
	inv := inventoryFor(t, root)
	a := httptest.NewServer(appHandler("#168783", false))
	b := httptest.NewServer(appHandler("#8a2be2", false))
	defer a.Close()
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cap := func(label, url string) *Set {
		s, err := e.Capture(ctx, CaptureOptions{Root: root, Inv: inv, BaseURL: url + "/portal/", Label: label, OutDir: filepath.Join(root, SetsDir, label), Control: true})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	sa, sb := cap("develop", a.URL), cap("rebranding", b.URL)
	if ok, _, failed := sa.Counts(); len(sa.Shots) != 4 || failed != 0 || ok+0 == 0 {
		t.Fatalf("shots: %+v", sa.Shots)
	}
	// the clock shown by the page changes between captures: it is masked, so the state is not stable
	// unless the mask is what resolves it ("masked: ...")
	for _, sh := range sa.Shots {
		if sh.Status == ShotUnstable && !strings.HasPrefix(sh.Reason, "masked:") {
			t.Errorf("unexpected instability: %+v", sh)
		}
	}
	// same deployment twice: no changes
	sa2 := cap("develop2", a.URL)
	c, err := Compare(sa, sa2, CompareOptions{OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range c.States {
		if s.Class != ClassSame && s.Class != ClassUnstable {
			t.Errorf("the same app twice must not change: %+v", s)
		}
	}
	// a different brand colour: changed
	c, err = Compare(sa, sb, CompareOptions{OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	for _, s := range c.States {
		if s.Class == ClassChanged || s.Class == ClassMinor {
			changed++
			if s.Percent <= 0 {
				t.Errorf("%+v", s)
			}
		}
	}
	if changed == 0 {
		t.Fatalf("the colour change was not seen: %+v", c.States)
	}
	// reports, rendered by the real browser
	rend, err := e.NewRenderer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rend.Close()
	dir := t.TempDir()
	if _, err := WriteComparisonReport(dir, sa, sb, c, LangEN, rend, nil); err != nil {
		t.Fatal(err)
	}
	for _, s := range c.States {
		if s.Class == ClassChanged || s.Class == ClassMinor {
			p := filepath.Join(dir, filepath.FromSlash(CompositionFile(s.Screen, s.State, s.Viewport)))
			if st, err := os.Stat(p); err != nil || st.Size() < 1000 {
				t.Errorf("composition not rendered: %s %v", p, err)
			}
		}
	}
	h, pdf, err := WriteCorporateReport(t.TempDir(), sa, root, LangES, rend)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(pdf); err != nil || st.Size() < 1000 {
		t.Fatalf("pdf %s: %v", pdf, err)
	}
	if _, err := os.Stat(h); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureFailsWhenTheAppAsksForAnUnmockedCall(t *testing.T) {
	e := testEngine(t)
	root := t.TempDir()
	inv := inventoryFor(t, root)
	srv := httptest.NewServer(appHandler("#168783", true))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	s, err := e.Capture(ctx, CaptureOptions{Root: root, Inv: inv, BaseURL: srv.URL + "/portal/", Label: "x", OutDir: filepath.Join(root, "x")})
	if err != nil {
		t.Fatal(err)
	}
	_, _, failed := s.Counts()
	if failed != 4 || !strings.Contains(s.Shots[0].Reason, "GET /api/secret") || !strings.Contains(s.Shots[0].Reason, "not intercepted") {
		t.Fatalf("%+v", s.Shots)
	}
}
