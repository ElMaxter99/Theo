// A new version of Theo (0.11, board V-17): the banner, the tag, Settings › Updates and the six states. Against the
// mock API; the real flow (a real server replacing a real file) is in e2e/real/update.test.mjs.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { chromium, startMock, openApp, cspViolations, axeViolations, seriousOnly, shot, waitIdle } from "./helpers.mjs";

let browser, mock;
before(async () => { browser = await chromium.launch(); mock = await startMock(); });
after(async () => { await browser?.close(); mock?.stop(); });

test("nothing is shown while Theo is up to date", async () => {
  await mock.reset();
  const page = await openApp(browser, mock, { route: "#/settings/updates" });
  await page.waitForSelector("#st-update");
  assert.equal(await page.locator("[data-testid=update-tag]").count(), 0);
  assert.match(await page.locator("#st-update").locator("xpath=..").innerText(), /You have the latest version \(0\.11\.0\)/);
  await page.ctx.close();
});

for (const scheme of ["light", "dark"]) {
  test(`a new version: banner, tag and the settings page (${scheme})`, async () => {
    await mock.reset();
    await mock.scenario({ update: "available", desktop: true });
    const page = await openApp(browser, mock, { colorScheme: scheme });
    const banner = page.locator(".banner.info", { hasText: "Theo 0.11.1 is available (you have 0.11.0)" });
    await banner.waitFor();
    assert.match(await page.locator("[data-testid=update-tag]").innerText(), /0\.11\.1 available/);
    await shot(page, `v17-banner-${scheme}`);
    // the tag leads to Settings › Updates
    await page.click("[data-testid=update-tag]");
    await page.waitForSelector("#st-update");
    assert.match(await page.locator("body").innerText(), /Update to 0\.11\.1/);
    const v = seriousOnly(await axeViolations(page));
    assert.deepEqual(v, [], JSON.stringify(v, null, 1));
    await waitIdle(page, 200);
    await shot(page, `v17-updates-${scheme}`);
    assert.deepEqual(await cspViolations(page), []);
    assert.deepEqual(page.problems, []);
    await page.ctx.close();
  });
}

test("Later hides the banner but keeps the tag", async () => {
  await mock.reset();
  await mock.scenario({ update: "available" });
  const page = await openApp(browser, mock);
  await page.locator(".banner.info").waitFor();
  await page.click(".banner.info >> text=Later");
  await page.locator(".banner.info").waitFor({ state: "detached" });
  assert.equal(await page.locator("[data-testid=update-tag]").count(), 1);
  await page.ctx.close();
});

test("Update now downloads, installs and offers the restart", async () => {
  await mock.reset();
  await mock.scenario({ update: "available", desktop: true, slow: 1 });
  const page = await openApp(browser, mock, { route: "#/settings/updates" });
  await page.click("button:has-text('Update to 0.11.1')");
  await page.waitForSelector("progress");
  assert.match(await page.locator("main").innerText(), /Downloading Theo 0\.11\.1/);
  await shot(page, "v17-downloading");
  await page.waitForSelector("text=Theo 0.11.1 is installed", { timeout: 15000 });
  assert.equal(await page.locator("button:has-text('Restart Theo')").count() > 0, true);
  await shot(page, "v17-installed");
  await page.ctx.close();
});

test("a download that does not match its checksum is refused, with the reason", async () => {
  await mock.reset();
  await mock.scenario({ update: "checksum", desktop: true });
  const page = await openApp(browser, mock, { route: "#/settings/updates" });
  await page.click("button:has-text('Update to 0.11.1')");
  await page.waitForSelector("text=Download rejected", { timeout: 15000 });
  assert.match(await page.locator("main").innerText(), /nothing was installed/);
  await shot(page, "v17-checksum");
  await page.ctx.close();
});

test("a copy that cannot replace itself links to the downloads page", async () => {
  await mock.reset();
  await mock.scenario({ update: "cannot" });
  const page = await openApp(browser, mock, { route: "#/settings/updates" });
  await page.waitForSelector("a:has-text('Open the downloads page')");
  assert.equal(await page.locator("button:has-text('Update to')").count(), 0);
  await shot(page, "v17-cannot");
  await page.ctx.close();
});

test("offline: the check says so and Theo keeps working", async () => {
  await mock.reset();
  await mock.scenario({ update: "offline" });
  const page = await openApp(browser, mock, { route: "#/settings/updates" });
  await page.click("button:has-text('Check now')");
  await page.waitForSelector("text=Could not check");
  await page.ctx.close();
});

test("release candidates are opt-in and switching back hides them", async () => {
  await mock.reset();
  await mock.scenario({ update: "rc", desktop: true });
  const page = await openApp(browser, mock, { route: "#/settings/updates" });
  await page.waitForSelector("#upd-rc");
  assert.equal(await page.isChecked("#upd-rc"), false);
  assert.equal(await page.locator("[data-testid=update-tag]").count(), 0);
  await page.click("#upd-rc");
  await page.waitForSelector("[data-testid=update-tag]");
  assert.match(await page.locator("main").innerText(), /A test version is available: 0\.12\.0-rc\.2/);
  assert.match(await page.locator(".banner.info").innerText(), /Test version 0\.12\.0-rc\.2 is available/);
  await shot(page, "v17-rc");
  await page.click("#upd-rc");
  await page.locator("[data-testid=update-tag]").waitFor({ state: "detached" });
  await page.ctx.close();
});

test("automatic checks and notifications can be turned off", async () => {
  await mock.reset();
  const page = await openApp(browser, mock, { route: "#/settings/updates" });
  await page.waitForSelector("#upd-auto");
  assert.equal(await page.isChecked("#upd-auto"), true);
  await page.click("#upd-auto");
  await page.waitForFunction(() => !document.querySelector("#upd-auto").checked && document.querySelector("#upd-notify").disabled);
  await page.ctx.close();
});

test("the Spanish interface has the same page", async () => {
  await mock.reset();
  await mock.scenario({ update: "available" });
  const page = await openApp(browser, mock, { route: "#/settings/updates", locale: "es-ES" });
  await page.waitForSelector("text=Actualizar a 0.11.1");
  assert.match(await page.locator("main").innerText(), /Avisarme también de versiones de prueba/);
  await page.ctx.close();
});
