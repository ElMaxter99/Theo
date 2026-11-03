// Interaction flows against the mock API: lifecycle and operations, the new instance panel, destructive dialogs, variables, SSE budget.
import { test, before, after, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { chromium, startMock, openApp, shot } from "./helpers.mjs";

let browser, mock;
before(async () => { mock = await startMock(); browser = await chromium.launch(); });
after(async () => { await browser?.close(); mock?.stop(); });
beforeEach(async () => { await mock.reset(); });

/** Counts the streams (event channel, logs, operation output) that are open at the same time in a page. */
function watchStreams(page) {
  const open = new Set();
  let max = 0;
  const isStream = (u) => /\/api\/v1\/(events$|operations\/[^/]+\/events|projects\/.*\/logs\?.*follow=1)/.test(u);
  page.on("request", (r) => { if (isStream(r.url())) { open.add(r); max = Math.max(max, open.size); } });
  page.on("requestfinished", (r) => open.delete(r));
  page.on("requestfailed", (r) => open.delete(r));
  return { max: () => max };
}

async function session() {
  const url = new URL(await mock.launchUrl());
  const r = await fetch(mock.origin + "/api/v1/session", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ nonce: url.hash.slice(3) }) });
  const cookie = r.headers.get("set-cookie").split(";")[0];
  const { csrf_token } = await r.json();
  const call = async (method, path, body) => {
    const res = await fetch(mock.origin + "/api/v1" + path, { method, headers: { Cookie: cookie, "X-Theo-Csrf": csrf_token, "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
    return { status: res.status, body: await res.json().catch(() => null) };
  };
  return call;
}
async function wt(branch) {
  const call = await session();
  const ov = (await call("GET", "/projects/shop/overview")).body;
  return ov.worktrees.find((w) => w.branch === branch)?.id;
}
const row = (page, name) => page.locator("tbody tr", { has: page.getByRole("link", { name, exact: true }) });

test("start from the panel: row-level busy state, then running with a link", async () => {
  await mock.scenario({ slow: 0.3 });
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  const r = row(page, "feature-x");
  await r.getByRole("button", { name: /Start feature-x/ }).click();
  await r.locator(".badge:has-text('Starting')").waitFor();
  assert.equal(await r.getByRole("button", { name: /Start feature-x/ }).count(), 0, "the row shows the progress instead of Start while busy");
  assert.ok(await row(page, "main").getByRole("button", { name: /More actions for main/ }).isEnabled(), "other rows stay usable");
  await r.locator(".badge:has-text('Running')").waitFor({ timeout: 15000 });
  await r.locator("td.url-list a[href^='http://localhost:']").waitFor();
  assert.ok((await page.locator("#live-region").textContent()) !== null);
  await page.ctx.close();
});

test("a second operation on a busy instance is refused with busy", async () => {
  await mock.scenario({ slow: 3 });
  const id = await wt("feature-x");
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  await row(page, "feature-x").getByRole("button", { name: /Start feature-x/ }).click();
  await row(page, "feature-x").locator(".badge:has-text('Starting')").waitFor();
  const res = await page.evaluate(async (wtid) => {
    const csrf = sessionStorage.getItem("theo.csrf");
    const r = await fetch(`/api/v1/projects/shop/worktrees/${wtid}/up`, { method: "POST", headers: { "Content-Type": "application/json", "X-Theo-Csrf": csrf }, body: "{}" });
    return { status: r.status, body: await r.json() };
  }, id);
  assert.equal(res.status, 409);
  assert.equal(res.body.error.code, "busy");
  await page.ctx.close();
});

test("cancel an operation: state becomes cancelled and the page stays consistent", async () => {
  await mock.scenario({ slow: 3 });
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  await row(page, "feature-x").getByRole("button", { name: /Start feature-x/ }).click();
  await page.getByRole("button", { name: /operation running/ }).click();
  await page.getByRole("button", { name: /Cancel Start feature-x/ }).click();
  await page.locator("#ops-panel .badge:has-text('Cancelled')").waitFor({ timeout: 15000 });
  await page.ctx.close();
});

test("closing the tab during an operation does not stop it; reopening shows it and the history records it", async () => {
  await mock.scenario({ slow: 1 });
  const id = await wt("feature-x");
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  await row(page, "feature-x").getByRole("button", { name: /Start feature-x/ }).click();
  await row(page, "feature-x").locator(".badge:has-text('Starting')").waitFor();
  await page.ctx.close();
  const page2 = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  await page2.getByRole("button", { name: /operation/ }).click();
  await page2.locator("#ops-panel").waitFor();
  await row(page2, "feature-x").locator(".badge:has-text('Running')").waitFor({ timeout: 20000 });
  const call = await session();
  const hist = (await call("GET", `/projects/shop/worktrees/${id}/history`)).body;
  assert.equal(hist[0].operation, "up");
  assert.equal(hist[0].result, "ok");
  await page2.ctx.close();
});

const panel = (page) => page.locator("dialog.drawer");

test("new instance: an existing branch in another worktree cannot be chosen, and says why", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop/new", wait: "dialog.drawer" });
  await panel(page).getByText("Existing branch").click();
  const option = page.locator("#ni-branch option", { hasText: "fix-123" });
  assert.ok(await option.evaluate((o) => o.disabled));
  assert.match(await option.innerText(), /in use: it is active in another worktree/);
  await page.selectOption("#ni-branch", "develop");
  assert.match(await panel(page).locator(".summary-line").innerText(), /develop/);
  await page.ctx.close();
});

test("new instance: conflict preview, automatic ports, both instances running with distinct URLs", async () => {
  await mock.scenario({ slow: 0.2 });
  const page = await openApp(browser, mock, { route: "#/p/shop/new", wait: "dialog.drawer" });
  await page.fill("#ni-name", "fix-456");
  await panel(page).locator("text=These host ports are taken").waitFor();
  assert.ok(await page.locator("#ni-auto").isChecked(), "automatic is the default");
  await panel(page).getByRole("button", { name: "Create and start" }).click();
  await page.waitForURL(/#\/p\/shop\/w\/wt_[0-9a-f]{10}\/logs/);
  await page.waitForSelector(".log-view");
  await shot(page, "20-after-new-instance-logs");
  const call = await session();
  const deadline = Date.now() + 20000;
  let ov;
  for (;;) {
    ov = (await call("GET", "/projects/shop/overview")).body;
    const w = ov.worktrees.find((x) => x.branch === "fix-456");
    if (w?.state === "running") break;
    assert.ok(Date.now() < deadline, "instance did not reach running");
    await new Promise((r) => setTimeout(r, 300));
  }
  const urls = ov.worktrees.filter((w) => w.state === "running").flatMap((w) => w.ports.filter((p) => p.status === "running" && p.url).map((p) => p.url));
  assert.equal(new Set(urls).size, urls.length, "URLs are distinct: " + urls.join(","));
  await page.ctx.close();
});

test("new instance: when starting fails the worktree is kept and the screen says so", async () => {
  await mock.scenario({ slow: 0.2 });
  const page = await openApp(browser, mock, { route: "#/p/shop/new", wait: "dialog.drawer" });
  await page.fill("#ni-name", "fail-it");
  await panel(page).getByRole("button", { name: "Create and start" }).click();
  await page.waitForURL(/\/logs/);
  await page.locator("text=The last operation failed").waitFor({ timeout: 20000 });
  assert.ok(await page.locator("text=The worktree and the branch were kept").count() > 0);
  await page.ctx.close();
});

test("new instance: notes from the engine are shown", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop/new", wait: "dialog.drawer" });
  await page.fill("#ni-name", "noenv-branch");
  await panel(page).getByRole("button", { name: "Create only" }).click();
  await page.locator("text=Notes from Theo").waitFor();
  assert.ok(await page.locator("text=no .env file was found").count() > 0);
  await page.ctx.close();
});

test("remove a worktree with uncommitted changes: typed confirmation, server revalidates", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  const r = row(page, "old-spike");
  await r.getByRole("button", { name: /More actions for/ }).click();
  await page.getByRole("menuitem", { name: /Remove worktree/ }).click();
  await page.getByRole("button", { name: "Remove worktree" }).click(); // plain confirmation, nothing deleted yet
  const dlg = page.locator("dialog[open]");
  await dlg.locator("text=Will be deleted").waitFor();
  assert.ok(await dlg.locator("text=src/server.ts").count() > 0, "changes are listed");
  assert.equal(await dlg.locator("img").count(), 0, "no mascot in a destructive dialog");
  const remove = dlg.getByRole("button", { name: "Remove worktree" });
  assert.ok(await remove.isDisabled());
  assert.ok(await dlg.getByRole("button", { name: "Cancel" }).evaluate((el) => el === document.activeElement), "Cancel has the initial focus");
  await dlg.getByLabel(/Type the folder name/).fill("shop-old-spik");
  assert.ok(await remove.isDisabled(), "wrong text keeps it disabled");
  await shot(page, "21-remove-dialog");
  await dlg.getByLabel(/Type the folder name/).fill("shop-old-spike");
  assert.ok(await remove.isEnabled());
  await remove.click();
  await page.locator("dialog[open]").waitFor({ state: "detached" });
  await page.locator("tbody tr", { hasText: "old-spike" }).waitFor({ state: "detached" });
  await page.ctx.close();
});

test("remove: Escape cancels; the server rejects a wrong confirmation when called directly", async () => {
  const id = await wt("old-spike");
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  const r = row(page, "old-spike");
  await r.getByRole("button", { name: /More actions for/ }).click();
  await page.getByRole("menuitem", { name: /Remove worktree/ }).click();
  await page.locator("dialog[open]").waitFor();
  await page.keyboard.press("Escape");
  await page.locator("dialog[open]").waitFor({ state: "detached" });
  await row(page, "old-spike").waitFor();
  const res = await page.evaluate(async (wtid) => {
    const csrf = sessionStorage.getItem("theo.csrf");
    const r = await fetch(`/api/v1/projects/shop/worktrees/${wtid}/remove`, { method: "POST", headers: { "Content-Type": "application/json", "X-Theo-Csrf": csrf }, body: JSON.stringify({ confirm: "wrong" }) });
    return { status: r.status, body: await r.json() };
  }, id);
  assert.equal(res.status, 409);
  assert.equal(res.body.error.code, "confirmation_required");
  await page.ctx.close();
});

test("remove a worktree that still has containers: refused, 'stop first'", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  const r = row(page, "fix-123");
  await r.getByRole("button", { name: /More actions for/ }).click();
  await page.getByRole("menuitem", { name: /Remove worktree/ }).click();
  await page.getByRole("button", { name: "Remove worktree" }).click();
  await page.locator("dialog[open] >> text=This instance still has containers").waitFor();
  assert.ok(await page.locator("dialog[open] >> text=Stop the instance first").count() > 0);
  await page.ctx.close();
});

test("purge volumes: typed confirmation with the Compose project name; external volumes are not deletable", async () => {
  const id = await wt("old-spike"); // no containers: the only state in which data can be deleted
  const page = await openApp(browser, mock, { route: `#/p/shop/w/${id}/volumes`, wait: "table.data" });
  assert.ok(await page.locator("text=Theo never touches external volumes").count() > 0);
  await page.getByRole("button", { name: /Delete data of this instance/ }).click();
  const dlg = page.locator("dialog[open]");
  await dlg.locator("text=shop-old-spike_pgdata").waitFor();
  assert.equal(await dlg.locator("img").count(), 0);
  const btn = dlg.getByRole("button", { name: "Delete data of this instance" });
  assert.ok(await btn.isDisabled());
  await dlg.getByLabel(/Type the instance name/).fill("nope");
  assert.ok(await btn.isDisabled());
  await shot(page, "22-purge-dialog");
  await dlg.getByLabel(/Type the instance name/).fill("shop-old-spike");
  await btn.click();
  await page.locator("dialog[open]").waitFor({ state: "detached" });
  await page.locator("text=not created yet").waitFor();
  await page.ctx.close();
});

test("purge is disabled while containers exist", async () => {
  const id = await wt("main");
  const page = await openApp(browser, mock, { route: `#/p/shop/w/${id}/volumes`, wait: "table.data" });
  assert.ok(await page.getByRole("button", { name: /Delete data of this instance/ }).isDisabled());
  assert.ok(await page.locator("text=Stop the instance first").count() > 0);
  await page.ctx.close();
});

test("secrets: never in the DOM or in list responses until Show; hidden again after 30 s", async () => {
  const id = await wt("main");
  const page = await openApp(browser, mock, { wait: "main h1" });
  const bodies = [];
  page.on("response", async (r) => { if (r.url().includes("/env") && !r.url().includes("reveal")) bodies.push(await r.text().catch(() => "")); });
  await page.clock.install();
  await page.evaluate((h) => { location.hash = h; }, `#/p/shop/w/${id}/variables`);
  await page.waitForSelector("table.data");
  assert.equal(await page.locator("body").innerHTML().then((h) => h.includes("s3cretpass")), false);
  assert.ok(bodies.length > 0 && bodies.every((b) => !b.includes("s3cretpass")), "list response is masked");
  await page.getByRole("button", { name: /Show for 30 s: DATABASE_URL/ }).click();
  await page.locator("text=s3cretpass").waitFor();
  await page.clock.fastForward(31000);
  await page.waitForFunction(() => !document.body.innerHTML.includes("s3cretpass"));
  await page.ctx.close();
});

test("variables: add, edit and delete", async () => {
  const id = await wt("main");
  const page = await openApp(browser, mock, { route: `#/p/shop/w/${id}/config`, wait: "table.data" });
  const vars = page.locator("section[aria-labelledby=cfg-vars]");
  await vars.getByRole("button", { name: "Add variable" }).click();
  await page.fill("#var-name", "1bad");
  await vars.getByRole("button", { name: "Save" }).click();
  await page.locator("text=do not start with a digit").waitFor();
  await page.fill("#var-name", "NEW_VAR");
  await page.fill("#var-value", "hello");
  await vars.getByRole("button", { name: "Save" }).click();
  await page.locator("th:has-text('NEW_VAR')").waitFor();
  await page.getByRole("button", { name: "Edit NEW_VAR" }).click();
  await page.fill("#var-value", "changed");
  await vars.getByRole("button", { name: "Save" }).click();
  await page.locator("td:has-text('changed')").waitFor();
  await page.getByRole("button", { name: "Delete NEW_VAR" }).click();
  await page.locator("dialog[open]").getByRole("button", { name: "Delete" }).click();
  await page.locator("th:has-text('NEW_VAR')").waitFor({ state: "detached" });
  await page.ctx.close();
});

test("never more than 2 SSE connections per tab, even with logs and operation output open", async () => {
  await mock.scenario({ slow: 3 });
  const id = await wt("feature-x");
  const page = await openApp(browser, mock, { route: `#/p/shop/w/${id}/logs`, wait: ".log-view" });
  const sse = watchStreams(page);
  await page.waitForTimeout(800);
  await page.getByRole("button", { name: "Start", exact: true }).first().click();
  await page.getByRole("button", { name: /operation running/ }).click();
  await page.getByRole("button", { name: "Show output" }).first().click();
  await page.waitForTimeout(1200);
  await page.locator("text=Another live view is using the connection").waitFor();
  for (const tab of ["summary", "config", "logs", "activity"]) { await page.evaluate((h) => { location.hash = h; }, `#/p/shop/w/${id}/${tab}`); await page.waitForTimeout(300); }
  assert.ok(sse.max() <= 2, `max simultaneous SSE connections in the tab was ${sse.max()}`);
  assert.ok(sse.max() >= 2, "the global channel and one focus stream were both used");
  await page.ctx.close();
});
