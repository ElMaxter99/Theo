package desktop

import (
	"encoding/json"
	"strings"
)

// BindName is the name of the Go function the page can call to open an
// address in the system browser.
const BindName = "theoOpenExternal"

const shimTemplate = `(function () {
  if (window.__theoDesktop) return;
  window.__theoDesktop = true;
  var APP = __ORIGIN__;
  function abs(u) { try { return new URL(u, document.baseURI); } catch (e) { return null; } }
  function same(u) { return u.origin === APP; }
  function ext(href) {
    try { var p = window.__BIND__(href); if (p && p.catch) p.catch(function () {}); } catch (e) {}
  }
  function onLink(e) {
    var el = e.target && e.target.closest ? e.target.closest("a[href], area[href]") : null;
    if (!el) return;
    var u = abs(el.getAttribute("href"));
    if (!u) { e.preventDefault(); return; }
    if (same(u)) {
      // No second window: a middle click does nothing, target=_blank loads here.
      if (e.type === "auxclick") { e.preventDefault(); return; }
      if (el.target && el.target !== "_self") { e.preventDefault(); e.stopPropagation(); location.assign(u.href); }
      return;
    }
    e.preventDefault(); e.stopPropagation();
    if (u.protocol === "http:" || u.protocol === "https:") ext(u.href);
  }
  document.addEventListener("click", onLink, true);
  document.addEventListener("auxclick", onLink, true);
  window.open = function (href) {
    var u = href ? abs(String(href)) : null;
    if (!u) return null;
    if (same(u)) location.assign(u.href);
    else if (u.protocol === "http:" || u.protocol === "https:") ext(u.href);
    return null;
  };
  document.addEventListener("submit", function (e) {
    var f = e.target, a = f && f.action ? abs(f.action) : null;
    if (a && !same(a)) { e.preventDefault(); e.stopPropagation(); }
  }, true);
  // A file dropped on the window would otherwise be loaded in it.
  ["dragover", "drop"].forEach(function (n) {
    window.addEventListener(n, function (e) { e.preventDefault(); }, true);
  });
})();`

// Shim returns the script injected in every page of the window (before the
// page's own scripts run). It sends links to other origins to the system
// browser through the bound function and keeps the window on the Theo origin:
// clicks, middle clicks, window.open, forms and dropped files. It is a
// convenience layer; the authoritative filter is Classify, applied again in Go
// to whatever the page sends.
func Shim(appOrigin string) string {
	o, _ := json.Marshal(appOrigin)
	r := strings.NewReplacer("__ORIGIN__", string(o), "__BIND__", BindName)
	return r.Replace(shimTemplate)
}
