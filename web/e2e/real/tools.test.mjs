// The Tools page against the REAL `theo serve` and the real capture engine (Playwright + Chromium, no mock): two
// branches of a small static application, captured, compared, and the reports opened from the page. No Docker needed.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, symlinkSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { chromium, openApp, axeViolations, seriousOnly, shot, theoBinary, startServer, rmrf, webDir } from "./real-helpers.mjs";

const bin = theoBinary();
const skip = !bin ? "no theo binary with `serve` (set THEO_BIN)" : false;
const project = "capapp";
let browser, server, root, repo, other;
const adapter = { launchUrl: () => server.launch() };
const git = (cwd, ...args) => execFileSync("git", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();

const inventory = {
  version: 1, app: "Capapp", entrada: "panel",
  viewports: [{ id: "desktop", width: 1280, height: 720 }, { id: "movil", width: 390, height: 800, mobile: true }],
  pantallas: [
    { id: "panel", titulo: "Panel", ruta: "/", origen: ["dist/index.html"], prioridad: "alta", estados: [{ id: "base" }] },
    { id: "ajustes", titulo: "Ajustes", ruta: "/ajustes.html", origen: ["dist/ajustes.html"], prioridad: "media", estados: [{ id: "base" }] },
  ],
  flujos: [{ id: "abrir", pasos: ["panel#base", "ajustes#base"] }],
};

before(async () => {
  if (skip) return;
  root = mkdtempSync(path.join(tmpdir(), "uft-"));
  repo = path.join(root, project);
  mkdirSync(path.join(repo, "dist"), { recursive: true });
  mkdirSync(path.join(repo, ".theo", "capturas"), { recursive: true });
  writeFileSync(path.join(repo, "dist", "index.html"), '<!doctype html><body style="font-family:sans-serif"><h1>Panel</h1><p>Hola</p><a href="/ajustes.html">Ajustes</a></body>');
  writeFileSync(path.join(repo, "dist", "ajustes.html"), '<!doctype html><body style="font-family:sans-serif"><h1>Ajustes</h1><button>Guardar</button></body>');
  writeFileSync(path.join(repo, ".theo.json"), JSON.stringify({ run: { kind: "static", dir: "dist", base: "/" } }));
  writeFileSync(path.join(repo, ".theo", "capturas", "pantallas.json"), JSON.stringify(inventory, null, 2));
  git(repo, "init", "-q", "-b", "main");
  git(repo, "config", "user.email", "t@example.com");
  git(repo, "config", "user.name", "t");
  git(repo, "add", ".");
  git(repo, "commit", "-q", "-m", "first");
  // The engine lives in Theo's data folder; here it reuses the Playwright of web/ (and its browser) instead of downloading one.
  const home = path.join(root, "home");
  mkdirSync(path.join(home, "tools", "capture"), { recursive: true });
  symlinkSync(path.join(webDir, "node_modules"), path.join(home, "tools", "capture", "node_modules"), "junction");
  writeFileSync(path.join(home, "tools", "capture", "package.json"), '{"name":"theo-capture","private":true}');
  server = await startServer(bin, home);
  browser = await chromium.launch();
});
after(async () => {
  await browser?.close();
  server?.stop();
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

test("setup: register the project and add a second branch with a changed screen", { skip }, async () => {
  const call = await apiSession();
  assert.equal((await call("POST", "/projects", { path: repo, name: project })).status, 201);
  const r = await call("POST", `/projects/${project}/worktrees`, { branch: "despues", new: true, from: "main" });
  assert.equal(r.status, 201, JSON.stringify(r.body));
  other = r.body.path;
  writeFileSync(path.join(other, "dist", "index.html"), '<!doctype html><body style="font-family:serif"><h1>Panel</h1><p>Hola, ahora con mas texto en esta pantalla</p><a href="/ajustes.html">Ajustes</a></body>');
  git(other, "commit", "-qam", "change the panel");
});

test("the Tools page compares two real branches and opens the reports it made", { skip }, async () => {
  const page = await openApp(browser, adapter, { route: `#/tools/${project}`, wait: "[data-testid=compare-form]" });
  assert.deepEqual(seriousOnly(await axeViolations(page)), []);
  assert.match(await page.locator("[data-testid=engine-ready]").innerText(), /Engine ready/);
  assert.match(await page.locator("[data-testid=inventory-summary]").innerText(), /2 viewports · 2 screens · 2 states · 1 flow/);
  assert.equal(await page.locator("[data-testid=report-row]").count(), 0);
  await shot(page, "v09-real-tools-empty");
  await page.selectOption("#tl-a", "main");
  await page.selectOption("#tl-b", "despues");
  await page.click("[data-testid=start-compare]");
  await page.waitForSelector("[data-testid=capture-running]");
  await shot(page, "v09-real-tools-running");
  await page.waitForSelector("[data-testid=capture-close]", { timeout: 180000 });
  const done = await page.locator("[data-testid=capture-running]").innerText();
  assert.match(done, /Finished\. main vs despues: 1 changed, 1 same, 0 new, 0 removed/, done);
  await shot(page, "v09-real-tools-finished");
  await page.click("[data-testid=capture-close]");
  await page.waitForSelector("[data-testid=report-row]");
  const row = page.locator("[data-testid=report-row]").first();
  assert.match(await row.innerText(), /main → despues\s+1\s+0 \/ 0\s+0/);
  await shot(page, "v09-real-tools-reports");

  // the buttons open the reports the command line would have made, served with a CSP that allows no connection
  for (const [name, type] of [["Compositions", /text\/html/], ["Corporate", /text\/html/], ["resumen.md", /text\/markdown/]]) {
    const href = await row.getByRole("link", { name: new RegExp(name) }).getAttribute("href");
    const res = await page.request.get(server.origin + href);
    assert.equal(res.status(), 200, `${name} ${href}`);
    assert.match(res.headers()["content-type"], type);
    assert.match(res.headers()["content-security-policy"], /connect-src 'none'/);
  }
  // clicking "Compositions" opens the gallery in a new tab, the way a person gets there
  const [rep] = await Promise.all([page.ctx.waitForEvent("page"), row.getByRole("link", { name: /Compositions/ }).click()]);
  await rep.setViewportSize({ width: 1300, height: 900 });
  await rep.waitForSelector("h1", { timeout: 15000 });
  assert.match(await rep.locator("h1").innerText(), /Before and after: main vs despues/);
  await rep.waitForFunction(() => [...document.images].every((i) => i.complete && i.naturalWidth > 0));
  await rep.screenshot({ path: path.join(path.dirname(new URL(import.meta.url).pathname), "..", "screenshots", "v09-real-report-compositions.png") });
  const md = await (await page.request.get(server.origin + (await row.getByRole("link", { name: /resumen\.md/ }).getAttribute("href")))).text();
  assert.match(md, /1 changed · 1 same/);
  assert.deepEqual(page.problems.filter((p) => !/Failed to load resource.*409/.test(p)), []);
  await page.ctx.close();
  // the report files the page lists are on disk in the project, where the command line puts them
  assert.match(readFileSync(path.join(repo, ".theo", "capturas", ".gitignore"), "utf8"), /conjuntos/);
});

test("report files outside the report folder are not served", { skip }, async () => {
  const call = await apiSession();
  for (const p of ["../../.gitignore", "..%2f..%2f.gitignore", "x/../../../pantallas.json"]) {
    const r = await call("GET", `/projects/${project}/capture/reports/${encodeURIComponent("2026")}/${p}`);
    assert.notEqual(r.status, 200, p);
  }
});
