// The interface against the REAL server: `theo serve --print-launch-url`, a temporary THEO_HOME, a real Git
// repository and real Docker (images built FROM scratch, names prefixed `uf`). Tests run in order and share one story:
// add a project, start it, read its logs, create a second instance with a port clash, remove, purge.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import net from "node:net";
import http from "node:http";
import { execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import {
  chromium, openApp, axeViolations, seriousOnly, cspViolations, shot,
  theoBinary, dockerAvailable, newPrefix, makeRepo, startServer, dockerCleanup, docker, rmrf,
} from "./real-helpers.mjs";

const bin = theoBinary();
const ready = !!bin && dockerAvailable();
const skip = !bin ? "no theo binary with `serve` (set THEO_BIN or build a tree that has internal/api)" : !dockerAvailable() ? "no Docker daemon running Linux containers" : false;

let browser, server, root, repo, prefix, port, project;
const adapter = { launchUrl: () => server.launch() };

async function freePort() {
  for (let i = 0; i < 100; i++) {
    const p = 24000 + Math.floor(Math.random() * 8000);
    const ok = await new Promise((res) => { const s = net.createServer(); s.once("error", () => res(false)); s.listen(p, "0.0.0.0", () => s.close(() => res(true))); });
    const ok2 = await new Promise((res) => { const s = net.createServer(); s.once("error", () => res(false)); s.listen(p + 1, "0.0.0.0", () => s.close(() => res(true))); });
    if (ok && ok2) return p;
  }
  throw new Error("no free port");
}

before(async () => {
  if (!ready) return;
  prefix = newPrefix();
  project = prefix + "p";
  root = mkdtempSync(path.join(tmpdir(), prefix + "-"));
  port = await freePort();
  repo = makeRepo(root, prefix + "app", port).dir;
  server = await startServer(bin, path.join(root, "home"));
  browser = await chromium.launch();
});
after(async () => {
  await browser?.close();
  server?.stop();
  if (prefix) dockerCleanup(prefix);
  if (root) rmrf(root);
});

const apiSession = async () => {
  const url = new URL(await server.launch());
  const r = await fetch(server.origin + "/api/v1/session", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ nonce: url.hash.slice(3) }) });
  const cookie = r.headers.get("set-cookie").split(";")[0];
  const { csrf_token } = await r.json();
  return async (method, p, body) => {
    const res = await fetch(server.origin + "/api/v1" + p, { method, headers: { Cookie: cookie, "X-Theo-Csrf": csrf_token, "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
    return { status: res.status, body: await res.json().catch(() => null) };
  };
};
const row = (page, name) => page.locator("tbody tr", { has: page.getByRole("link", { name, exact: true }) });

test("welcome screen on an empty registry, then add a project with the folder picker", { skip }, async () => {
  const page = await openApp(browser, adapter);
  await page.locator(".welcome img.mascot").waitFor();
  assert.deepEqual(seriousOnly(await axeViolations(page)), []);
  await shot(page, "30-real-welcome");
  await page.getByRole("link", { name: "Add a project" }).click();
  await page.waitForSelector("#ap-path");
  // Folder picker over the real /fs/browse
  await page.fill("#ap-path", root);
  await page.getByRole("button", { name: /Browse/ }).click();
  await page.locator("dialog[open]").getByRole("button", { name: `Open folder ${prefix}app` }).waitFor();
  assert.ok(await page.locator("dialog[open] .tag:has-text('Git repository')").count() >= 1);
  await page.locator("dialog[open]").getByRole("button", { name: `Open folder ${prefix}app` }).click();
  await page.locator("dialog[open] .mono", { hasText: `${prefix}app` }).waitFor();
  await page.getByRole("button", { name: "Use this folder" }).click();
  assert.equal(await page.inputValue("#ap-path"), repo);
  await page.fill("#ap-name", project);
  await page.getByRole("button", { name: "Add project" }).click();
  await page.waitForSelector("table.data");
  await page.getByRole("link", { name: "main", exact: true }).waitFor();
  assert.ok(await page.locator(".badge:has-text('No containers')").count() === 1);
  assert.deepEqual(await cspViolations(page), []);
  assert.deepEqual(seriousOnly(await axeViolations(page)), []);
  await shot(page, "31-real-panel-empty");
  assert.deepEqual(page.problems, []);
  await page.ctx.close();
});

test("start from the interface: containers exist, state Running, link works, logs arrive in under 3 s", { skip }, async () => {
  const page = await openApp(browser, adapter, { route: `#/p/${project}`, wait: "table.data" });
  await row(page, "main").getByRole("button", { name: /Start main/ }).click();
  await row(page, "main").locator(".badge:has-text('Starting')").waitFor();
  await row(page, "main").locator(".badge:has-text('Running')").waitFor({ timeout: 120000 });
  const names = docker("ps", "--filter", `name=${prefix}`, "--format", "{{.Names}}");
  assert.ok(names.includes("web"), "container exists: " + names);
  const link = row(page, "main").locator("a[rel='noopener noreferrer']").first();
  const href = await link.getAttribute("href");
  assert.match(href, new RegExp(`:${port}$`));
  const body = await (await fetch(`http://127.0.0.1:${port}/`)).text();
  assert.equal(body.trim(), "ok");
  await shot(page, "32-real-panel-running");
  // Detail -> Logs
  await row(page, "main").getByRole("link", { name: "main", exact: true }).click();
  await page.waitForSelector("[role=tablist]");
  await page.getByRole("tab", { name: "Logs" }).click();
  const t0 = Date.now();
  await page.locator(".log-line", { hasText: "server ready" }).first().waitFor({ timeout: 3000 });
  await page.locator(".log-line", { hasText: "tick" }).first().waitFor({ timeout: 5000 });
  assert.ok(Date.now() - t0 < 8000);
  await shot(page, "33-real-logs");
  assert.deepEqual(seriousOnly(await axeViolations(page)), []);
  // Leaving the view ends the docker compose logs process (the backend tests this too; here we check it from the UI side).
  await page.getByRole("tab", { name: "Summary" }).click();
  await page.waitForTimeout(1500);
  const logsProcs = execFileSync("sh", ["-c", `pgrep -fa "compose.*${prefix}.*logs" || true`], { encoding: "utf8" }).trim();
  assert.equal(logsProcs, "", "no orphan `docker compose logs` process: " + logsProcs);
  assert.deepEqual(page.problems, []);
  await page.ctx.close();
});

test("History records the start; Ports & URLs and Services show real data", { skip }, async () => {
  const page = await openApp(browser, adapter, { route: `#/p/${project}`, wait: "table.data" });
  await row(page, "main").getByRole("link", { name: "main", exact: true }).click();
  await page.getByRole("tab", { name: "History" }).click();
  await page.locator("td:has-text('Succeeded')").first().waitFor();
  assert.ok(await page.locator("th[scope=row]:has-text('up')").count() >= 1);
  await page.getByRole("tab", { name: "Ports & URLs" }).click();
  await page.locator("td:has-text('In use by this instance')").waitFor();
  await page.getByRole("tab", { name: "Services" }).click();
  await page.locator("th[scope=row]:has-text('web')").waitFor();
  assert.ok(await page.locator(".badge:has-text('running')").count() >= 1);
  await shot(page, "34-real-services");
  assert.deepEqual(seriousOnly(await axeViolations(page)), []);
  await page.ctx.close();
});

test("UC-02: a new branch with the same fixed port: the wizard shows the clash, offers automatic ports, both instances run with distinct URLs", { skip }, async () => {
  const page = await openApp(browser, adapter, { route: `#/p/${project}/new`, wait: ".stepper" });
  await page.fill("#wz-new", "feat-two");
  await page.getByRole("button", { name: "Next" }).click();
  await page.getByRole("button", { name: "Next" }).click();
  await page.waitForSelector("text=would clash", { timeout: 20000 });
  assert.ok(await page.locator(`td:has-text("${port}")`).count() >= 1);
  assert.ok(await page.getByRole("radio", { name: /Assign free ports automatically/ }).isChecked());
  await shot(page, "35-real-wizard-conflict");
  assert.deepEqual(seriousOnly(await axeViolations(page)), []);
  await page.getByRole("button", { name: "Next" }).click();
  await page.getByRole("button", { name: "Create and start" }).click();
  await page.waitForURL(/\/logs/, { timeout: 30000 });
  const call = await apiSession();
  const deadline = Date.now() + 180000;
  let ov;
  for (;;) {
    ov = (await call("GET", `/projects/${project}/overview`)).body;
    if (ov.worktrees.length === 2 && ov.worktrees.every((w) => w.state === "running")) break;
    assert.ok(Date.now() < deadline, "both instances did not reach running: " + JSON.stringify(ov.worktrees.map((w) => w.state)));
    await new Promise((r) => setTimeout(r, 1000));
  }
  const urls = ov.worktrees.flatMap((w) => w.ports.filter((p) => p.status === "running").map((p) => p.url));
  assert.equal(new Set(urls).size, 2, "distinct URLs: " + urls);
  for (const u of urls) assert.equal((await (await fetch(u.replace("localhost", "127.0.0.1"))).text()).trim(), "ok");
  await page.goto(server.origin + `/#/p/${project}`).catch(() => undefined);
  await page.ctx.close();
});

test("remove: a worktree with containers is refused; after Stop, one with a changed file needs the typed name", { skip }, async () => {
  const call = await apiSession();
  const ov = (await call("GET", `/projects/${project}/overview`)).body;
  const second = ov.worktrees.find((w) => w.branch === "feat-two");
  // a) with containers
  const page = await openApp(browser, adapter, { route: `#/p/${project}`, wait: "table.data" });
  await row(page, "feat-two").locator("summary").click();
  await row(page, "feat-two").getByRole("button", { name: /Remove worktree/ }).click();
  await page.getByRole("button", { name: "Remove worktree" }).click();
  await page.locator("dialog[open] >> text=This instance still has containers").waitFor();
  await shot(page, "36-real-remove-has-containers");
  await page.locator("dialog[open]").getByRole("button", { name: "Stop the instance" }).click();
  await row(page, "feat-two").locator(".badge:has-text('No containers')").waitFor({ timeout: 60000 });
  // b) uncommitted file
  writeFileSync(path.join(second.path, "notes.txt"), "work in progress\n");
  await row(page, "feat-two").locator("summary").click();
  await row(page, "feat-two").getByRole("button", { name: /Remove worktree/ }).click();
  await page.getByRole("button", { name: "Remove worktree" }).click();
  const dlg = page.locator("dialog[open]");
  await dlg.locator("text=notes.txt").waitFor();
  assert.equal(await dlg.locator("img").count(), 0);
  const expected = path.basename(second.path);
  const btn = dlg.getByRole("button", { name: "Remove worktree" });
  await dlg.getByLabel(/Type the directory name/).fill(expected + "x");
  assert.ok(await btn.isDisabled());
  await shot(page, "37-real-remove-typed");
  // The server revalidates: calling the API directly with a wrong text refuses and removes nothing.
  const wrong = await call("POST", `/projects/${project}/worktrees/${second.id}/remove`, { confirm: "nope" });
  assert.equal(wrong.status, 409);
  assert.ok(existsSync(second.path));
  await dlg.getByLabel(/Type the directory name/).fill(expected);
  await btn.click();
  await page.locator("dialog[open]").waitFor({ state: "detached" });
  await page.locator("tbody tr", { hasText: "feat-two" }).waitFor({ state: "detached" });
  assert.ok(!existsSync(second.path));
  assert.equal(execFileSync("git", ["-C", repo, "status", "--porcelain"], { encoding: "utf8" }).trim(), "", "tracked files of the project are untouched");
  await page.ctx.close();
});

test("variables: a secret is masked in the DOM and in the list response until Show", { skip }, async () => {
  const call = await apiSession();
  const ov = (await call("GET", `/projects/${project}/overview`)).body;
  const main = ov.worktrees.find((w) => w.main);
  const page = await openApp(browser, adapter, { route: `#/p/${project}/w/${main.id}/variables`, wait: "table.data, .empty" });
  const bodies = [];
  page.on("response", async (r) => { if (/\/env($|\?)/.test(r.url())) bodies.push(await r.text().catch(() => "")); });
  await page.getByRole("button", { name: "Add variable" }).click();
  await page.fill("#var-name", "UF_SECRET");
  await page.fill("#var-value", "hunter2-uf-secret");
  await page.check("#var-secret");
  await page.getByRole("button", { name: "Save" }).click();
  await page.locator("th:has-text('UF_SECRET')").waitFor();
  assert.equal((await page.locator("body").innerHTML()).includes("hunter2-uf-secret"), false);
  assert.ok(bodies.length > 0 && bodies.every((b) => !b.includes("hunter2-uf-secret")));
  await page.getByRole("button", { name: /Show for 30 s: UF_SECRET/ }).click();
  await page.locator("text=hunter2-uf-secret").waitFor();
  await shot(page, "38-real-variables");
  assert.deepEqual(seriousOnly(await axeViolations(page)), []);
  await page.ctx.close();
});

test("purge: after Stop, data is deleted only with the exact Compose project name; the volume disappears from Docker", { skip }, async () => {
  const call = await apiSession();
  const ov = (await call("GET", `/projects/${project}/overview`)).body;
  const main = ov.worktrees.find((w) => w.main);
  const page = await openApp(browser, adapter, { route: `#/p/${project}`, wait: "table.data" });
  await row(page, "main").getByRole("button", { name: /Stop main/ }).click();
  await row(page, "main").locator(".badge:has-text('No containers')").waitFor({ timeout: 60000 });
  const vols = () => docker("volume", "ls", "-q", "--filter", `name=${prefix}`).split("\n").filter(Boolean);
  assert.ok(vols().length >= 1, "volume kept after down");
  await page.goto(server.origin + `/#/p/${project}/w/${main.id}/volumes`);
  await page.waitForSelector("table.data");
  await page.locator(".badge:has-text('Kept after down')").waitFor();
  await page.getByRole("button", { name: /Delete data of this instance/ }).click();
  const dlg = page.locator("dialog[open]");
  await dlg.locator(".affected li").first().waitFor();
  assert.equal(await dlg.locator("img").count(), 0);
  await shot(page, "39-real-purge");
  const input = dlg.getByLabel(/Type the instance name/);
  const btn = dlg.getByRole("button", { name: "Delete data of this instance" });
  await input.fill("wrong");
  assert.ok(await btn.isDisabled());
  assert.ok(vols().length >= 1);
  const expected = (await call("POST", `/projects/${project}/worktrees/${main.id}/volumes/purge`, {})).body.error.confirm.expected;
  await input.fill(expected);
  await btn.click();
  await page.locator("dialog[open]").waitFor({ state: "detached" });
  assert.equal(vols().length, 0, "volume deleted: " + vols());
  await page.ctx.close();
});

test("security against the real server: Host, Origin, cookie, CSRF, single-use nonce, listening only on loopback", { skip }, async () => {
  const raw = (opts) => new Promise((resolve, reject) => {
    const req = http.request({ host: "127.0.0.1", port: server.port, ...opts }, (res) => { let b = ""; res.on("data", (d) => (b += d)); res.on("end", () => resolve({ status: res.statusCode, headers: res.headers, body: b })); });
    req.on("error", reject);
    if (opts.body) req.write(opts.body);
    req.end();
  });
  const launch = await server.launch();
  const nonce = launch.split("#n=")[1];
  const host = `127.0.0.1:${server.port}`;
  const ex = await raw({ method: "POST", path: "/api/v1/session", headers: { Host: host, "Content-Type": "application/json" }, body: JSON.stringify({ nonce }) });
  assert.equal(ex.status, 200);
  const cookie = ex.headers["set-cookie"][0].split(";")[0];
  const csrf = JSON.parse(ex.body).csrf_token;
  const good = { Host: host, Cookie: cookie };
  assert.equal((await raw({ path: "/api/v1/meta", headers: { ...good, Host: "evil.example" } })).status, 403);
  assert.equal((await raw({ path: "/api/v1/meta", headers: { ...good, Origin: "http://evil.example" } })).status, 403);
  assert.equal((await raw({ path: "/api/v1/meta", headers: { Host: host } })).status, 401);
  assert.equal((await raw({ method: "POST", path: "/api/v1/projects", headers: { ...good, "Content-Type": "application/json" }, body: "{}" })).status, 403);
  assert.equal((await raw({ path: "/api/v1/meta", headers: good })).status, 200);
  assert.equal((await raw({ method: "POST", path: "/api/v1/session", headers: { Host: host, "Content-Type": "application/json" }, body: JSON.stringify({ nonce }) })).status, 401, "nonce is single use");
  void csrf;
  const index = await raw({ path: "/", headers: { Host: host } });
  assert.match(index.headers["content-security-policy"], /^default-src 'none'; script-src 'self'; style-src 'self'/);
  // Nothing listens outside 127.0.0.1 for this process.
  const ss = execFileSync("sh", ["-c", `ss -ltnp 2>/dev/null | grep "pid=${server.pid}," || true`], { encoding: "utf8" }).trim();
  assert.ok(ss.length > 0, "found the listening socket");
  for (const line of ss.split("\n")) assert.match(line, /127\.0\.0\.1:\d+/, line);
});

test("a page on another origin cannot read the real API", { skip }, async () => {
  const evil = http.createServer((req, res) => {
    res.writeHead(200, { "Content-Type": "text/html" });
    res.end(`<!doctype html><title>evil</title><script>
      window.result = "pending";
      fetch("http://127.0.0.1:${server.port}/api/v1/projects", { credentials: "include" })
        .then(async (r) => { window.result = "status " + r.status; })
        .catch((e) => { window.result = "blocked"; });
    </script>`);
  });
  await new Promise((r) => evil.listen(0, "127.0.0.1", r));
  try {
    const context = await browser.newContext();
    const victim = await context.newPage();
    await victim.goto(await server.launch());
    await victim.waitForSelector("main h1, .welcome h1");
    const page = await context.newPage();
    await page.goto(`http://127.0.0.1:${evil.address().port}/`);
    await page.waitForFunction(() => window.result !== "pending");
    assert.ok(!/status 200/.test(await page.evaluate(() => window.result)));
    await context.close();
  } finally { evil.close(); }
});
