// The 0.2 screens: every route renders without console errors, CSP violations or serious axe findings, in light
// and dark, and the main flows of the redesign work (side panel, palette, menus, filters, old addresses).
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { chromium, startMock, openApp, cspViolations, axeViolations, seriousOnly, shot, waitIdle } from "./helpers.mjs";

let browser, mock;
before(async () => { browser = await chromium.launch(); mock = await startMock(); });
after(async () => { await browser?.close(); mock?.stop(); });

const shopMain = async (page) => page.evaluate(async () => {
  const r = await fetch("/api/v1/status", { credentials: "same-origin" });
  return (await r.json()).projects[0].worktrees[0].id;
});

const ROUTES = [
  ["home", "#/"],
  ["projects", "#/projects"],
  ["project-instances", "#/p/shop"],
  ["project-new-instance", "#/p/shop/new"],
  ["project-shared", "#/p/shop/shared"],
  ["project-activity", "#/p/shop/activity"],
  ["project-settings", "#/p/shop/settings"],
  ["problems", "#/problems"],
  ["cleanup", "#/problems/cleanup"],
  ["settings", "#/settings"],
  ["settings-agents", "#/settings/agents"],
  ["settings-diagnostics", "#/settings/diagnostics"],
];
const TABS = ["summary", "logs", "config", "data", "activity"];

for (const scheme of ["light", "dark"]) {
  test(`every 0.2 screen renders cleanly (${scheme})`, async () => {
    await mock.reset();
    const page = await openApp(browser, mock, { colorScheme: scheme });
    const id = await shopMain(page);
    const all = [...ROUTES, ...TABS.map((t) => [`instance-${t}`, `#/p/shop/w/${id}/${t}`])];
    for (const [name, hash] of all) {
      await page.evaluate((h) => { location.hash = h; }, hash);
      await page.waitForSelector("main h1", { timeout: 10000 });
      await waitIdle(page, 500);
      const v = seriousOnly(await axeViolations(page));
      assert.deepEqual(v, [], `${name}: ${JSON.stringify(v, null, 1)}`);
      await shot(page, `v02-${name}-${scheme}`);
      if (name === "project-new-instance") await page.keyboard.press("Escape");
    }
    assert.deepEqual(await cspViolations(page), []);
    assert.deepEqual(page.problems.filter((p) => !/Failed to load resource.*(404|409)/.test(p)), []);
    await page.ctx.close();
  });
}

test("addresses of 0.1 are rewritten", async () => {
  await mock.reset();
  const page = await openApp(browser, mock, { route: "#/health" });
  await page.waitForFunction(() => location.hash === "#/problems");
  const id = await shopMain(page);
  await page.evaluate((h) => { location.hash = h; }, `#/p/shop/w/${id}/volumes`);
  await page.waitForFunction((i) => location.hash === `#/p/shop/w/${i}/data`, id);
  await page.ctx.close();
});

test("the side panel creates an instance and starts it", async () => {
  await mock.reset();
  const page = await openApp(browser, mock, { route: "#/p/shop" });
  await page.getByRole("link", { name: /new instance|nueva instancia/i }).first().click();
  const dialog = page.locator("dialog.drawer");
  await dialog.waitFor();
  await dialog.locator("#ni-name").fill("feature-panel");
  await dialog.locator("button[type=submit]").click();
  await page.waitForFunction(() => /#\/p\/shop\/w\//.test(location.hash), null, { timeout: 15000 });
  await page.waitForSelector("main h1");
  assert.match(await page.locator("main h1").innerText(), /feature-panel/);
  await page.ctx.close();
});

test("the command palette finds an instance and opens it", async () => {
  await mock.reset();
  const page = await openApp(browser, mock);
  await page.keyboard.press("Control+k");
  const input = page.locator("dialog.palette input[role=combobox]");
  await input.waitFor();
  await input.fill("fix-123");
  await waitIdle(page);
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => /#\/p\/shop\/w\//.test(location.hash) && /fix-123/.test(document.querySelector("main h1")?.textContent ?? ""), null, { timeout: 10000 });
  await page.ctx.close();
});

test("state filters, selection and the row menu of the instance list", async () => {
  await mock.reset();
  const page = await openApp(browser, mock, { route: "#/p/shop" });
  await page.waitForSelector("table.data.rows tbody tr");
  const total = await page.locator("table.data.rows tbody tr").count();
  await page.locator(".chips .chip").nth(1).click();
  await waitIdle(page);
  const running = await page.locator("table.data.rows tbody tr").count();
  assert.ok(running > 0 && running < total, `${running} of ${total}`);
  await page.locator("table.data.rows tbody tr td.pick input").first().check();
  await page.locator(".bulkbar").waitFor();
  const menu = page.locator("table.data.rows tbody tr .menu button[aria-haspopup=menu]").first();
  await menu.click();
  await page.locator("[role=menu] [role=menuitem]").first().waitFor();
  await waitIdle(page, 200);
  await page.keyboard.press("Escape");
  await waitIdle(page, 200);
  assert.equal(await page.locator("[role=menu]").count(), 0);
  assert.equal(await menu.evaluate((b) => b === document.activeElement), true, "focus goes back to the menu button");
  await page.ctx.close();
});

// Review of 0.3: the row menus used to open inside the table box, cut by its overflow (Projects ⋯ on the last row).
for (const [route, sel] of [["#/projects", "table.data tbody tr:last-child [aria-haspopup=menu]"], ["#/p/shop", "table.data.rows tbody tr:last-child [aria-haspopup=menu]"]]) {
  test(`the last row's menu is not cut by its table (${route})`, async () => {
    await mock.reset();
    const page = await openApp(browser, mock, { route, viewport: { width: 1100, height: 640 }, wait: "table.data tbody tr" });
    await waitIdle(page, 400);
    await page.locator(sel).click();
    await page.locator("[role=menu] [role=menuitem]").first().waitFor();
    await waitIdle(page, 200);
    const hidden = await page.evaluate(() => {
      const m = document.querySelector("[role=menu]");
      const r = m.getBoundingClientRect();
      const out = [];
      for (const it of m.querySelectorAll("[role=menuitem]")) {
        const b = it.getBoundingClientRect();
        const hit = document.elementFromPoint(b.left + b.width / 2, b.top + b.height / 2);
        if (!hit || !m.contains(hit) || b.bottom > innerHeight || b.right > innerWidth) out.push(it.textContent);
      }
      return { out, inside: r.top >= 0 && r.bottom <= innerHeight };
    });
    assert.deepEqual(hidden.out, [], "every item of the menu can be seen and clicked");
    assert.ok(hidden.inside, "the menu stays inside the window");
    await page.ctx.close();
  });
}

test("a backup can be made from the data tab", async () => {
  await mock.reset();
  const page = await openApp(browser, mock);
  const id = await shopMain(page);
  await page.evaluate((h) => { location.hash = h; }, `#/p/shop/w/${id}/data`);
  await page.waitForSelector("#data-backups");
  const rows = () => page.locator("section[aria-labelledby=data-backups] tbody tr").count();
  const n = await rows();
  await page.getByRole("button", { name: /back up now|copia ahora|hacer copia/i }).click();
  await page.waitForFunction((k) => document.querySelectorAll("section[aria-labelledby=data-backups] tbody tr").length > k, n, { timeout: 10000 });
  await page.ctx.close();
});

test("home stops every instance", async () => {
  await mock.reset();
  const page = await openApp(browser, mock);
  await page.waitForSelector(".list-rows .list-row");
  await page.getByRole("button", { name: /stop everything|parar todo/i }).first().click();
  await page.waitForFunction(() => document.querySelectorAll(".list-rows .list-row").length === 0, null, { timeout: 15000 });
  await page.ctx.close();
});
