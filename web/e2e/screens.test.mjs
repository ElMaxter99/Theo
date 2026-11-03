// Every screen loads, has no critical/serious axe violations (light and dark), and produces no CSP violation or console error.
// Runs against the mock API server (see README of the tests in docs/cli/ui-web.md).
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { chromium, startMock, openApp, axeViolations, seriousOnly, cspViolations, shot } from "./helpers.mjs";

let browser, mock;
before(async () => { mock = await startMock(); browser = await chromium.launch(); });
after(async () => { await browser?.close(); mock?.stop(); });

const shopId = async () => {
  const url = new URL(await mock.launchUrl());
  // Ids are deterministic in the mock: read them through the API with a throw-away session.
  const r = await fetch(mock.origin + "/api/v1/session", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ nonce: url.hash.slice(3) }) });
  const cookie = r.headers.get("set-cookie").split(";")[0];
  const ov = await (await fetch(mock.origin + "/api/v1/projects/shop/overview", { headers: { Cookie: cookie } })).json();
  const by = (b) => ov.worktrees.find((w) => w.branch === b)?.id;
  return { main: by("main"), fix: by("fix-123"), feature: by("feature-x"), old: by("old-spike"), detached: ov.worktrees.find((w) => w.detached)?.id };
};

async function check(page, name, { shots = true, dark = true } = {}) {
  assert.deepEqual(await cspViolations(page), [], `${name}: CSP violations`);
  const v = seriousOnly(await axeViolations(page));
  assert.deepEqual(v, [], `${name} (light): axe ${JSON.stringify(v, null, 1)}`);
  if (shots) await shot(page, name);
  if (dark) {
    await page.emulateMedia({ colorScheme: "dark" });
    const vd = seriousOnly(await axeViolations(page));
    assert.deepEqual(vd, [], `${name} (dark): axe ${JSON.stringify(vd, null, 1)}`);
    if (shots) await shot(page, name + "-dark");
    await page.emulateMedia({ colorScheme: "light" });
  }
  assert.deepEqual(page.problems, [], `${name}: console problems`);
}

test("welcome / empty state", async () => {
  await mock.reset();
  await mock.scenario({ empty: true });
  const page = await openApp(browser, mock);
  assert.equal(await page.locator(".welcome img.mascot").count(), 1);
  await check(page, "01-welcome");
  await page.ctx.close();
});

test("home and project list", async () => {
  await mock.reset();
  const page = await openApp(browser, mock);
  await page.locator(".list-rows .list-row").first().waitFor();
  await page.waitForTimeout(500);
  assert.equal(await page.locator(".mascot").count(), 0, "no mascot on the home screen");
  await check(page, "02-home");
  await page.evaluate(() => { location.hash = "#/projects"; });
  await page.locator("table.data tbody tr").first().waitFor();
  await page.waitForTimeout(300);
  assert.equal(await page.locator(".mascot").count(), 0, "no mascot on the project list");
  await check(page, "02-projects");
  await page.ctx.close();
});

test("add project and browse dialog", async () => {
  await mock.reset();
  const page = await openApp(browser, mock, { route: "#/projects/new" });
  await check(page, "03-add-project");
  await page.getByRole("button", { name: /Browse/ }).click();
  await page.waitForSelector("dialog[open] .affected");
  await page.waitForTimeout(200);
  await check(page, "04-browse-dialog", { dark: false });
  await page.keyboard.press("Escape");
  await page.ctx.close();
});

test("project panel (all states) and docker-down state", async () => {
  await mock.reset();
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  await page.waitForTimeout(300);
  for (const label of ["Running", "Partial", "Stopped", "No containers", "Failing"]) assert.ok(await page.locator(`.badge:has-text("${label}")`).count() > 0, `state ${label} shown`);
  assert.ok(await page.locator(".badge:has-text('Port 3000 in use')").count() > 0, "port conflict is shown as text");
  assert.ok(await page.locator("a[href^='http://localhost:3000'][rel='noopener noreferrer'][target='_blank']").count() > 0, "URLs are safe links");
  await check(page, "05-project-panel");
  await mock.scenario({ docker: false });
  await page.reload();
  await page.waitForSelector("text=Docker is not available");
  await page.waitForTimeout(300);
  assert.ok(await page.locator("table.data tbody tr").count() >= 5, "registry views still shown without Docker");
  assert.ok(await page.locator(".badge:has-text('Unknown')").count() >= 5);
  await check(page, "06-docker-down", { dark: false });
  await page.ctx.close();
});

test("instance detail tabs", async () => {
  await mock.reset();
  const ids = await shopId();
  const tabs = ["summary", "logs", "config", "data", "activity"];
  const page = await openApp(browser, mock, { route: `#/p/shop/w/${ids.main}/summary` });
  let n = 7;
  for (const tab of tabs) {
    await page.evaluate((h) => { location.hash = h; }, `#/p/shop/w/${ids.main}/${tab}`);
    await page.waitForSelector(`[role=tab][aria-selected=true]#inst-tab-${tab}`);
    await page.waitForTimeout(tab === "logs" ? 1200 : 500);
    await check(page, `${String(n++).padStart(2, "0")}-detail-${tab}`, { dark: tab === "summary" || tab === "logs" });
  }
  await page.ctx.close();
});

test("instance with risky mounts shows text + icon warnings", async () => {
  await mock.reset();
  const ids = await shopId();
  const page = await openApp(browser, mock, { route: `#/p/shop/w/${ids.feature}/services` });
  await page.waitForSelector("table.data");
  assert.ok(await page.locator("text=outside the workspace").count() > 0);
  assert.ok(await page.locator("text=Docker socket").count() > 0);
  await check(page, "14-services-risky", { dark: false });
  await page.ctx.close();
});

test("new instance panel", async () => {
  await mock.reset();
  const page = await openApp(browser, mock, { route: "#/p/shop/new", wait: "dialog.drawer" });
  await page.fill("#ni-name", "fix-456");
  await page.waitForSelector("text=These host ports are taken");
  await check(page, "15-new-instance");
  await page.locator("details.more summary").click();
  await check(page, "16-new-instance-more", { dark: false });
  await page.getByText("Existing branch").click();
  await check(page, "17-new-instance-existing", { dark: false });
  await page.ctx.close();
});

test("operations panel", async () => {
  await mock.reset();
  await mock.scenario({ slow: 4 });
  const ids = await shopId();
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  await page.getByRole("button", { name: /Start feature-x/ }).click();
  await page.waitForSelector("text=1 operation running");
  await page.getByRole("button", { name: /operation running/ }).click();
  await page.waitForSelector("#ops-panel");
  await page.getByRole("button", { name: "Show output" }).first().click();
  await page.waitForSelector(".log-line");
  await check(page, "19-operations-panel", { dark: false });
  void ids;
  await page.ctx.close();
});
