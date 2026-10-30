// Shared helpers for the Playwright tests. Playwright itself comes preinstalled in the environment
// (require('playwright') from any directory); it is not a dependency of this package.
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { readFileSync, mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const require = createRequire(import.meta.url);
export const { chromium } = require("playwright");

const here = path.dirname(fileURLToPath(import.meta.url));
export const webDir = path.resolve(here, "..");
export const shotsDir = path.join(here, "screenshots");
mkdirSync(shotsDir, { recursive: true });

const axeSource = readFileSync(path.join(webDir, "node_modules", "axe-core", "axe.min.js"), "utf8");

/** Starts the mock API server on a random port. */
export async function startMock() {
  const child = spawn(process.execPath, [path.join(webDir, "mock", "server.mjs")], { stdio: ["ignore", "pipe", "inherit"] });
  const info = await new Promise((resolve, reject) => {
    let buf = "";
    child.stdout.on("data", (d) => {
      buf += d;
      const i = buf.indexOf("\n");
      if (i >= 0) { try { resolve(JSON.parse(buf.slice(0, i))); } catch (e) { reject(e); } }
    });
    child.on("exit", (c) => reject(new Error("mock exited " + c)));
  });
  const origin = `http://127.0.0.1:${info.port}`;
  const hook = async (p, body) => {
    const r = await fetch(origin + "/__mock/" + p, { method: body ? "POST" : "GET", headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
    return r.json();
  };
  return {
    origin, port: info.port, hook,
    launchUrl: async () => (await hook("launch")).url,
    reset: () => hook("reset"),
    scenario: (s) => hook("scenario", s),
    stats: () => hook("stats"),
    stop: () => { child.kill(); },
  };
}

/**
 * Opens a page through the real launch exchange (nonce in the fragment). Console messages and CSP violations
 * are collected in page.problems.
 */
export async function openApp(browser, mock, { route = "", viewport = { width: 1100, height: 760 }, colorScheme, forcedColors, reducedMotion, deviceScaleFactor = 1, wait = "main h1, .welcome h1" } = {}) {
  const context = await browser.newContext({ viewport, colorScheme, forcedColors, reducedMotion, deviceScaleFactor });
  const page = await context.newPage();
  page.problems = [];
  page.on("console", (m) => { if (["error", "warning"].includes(m.type())) page.problems.push(`${m.type()}: ${m.text()}`); });
  page.on("pageerror", (e) => page.problems.push("pageerror: " + e.message));
  await page.addInitScript(() => {
    document.addEventListener("securitypolicyviolation", (e) => { (window.__csp ??= []).push(`${e.violatedDirective} ${e.blockedURI}`); });
  });
  const url = await mock.launchUrl();
  await page.goto(url);
  await page.waitForSelector("main h1, .welcome h1", { timeout: 10000 });
  if (route) {
    await page.evaluate((r) => { location.hash = r; }, route);
  }
  await page.waitForSelector(wait, { timeout: 10000 });
  page.ctx = context;
  return page;
}

export const cspViolations = (page) => page.evaluate(() => window.__csp ?? []);

/** Runs axe-core and returns the critical and serious violations (compact). */
export async function axeViolations(page, { include } = {}) {
  await page.evaluate(axeSource);
  const result = await page.evaluate(async (inc) => {
    const ctx = inc ? { include: [[inc]] } : document;
    // eslint-disable-next-line no-undef
    const r = await axe.run(ctx, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa", "best-practice"] } });
    return r.violations.map((v) => ({ id: v.id, impact: v.impact, help: v.help, nodes: v.nodes.slice(0, 3).map((n) => n.target.join(" ") + " :: " + (n.failureSummary || "").split("\n").slice(0, 3).join(" | ")) }));
  }, include ?? null);
  return result;
}
export const seriousOnly = (v) => v.filter((x) => x.impact === "critical" || x.impact === "serious");

export async function shot(page, name) {
  await page.screenshot({ path: path.join(shotsDir, name + ".png") });
}

export async function waitIdle(page, ms = 300) {
  // No "networkidle": the page keeps an SSE connection open on purpose.
  await page.waitForTimeout(ms);
}
