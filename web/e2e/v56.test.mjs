// V5/V6 screens against the mock API: shared services, health (reconcile), settings and diagnostics, paginated history,
// the health indicator in the shell and the wizard's "neutralize fixed container names" checkbox.
import { test, before, after, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { chromium, startMock, openApp, axeViolations, seriousOnly, cspViolations, shot } from "./helpers.mjs";

let browser, mock;
before(async () => { mock = await startMock(); browser = await chromium.launch(); });
after(async () => { await browser?.close(); mock?.stop(); });
beforeEach(async () => { await mock.reset(); });

async function session() {
  const url = new URL(await mock.launchUrl());
  const r = await fetch(mock.origin + "/api/v1/session", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ nonce: url.hash.slice(3) }) });
  const cookie = r.headers.get("set-cookie").split(";")[0];
  const { csrf_token } = await r.json();
  return async (method, path, body) => {
    const res = await fetch(mock.origin + "/api/v1" + path, { method, headers: { Cookie: cookie, "X-Theo-Csrf": csrf_token, "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
    return { status: res.status, body: await res.json().catch(() => null) };
  };
}

/** axe in light and dark, CSP and console clean, optional screenshots. */
async function check(page, name, { ignoreNotFound = false } = {}) {
  assert.deepEqual(await cspViolations(page), [], `${name}: CSP violations`);
  const v = seriousOnly(await axeViolations(page));
  assert.deepEqual(v, [], `${name} (light): axe ${JSON.stringify(v, null, 1)}`);
  await shot(page, name);
  await page.emulateMedia({ colorScheme: "dark" });
  const vd = seriousOnly(await axeViolations(page));
  assert.deepEqual(vd, [], `${name} (dark): axe ${JSON.stringify(vd, null, 1)}`);
  await shot(page, name + "-dark");
  await page.emulateMedia({ colorScheme: "light" });
  // A project without shared services answers 404 shared_not_configured on purpose; Chrome logs every 404 fetch.
  const problems = ignoreNotFound ? page.problems.filter((p) => !/status of 404/.test(p)) : page.problems;
  assert.deepEqual(problems, [], `${name}: console problems`);
}

const active = (page) => page.evaluate(() => { const e = document.activeElement; return e ? `${e.tagName}|${e.id}|${(e.textContent || "").trim().slice(0, 40)}` : ""; });

// ------------------------------------------------------------------ shared services

test("shared services: provider, consumers and the explanation of exclusive vs shared", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop/shared", wait: "#sh-provider" });
  await page.locator("table tbody tr", { hasText: "db" }).first().waitFor();
  assert.ok(await page.getByText("Running").count() > 0);
  await page.locator("#sh-consumers").waitFor();
  const consumers = page.locator("section[aria-labelledby=sh-consumers] tbody tr");
  assert.equal(await consumers.count(), 2);
  assert.match(await page.locator("section[aria-labelledby=sh-provider] dl").textContent(), /theo-shop-shared/);
  assert.match(await page.locator("section[aria-labelledby=sh-provider] dl").textContent(), /Not published on the host/);
  await check(page, "40-shared-services");
  await page.ctx.close();
});

test("shared services: empty state explains exclusive vs shared; adding a service declares it without starting it", async () => {
  await mock.scenario({ sharedEmpty: true });
  const page = await openApp(browser, mock, { route: "#/p/shop/shared", wait: "#sh-empty" });
  assert.match(await page.locator("#sh-empty").textContent(), /No services are shared/);
  assert.match(await page.locator("section[aria-labelledby=sh-empty]").textContent(), /exclusive/);
  await check(page, "41-shared-empty", { ignoreNotFound: true });
  await page.fill("#sh-add-name", "db");
  await page.getByRole("button", { name: "Share this service" }).click();
  await page.locator("#sh-provider").waitFor();
  assert.match(await page.locator("section[aria-labelledby=sh-provider]").textContent(), /Not created/);
  assert.equal(await page.getByRole("button", { name: "Start shared services" }).isEnabled(), true);
  await page.ctx.close();
});

test("shared services: stopping with running consumers lists them and needs the typed text; Escape cancels", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop/shared", wait: "#sh-provider" });
  await page.locator("section[aria-labelledby=sh-consumers] tbody tr").first().waitFor();
  await page.getByRole("button", { name: "Stop shared services" }).click();
  const dlg = page.locator("dialog[open]");
  await dlg.waitFor();
  assert.match(await dlg.textContent(), /Instances that use them/);
  assert.match(await dlg.textContent(), /main/);
  assert.match(await dlg.textContent(), /fix-123/);
  assert.equal(await dlg.locator("img").count(), 0, "no mascot in a destructive dialog");
  assert.match(await active(page), /Cancel/, "the safe button has the focus");
  const confirm = dlg.getByRole("button", { name: "Stop shared services" });
  assert.equal(await confirm.isDisabled(), true);
  await dlg.locator("input").fill("wrong");
  assert.equal(await confirm.isDisabled(), true);
  await shot(page, "42-shared-stop-dialog");
  await page.keyboard.press("Escape");
  await dlg.waitFor({ state: "detached" });
  assert.match(await page.locator("section[aria-labelledby=sh-provider] tbody").textContent(), /Running/, "nothing was stopped");
  // Confirm for real.
  await page.getByRole("button", { name: "Stop shared services" }).click();
  await dlg.waitFor();
  await dlg.locator("input").fill("shop-shared");
  await confirm.click();
  await dlg.waitFor({ state: "detached" });
  await page.locator("section[aria-labelledby=sh-provider] tbody", { hasText: "Stopped" }).waitFor({ timeout: 10000 });
  assert.ok(await page.getByRole("button", { name: "Start shared services" }).isEnabled());
  await page.ctx.close();
});

test("shared services: the server revalidates the typed text of stop and purge", async () => {
  const call = await session();
  const r = await call("POST", "/projects/shop/shared/down", { confirm: "nope" });
  assert.equal(r.status, 409);
  assert.equal(r.body.error.code, "confirmation_required");
  assert.deepEqual(r.body.error.affected.map((a) => a.name).sort(), ["fix-123", "main"]);
});

test("shared services: purge is refused while instances use them, then needs the level-3 text", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop/shared", wait: "#sh-provider" });
  await page.locator("section[aria-labelledby=sh-consumers] tbody tr").first().waitFor();
  await page.getByRole("button", { name: "Delete data…" }).click();
  const dlg = page.locator("dialog[open]");
  await dlg.waitFor();
  assert.match(await dlg.textContent(), /These shared services are in use/);
  assert.match(await dlg.textContent(), /main/);
  await shot(page, "43-shared-purge-refused");
  await dlg.getByRole("button", { name: "Close" }).click();
  await dlg.waitFor({ state: "detached" });

  // Stop using it in both instances, stop the provider, then purge with the typed text.
  for (const svc of ["main", "fix-123"]) {
    await page.getByRole("button", { name: `Stop using db in ${svc}` }).click();
    await page.getByRole("button", { name: `Stop using db in ${svc}` }).waitFor({ state: "detached" });
  }
  await page.getByText("No instance uses the shared services yet.").waitFor();
  await page.getByRole("button", { name: "Stop shared services" }).click(); // no consumers left: no confirmation, runs directly
  await page.locator("section[aria-labelledby=sh-provider] tbody", { hasText: "Stopped" }).waitFor({ timeout: 10000 });
  await page.getByRole("button", { name: "Delete data…" }).click();
  await dlg.waitFor();
  assert.match(await dlg.textContent(), /shop-shared_pgdata/);
  const go = dlg.getByRole("button", { name: "Delete data" });
  assert.equal(await go.isDisabled(), true);
  await dlg.locator("input").fill("shop-shared");
  assert.equal(await go.isEnabled(), true);
  await shot(page, "44-shared-purge-dialog");
  await go.click();
  await dlg.waitFor({ state: "detached" });
  await page.ctx.close();
});

test("shared services: link an instance to a shared service, and stop sharing is refused while linked", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop/shared", wait: "#sh-provider" });
  await page.locator("section[aria-labelledby=sh-consumers] tbody tr").first().waitFor();
  await page.selectOption("#sh-link-wt", { label: "feature-x" });
  await page.getByLabel("db", { exact: true }).check();
  await page.getByRole("button", { name: "Use shared services" }).click();
  await page.locator("section[aria-labelledby=sh-consumers] tbody tr", { hasText: "feature-x" }).waitFor();
  assert.equal(await page.locator("section[aria-labelledby=sh-consumers] tbody tr").count(), 3);
  // "Stop sharing" db is refused by the server while linked: the dialog stays and shows why.
  await page.getByRole("button", { name: "Stop sharing db" }).click();
  const dlg = page.locator("dialog[open]");
  await dlg.getByRole("button", { name: "Stop sharing" }).click();
  await dlg.locator("[role=alert]").waitFor();
  assert.match(await dlg.locator("[role=alert]").textContent(), /used by instances/);
  await page.keyboard.press("Escape");
  await page.ctx.close();
});

test("shared services: keyboard only, the stop dialog returns the focus to its button", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop/shared", wait: "#sh-provider" });
  await page.locator("section[aria-labelledby=sh-consumers] tbody tr").first().waitFor();
  const stop = page.getByRole("button", { name: "Stop shared services" });
  await stop.focus();
  await page.keyboard.press("Enter");
  await page.locator("dialog[open]").waitFor();
  await page.keyboard.press("Escape");
  await page.locator("dialog[open]").waitFor({ state: "detached" });
  assert.match(await active(page), /Stop shared services/);
  await page.ctx.close();
});

test("shared services: a project without shared services answers shared_not_configured", async () => {
  const call = await session();
  const r = await call("GET", "/projects/blog/shared");
  assert.equal(r.status, 404);
  assert.equal(r.body.error.code, "shared_not_configured");
});

// ------------------------------------------------------------------ health / reconcile

test("health: findings grouped by kind, fixed text, indicator with a number, axe light and dark", async () => {
  const page = await openApp(browser, mock, { route: "#/health", wait: "h1" });
  await page.getByRole("heading", { name: /Worktree missing \(1\)/ }).waitFor();
  assert.ok(await page.getByRole("heading", { name: /Orphan containers \(1\)/ }).count() === 1);
  assert.ok(await page.getByRole("heading", { name: /Stale ports \(2\)/ }).count() === 1);
  assert.match(await page.locator("main").textContent(), /This does not delete containers, volumes or worktrees/);
  const nav = page.getByTestId("nav-health");
  assert.match(await nav.textContent(), /Health\s*6/);
  assert.match(await nav.textContent(), /findings/);
  // Only Theo's own records have a checkbox.
  const orphan = page.locator("section", { has: page.getByRole("heading", { name: /Orphan volumes/ }) });
  assert.equal(await orphan.locator("input[type=checkbox]").count(), 0);
  assert.match(await orphan.textContent(), /Nothing Theo can forget/);
  await check(page, "45-health");
  await page.ctx.close();
});

test("health: forget removes Theo's records only, after a confirmation, and the indicator follows", async () => {
  const call = await session();
  const page = await openApp(browser, mock, { route: "#/health", wait: "h1" });
  await page.getByRole("heading", { name: /Stale ports/ }).waitFor();
  const button = page.getByRole("button", { name: /Forget Theo's records/ });
  assert.equal(await button.isDisabled(), true);
  const staleSection = page.locator("section", { has: page.getByRole("heading", { name: /Stale ports/ }) });
  await staleSection.getByLabel("Select all Theo records in this group").check();
  assert.equal(await button.isEnabled(), true);
  await button.click();
  const dlg = page.locator("dialog[open]");
  await dlg.waitFor();
  assert.match(await dlg.textContent(), /Containers, volumes and worktrees are not touched/);
  await page.keyboard.press("Escape");
  await dlg.waitFor({ state: "detached" });
  assert.equal((await call("GET", "/reconcile")).body.findings.length, 6, "cancel changes nothing");
  await button.click();
  await dlg.getByRole("button", { name: "Forget records" }).click();
  await dlg.waitFor({ state: "detached" });
  await page.waitForFunction(() => document.querySelectorAll("section h2").length === 4);
  const after = (await call("GET", "/reconcile")).body.findings;
  assert.equal(after.length, 4);
  assert.deepEqual(after.map((f) => f.kind).sort(), ["orphan containers", "orphan volumes", "stale settings", "worktree missing"]);
  // The server refuses to forget anything that is not Theo's own record.
  const bad = after.find((f) => f.kind === "orphan volumes");
  const r = await call("POST", "/reconcile/forget", { finding_ids: [[bad.kind, bad.project, bad.subject, ""].join("|")] });
  assert.equal(r.status, 400);
  assert.equal((await call("GET", "/reconcile")).body.findings.length, 4);
  assert.match(await page.getByTestId("nav-health").textContent(), /Health\s*4/);
  await page.ctx.close();
});

test("health: with Docker down the report says docker_error and the indicator says it too", async () => {
  await mock.scenario({ docker: false });
  const page = await openApp(browser, mock, { route: "#/health", wait: "h1" });
  await page.getByText("Docker could not be asked").waitFor();
  assert.match(await page.locator("main").textContent(), /Cannot connect to the Docker daemon/);
  assert.match(await page.locator("main").textContent(), /findings that need Docker are missing/);
  assert.match(await page.getByTestId("nav-health").textContent(), /Docker unavailable/);
  await check(page, "46-health-docker-down");
  await page.ctx.close();
});

test("health: no findings says so in text and the indicator shows 0", async () => {
  await mock.scenario({ healthy: true });
  const page = await openApp(browser, mock, { route: "#/health", wait: "h1" });
  await page.getByText("No differences found").waitFor();
  assert.match(await page.getByTestId("nav-health").textContent(), /Health\s*0/);
  await check(page, "47-health-clean");
  await page.ctx.close();
});

// ------------------------------------------------------------------ settings and diagnostics

test("settings: doctor checks with status text, versions, copy report without secrets", async () => {
  const page = await openApp(browser, mock, { route: "#/settings", wait: "h1" });
  await page.getByRole("heading", { name: "Checks" }).waitFor();
  await page.locator("table tbody tr", { hasText: "Docker Compose" }).waitFor();
  assert.match(await page.locator("section[aria-labelledby=st-about]").textContent(), /0\.0\.0-mock/);
  assert.match(await page.locator("section[aria-labelledby=st-about]").textContent(), /\/home\/ana\/\.local\/share\/theo/);
  await check(page, "48-settings");
  // Report text: the clipboard may be blocked in the test browser; either way the text is available.
  await page.getByRole("button", { name: "Copy information for a report" }).click();
  const clip = await page.evaluate(() => navigator.clipboard.readText().catch(() => null));
  const text = clip ?? (await page.locator("#st-report-text").inputValue());
  assert.match(text, /Theo 0\.0\.0-mock/);
  assert.match(text, /\[ok\] Git/);
  assert.doesNotMatch(text, /\/home\/ana/, "no paths in the report");
  await page.ctx.close();
});

test("settings: an old Compose is reported as failed, with how to fix it", async () => {
  await mock.scenario({ composeOld: true });
  const page = await openApp(browser, mock, { route: "#/settings", wait: "h1" });
  const row = page.locator("table tbody tr", { hasText: "Docker Compose" });
  await row.waitFor();
  assert.match(await row.textContent(), /Failed/);
  assert.match(await row.textContent(), /Upgrade the Docker Compose plugin to 2\.24\.4/);
  assert.match(await page.locator("section[aria-labelledby=st-doctor]").textContent(), /does not work/);
  await check(page, "49-settings-compose-old");
  await page.ctx.close();
});

test("settings: a platform warning (Windows outside WSL 2) is shown as a warning, not as a failure", async () => {
  await mock.scenario({ windows: true });
  const page = await openApp(browser, mock, { route: "#/settings", wait: "h1" });
  const row = page.locator("table tbody tr", { hasText: "Project location" });
  await row.waitFor();
  assert.match(await row.textContent(), /Warning/);
  assert.match(await page.locator("section[aria-labelledby=st-doctor]").textContent(), /Everything Theo needs works/);
  await page.ctx.close();
});

test("settings: density and motion preferences persist in this browser", async () => {
  const page = await openApp(browser, mock, { route: "#/settings", wait: "h1" });
  await page.selectOption("#pref-density", "compact");
  await page.selectOption("#pref-motion", "reduce");
  assert.equal(await page.evaluate(() => document.documentElement.getAttribute("data-density")), "compact");
  assert.equal(await page.evaluate(() => document.documentElement.getAttribute("data-motion")), "reduce");
  await page.reload();
  await page.waitForSelector("#pref-density");
  assert.equal(await page.inputValue("#pref-density"), "compact");
  assert.equal(await page.inputValue("#pref-motion"), "reduce");
  await page.ctx.close();
});

// ------------------------------------------------------------------ history

test("history: pages of 20, older events on demand, until everything is loaded", async () => {
  const call = await session();
  const ov = (await call("GET", "/projects/shop/overview")).body;
  const main = ov.worktrees.find((w) => w.main).id;
  const page = await openApp(browser, mock, { route: `#/p/shop/w/${main}/history`, wait: "[role=tabpanel] table" });
  const rows = () => page.locator("[role=tabpanel] tbody tr").count();
  assert.equal(await rows(), 20);
  await page.getByRole("button", { name: "Load older events" }).click();
  await page.waitForFunction(() => document.querySelectorAll("[role=tabpanel] tbody tr").length === 40);
  await page.getByRole("button", { name: "Load older events" }).click();
  await page.waitForFunction(() => document.querySelectorAll("[role=tabpanel] tbody tr").length === 47); // 45 generated + 2 seeded
  await page.getByText("That is everything that was recorded.").waitFor();
  assert.equal(await page.getByRole("button", { name: "Load older events" }).count(), 0);
  await shot(page, "50-history-paginated");
  await page.ctx.close();
});

// ------------------------------------------------------------------ wizard: fixed container names

async function wizardToStep2(page, name) {
  await page.fill("#wz-new", name);
  await page.getByRole("button", { name: "Next" }).click();
  await page.waitForSelector("#wz-folder");
}

test("wizard: fixed container names are offered with an explicit checkbox that is OFF by default", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop/new", wait: ".stepper" });
  await wizardToStep2(page, "no-neutralize");
  const box = page.getByLabel("Neutralize fixed container names for this worktree");
  await box.waitFor();
  assert.equal(await box.isChecked(), false);
  assert.match(await page.locator("main").textContent(), /web: shop-web/);
  await shot(page, "51-wizard-fixed-names");
  await page.getByRole("button", { name: "Next" }).click();
  await page.getByRole("button", { name: "Next" }).click();
  assert.equal(await page.getByText("Remove the fixed container names for this worktree only").count(), 0, "not in the summary when not ticked");
  await page.getByLabel("Start the instance now").uncheck();
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await page.locator(".page-head h1").first().waitFor();
  await page.waitForTimeout(300);
  const call = await session();
  const ov = (await call("GET", "/projects/shop/overview")).body;
  const w = ov.worktrees.find((x) => x.branch === "no-neutralize");
  assert.equal((await call("GET", `/projects/shop/worktrees/${w.id}/settings`)).body.neutralize_container_names, false, "never silent");
  await page.ctx.close();
});

test("wizard: ticking the checkbox neutralizes the names of the new worktree only, and the summary says so", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop/new", wait: ".stepper" });
  await wizardToStep2(page, "do-neutralize");
  await page.getByLabel("Neutralize fixed container names for this worktree").check();
  await page.getByRole("button", { name: "Next" }).click();
  await page.getByRole("button", { name: "Next" }).click();
  await page.getByText("Remove the fixed container names for this worktree only").waitFor();
  await page.getByLabel("Start the instance now").uncheck();
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await page.locator(".page-head h1").first().waitFor();
  await page.waitForTimeout(300);
  const call = await session();
  const ov = (await call("GET", "/projects/shop/overview")).body;
  const w = ov.worktrees.find((x) => x.branch === "do-neutralize");
  assert.equal((await call("GET", `/projects/shop/worktrees/${w.id}/settings`)).body.neutralize_container_names, true);
  const main = ov.worktrees.find((x) => x.main);
  assert.equal((await call("GET", `/projects/shop/worktrees/${main.id}/settings`)).body.neutralize_container_names, false, "other instances untouched");
  await page.ctx.close();
});

test("wizard: a project without fixed container names does not show the checkbox", async () => {
  const page = await openApp(browser, mock, { route: "#/p/blog/new", wait: ".stepper" });
  await wizardToStep2(page, "plain-branch");
  assert.equal(await page.getByLabel("Neutralize fixed container names for this worktree").count(), 0);
  await page.ctx.close();
});

// ------------------------------------------------------------------ keyboard + shell

test("keyboard: the new pages are reachable from the sidebar and the project panel", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  await page.getByRole("link", { name: "Shared services" }).click();
  await page.waitForSelector("#sh-provider");
  await page.getByRole("link", { name: /^Health/ }).click();
  await page.waitForSelector("h1:has-text('Health')");
  await page.getByRole("link", { name: "Settings" }).click();
  await page.waitForSelector("h1:has-text('Settings and diagnostics')");
  assert.equal(await page.locator("[role=navigation][aria-label=Main] a[aria-current=page], nav[aria-label=Main] a[aria-current=page]").count(), 1);
  await page.ctx.close();
});

test("shared start from the page is an operation: it shows in the operations panel with its project", async () => {
  await mock.scenario({ slow: 2 });
  const call = await session();
  await call("POST", "/projects/shop/shared/down", { confirm: "shop-shared" });
  const page = await openApp(browser, mock, { route: "#/p/shop/shared", wait: "#sh-provider" });
  await page.locator("section[aria-labelledby=sh-provider] tbody", { hasText: /Stopped|Working/ }).waitFor({ timeout: 10000 });
  await page.getByRole("button", { name: "Start shared services" }).click();
  await page.getByRole("button", { name: /operation.* running/ }).click();
  await page.locator("#ops-panel").getByText(/Start shared services of shop/).first().waitFor();
  await page.locator("section[aria-labelledby=sh-provider] tbody", { hasText: "Running" }).waitFor({ timeout: 15000 });
  await page.ctx.close();
});
