// Interface language (Q-029): both catalogs have exactly the same keys, every text is non-empty, parameterised texts
// produce non-empty strings, Spanish plurals agree, and the language resolution follows the browser and the preference.
import { test } from "node:test";
import assert from "node:assert/strict";
import { en, t } from "../src/strings.ts";
import { es } from "../src/strings.es.ts";
import { resolveLang, intlLocaleFor, lang, plural } from "../src/lib/locale.ts";

/** Every leaf of a catalog as [path, value]. Arrays are leaves keyed by index. */
function leaves(obj, prefix = "") {
  const out = [];
  for (const [k, v] of Object.entries(obj)) {
    const p = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === "object" && !Array.isArray(v)) out.push(...leaves(v, p));
    else if (Array.isArray(v)) v.forEach((x, i) => out.push([`${p}[${i}]`, x]));
    else out.push([p, v]);
  }
  return out;
}

test("outside a browser the active catalog is English (unit tests do not depend on the machine's locale)", () => {
  assert.equal(lang, "en");
  assert.equal(t, en);
});

test("the Spanish catalog has exactly the keys of the English one, open records and arrays included", () => {
  const a = leaves(en).map(([p]) => p).sort();
  const b = leaves(es).map(([p]) => p).sort();
  assert.deepEqual(b.filter((k) => !a.includes(k)), [], "keys only in Spanish");
  assert.deepEqual(a.filter((k) => !b.includes(k)), [], "keys missing in Spanish");
});

test("every text is non-empty and of the same kind (text or function with the same number of parameters)", () => {
  const esMap = new Map(leaves(es));
  for (const [p, v] of leaves(en)) {
    const w = esMap.get(p);
    assert.equal(typeof w, typeof v, p);
    if (typeof v === "string") { assert.ok(v.length > 0, `en ${p}`); assert.ok(w.length > 0, `es ${p}`); }
    if (typeof v === "function") assert.equal(w.length, v.length, `parameters of ${p}`);
  }
});

// Sample arguments by parameter name, so each function gets something plausible.
function argsFor(fn) {
  const src = fn.toString();
  const params = src.slice(src.indexOf("(") + 1, src.indexOf(")")).split(",").map((s) => s.trim().replace(/[?:].*$/, "")).filter(Boolean);
  return params.map((name) => (/^(n|r|p|a|b|shown|total|minutes)$/.test(name) ? 2 : /^(ok|nu|docker)$/.test(name) ? true : "x"));
}

test("functions with parameters produce non-empty strings in both languages, for 0, 1 and many", () => {
  for (const cat of [en, es]) {
    for (const [p, fn] of leaves(cat)) {
      if (typeof fn !== "function") continue;
      const base = argsFor(fn);
      for (const n of [0, 1, 2, 1234]) {
        const args = base.map((x) => (typeof x === "number" ? n : x));
        const out = fn(...args);
        assert.equal(typeof out, "string", p);
        assert.ok(out.trim().length > 0, `${p}(${args.join(", ")}) is empty`);
        assert.doesNotMatch(out, /undefined|NaN|\[object/, `${p}(${args.join(", ")}) = ${out}`);
      }
      if (fn.length === 0) assert.ok(fn().length > 0, p);
    }
  }
});

test("Spanish plurals agree with the count, including the verb or participle that follows", () => {
  assert.equal(es.projects.worktrees(1), "1 worktree");
  assert.equal(es.projects.worktrees(3), "3 worktrees");
  assert.equal(es.logs.lines(1, 1), "1 línea");
  assert.equal(es.logs.lines(2, 5), "2 de 5 líneas coinciden");
  assert.equal(es.ops.indicator(0), "Ninguna operación en curso");
  assert.equal(es.ops.indicator(1), "1 operación en curso");
  assert.equal(es.ops.indicator(2), "2 operaciones en curso");
  assert.equal(es.reconcile.forgotten(1), "1 registro olvidado.");
  assert.equal(es.reconcile.forgotten(4), "4 registros olvidados.");
  assert.equal(es.wizard.s3Conflict(1), "1 puerto chocaría con algo que ya está en ejecución.");
  assert.equal(es.wizard.s3Conflict(2), "2 puertos chocarían con algo que ya está en ejecución.");
  assert.equal(es.nav.healthCount(0), "Salud: sin incidencias");
  assert.equal(es.nav.healthCount(2), "Salud: 2 incidencias");
  assert.equal(es.history.loaded(1), "1 evento cargado");
});

test("English plurals and counts keep their old texts", () => {
  assert.equal(plural(1, "line"), "1 line");
  assert.equal(plural(0, "line"), "0 lines");
  assert.equal(en.projects.worktrees(5), "5 worktrees");
  assert.equal(en.logs.lines(3, 10), "3 of 10 lines match");
  assert.equal(en.reconcile.summary(1), "1 finding");
  assert.equal(en.history.shown(1200, 3400), "Showing 1,200 of 3,400");
  assert.equal(en.nav.healthPillSr(1, true), "finding, Docker unavailable");
});

test("the language follows the browser on first load and the stored preference afterwards", () => {
  assert.equal(resolveLang("system", "es-ES"), "es");
  assert.equal(resolveLang("system", "es"), "es");
  assert.equal(resolveLang("system", "es-419"), "es");
  assert.equal(resolveLang("system", "en-GB"), "en");
  assert.equal(resolveLang("system", "fr-FR"), "en");
  assert.equal(resolveLang("system", "estonian-not-a-tag"), "en");
  assert.equal(resolveLang("system", ""), "en");
  assert.equal(resolveLang("en", "es-ES"), "en");
  assert.equal(resolveLang("es", "en-US"), "es");
});

test("number and date formats use the browser's variant of the active language, or a default", () => {
  assert.equal(intlLocaleFor("es", "es-MX"), "es-MX");
  assert.equal(intlLocaleFor("es", "en-GB"), "es-ES");
  assert.equal(intlLocaleFor("en", "en-GB"), "en-GB");
  assert.equal(intlLocaleFor("en", "es-ES"), "en-US");
  assert.equal(new Intl.NumberFormat(intlLocaleFor("es", "")).format(12345), "12.345");
});

test("the language names are written in their own language in both catalogs", () => {
  for (const cat of [en, es]) { assert.equal(cat.app.langEn, "English"); assert.equal(cat.app.langEs, "Español"); }
});
