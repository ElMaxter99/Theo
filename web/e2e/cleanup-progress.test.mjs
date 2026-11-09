// Parking inactive worktrees in bulk shows a progress window (count, current branch, outcome of each) instead of a
// dialog whose button stays disabled until everything is done.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { chromium, startMock, openApp } from "./helpers.mjs";

let browser, mock;
before(async () => { process.env.THEO_MOCK_STALE = "4"; browser = await chromium.launch(); mock = await startMock(); });
after(async () => { await browser?.close(); mock?.stop(); delete process.env.THEO_MOCK_STALE; });

test("bulk park shows progress and a result", async () => {
  await mock.reset();
  const page = await openApp(browser, mock, { route: "#/problems/cleanup" });
  await page.waitForSelector("th.pick input[type=checkbox]", { timeout: 10000 });
  await page.locator("th.pick input[type=checkbox]").check();
  await page.locator(".bulkbar button.danger").first().click();
  const dialog = page.locator("dialog[open]");
  await dialog.locator("input.mono").fill("all");
  await dialog.locator("button.danger").click();
  await page.waitForSelector("progress", { timeout: 10000 });
  await page.waitForFunction(() => document.querySelector("dialog[open] ul.affected li"), null, { timeout: 10000 });
  await page.waitForFunction(() => !document.querySelector("dialog[open] progress") || document.querySelector("dialog[open] progress").value === document.querySelector("dialog[open] progress").max, null, { timeout: 15000 });
  await page.waitForFunction(() => [...document.querySelectorAll("dialog[open] strong")].some((s) => /removed/i.test(s.textContent)), null, { timeout: 15000 });
  assert.equal(await page.locator("dialog[open] ul.affected li").count() >= 4, true);
});
