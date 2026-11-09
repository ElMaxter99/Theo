// Cleanup in four steps: the groups are decided, the confirmation says what happens, the removal shows progress
// worktree by worktree, and the result says what happened to each one.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { chromium, startMock, openApp, axeViolations, seriousOnly } from "./helpers.mjs";

let browser, mock;
before(async () => { process.env.THEO_MOCK_STALE = "4"; browser = await chromium.launch(); mock = await startMock(); });
after(async () => { await browser?.close(); mock?.stop(); delete process.env.THEO_MOCK_STALE; });

test("review, decide, confirm, progress and result", async () => {
  await mock.reset();
  const page = await openApp(browser, mock, { route: "#/problems/cleanup" });
  await page.waitForSelector(".cl-group", { timeout: 10000 });
  await page.selectOption("#cl-project", "shop");
  await page.waitForSelector("input[name=dec-ignored]", { timeout: 10000 });
  // Step 1: the headline and the four counters add up; the group with ignored folders waits for a decision.
  const tiles = await page.locator(".cl-tile strong").allTextContents();
  assert.equal(tiles.length, 4);
  assert.ok(Number(tiles[1]) >= 1, "something waits for the person's OK");
  assert.equal(await page.locator(".cl-steps [aria-current=step]").count(), 1);
  const bar = page.locator(".cl-bar button.danger");
  const readyBefore = Number(tiles[0]);
  await page.locator("input[name=dec-ignored][type=radio]").first().check();
  const tilesAfter = await page.locator(".cl-tile strong").allTextContents();
  assert.ok(Number(tilesAfter[0]) > readyBefore, "deciding the group makes its worktrees ready");
  assert.equal(Number(tilesAfter[1]), 0);
  // Step 2: the confirmation names what is saved, what is deleted for good and what is kept.
  await bar.click();
  const dialog = page.locator("dialog[open]");
  await dialog.waitFor();
  assert.ok(await dialog.locator(".cl-block-warn").count() === 1, "the irreversible block is shown");
  assert.ok(await dialog.locator("text=certs/").count() >= 1);
  const submit = dialog.locator("button.danger.solid");
  assert.equal(await submit.isDisabled(), true);
  await dialog.locator("input.mono").fill("all");
  await submit.click();
  // Step 3: progress with the count and one line per worktree.
  await page.waitForSelector("dialog[open] progress", { timeout: 10000 });
  await page.waitForFunction(() => document.querySelector("dialog[open] ul.affected li"), null, { timeout: 10000 });
  await page.waitForFunction(() => [...document.querySelectorAll("dialog[open] strong")].some((s) => /removed/i.test(s.textContent)), null, { timeout: 15000 });
  assert.equal(await page.locator("dialog[open] ul.affected li").count() >= 3, true);
  // Step 4: closing the window shows the result of each one.
  await page.locator("dialog[open] .dialog-actions button").click();
  await page.waitForSelector("#cl-res", { timeout: 10000 });
  assert.ok(await page.locator(".cl-res").count() >= 3);
  assert.ok(await page.locator(".cl-steps [aria-current=step]").textContent().then((x) => /4|Result/.test(x)));
});

test("the review screen has no serious accessibility problems, light and dark", async () => {
  await mock.reset();
  for (const colorScheme of ["light", "dark"]) {
    const page = await openApp(browser, mock, { route: "#/problems/cleanup/shop", colorScheme });
    await page.waitForSelector("input[name=dec-ignored]", { timeout: 10000 });
    await page.locator("input[name=dec-ignored]").first().check();
    await page.locator(".cl-group .btn.sm[aria-expanded]").first().click();
    assert.deepEqual(seriousOnly(await axeViolations(page)).map((v) => v.id), [], colorScheme);
    await page.close();
  }
});
