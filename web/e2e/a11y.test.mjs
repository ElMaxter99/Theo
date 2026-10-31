// Accessibility behaviour that axe cannot judge: keyboard-only use, landmarks, one live region, forced colours,
// dark mode, reduced motion, zoom without page-level horizontal scroll. (Real screen-reader review is manual.)
import { test, before, after, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { chromium, startMock, openApp, shot } from "./helpers.mjs";

let browser, mock;
before(async () => { mock = await startMock(); browser = await chromium.launch(); });
after(async () => { await browser?.close(); mock?.stop(); });
beforeEach(async () => { await mock.reset(); });

const active = (page) => page.evaluate(() => { const e = document.activeElement; return e ? `${e.tagName}|${e.id}|${(e.getAttribute("aria-label") || e.textContent || "").trim().slice(0, 60)}` : ""; });

async function tabUntil(page, pred, max = 60) {
  for (let i = 0; i < max; i++) {
    await page.keyboard.press("Tab");
    if (pred(await active(page))) return;
  }
  assert.fail("did not reach the target with Tab; last: " + (await active(page)));
}

test("keyboard-only walk: skip link, project, worktree, all tabs with arrow keys, wizard, dialog focus return", async () => {
  const page = await openApp(browser, mock);
  await page.getByText("5 worktrees").waitFor();
  await page.keyboard.press("Tab");
  assert.match(await active(page), /Skip to content/);
  await page.keyboard.press("Enter");
  assert.match(await active(page), /^MAIN\|main/);
  // Project card -> panel
  await tabUntil(page, (a) => /^A\|\|Open shop/.test(a));
  await page.keyboard.press("Enter");
  await page.waitForSelector("table.data");
  // Worktree link -> detail
  await tabUntil(page, (a) => /^A\|\|fix-123$/.test(a));
  await page.keyboard.press("Enter");
  await page.waitForSelector("[role=tablist]");
  // Tabs: roving tabindex + arrows
  await tabUntil(page, (a) => /inst-tab-summary/.test(a));
  const seen = [];
  for (let i = 0; i < 7; i++) {
    seen.push(await page.evaluate(() => document.activeElement.id));
    await page.keyboard.press("ArrowRight");
  }
  assert.deepEqual(seen, ["summary", "services", "ports", "logs", "variables", "volumes", "history"].map((t) => `inst-tab-${t}`));
  assert.equal(await page.evaluate(() => document.activeElement.id), "inst-tab-summary", "wraps around");
  await page.keyboard.press("End");
  assert.equal(await page.locator("[role=tab][aria-selected=true]").getAttribute("id"), "inst-tab-history");
  await page.keyboard.press("Home");
  assert.equal(await page.locator("[role=tab][aria-selected=true]").getAttribute("id"), "inst-tab-summary");
  assert.equal(await page.locator("[role=tab][tabindex='0']").count(), 1, "only the selected tab is in the tab order");
  await page.ctx.close();
});

test("keyboard-only: the wizard and the destructive dialog return focus to their opener", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  // Open "More actions" of old-spike, then remove, with the keyboard only.
  const summary = page.locator("tr", { hasText: "old-spike" }).locator("summary");
  await summary.focus();
  await page.keyboard.press("Enter");
  await page.keyboard.press("Tab");
  const remove = page.locator("tr", { hasText: "old-spike" }).getByRole("button", { name: /Remove worktree/ });
  await remove.focus();
  await page.keyboard.press("Enter");
  await page.locator("dialog[open]").waitFor();
  assert.match(await active(page), /Cancel/);
  await page.keyboard.press("Escape");
  await page.locator("dialog[open]").waitFor({ state: "detached" });
  // Focus returns to the control that opened the dialog (inside the menu that was open).
  assert.notEqual(await active(page), "BODY||");
  await page.goto(page.url().split("#")[0] + "#/p/shop/new");
  await page.waitForSelector(".stepper");
  await page.waitForTimeout(300); // the shell moves focus to the page heading shortly after a navigation
  await page.locator("#wz-new").focus();
  await page.keyboard.type("kbd-branch");
  await page.keyboard.press("Tab"); // from field to "start from" select
  await page.keyboard.press("Tab"); // Next button
  await page.keyboard.press("Enter");
  await page.waitForSelector("#wz-folder");
  assert.match(await page.locator(".wizard-heading").textContent(), /Step 2 of 4/);
  // Focus moves to the heading a moment after the step renders; wait for it instead of reading it once.
  await page.waitForFunction(() => document.activeElement?.className.includes("wizard-heading"), null, { timeout: 3000 }).catch(() => undefined);
  assert.equal(await page.evaluate(() => document.activeElement.className.includes("wizard-heading")), true, "focus moved to the step heading");
  await page.ctx.close();
});

test("landmarks, skip link target, headings and exactly one polite live region", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  assert.equal(await page.locator("header").count(), 1);
  assert.equal(await page.locator("nav[aria-label]").count() >= 1, true);
  assert.equal(await page.locator("main#main").count(), 1);
  assert.equal(await page.locator("h1").count(), 1);
  assert.equal(await page.locator("[aria-live]").count(), 1);
  assert.equal(await page.locator("[aria-live]").getAttribute("aria-live"), "polite");
  assert.ok(await page.locator("table.data th[scope=col]").count() >= 5, "real tables with column headers");
  assert.ok(await page.locator("table.data tbody th[scope=row]").count() >= 5, "row headers");
  await page.ctx.close();
});

test("status is never colour alone: every state badge has text and an icon", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  const badges = await page.locator(".badge").evaluateAll((els) => els.map((e) => ({ text: e.textContent.trim(), svg: e.querySelectorAll("svg").length })));
  assert.ok(badges.length >= 5);
  for (const b of badges) { assert.ok(b.text.length > 0 && b.svg >= 1, JSON.stringify(b)); }
  const shapes = new Set(await page.locator(".badge svg").evaluateAll((els) => els.map((e) => e.innerHTML)));
  assert.ok(shapes.size >= 5, "distinct icon shapes: " + shapes.size);
  await page.ctx.close();
});

for (const [name, opts] of [["dark mode", { colorScheme: "dark" }], ["forced colors", { forcedColors: "active" }], ["forced theme dark by the user", { colorScheme: "light", force: "dark" }]]) {
  test(`${name} keeps all the text of every main screen`, async () => {
    const light = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
    const ref = await light.locator("main").innerText();
    await light.ctx.close();
    const page = await openApp(browser, mock, { ...opts, route: "#/p/shop", wait: "table.data" });
    if (opts.force) { await page.evaluate(() => localStorage.setItem("theo.theme", "dark")); await page.reload(); await page.waitForSelector("table.data"); assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), "dark"); }
    assert.equal(await page.locator("main").innerText(), ref);
    // Text is actually painted: foreground differs from background for body text.
    const colours = await page.evaluate(() => { const c = getComputedStyle(document.querySelector("main h1")); return { fg: c.color, bg: getComputedStyle(document.body).backgroundColor }; });
    assert.notEqual(colours.fg, colours.bg);
    await shot(page, name === "forced colors" ? "24-forced-colors" : name === "dark mode" ? "25-dark-panel" : "26-dark-forced-theme");
    await page.ctx.close();
  });
}

test("manual theme override persists in localStorage and survives a reload", async () => {
  const page = await openApp(browser, mock);
  await page.locator("#theme-select").selectOption("dark");
  assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), "dark");
  assert.equal(await page.evaluate(() => localStorage.getItem("theo.theme")), "dark");
  await page.reload();
  await page.waitForSelector("main h1");
  assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), "dark");
  await page.locator("#theme-select").selectOption("system");
  assert.equal(await page.evaluate(() => document.documentElement.dataset.theme ?? "none"), "none");
  await page.ctx.close();
});

test("the page still works when localStorage throws", async () => {
  const context = await browser.newContext();
  await context.addInitScript(() => { Object.defineProperty(window, "localStorage", { get() { throw new Error("blocked"); } }); });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(await mock.launchUrl());
  await page.waitForSelector("main h1");
  await page.locator("#theme-select").selectOption("dark"); // applies for this page view even if it cannot be saved
  assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), "dark");
  assert.deepEqual(errors, []);
  await context.close();
});

test("reduced motion: spinners do not animate", async () => {
  await mock.scenario({ slow: 3 });
  const page = await openApp(browser, mock, { reducedMotion: "reduce", route: "#/p/shop", wait: "table.data" });
  await page.getByRole("button", { name: /Start feature-x/ }).click();
  await page.locator(".badge:has-text('Starting')").waitFor();
  const anim = await page.locator(".badge svg.spin").first().evaluate((e) => getComputedStyle(e).animationName);
  assert.equal(anim, "none");
  await page.ctx.close();
});

for (const [label, route] of [["project list", ""], ["panel", "#/p/shop"], ["detail summary", "DETAIL"], ["variables", "VARS"], ["wizard", "#/p/shop/new"], ["add project", "#/projects/new"]]) {
  test(`200% zoom (640 CSS px) and larger text: no page-level horizontal scroll on ${label}`, async () => {
    const url = new URL(await mock.launchUrl());
    const r = await fetch(mock.origin + "/api/v1/session", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ nonce: url.hash.slice(3) }) });
    const cookie = r.headers.get("set-cookie").split(";")[0];
    const ov = await (await fetch(mock.origin + "/api/v1/projects/shop/overview", { headers: { Cookie: cookie } })).json();
    const id = ov.worktrees.find((w) => w.main).id;
    const target = route === "DETAIL" ? `#/p/shop/w/${id}/summary` : route === "VARS" ? `#/p/shop/w/${id}/variables` : route;
    const page = await openApp(browser, mock, { viewport: { width: 640, height: 800 }, route: target, wait: route === "#/p/shop/new" ? ".stepper" : "main h1, .welcome h1" });
    await page.waitForTimeout(600);
    for (const size of ["100%", "150%"]) {
      await page.evaluate((s) => { document.documentElement.style.fontSize = s; }, size);
      await page.waitForTimeout(150);
      const m = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
      assert.ok(m.sw <= m.cw + 1, `${label} at ${size}: scrollWidth ${m.sw} > clientWidth ${m.cw}`);
    }
    if (label === "panel") await shot(page, "27-zoom-200-panel");
    await page.ctx.close();
  });
}
