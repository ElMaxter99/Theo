// Interface language (Q-029): English by default, Spanish when the browser asks for it on first load, a selector in
// Settings › Preferences that persists the choice, <html lang> that follows it, and the Spanish screens stay accessible.
import { test, before, after, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { chromium, startMock, openApp, axeViolations, seriousOnly, cspViolations } from "./helpers.mjs";

let browser, mock;
before(async () => { mock = await startMock(); browser = await chromium.launch(); });
after(async () => { await browser?.close(); mock?.stop(); });
beforeEach(async () => { await mock.reset(); });

const htmlLang = (page) => page.evaluate(() => document.documentElement.lang);
const navText = (page) => page.locator("nav.app-nav").innerText();

/** Picks a language in Settings › Preferences and waits for the reload it triggers. */
async function pickLanguage(page, value) {
  if (!(await page.locator("#pref-lang").count())) {
    await page.evaluate(() => { location.hash = "#/settings"; });
    await page.waitForSelector("#pref-lang");
  }
  await Promise.all([page.waitForEvent("load"), page.locator("#pref-lang").selectOption(value)]);
  await page.waitForSelector("main h1, .welcome h1");
}

test("an English browser gets English, with lang=en", async () => {
  const page = await openApp(browser, mock, { locale: "en-US" });
  assert.equal(await htmlLang(page), "en");
  assert.match(await navText(page), /Projects/);
  await page.evaluate(() => { location.hash = "#/settings"; });
  await page.waitForSelector("#pref-lang");
  assert.equal(await page.locator("#pref-lang").inputValue(), "system");
  await page.ctx.close();
});

test("a Spanish browser gets Spanish on first load, with lang=es and no English in the shell", async () => {
  const page = await openApp(browser, mock, { locale: "es-ES" });
  assert.equal(await htmlLang(page), "es");
  const nav = await navText(page);
  assert.match(nav, /Proyectos/);
  assert.match(nav, /Ajustes/);
  assert.doesNotMatch(nav, /Settings|Projects/);
  await page.evaluate(() => { location.hash = "#/settings"; });
  await page.waitForSelector("#pref-lang");
  assert.equal(await page.locator("label[for=pref-lang]").innerText(), "Idioma");
  assert.equal(await page.locator("label[for=pref-theme]").innerText(), "Tema");
  assert.deepEqual(page.problems, []);
  await page.ctx.close();
});

test("switching to Español in the settings changes the visible text and the lang attribute, and persists", async () => {
  const page = await openApp(browser, mock, { locale: "en-US", route: "#/p/shop", wait: "table.data" });
  assert.ok(await page.getByRole("button", { name: "Start feature-x" }).count());
  await pickLanguage(page, "es");
  assert.equal(await htmlLang(page), "es");
  assert.equal(await page.evaluate(() => localStorage.getItem("theo.lang")), "es");
  assert.equal(await page.locator("#pref-lang").inputValue(), "es");
  await page.evaluate(() => { location.hash = "#/p/shop"; });
  await page.waitForSelector("table.data");
  assert.ok(await page.getByRole("button", { name: "Iniciar feature-x" }).count(), "the panel is in Spanish");
  assert.equal(await page.getByRole("button", { name: "Start feature-x" }).count(), 0);
  assert.match(await page.locator("table.data thead").innerText(), /Estado/);
  // The choice survives a reload, and System goes back to the browser's language (English here).
  await page.reload();
  await page.waitForSelector("table.data");
  assert.equal(await htmlLang(page), "es");
  await pickLanguage(page, "system");
  assert.equal(await htmlLang(page), "en");
  assert.equal(await page.evaluate(() => localStorage.getItem("theo.lang")), null);
  await page.evaluate(() => { location.hash = "#/p/shop"; });
  await page.waitForSelector("table.data");
  assert.ok(await page.getByRole("button", { name: "Start feature-x" }).count());
  await page.ctx.close();
});

test("English chosen explicitly wins over a Spanish browser", async () => {
  const page = await openApp(browser, mock, { locale: "es-ES" });
  assert.equal(await htmlLang(page), "es");
  await pickLanguage(page, "en");
  assert.equal(await htmlLang(page), "en");
  assert.match(await navText(page), /Projects/);
  await page.ctx.close();
});

test("the settings page offers the language too, with a hint that it reloads the page", async () => {
  const page = await openApp(browser, mock, { locale: "es-ES", route: "#/settings", wait: "#pref-lang" });
  assert.equal(await page.locator("main h1").innerText(), "Ajustes y diagnóstico");
  assert.match(await page.locator("#pref-lang-hint").innerText(), /recarga la página/);
  assert.equal(await page.locator("#pref-lang").getAttribute("aria-describedby"), "pref-lang-hint");
  assert.equal(await page.locator("#pref-lang option[value=es]").getAttribute("lang"), "es");
  assert.equal(await page.locator("#pref-lang option[value=en]").getAttribute("lang"), "en");
  await page.ctx.close();
});

for (const [name, route, wait] of [["projects", "", "main h1"], ["panel", "#/p/shop", "table.data"], ["problems", "#/problems", "main h1"], ["settings", "#/settings", "#pref-lang"], ["new instance panel", "#/p/shop/new", "dialog.drawer"], ["shared services", "#/p/shop/shared", "main h1"]]) {
  test(`Spanish ${name}: no serious axe violations, no CSP violation, no console problems`, async () => {
    const page = await openApp(browser, mock, { locale: "es-ES", route, wait });
    await page.waitForTimeout(300);
    assert.equal(await htmlLang(page), "es");
    assert.deepEqual(await cspViolations(page), []);
    const v = seriousOnly(await axeViolations(page));
    assert.deepEqual(v, [], JSON.stringify(v, null, 1));
    assert.deepEqual(page.problems.filter((p) => !/status of 404/.test(p)), []);
    await page.ctx.close();
  });
}
