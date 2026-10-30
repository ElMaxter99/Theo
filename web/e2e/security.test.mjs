// Client-side security behaviour: launch nonce handling, no secret in any URL, no external request, CSP,
// cross-origin pages cannot use the API. (Server-side negative tests live in the Go tests of internal/api and,
// against the real server, in e2e/real/.)
import { test, before, after, beforeEach } from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import { chromium, startMock, openApp, cspViolations } from "./helpers.mjs";

let browser, mock;
before(async () => { mock = await startMock(); browser = await chromium.launch(); });
after(async () => { await browser?.close(); mock?.stop(); });
beforeEach(async () => { await mock.reset(); });

function rawRequest(port, { method = "GET", path = "/api/v1/meta", headers = {}, body } = {}) {
  return new Promise((resolve, reject) => {
    const req = http.request({ host: "127.0.0.1", port, method, path, headers }, (res) => {
      let b = ""; res.on("data", (d) => (b += d)); res.on("end", () => resolve({ status: res.statusCode, headers: res.headers, body: b }));
    });
    req.on("error", reject);
    if (body) req.write(body);
    req.end();
  });
}

test("the nonce leaves the address bar, appears in no request URL and the session cookie is HttpOnly", async () => {
  const urls = [];
  const context = await browser.newContext();
  const page = await context.newPage();
  page.on("request", (r) => urls.push(r.url()));
  const launch = await mock.launchUrl();
  const nonce = launch.split("#n=")[1];
  await page.goto(launch);
  await page.waitForSelector("main h1, .welcome h1");
  await page.waitForTimeout(500);
  assert.ok(!page.url().includes(nonce) && !page.url().includes("#n="), "fragment removed: " + page.url());
  const csrf = await page.evaluate(() => sessionStorage.getItem("theo.csrf"));
  assert.ok(csrf && csrf.length > 10);
  for (const u of urls) {
    assert.ok(!u.includes(nonce), "nonce in URL " + u);
    assert.ok(!u.includes(csrf), "csrf in URL " + u);
    assert.ok(u.startsWith(mock.origin), "request to another origin: " + u);
  }
  assert.equal(await page.evaluate(() => document.cookie), "", "cookie is HttpOnly");
  const cookies = await context.cookies();
  const c = cookies.find((x) => x.name === "theo_session");
  assert.ok(c && c.httpOnly && c.sameSite === "Strict");
  await context.close();
});

test("a launch link works once: the second use shows how to get a new one", async () => {
  const launch = await mock.launchUrl();
  const c1 = await browser.newContext();
  const p1 = await c1.newPage();
  await p1.goto(launch);
  await p1.waitForSelector("main h1, .welcome h1");
  const c2 = await browser.newContext();
  const p2 = await c2.newPage();
  await p2.goto(launch);
  await p2.locator("text=This page is not connected to Theo").waitFor();
  assert.ok(await p2.locator("text=already used or has expired").count() > 0);
  assert.ok(await p2.locator("code:has-text('theo ui')").count() > 0);
  await c1.close(); await c2.close();
});

test("a tab without session shows the reconnect help instead of an empty page", async () => {
  const c = await browser.newContext();
  const p = await c.newPage();
  await p.goto(mock.origin + "/");
  await p.locator("text=This page is not connected to Theo").waitFor();
  await c.close();
});

test("the page makes no request outside its own origin and has no console CSP violation", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  const foreign = [];
  page.on("request", (r) => { if (!r.url().startsWith(mock.origin) && !r.url().startsWith("data:")) foreign.push(r.url()); });
  for (const h of ["#/projects", "#/projects/new", "#/p/shop/new", "#/p/shop"]) { await page.evaluate((x) => { location.hash = x; }, h); await page.waitForTimeout(300); }
  assert.deepEqual(foreign, []);
  assert.deepEqual(await cspViolations(page), []);
  assert.deepEqual(page.problems, []);
  const html = await page.evaluate(() => document.documentElement.outerHTML);
  assert.ok(!/<script(?![^>]*\bsrc=)[^>]*>[^<]/.test(html), "no inline script");
  assert.ok(!/<style[\s>]/.test(html), "no <style> element");
  assert.ok(!/ style=/.test(html.replace(/<(div|span)[^>]*class="log-(inner|line)[^>]*>/g, "")), "no style attribute outside the log viewer positions (set through CSSOM)");
  await page.ctx.close();
});

test("the served shell carries the strict CSP and security headers", async () => {
  const r = await rawRequest(mock.port, { path: "/", headers: { Host: `127.0.0.1:${mock.port}` } });
  assert.equal(r.status, 200);
  assert.equal(r.headers["content-security-policy"], "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'");
  assert.equal(r.headers["x-content-type-options"], "nosniff");
  assert.equal(r.headers["referrer-policy"], "no-referrer");
  assert.ok(!/<script>[^<]/.test(r.body));
});

test("a page on another origin cannot read the API (no CORS, SameSite=Strict cookie)", async () => {
  const evil = http.createServer((req, res) => {
    res.writeHead(200, { "Content-Type": "text/html" });
    res.end(`<!doctype html><title>evil</title><script>
      window.result = "pending";
      fetch("http://127.0.0.1:${mock.port}/api/v1/projects", { credentials: "include" })
        .then(async (r) => { window.result = "status " + r.status + " " + (await r.text()).slice(0, 80); })
        .catch((e) => { window.result = "blocked: " + e.message; });
    </script>`);
  });
  await new Promise((r) => evil.listen(0, "127.0.0.1", r));
  const evilPort = evil.address().port;
  try {
    // A victim session exists in the same browser profile.
    const context = await browser.newContext();
    const victim = await context.newPage();
    await victim.goto(await mock.launchUrl());
    await victim.waitForSelector("main h1, .welcome h1");
    const page = await context.newPage();
    await page.goto(`http://127.0.0.1:${evilPort}/`);
    await page.waitForFunction(() => window.result !== "pending");
    const result = await page.evaluate(() => window.result);
    assert.ok(!/^status 200/.test(result), "cross-origin fetch must not succeed: " + result);
    await context.close();
  } finally { evil.close(); }
});

test("server refuses a foreign Host, a foreign Origin, a missing cookie and a missing CSRF header", async () => {
  const launch = await mock.launchUrl();
  const nonce = launch.split("#n=")[1];
  const ex = await rawRequest(mock.port, { method: "POST", path: "/api/v1/session", headers: { Host: `127.0.0.1:${mock.port}`, "Content-Type": "application/json" }, body: JSON.stringify({ nonce }) });
  assert.equal(ex.status, 200);
  const cookie = ex.headers["set-cookie"][0].split(";")[0];
  const good = { Host: `127.0.0.1:${mock.port}`, Cookie: cookie };
  assert.equal((await rawRequest(mock.port, { headers: { ...good, Host: "evil.example:80" } })).status, 403, "foreign Host");
  assert.equal((await rawRequest(mock.port, { headers: { ...good, Origin: "http://evil.example" } })).status, 403, "foreign Origin");
  assert.equal((await rawRequest(mock.port, { headers: { Host: good.Host } })).status, 401, "no cookie");
  assert.equal((await rawRequest(mock.port, { method: "POST", path: "/api/v1/projects", headers: { ...good, "Content-Type": "application/json" }, body: "{}" })).status, 403, "no CSRF");
  assert.equal((await rawRequest(mock.port, { headers: good })).status, 200, "valid request");
  const again = await rawRequest(mock.port, { method: "POST", path: "/api/v1/session", headers: { Host: good.Host, "Content-Type": "application/json" }, body: JSON.stringify({ nonce }) });
  assert.equal(again.status, 401, "nonce is single use");
});

test("project data is rendered as text, never as HTML", async () => {
  // A branch name that looks like markup must be shown literally.
  const call = await (async () => {
    const url = new URL(await mock.launchUrl());
    const r = await fetch(mock.origin + "/api/v1/session", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ nonce: url.hash.slice(3) }) });
    const cookie = r.headers.get("set-cookie").split(";")[0];
    const { csrf_token } = await r.json();
    return (method, path, body) => fetch(mock.origin + "/api/v1" + path, { method, headers: { Cookie: cookie, "X-Theo-Csrf": csrf_token, "Content-Type": "application/json" }, body: JSON.stringify(body) });
  })();
  const evil = "<img src=x onerror=window.pwned=1>";
  const res = await call("POST", "/projects/shop/worktrees", { branch: evil, new: true });
  assert.equal(res.status, 201);
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  assert.ok(await page.locator("table.data").getByText(evil).count() > 0, "shown as literal text");
  assert.equal(await page.locator("table.data img").count(), 0);
  assert.equal(await page.evaluate(() => window.pwned), undefined);
  await page.ctx.close();
});
