// The v0.2 additions in the interface: owners of worktrees, "Pause every instance", the local-domains switch and its links.
import { test, before, after, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { chromium, startMock, openApp, axeViolations, seriousOnly, cspViolations, shot } from "./helpers.mjs";

let browser, mock;
before(async () => { mock = await startMock(); browser = await chromium.launch(); });
after(async () => { await browser?.close(); mock?.stop(); });
beforeEach(async () => { await mock.reset(); });

const row = (page, name) => page.locator("tbody tr", { has: page.getByRole("link", { name, exact: true }) });

test("the owner of a worktree is shown and can be filtered by", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  await row(page, "fix-123").locator(".tag:has-text('Owner: agent-1')").waitFor();
  await page.getByLabel("Owner").selectOption("agent-1");
  assert.equal(await page.locator("tbody tr").count(), 1);
  await page.getByLabel("Owner").selectOption("-");
  assert.equal(await row(page, "fix-123").count(), 0);
  await page.getByLabel("Owner").selectOption("");
  await page.getByLabel("Filter worktrees").fill("agent");
  assert.equal(await page.locator("tbody tr").count(), 1, "the text filter matches the owner too");
  assert.deepEqual(seriousOnly(await axeViolations(page)), []);
  await page.ctx.close();
});

test("Pause every instance stops every running instance and keeps its containers", async () => {
  await mock.scenario({ slow: 0.2 });
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  await page.locator(".chips .chip").first().waitFor();
  await page.getByRole("button", { name: /^More actions for shop$/ }).click();
  const pause = page.getByRole("menuitem", { name: "Pause every instance" });
  assert.notEqual(await pause.getAttribute("aria-disabled"), "true");
  await pause.click();
  await row(page, "main").locator(".badge:has-text('Stopped')").waitFor({ timeout: 15000 });
  await row(page, "fix-123").locator(".badge:has-text('Stopped')").waitFor({ timeout: 15000 });
  assert.equal(await row(page, "old-spike").locator(".badge:has-text('No containers')").count(), 1, "an instance without containers is left alone");
  await page.ctx.close();
});

test("the local-domains switch runs the proxy and the panel links to the names", async () => {
  const page = await openApp(browser, mock, { route: "#/settings/domains", wait: "#proxy-on" });
  const sw = page.getByLabel("Serve local domains");
  assert.equal(await sw.isChecked(), false);
  await sw.check();
  await page.locator("#proxy-state:has-text('listening on 127.0.0.1:7080')").waitFor();
  assert.deepEqual(await cspViolations(page), []);
  assert.deepEqual(seriousOnly(await axeViolations(page)), []);
  await shot(page, "settings-proxy-on");

  await page.goto(page.url().replace(/#.*/, "#/p/shop"));
  await page.locator("table.data").waitFor();
  await row(page, "main").locator("td.url-list a[href='http://main.shop.localhost:7080']").waitFor();
  await shot(page, "panel-proxy-on");

  await page.goto(page.url().replace(/#.*/, "#/settings/domains"));
  await page.getByLabel("Serve local domains").uncheck();
  await page.locator("#proxy-state:has-text('Off.')").waitFor();
  assert.deepEqual(page.problems, []);
  await page.ctx.close();
});
