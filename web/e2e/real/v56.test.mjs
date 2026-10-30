// V5/V6 screens against the REAL `theo serve` and real Docker (prefix `uf5`): shared services (declare, start, stop,
// purge with the typed text, refusals with linked instances), health (a worktree deleted by hand, forgetting only Theo's
// records), settings and diagnostics, history headers.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import net from "node:net";
import { mkdtempSync, rmSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import {
  chromium, openApp, axeViolations, seriousOnly, cspViolations, shot,
  theoBinary, dockerAvailable, makeRepo, startServer, dockerCleanup, docker, rmrf,
} from "./real-helpers.mjs";
import crypto from "node:crypto";

const bin = theoBinary();
const skip = !bin ? "no theo binary with `serve` (set THEO_BIN)" : !dockerAvailable() ? "no Docker daemon running Linux containers" : false;
const prefix = "uf5" + crypto.randomBytes(3).toString("hex");
const project = prefix + "p";
let browser, server, root, repo, port;
const adapter = { launchUrl: () => server.launch() };

async function freePort() {
  for (let i = 0; i < 100; i++) {
    const p = 24000 + Math.floor(Math.random() * 8000);
    const ok = await new Promise((res) => { const s = net.createServer(); s.once("error", () => res(false)); s.listen(p, "0.0.0.0", () => s.close(() => res(true))); });
    if (ok) return p;
  }
  throw new Error("no free port");
}

before(async () => {
  if (skip) return;
  root = mkdtempSync(path.join(tmpdir(), prefix + "-"));
  port = await freePort();
  repo = makeRepo(root, prefix + "app", port).dir;
  server = await startServer(bin, path.join(root, "home"));
  browser = await chromium.launch();
});
after(async () => {
  await browser?.close();
  server?.stop();
  dockerCleanup(prefix);
  if (root) rmrf(root);
});

const apiSession = async () => {
  const url = new URL(await server.launch());
  const r = await fetch(server.origin + "/api/v1/session", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ nonce: url.hash.slice(3) }) });
  const cookie = r.headers.get("set-cookie").split(";")[0];
  const { csrf_token } = await r.json();
  return async (method, p, body) => {
    const res = await fetch(server.origin + "/api/v1" + p, { method, headers: { Cookie: cookie, "X-Theo-Csrf": csrf_token, "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
    return { status: res.status, body: await res.json().catch(() => null), headers: res.headers };
  };
};
const waitOp = async (call, id) => {
  for (let i = 0; i < 120; i++) {
    const r = await call("GET", "/operations/" + id);
    if (!["queued", "running", "cancelling"].includes(r.body.state)) return r.body;
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error("operation did not finish");
};

test("setup: register the project", { skip }, async () => {
  const call = await apiSession();
  const r = await call("POST", "/projects", { path: repo, name: project });
  assert.equal(r.status, 201, JSON.stringify(r.body));
});

test("shared services: empty state, declare, start and stop as operations, in the UI against real Docker", { skip }, async () => {
  const page = await openApp(browser, adapter, { route: `#/p/${project}/shared`, wait: "#sh-empty" });
  assert.deepEqual(seriousOnly(await axeViolations(page)), []);
  await shot(page, "60-real-shared-empty");
  await page.fill("#sh-add-name", "web");
  await page.getByRole("button", { name: "Share this service" }).click();
  await page.locator("#sh-provider").waitFor();
  assert.match(await page.locator("section[aria-labelledby=sh-provider]").textContent(), /Not created/);
  await page.getByRole("button", { name: "Start shared services" }).click();
  await page.locator("section[aria-labelledby=sh-provider] tbody", { hasText: "Running" }).waitFor({ timeout: 120000 });
  assert.match(docker("ps", "--filter", `name=${project}-shared`, "--format", "{{.Names}}"), new RegExp(project + "-shared"));
  await shot(page, "61-real-shared-running");
  // No consumers: stopping needs no confirmation.
  await page.getByRole("button", { name: "Stop shared services" }).click();
  await page.locator("section[aria-labelledby=sh-provider] tbody", { hasText: /Stopped|Not created/ }).waitFor({ timeout: 60000 });
  assert.equal(docker("ps", "--filter", `name=${project}-shared`, "--format", "{{.Names}}"), "");
  assert.deepEqual(page.problems.filter((p) => !/status of 404/.test(p)), []);
  await page.ctx.close();
});

test("shared services: a linked instance makes purge refuse; unlinking and the typed text delete the data", { skip }, async () => {
  const call = await apiSession();
  const ov = (await call("GET", `/projects/${project}/overview`)).body;
  const main = ov.worktrees.find((w) => w.main);
  const page = await openApp(browser, adapter, { route: `#/p/${project}/shared`, wait: "#sh-provider" });
  await page.selectOption("#sh-link-wt", main.id);
  await page.getByLabel("web", { exact: true }).check();
  await page.getByRole("button", { name: "Use shared services" }).click();
  await page.locator("section[aria-labelledby=sh-consumers] tbody tr").first().waitFor();
  await page.getByRole("button", { name: "Delete data…" }).click();
  const dlg = page.locator("dialog[open]");
  await dlg.waitFor();
  assert.match(await dlg.textContent(), /These shared services are in use/);
  await shot(page, "62-real-shared-purge-refused");
  await dlg.getByRole("button", { name: "Close" }).click();
  await page.getByRole("button", { name: new RegExp("Stop using web in") }).click();
  await page.getByText("No instance uses the shared services yet.").waitFor();
  await page.getByRole("button", { name: "Delete data…" }).click();
  await dlg.waitFor();
  const expected = `${project}-shared`;
  await dlg.locator("input").fill("wrong");
  assert.equal(await dlg.getByRole("button", { name: "Delete data" }).isDisabled(), true);
  const bad = await call("POST", `/projects/${project}/shared/purge`, { confirm: "wrong" });
  assert.equal(bad.status, 409);
  assert.equal(bad.body.error.code, "confirmation_required");
  await dlg.locator("input").fill(expected);
  await dlg.getByRole("button", { name: "Delete data" }).click();
  await dlg.waitFor({ state: "detached" });
  await page.ctx.close();
});

test("health: a worktree deleted by hand is reported; forgetting touches only Theo's own records", { skip }, async () => {
  const call = await apiSession();
  const created = await call("POST", `/projects/${project}/worktrees`, { branch: "gone-branch", new: true });
  assert.equal(created.status, 201, JSON.stringify(created.body));
  // Theo records something for it (instance settings), then the folder vanishes behind its back.
  const set = await call("PATCH", `/projects/${project}/worktrees/${created.body.id}/settings`, { neutralize_container_names: true });
  assert.equal(set.status, 200, JSON.stringify(set.body));
  rmSync(created.body.path, { recursive: true, force: true });

  const page = await openApp(browser, adapter, { route: "#/health", wait: "h1" });
  await page.getByRole("heading", { name: /Worktree missing/ }).waitFor({ timeout: 20000 });
  const staleHeading = page.getByRole("heading", { name: /Stale settings/ });
  await staleHeading.waitFor();
  assert.match(await page.getByTestId("nav-health").textContent(), /Health\s*[1-9]/);
  assert.deepEqual(seriousOnly(await axeViolations(page)), []);
  await shot(page, "63-real-health");
  const before = (await call("GET", "/reconcile")).body.findings;
  const missing = before.find((f) => f.kind === "worktree missing");
  assert.ok(missing && !missing.forgettable, "a worktree missing from disk is reported, not forgettable");
  const section = page.locator("section", { has: staleHeading });
  await section.locator("input[type=checkbox]").first().check();
  await page.getByRole("button", { name: /Forget Theo's records/ }).click();
  const dlg = page.locator("dialog[open]");
  await dlg.getByRole("button", { name: "Forget records" }).click();
  await dlg.waitFor({ state: "detached" });
  await staleHeading.waitFor({ state: "detached" });
  const after = (await call("GET", "/reconcile")).body.findings;
  assert.ok(!after.some((f) => f.kind === "stale settings"));
  assert.ok(after.some((f) => f.kind === "worktree missing"), "the worktree finding is still there: nothing else was touched");
  assert.ok(existsSync(repo), "the main worktree is intact");
  // Applying a non-forgettable id is skipped by the server.
  const r = await call("POST", "/reconcile/apply", { ids: [missing.id] });
  assert.equal(r.status, 200);
  assert.deepEqual(r.body.skipped, [missing.id]);
  await page.ctx.close();
});

test("health with Docker unreachable: docker_error is shown (simulated by an unreachable DOCKER_HOST)", { skip: skip || "needs a server started with a broken DOCKER_HOST; covered against the mock and by the backend tests" }, async () => {});

test("settings: diagnostics from the real doctor, Compose minimum, report text", { skip }, async () => {
  const page = await openApp(browser, adapter, { route: "#/settings", wait: "h1" });
  await page.locator("table tbody tr", { hasText: /compose/i }).first().waitFor();
  const text = await page.locator("main").textContent();
  assert.match(text, /Everything Theo needs works|does not work/);
  assert.match(text, /Theo needs 2\.24\.4 or later/);
  assert.deepEqual(seriousOnly(await axeViolations(page)), []);
  await shot(page, "64-real-settings");
  await page.getByRole("button", { name: "Copy information for a report" }).click();
  const clip = await page.evaluate(() => navigator.clipboard.readText().catch(() => null));
  const rep = clip ?? (await page.locator("#st-report-text").inputValue());
  assert.match(rep, /^Theo /);
  assert.match(rep, /Checks:/);
  assert.deepEqual(await cspViolations(page), []);
  await page.ctx.close();
});

test("history: the real server pages with the Theo-History-Next header and the tab shows the events", { skip }, async () => {
  const call = await apiSession();
  const ov = (await call("GET", `/projects/${project}/overview`)).body;
  const main = ov.worktrees.find((w) => w.main);
  for (const kind of ["up", "down"]) {
    const op = await call("POST", `/projects/${project}/worktrees/${main.id}/${kind}`, {});
    assert.equal(op.status, 202, JSON.stringify(op.body));
    const done = await waitOp(call, op.body.id);
    assert.equal(done.state, "succeeded", JSON.stringify(done));
  }
  const p1 = await call("GET", `/projects/${project}/worktrees/${main.id}/history?limit=1`);
  assert.equal(p1.body.length, 1);
  assert.ok(p1.headers.get("theo-history-next"), "there is an older page");
  const page = await openApp(browser, adapter, { route: `#/p/${project}/w/${main.id}/history`, wait: "[role=tabpanel] table" });
  assert.ok((await page.locator("[role=tabpanel] tbody tr").count()) >= 2);
  assert.equal(await page.getByRole("button", { name: "Load older events" }).count(), 0, "everything fits in one page");
  await shot(page, "65-real-history");
  await page.ctx.close();
});
