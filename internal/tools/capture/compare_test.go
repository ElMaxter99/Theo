package capture

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixtureShot struct {
	screen, state, vp string
	img               *image.NRGBA
	status, reason    string
}

func makeSet(t *testing.T, label string, entry string, screens []ScreenInfo, flows []Flow, shots []fixtureShot) *Set {
	t.Helper()
	s := &Set{Dir: filepath.Join(t.TempDir(), label)}
	s.Manifest = Manifest{Version: 1, Label: label, App: "portal", Date: "2026-11-15T10:00:00Z", Mode: "mock", Chromium: "136", Entry: entry,
		Viewports: []Viewport{{ID: "desktop", Width: 100, Height: 100}}, Screens: screens, Flows: flows}
	for _, sh := range shots {
		m := Shot{Screen: sh.screen, State: sh.state, Viewport: sh.vp, Status: sh.status}
		if m.Status == "" {
			m.Status = ShotOK
		}
		m.Reason = sh.reason
		if sh.img != nil {
			m.File = ShotFile(sh.screen, sh.state, sh.vp)
			p := filepath.Join(s.Dir, filepath.FromSlash(m.File))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			f, _ := os.Create(p)
			png.Encode(f, sh.img)
			f.Close()
		}
		s.Shots = append(s.Shots, m)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCompareClassifiesScreensAndFlows(t *testing.T) {
	red := color.NRGBA{200, 0, 0, 255}
	changed := solid(100, 100, white)
	fill(changed, Rect{0, 0, 100, 40}, red) // 40 %
	a := makeSet(t, "develop", "inicio",
		[]ScreenInfo{
			{ID: "inicio", Route: "#/inicio", Origin: []string{"src/Inicio.tsx"}, States: []string{"con-datos"}},
			{ID: "facturas", Route: "/facturas", States: []string{"con-datos"}},
			{ID: "ayuda", Route: "/ayuda", States: []string{"ver"}},
			{ID: "perfil", Route: "/perfil", States: []string{"ver"}},
		},
		[]Flow{{ID: "alta", Pasos: []string{"inicio#con-datos", "facturas#con-datos"}}},
		[]fixtureShot{
			{"inicio", "con-datos", "desktop", solid(100, 100, white), "", ""},
			{"facturas", "con-datos", "desktop", solid(100, 100, white), "", ""},
			{"ayuda", "ver", "desktop", solid(100, 100, white), "", ""},
			{"perfil", "ver", "desktop", solid(100, 100, white), "", ""},
		})
	b := makeSet(t, "rebranding", "login",
		[]ScreenInfo{
			{ID: "login", Route: "#/login", States: []string{"vacio"}},
			{ID: "panel", Route: "#/panel", Origin: []string{"src/Inicio.tsx"}, States: []string{"con-datos"}}, // moved by file
			{ID: "facturas", Route: "/facturas", States: []string{"con-datos"}},
			{ID: "perfil", Route: "/perfil", States: []string{"ver"}},
		},
		[]Flow{{ID: "alta", Pasos: []string{"login#vacio", "panel#con-datos", "facturas#con-datos"}}},
		[]fixtureShot{
			{"login", "vacio", "desktop", solid(100, 100, white), "", ""},
			{"panel", "con-datos", "desktop", solid(100, 100, white), "", ""},
			{"facturas", "con-datos", "desktop", changed, "", ""},
			{"perfil", "ver", "desktop", nil, ShotFailed, "GET /api/perfil has no mock (not intercepted)"},
		})
	out := t.TempDir()
	c, err := Compare(a, b, CompareOptions{OutDir: out, ChangedFiles: []string{"src/Ayuda.tsx", "src/Inicio.tsx", "README.md"}})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]ScreenResult{}
	for _, s := range c.Screens {
		by[s.ID] = s
	}
	if by["facturas"].Class != ClassChanged || by["facturas"].Percent != 40 {
		t.Errorf("facturas %+v", by["facturas"])
	}
	if p := by["panel"]; p.Class != ClassSame || !p.Moved || p.Method != ByFile || p.FromID != "inicio" {
		t.Errorf("panel %+v", p)
	}
	if by["login"].Class != ClassNew || by["ayuda"].Class != ClassRemoved {
		t.Errorf("new/removed %+v %+v", by["login"], by["ayuda"])
	}
	if c.Counts.Changed != 1 || c.Counts.New != 1 || c.Counts.Removed != 1 || c.Counts.Moved != 1 || c.Counts.FlowChanged != 2 || c.Counts.NeedLook != 1 {
		t.Errorf("counts %+v", c.Counts)
	}
	flows := map[string]FlowResult{}
	for _, f := range c.Flows {
		flows[f.ID] = f
	}
	if f := flows["entry"]; f.Class != "changed" || len(f.B) != 1 || f.B[0] != "login" {
		t.Errorf("entry %+v", f)
	}
	if f := flows["alta"]; f.Class != "changed" || len(f.Ops) != 1 || f.Ops[0].Kind != "add" || f.Ops[0].Screen != "login" || f.Ops[0].After != "" {
		t.Errorf("alta %+v", f)
	}
	var kinds []string
	for _, n := range c.NotCaptured {
		kinds = append(kinds, n.Kind+":"+n.Item)
	}
	got := strings.Join(kinds, "|")
	if !strings.Contains(got, "no_mock:perfil#ver") || !strings.Contains(got, "no_screen:src/Ayuda.tsx") || strings.Contains(got, "README") || strings.Contains(got, "no_screen:src/Inicio.tsx") {
		t.Errorf("not captured: %s", got)
	}
	if c.Largest == nil || c.Largest.Screen != "facturas" {
		t.Errorf("largest %+v", c.Largest)
	}
	if _, err := os.Stat(filepath.Join(out, "diff", "facturas--con-datos--desktop.png")); err != nil {
		t.Error("the diff image was not written")
	}
}

func TestSeqDiff(t *testing.T) {
	ops := seqDiff([]string{"a", "b", "c"}, []string{"a", "x", "b", "c", "d"})
	if len(ops) != 2 || ops[0] != (FlowOp{Kind: "add", Screen: "x", After: "a"}) || ops[1] != (FlowOp{Kind: "add", Screen: "d", After: "c", AtEnd: true}) {
		t.Fatalf("%+v", ops)
	}
	if ops := seqDiff([]string{"a", "b"}, []string{"b"}); len(ops) != 1 || ops[0].Kind != "remove" || ops[0].Screen != "a" {
		t.Fatalf("%+v", ops)
	}
}

func TestSummaryEnglishAndSpanish(t *testing.T) {
	red := color.NRGBA{200, 0, 0, 255}
	changed := solid(100, 100, white)
	fill(changed, Rect{0, 0, 100, 40}, red)
	a := makeSet(t, "develop", "inicio",
		[]ScreenInfo{{ID: "inicio", Route: "#/inicio", Origin: []string{"src/Inicio.tsx"}, States: []string{"con-datos"}}, {ID: "facturas", Route: "/facturas", States: []string{"con-datos"}}, {ID: "ayuda", Route: "/ayuda", States: []string{"ver"}}},
		[]Flow{{ID: "alta", Pasos: []string{"inicio#con-datos", "facturas#con-datos"}}},
		[]fixtureShot{{"inicio", "con-datos", "desktop", solid(100, 100, white), "", ""}, {"facturas", "con-datos", "desktop", solid(100, 100, white), "", ""}, {"ayuda", "ver", "desktop", solid(100, 100, white), "", ""}})
	b := makeSet(t, "rebranding", "login",
		[]ScreenInfo{{ID: "login", Route: "#/login", States: []string{"vacio"}}, {ID: "panel", Route: "#/panel", Origin: []string{"src/Inicio.tsx"}, States: []string{"con-datos"}}, {ID: "facturas", Route: "/facturas", States: []string{"con-datos"}}},
		[]Flow{{ID: "alta", Pasos: []string{"login#vacio", "panel#con-datos", "facturas#con-datos"}}},
		[]fixtureShot{{"login", "vacio", "desktop", solid(100, 100, white), "", ""}, {"panel", "con-datos", "desktop", solid(100, 100, white), "", ""}, {"facturas", "con-datos", "desktop", changed, "", ""}})
	c, err := Compare(a, b, CompareOptions{OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	en := BuildSummary(c, LangEN)
	for _, w := range []string{
		"# Before and after: develop vs rebranding",
		"1 changed · 1 same · 1 new · 1 removed · 1 moved · 0 unstable · 0 not captured",
		"## Flow changes",
		"- **entry**: first screen: `panel` → `login`",
		"  - + `login` at the start",
		"| facturas | con-datos | desktop | 40.0 % | [view](composiciones/facturas--con-datos--desktop.png) |",
		"## New screens\nlogin",
		"## Removed screens\nayuda",
		"`#/inicio` → `#/panel` (matched by file)",
	} {
		if !strings.Contains(en, w) {
			t.Errorf("missing %q in\n%s", w, en)
		}
	}
	es := BuildSummary(c, LangES)
	for _, w := range []string{"# Antes y después: develop frente a rebranding", "## Cambios de flujo", "40,0 %", "## Pantallas nuevas", "al principio", "emparejada por archivo"} {
		if !strings.Contains(es, w) {
			t.Errorf("missing %q in\n%s", w, es)
		}
	}
	if strings.Contains(en, "## Unstable") || strings.Contains(en, "## Not captured") {
		t.Error("empty sections must not appear")
	}
	// the report folder is self-contained
	out := t.TempDir()
	if _, err := WriteComparisonReport(out, a, b, c, LangEN, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"resumen.md", "comparacion.json", "composiciones.html", "images/b/shots/facturas--con-datos--desktop.png", "composiciones/src/facturas--con-datos--desktop.html"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(f))); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if g := BuildGallery(c, LangES); !strings.Contains(g, "Cambiadas 1") {
		t.Error("the gallery is not in Spanish")
	}
	if ResolveLang("", "es") != LangES || ResolveLang("en", "es") != LangEN {
		t.Error("language resolution")
	}
}

func TestSmallChangeOnLargePageIsMinorNotSame(t *testing.T) {
	// 200 pixels are 0.02 % of the screenshot, but they are a changed word, not noise
	big := solid(1000, 1000, white)
	small := solid(1000, 1000, white)
	fill(small, Rect{0, 0, 20, 10}, color.NRGBA{0, 0, 0, 255})
	scr := []ScreenInfo{{ID: "inicio", Route: "/", States: []string{"base"}}}
	a := makeSet(t, "a", "inicio", scr, nil, []fixtureShot{{"inicio", "base", "desktop", big, "", ""}})
	b := makeSet(t, "b", "inicio", scr, nil, []fixtureShot{{"inicio", "base", "desktop", small, "", ""}})
	c, err := Compare(a, b, CompareOptions{OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if c.Screens[0].Class != ClassMinor || c.Counts.Changed != 1 {
		t.Errorf("%+v %+v", c.Screens[0], c.Counts)
	}
}
