// The log viewer: virtualization with 50 000 lines, follow / pause / search, gap markers, download link.
import { test, before, after, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { chromium, startMock, openApp, shot } from "./helpers.mjs";

let browser, mock;
before(async () => { mock = await startMock(); browser = await chromium.launch(); });
after(async () => { await browser?.close(); mock?.stop(); });
beforeEach(async () => { await mock.reset(); });

async function mainId() {
  const url = new URL(await mock.launchUrl());
  const r = await fetch(mock.origin + "/api/v1/session", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ nonce: url.hash.slice(3) }) });
  const cookie = r.headers.get("set-cookie").split(";")[0];
  const ov = await (await fetch(mock.origin + "/api/v1/projects/shop/overview", { headers: { Cookie: cookie } })).json();
  return ov.worktrees.find((w) => w.main).id;
}

const lineNodes = (page) => page.locator(".log-line").count();

test("50 000 lines: the DOM never holds more than 10 000 line nodes, scrolling and search stay responsive", async () => {
  const id = await mainId();
  const page = await openApp(browser, mock, { route: `#/p/shop/w/${id}/logs`, wait: ".log-view" });
  await page.locator("#log-tail").selectOption("50000");
  await page.waitForFunction(() => Number((document.querySelector("[data-testid=log-count]")?.textContent ?? "").replace(/[^\d]/g, "")) >= 50000, null, { timeout: 30000 });
  let n = await lineNodes(page);
  assert.ok(n > 0 && n <= 10000, `line nodes: ${n}`);
  assert.ok(n < 500, `only a window is rendered (${n})`);
  // Scroll to the middle and to the top: still a window, and it shows different lines.
  const first = await page.locator(".log-line").first().textContent();
  await page.locator(".log-view").evaluate((el) => { el.scrollTop = el.scrollHeight / 2; });
  await page.waitForTimeout(300);
  n = await lineNodes(page);
  assert.ok(n < 500, `after scroll: ${n}`);
  const mid = await page.locator(".log-line").first().textContent();
  assert.notEqual(first, mid);
  // Search filters 50 000 lines.
  const t0 = Date.now();
  await page.fill("#log-search", "ERROR");
  await page.waitForFunction(() => /of [\d,]+ lines match/.test(document.querySelector("[data-testid=log-count]")?.textContent ?? ""));
  assert.ok(Date.now() - t0 < 3000, "search took " + (Date.now() - t0) + " ms");
  assert.ok(await page.locator(".log-line mark").count() > 0, "matches are highlighted");
  await shot(page, "23-logs-50000-search");
  await page.ctx.close();
});

test("a line written by the instance appears in under 3 seconds and follow keeps the end in view", async () => {
  const id = await mainId();
  const page = await openApp(browser, mock, { route: `#/p/shop/w/${id}/logs`, wait: ".log-view" });
  await page.locator(".log-line").first().waitFor();
  await page.locator("text=live line").first().waitFor({ timeout: 3000 });
  const atBottom = await page.locator(".log-view").evaluate((el) => el.scrollTop + el.clientHeight >= el.scrollHeight - 40);
  assert.ok(atBottom, "following: the view is at the end");
  await page.ctx.close();
});

test("scrolling up stops following; pause freezes the view; resume shows what arrived", async () => {
  const id = await mainId();
  const page = await openApp(browser, mock, { route: `#/p/shop/w/${id}/logs`, wait: ".log-view" });
  await page.locator(".log-line").first().waitFor();
  await page.locator(".log-view").evaluate((el) => { el.scrollTop = 0; });
  await page.getByRole("button", { name: "Jump to the end" }).waitFor();
  assert.equal(await page.getByLabel("Follow").isChecked(), false);
  await page.getByLabel("Follow").check();
  await page.getByRole("button", { name: "Pause" }).click();
  const before = await page.locator("[data-testid=log-count]").textContent();
  await page.waitForTimeout(1600);
  assert.equal(await page.locator("[data-testid=log-count]").textContent(), before, "paused: count does not move");
  assert.ok(await page.locator("text=Paused").count() > 0);
  await page.getByRole("button", { name: "Resume" }).click();
  await page.waitForFunction((b) => document.querySelector("[data-testid=log-count]")?.textContent !== b, before);
  await page.ctx.close();
});

test("gap marker is shown as text and ANSI sequences are stripped", async () => {
  await mock.scenario({ gap: true });
  const id = await mainId();
  const page = await openApp(browser, mock, { route: `#/p/shop/w/${id}/logs`, wait: ".log-view" });
  await page.locator(".log-line.gap").first().scrollIntoViewIfNeeded().catch(() => undefined);
  await page.locator(".log-view").evaluate((el) => { el.scrollTop = el.scrollHeight; });
  await page.locator(".log-line.gap").waitFor();
  assert.ok((await page.locator(".log-line.gap").first().textContent()).includes("321 lines not shown"));
  const html = await page.locator(".log-view").innerHTML();
  assert.ok(!html.includes("\u001b"), "no escape sequences in the DOM");
  await page.ctx.close();
});

test("download is a plain link to the text endpoint, without any token in it", async () => {
  const id = await mainId();
  const page = await openApp(browser, mock, { route: `#/p/shop/w/${id}/logs`, wait: ".log-view" });
  const href = await page.getByRole("link", { name: /Download/ }).getAttribute("href");
  assert.match(href, /^\/api\/v1\/projects\/shop\/worktrees\/wt_[0-9a-f]{10}\/logs\?tail=all$/);
  const res = await page.evaluate(async (h) => { const r = await fetch(h); return { status: r.status, type: r.headers.get("content-type"), text: (await r.text()).slice(0, 200) }; }, href);
  assert.equal(res.status, 200);
  assert.match(res.type, /text\/plain/);
  await page.ctx.close();
});
