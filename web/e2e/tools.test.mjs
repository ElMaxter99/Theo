// The Tools page (0.9, V-16): the capture tool. Against the mock API; the real flow is in e2e/real/tools.test.mjs.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { chromium, startMock, openApp, cspViolations, axeViolations, seriousOnly, shot, waitIdle } from "./helpers.mjs";

let browser, mock;
before(async () => { browser = await chromium.launch(); mock = await startMock(); });
after(async () => { await browser?.close(); mock?.stop(); });

for (const scheme of ["light", "dark"]) {
  test(`the Tools page renders cleanly (${scheme})`, async () => {
    await mock.reset();
    const page = await openApp(browser, mock, { route: "#/tools", colorScheme: scheme });
    await page.waitForSelector("[data-testid=compare-form]");
    assert.equal(await page.locator("[data-testid=report-row]").count(), 2);
    assert.match(await page.locator("[data-testid=inventory-summary]").innerText(), /2 viewports · 31 screens · 64 states · 5 flows/);
    assert.match(await page.locator("[data-testid=sets-list]").innerText(), /develop · 2f9c1ab · 128 captures/);
    assert.match(await page.locator("a[data-testid=nav-tools], nav a[href='#/tools']").first().getAttribute("aria-current") ?? "", /page/);
    const v = seriousOnly(await axeViolations(page));
    assert.deepEqual(v, [], JSON.stringify(v, null, 1));
    await waitIdle(page, 300);
    await shot(page, `v09-tools-${scheme}`);
    assert.deepEqual(await cspViolations(page), []);
    assert.deepEqual(page.problems, []);
    await page.ctx.close();
  });
}

test("comparing two branches: approval, progress and the new report", async () => {
  await mock.reset();
  const page = await openApp(browser, mock, { route: "#/tools" });
  await page.waitForSelector("[data-testid=compare-form]");
  // the same branch twice is refused before anything is sent
  await page.selectOption("#tl-a", "main");
  await page.selectOption("#tl-b", "main");
  await page.click("[data-testid=start-compare]");
  assert.match(await page.locator(".field-error").innerText(), /two different branches/i);
  await page.selectOption("#tl-b", "fix-123");
  await page.click("[data-testid=start-compare]");
  // the commands that start the app have to be allowed first
  await page.waitForSelector("[data-testid=approve-commands]");
  assert.match(await page.locator("[data-testid=approve-commands]").innerText(), /npm run build/);
  await shot(page, "v09-tools-approve");
  await page.getByRole("button", { name: /Allow and start/ }).click();
  await page.waitForSelector("[data-testid=capture-running]");
  await page.waitForFunction(() => /main: \d+ of 12/.test(document.querySelector("[data-testid=capture-progress]")?.textContent ?? ""));
  await shot(page, "v09-tools-running");
  await page.waitForSelector("[data-testid=capture-close]", { timeout: 15000 });
  assert.match(await page.locator("[data-testid=capture-running]").innerText(), /Finished\. main vs fix-123: 5 changed/);
  await shot(page, "v09-tools-finished");
  await page.click("[data-testid=capture-close]");
  await page.waitForFunction(() => document.querySelectorAll("[data-testid=report-row]").length === 3);
  assert.match(await page.locator("[data-testid=report-row]").first().innerText(), /main → fix-123/);
  // the report buttons open in a new tab and are served by the API
  const link = page.getByRole("link", { name: /Compositions/ }).first();
  assert.equal(await link.getAttribute("target"), "_blank");
  assert.match(await link.getAttribute("href"), /\/api\/v1\/projects\/shop\/capture\/reports\/2026-11-15-main-vs-fix-123\/composiciones\.html$/);
  assert.deepEqual(page.problems.filter((p) => !/Failed to load resource.*409/.test(p)), []);
  await page.ctx.close();
});

test("without the engine the page offers to install it, and without inventory it says how to start", async () => {
  await mock.reset();
  await mock.scenario({ capture: "noengine" });
  const page = await openApp(browser, mock, { route: "#/tools" });
  await page.waitForSelector("[data-testid=install-engine]");
  assert.equal(await page.locator("[data-testid=compare-form]").count(), 0);
  await shot(page, "v09-tools-no-engine");
  await page.click("[data-testid=install-engine]");
  await page.getByRole("button", { name: "Install", exact: true }).click();
  await page.waitForSelector("[data-testid=engine-ready]", { timeout: 10000 });
  assert.match(await page.locator("[data-testid=engine-ready]").innerText(), /Engine ready/);
  await mock.scenario({ capture: "noinv" });
  await page.reload();
  await page.waitForSelector("text=There are no captures yet");
  assert.match(await page.locator("main").innerText(), /pantallas\.json/);
  assert.equal(await page.locator("[data-testid=compare-form]").count(), 0);
  await shot(page, "v09-tools-no-inventory");
  await page.ctx.close();
});

test("the Tools page in Spanish", async () => {
  await mock.reset();
  const page = await openApp(browser, mock, { route: "#/tools", locale: "es-ES" });
  await page.waitForSelector("[data-testid=compare-form]");
  assert.equal(await page.locator("main h1").innerText(), "Herramientas");
  assert.match(await page.locator("[data-testid=compare-form]").innerText(), /Capturar dos ramas/);
  await shot(page, "v09-tools-es");
  await page.ctx.close();
});
