// Theo's capture engine: a small Node program that Theo (Go) launches. It opens
// pages with Playwright and Chromium and reports what it did as JSON lines on
// stdout. Theo does the orchestration, the comparison and the reports; this
// file only takes screenshots and renders HTML.
//
//   node runner.mjs check                 -> {"event":"check","chromium":"136.0..."}
//   node runner.mjs capture <config.json> -> one "shot" event per screenshot
//   node runner.mjs serve                 -> reads {id,kind,html,out} lines, answers {id,ok,error}
import { createRequire } from "node:module";
import { readFileSync, mkdirSync, existsSync } from "node:fs";
import path from "node:path";
import readline from "node:readline";
import { pathToFileURL, fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const require = createRequire(path.join(here, "runner.cjs"));
const emit = (o) => process.stdout.write(JSON.stringify(o) + "\n");

function playwright() {
  try {
    return require("playwright");
  } catch (e) {
    emit({ event: "error", error: "Playwright is not installed: run `theo tool capture setup`" });
    process.exit(3);
  }
}

async function launch(pw) {
  const opts = { args: ["--no-sandbox", "--hide-scrollbars", "--font-render-hinting=none", "--disable-lcd-text"] };
  if (process.env.THEO_CAPTURE_CHROMIUM) opts.executablePath = process.env.THEO_CAPTURE_CHROMIUM;
  try {
    return await pw.chromium.launch(opts);
  } catch (e) {
    emit({ event: "error", error: "Chromium could not start: " + String(e.message).split("\n")[0] + " (run `theo tool capture setup`)" });
    process.exit(3);
  }
}

const FROZEN = new Date("2026-01-15T10:00:00Z");
const QUIET_CSS = `*,*::before,*::after{animation:none!important;transition:none!important;caret-color:transparent!important;scroll-behavior:auto!important}
html{scrollbar-width:none}::-webkit-scrollbar{display:none}`;

function target(page, t) {
  return t.selector ? page.locator(t.selector).first() : page.getByRole(t.rol, { name: t.nombre }).first();
}

async function runAction(page, a, baseURL) {
  const [verb, arg] = Object.entries(a)[0];
  switch (verb) {
    case "ir": await page.goto(routeURL(baseURL, arg), { waitUntil: "load" }); break;
    case "click": await target(page, arg).click({ timeout: 8000 }); break;
    case "pasar_raton": await target(page, arg).hover({ timeout: 8000 }); break;
    case "escribir": await target(page, arg.en).fill(arg.texto ?? "", { timeout: 8000 }); break;
    case "elegir": await target(page, arg.en).selectOption(arg.valor, { timeout: 8000 }); break;
    case "esperar": await page.getByText(arg).first().waitFor({ timeout: 10000 }); break;
    case "scroll": await page.evaluate((v) => window.scrollTo(0, v === "fin" ? document.body.scrollHeight : v), arg); break;
    case "tecla": await page.keyboard.press(arg); break;
    default: throw new Error("unknown action " + verb);
  }
}

// A route is relative to the base of the application: "/facturas" is
// <base>/facturas and "#/login" is <base>#/login.
function routeURL(baseURL, route) {
  if (route.startsWith("#")) return baseURL.replace(/#.*$/, "") + route;
  return new URL(route.replace(/^\//, ""), baseURL.replace(/#.*$/, "")).toString();
}

function stateKey(m, p) { return `${m} ${p}`; }

async function installMock(page, state, cfg, unmocked) {
  const datos = state.datos || {};
  const origin = new URL(cfg.baseURL).origin;
  await page.route("**/*", async (route) => {
    const req = route.request();
    const type = req.resourceType();
    if (type !== "fetch" && type !== "xhr") return route.continue();
    const u = new URL(req.url());
    if (u.origin === origin && /\.(json|txt|svg|html|js|css)$/i.test(u.pathname)) return route.continue(); // a static file of the app
    const key = Object.keys(datos).find((k) => {
      const [m, p] = k.split(/\s+/, 2);
      return m.toUpperCase() === req.method() && (u.pathname === p || u.pathname.endsWith(p));
    });
    if (!key) {
      unmocked.add(stateKey(req.method(), u.pathname));
      return route.abort("failed");
    }
    const v = datos[key];
    if (typeof v === "string") {
      const file = path.resolve(cfg.root, v);
      return route.fulfill({ status: 200, contentType: "application/json", body: readFileSync(file) });
    }
    return route.fulfill({ status: v.status ?? 200, contentType: "application/json", body: JSON.stringify(v.json ?? null) });
  });
}

async function settle(page) {
  await page.waitForLoadState("networkidle", { timeout: 5000 }).catch(() => {});
  await page.evaluate(async () => {
    await document.fonts.ready;
    await Promise.all([...document.images].map((i) => (i.decode ? i.decode().catch(() => {}) : null)));
  }).catch(() => {});
  await page.waitForTimeout(150);
}

async function shoot(browser, cfg, screen, state, vp, file) {
  const ctx = await browser.newContext({
    viewport: { width: vp.width, height: vp.height },
    deviceScaleFactor: vp.scale || 1,
    isMobile: !!vp.mobile,
    locale: "en-US",
    timezoneId: "UTC",
    reducedMotion: "reduce",
    colorScheme: "light",
    ...(cfg.storageState ? { storageState: cfg.storageState } : {}),
  });
  const page = await ctx.newPage();
  const unmocked = new Set();
  const failedReq = [];
  try {
    await page.clock.setFixedTime(FROZEN);
    await page.addInitScript(() => {});
    if (cfg.data === "mock") await installMock(page, state, cfg, unmocked);
    await page.goto(routeURL(cfg.baseURL, screen.ruta), { waitUntil: "load" });
    await page.addStyleTag({ content: QUIET_CSS });
    for (const a of state.acciones || []) await runAction(page, a, cfg.baseURL);
    await page.addStyleTag({ content: QUIET_CSS });
    await settle(page);
    if (unmocked.size) {
      const first = [...unmocked][0];
      return { status: "failed", reason: `the app asked ${first} and no mock answers it (not intercepted)` };
    }
    const masks = [];
    for (const ig of screen.ignorar || []) {
      for (const h of await page.locator(ig.selector).all()) {
        const b = await h.boundingBox().catch(() => null);
        if (b) {
          const sy = await page.evaluate(() => window.scrollY);
          masks.push({ x: Math.round(b.x * (vp.scale || 1)), y: Math.round((b.y + sy) * (vp.scale || 1)), w: Math.ceil(b.width * (vp.scale || 1)), h: Math.ceil(b.height * (vp.scale || 1)) });
        }
      }
    }
    mkdirSync(path.dirname(file), { recursive: true });
    const full = await page.evaluate(() => document.documentElement.scrollHeight);
    const opts = { path: file, fullPage: true, animations: "disabled", caret: "hide" };
    if (full > (cfg.maxHeight || 8000)) Object.assign(opts, { fullPage: false, clip: { x: 0, y: 0, width: vp.width, height: cfg.maxHeight || 8000 } });
    await page.screenshot(opts);
    return { status: "ok", masks };
  } catch (e) {
    return { status: "failed", reason: String(e.message).split("\n")[0] };
  } finally {
    await ctx.close();
  }
}

async function capture(cfgFile) {
  const cfg = JSON.parse(readFileSync(cfgFile, "utf8"));
  const pw = playwright();
  const browser = await launch(pw);
  const inv = cfg.inventory;
  let total = 0;
  for (const s of inv.pantallas) total += s.estados.length * inv.viewports.length;
  emit({ event: "start", shots: total, chromium: browser.version() });
  for (const screen of inv.pantallas) {
    for (const state of screen.estados) {
      for (const vp of inv.viewports) {
        const rel = cfg.files[`${screen.id}#${state.id}#${vp.id}`];
        const abs = path.join(cfg.outDir, rel);
        const r = await shoot(browser, cfg, screen, state, vp, abs);
        let control = "";
        if (r.status === "ok" && cfg.control) {
          control = rel.replace(/\.png$/, ".control.png");
          const c = await shoot(browser, cfg, screen, state, vp, path.join(cfg.outDir, control));
          if (c.status !== "ok") control = "";
        }
        emit({ event: "shot", screen: screen.id, state: state.id, viewport: vp.id, file: r.status === "ok" ? rel : "", control, ...r });
      }
    }
  }
  await browser.close();
  emit({ event: "done" });
}

async function renderOne(browser, job) {
  const ctx = await browser.newContext({ viewport: { width: job.width || 1532, height: 900 }, deviceScaleFactor: 1, locale: "en-US", timezoneId: "UTC" });
  const page = await ctx.newPage();
  try {
    await page.goto(pathToFileURL(job.html).toString(), { waitUntil: "load" });
    await settle(page);
    mkdirSync(path.dirname(job.out), { recursive: true });
    if (job.kind === "pdf") await page.pdf({ path: job.out, format: "A4", landscape: true, printBackground: true });
    else await page.screenshot({ path: job.out, fullPage: true });
  } finally {
    await ctx.close();
  }
}

async function serve() {
  const pw = playwright();
  const browser = await launch(pw);
  emit({ event: "ready" });
  const rl = readline.createInterface({ input: process.stdin });
  for await (const line of rl) {
    if (!line.trim()) continue;
    const job = JSON.parse(line);
    try {
      await renderOne(browser, job);
      emit({ id: job.id, ok: true });
    } catch (e) {
      emit({ id: job.id, ok: false, error: String(e.message).split("\n")[0] });
    }
  }
  await browser.close();
}

const [cmd, arg] = process.argv.slice(2);
if (cmd === "check") {
  const pw = playwright();
  const browser = await launch(pw);
  emit({ event: "check", chromium: browser.version(), node: process.version });
  await browser.close();
} else if (cmd === "capture") {
  await capture(arg);
} else if (cmd === "serve") {
  await serve();
} else {
  console.error("usage: runner.mjs check | capture <config.json> | serve");
  process.exit(2);
}
