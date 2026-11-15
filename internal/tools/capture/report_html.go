package capture

import (
	"encoding/base64"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

const reportCSS = `:root{--ink:#27132f;--muted:#452342;--line:#ddd3dd;--bg:#faf5eb;--card:#fff;--brand:#168783;--warn:#7a4a00;--warnbg:#fdf0d2;--okbg:#e3f4e8;--ok:#14602a;--info:#17509e;--infobg:#e6eefb;--err:#9f1d16;--errbg:#fde8e6}
*{box-sizing:border-box}body{margin:0;font-family:system-ui,-apple-system,"Segoe UI",Roboto,Arial,sans-serif;color:var(--ink);background:var(--bg);line-height:1.45}
h1{font-size:26px;margin:0}h2{font-size:18px;margin:0}.muted{color:var(--muted)}.small{font-size:12px}
.chip{display:inline-block;padding:1px 9px;border-radius:999px;font-size:12px;font-weight:600;border:1px solid currentColor}
.c-changed,.c-minor{background:var(--warnbg);color:var(--warn)}.c-same{background:var(--okbg);color:var(--ok)}.c-new,.c-removed{background:var(--infobg);color:var(--info)}.c-unstable,.c-not_captured{background:var(--errbg);color:var(--err)}
.shot{width:100%;height:auto;border:1px solid var(--line);border-radius:6px;background:#fff;display:block}
.missing{display:flex;align-items:center;justify-content:center;min-height:160px;border:1px dashed #8570A0;border-radius:6px;color:var(--muted);font-size:13px}
`

func esc(s string) string { return html.EscapeString(s) }

func stateTitle(c *Comparison, s StateResult) string {
	for _, sc := range c.Screens {
		if sc.ID == s.Screen && sc.Title != "" {
			return sc.Title
		}
	}
	return s.Screen
}

// BuildCompositionHTML is the page rendered into the before/after image of a
// state: BEFORE, AFTER and DIFFERENCE side by side, with the label of each set
// and the percentage. Image paths are relative to its folder (composiciones/src).
func BuildCompositionHTML(c *Comparison, s StateResult, lang string) string {
	var b strings.Builder
	img := func(label, setLabel, file string) {
		fmt.Fprintf(&b, `<div class="col"><div class="lab">%s · %s</div>`, esc(label), esc(setLabel))
		if file == "" {
			missing := Tr(lang, "did.not.exist")
			if label == Tr(lang, "after") {
				missing = Tr(lang, "no.longer")
			}
			fmt.Fprintf(&b, `<div class="missing">%s</div>`, esc(missing))
		} else {
			fmt.Fprintf(&b, `<img class="shot" src="%s" alt="%s">`, esc(file), esc(label))
		}
		b.WriteString("</div>")
	}
	pctText := ""
	if s.Class == ClassChanged || s.Class == ClassMinor {
		pctText = " · " + Tr(lang, "changed.pct", pct(lang, s.Percent))
	}
	cols := 3
	if s.DiffFile == "" {
		cols = 2
	}
	fmt.Fprintf(&b, `<!doctype html><html lang="%s"><head><meta charset="utf-8"><title>%s</title><style>%s
body{padding:16px;width:%dpx;background:#fff}.head{display:flex;justify-content:space-between;align-items:center;margin-bottom:10px}
.grid{display:grid;grid-template-columns:repeat(%d,1fr);gap:12px;align-items:start}.lab{font-size:12px;font-weight:700;letter-spacing:.04em;color:var(--muted);margin-bottom:4px}
</style></head><body><div class="head"><b>%s · %s · %s</b><span class="chip c-%s">%s%s</span></div><div class="grid">`,
		lang, esc(s.Screen+"#"+s.State), reportCSS, 1500, cols,
		esc(stateTitle(c, s)), esc(s.State), esc(s.Viewport), s.Class, esc(Tr(lang, "class."+s.Class)), esc(pctText))
	before, after := "", ""
	if s.Before != "" {
		before = "../../images/a/" + s.Before
	}
	if s.After != "" {
		after = "../../images/b/" + s.After
	}
	img(Tr(lang, "before"), c.A.Label, before)
	img(Tr(lang, "after"), c.B.Label, after)
	if s.DiffFile != "" {
		img(Tr(lang, "diff"), "", "../../"+s.DiffFile)
	}
	b.WriteString("</div></body></html>")
	return b.String()
}

// BuildGallery is composiciones.html: every state with its before/after image,
// filterable by class.
func BuildGallery(c *Comparison, lang string) string {
	var b strings.Builder
	count := map[string]int{}
	for _, s := range c.States {
		count[s.Class]++
	}
	fmt.Fprintf(&b, `<!doctype html><html lang="%s"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title><style>%s
body{padding:24px;max-width:1400px;margin:0 auto}.bar{display:flex;gap:8px;flex-wrap:wrap;margin:14px 0}
button{font:inherit;font-size:13px;border:1px solid #8570A0;border-radius:8px;background:#fff;padding:4px 12px;cursor:pointer}button[aria-pressed=true]{background:var(--brand);border-color:var(--brand);color:#fff}
.cards{display:grid;grid-template-columns:repeat(auto-fill,minmax(320px,1fr));gap:16px}.card{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:10px}.card[hidden]{display:none}
.card a{text-decoration:none;color:inherit}.top{display:flex;justify-content:space-between;gap:8px;margin-bottom:6px;font-size:13px}</style></head><body>
<h1>%s</h1><div class="small muted">%s</div><div class="bar" role="group" aria-label="%s">`,
		lang, esc(Tr(lang, "title", c.A.Label, c.B.Label)), reportCSS, esc(Tr(lang, "title", c.A.Label, c.B.Label)),
		esc(Tr(lang, "gallery")), esc(Tr(lang, "gallery")))
	btn := func(key string, n int, filter string) {
		pressed := "false"
		if filter == "" {
			pressed = "true"
		}
		fmt.Fprintf(&b, `<button data-filter="%s" aria-pressed="%s">%s %d</button>`, filter, pressed, esc(Tr(lang, key)), n)
	}
	btn("filter.all", len(c.States), "")
	btn("filter.changed", count[ClassChanged]+count[ClassMinor], "changed minor")
	btn("filter.new", count[ClassNew], "new")
	btn("filter.removed", count[ClassRemoved], "removed")
	btn("filter.unstable", count[ClassUnstable], "unstable")
	b.WriteString(`</div><div class="cards">`)
	for _, s := range c.States {
		if s.Class == ClassNotCapture {
			continue
		}
		label := ""
		if s.Class == ClassChanged || s.Class == ClassMinor {
			label = " · " + pct(lang, s.Percent) + " %"
		}
		inner := esc(s.Screen + "#" + s.State)
		file := CompositionFile(s.Screen, s.State, s.Viewport)
		fmt.Fprintf(&b, `<div class="card" data-class="%s"><div class="top"><b>%s · %s</b><span class="chip c-%s">%s%s</span></div>`,
			s.Class, inner, esc(s.Viewport), s.Class, esc(Tr(lang, "class."+s.Class)), esc(label))
		if s.Class == ClassUnstable {
			fmt.Fprintf(&b, `<div class="missing">%s</div>`, esc(s.Reason))
		} else {
			fmt.Fprintf(&b, `<a href="%s"><img class="shot" loading="lazy" src="%s" alt="%s"></a>`, esc(file), esc(file), inner)
		}
		b.WriteString("</div>")
	}
	b.WriteString(`</div><script>
document.querySelectorAll('button[data-filter]').forEach(function(btn){btn.addEventListener('click',function(){
var f=btn.dataset.filter.split(' ').filter(Boolean);
document.querySelectorAll('button[data-filter]').forEach(function(o){o.setAttribute('aria-pressed',o===btn?'true':'false')});
document.querySelectorAll('.card').forEach(function(c){c.hidden=f.length>0&&f.indexOf(c.dataset.class)<0});});});
</script></body></html>`)
	return b.String()
}

// Brand is the look of the corporate report: logo, colour and footer text.
// It comes from .theo/capturas/marca.json when there is one.
type Brand struct {
	Name  string `json:"name,omitempty"`
	Logo  string `json:"logo,omitempty"` // path relative to the worktree
	Color string `json:"color,omitempty"`
	Pie   string `json:"pie,omitempty"`
}

// dataURI reads the file rel of base (which cannot leave it) as a data: URI; "" when it cannot be read.
func dataURI(base, rel string) string {
	path, err := Within(base, rel)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	mime := "image/png"
	switch strings.ToLower(filepath.Ext(path)) {
	case ".svg":
		mime = "image/svg+xml"
	case ".jpg", ".jpeg":
		mime = "image/jpeg"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// BuildCorporate is the corporate report of one set: a cover and, for each
// screen, its states with the desktop and mobile captures side by side. It is
// one self-contained HTML (images embedded) that Chromium prints as a PDF.
func BuildCorporate(s *Set, lang string, brand Brand, root string) string {
	color := brand.Color
	if color == "" {
		color = "#168783"
	}
	var b strings.Builder
	pages := len(s.Screens) + 1
	fmt.Fprintf(&b, `<!doctype html><html lang="%s"><head><meta charset="utf-8"><title>%s</title><style>%s
:root{--brand:%s}@page{size:A4 landscape;margin:12mm}
.page{background:#fff;max-width:1100px;margin:16px auto;padding:28px;border:1px solid var(--line);border-radius:10px;break-after:page}
.cover{display:flex;flex-direction:column;gap:14px;border-top:6px solid var(--brand)}.cover img.logo{max-height:56px;align-self:flex-start}
.index{columns:2;font-size:14px}.pair{display:flex;gap:28px;align-items:flex-end;margin-top:12px}.pair figure{margin:0}.pair figcaption{font-size:12px;color:var(--muted);margin-bottom:4px}
.foot{margin-top:16px;border-top:1px solid var(--line);padding-top:8px;font-size:12px;color:var(--muted);display:flex;justify-content:space-between}h2{border-bottom:2px solid var(--brand);padding-bottom:6px;margin-bottom:6px}</style></head><body>`,
		lang, esc(s.App), reportCSS, color)
	name := firstNonEmpty(brand.Name, s.App)
	fmt.Fprintf(&b, `<section class="page cover">`)
	if brand.Logo != "" {
		if uri := dataURI(root, brand.Logo); uri != "" {
			fmt.Fprintf(&b, `<img class="logo" src="%s" alt="">`, uri)
		}
	}
	fmt.Fprintf(&b, `<h1>%s</h1><div class="muted">%s</div><h2>%s</h2><ol class="index">`, esc(name),
		esc(Tr(lang, "corp.cover.meta", s.Label, shortCommit(s.Commit), s.Date[:min(10, len(s.Date))])), esc(Tr(lang, "corp.index")))
	for _, sc := range s.Screens {
		fmt.Fprintf(&b, `<li>%s</li>`, esc(firstNonEmpty(sc.Title, sc.ID)))
	}
	fmt.Fprintf(&b, `</ol><div class="foot"><span>%s</span><span>%s</span></div></section>`, esc(brand.Pie), esc(Tr(lang, "corp.footer", 1, pages)))
	var desktop, mobile *Viewport
	for i := range s.Viewports {
		if s.Viewports[i].Mobile && mobile == nil {
			mobile = &s.Viewports[i]
		} else if !s.Viewports[i].Mobile && desktop == nil {
			desktop = &s.Viewports[i]
		}
	}
	for i, sc := range s.Screens {
		fmt.Fprintf(&b, `<section class="page"><h2>%d · %s <span class="small muted">%s %s</span></h2>`, i+1, esc(firstNonEmpty(sc.Title, sc.ID)), esc(Tr(lang, "corp.route")), esc(sc.Route))
		for _, st := range sc.States {
			fmt.Fprintf(&b, `<div class="pair" role="group" aria-label="%s"><div class="small muted" style="align-self:flex-start">%s: <b>%s</b></div>`, esc(st), esc(Tr(lang, "corp.state")), esc(st))
			for _, vp := range []*Viewport{desktop, mobile} {
				if vp == nil {
					continue
				}
				sh := s.Shot(sc.ID, st, vp.ID)
				if sh == nil || sh.File == "" {
					continue
				}
				cap := Tr(lang, "corp.desktop")
				if vp.Mobile {
					cap = Tr(lang, "corp.mobile")
				}
				fmt.Fprintf(&b, `<figure><figcaption>%s %d × %d</figcaption><img class="shot" style="width:%dpx" src="%s" alt="%s %s"></figure>`,
					esc(cap), vp.Width, vp.Height, shotWidth(vp), dataURI(s.Dir, sh.File), esc(sc.ID), esc(cap))
			}
			b.WriteString("</div>")
		}
		fmt.Fprintf(&b, `<div class="foot"><span>%s</span><span>%s</span></div></section>`, esc(brand.Pie), esc(Tr(lang, "corp.footer", i+2, pages)))
	}
	b.WriteString("</body></html>")
	return b.String()
}

func shotWidth(v *Viewport) int {
	if v.Mobile {
		return 200
	}
	return 640
}
