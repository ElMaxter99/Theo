// Mock of the Theo API (gui-contract.md) for developing and testing the interface without Docker.
// Node only, no dependencies. It implements the same security rules as the real server on purpose
// (exact Host, single-use launch nonce, HttpOnly cookie, CSRF header, strict CSP) so that the interface is
// exercised the same way. It is NOT the real server: real-server tests live in e2e/real/.
//
//   node mock/server.mjs [--port N]      prints one JSON line: {"url": "...", "port": N, "pid": N}
//
// Test hooks (only on this mock): POST /__mock/reset, POST /__mock/scenario {...}, GET /__mock/stats, GET /__mock/launch.
import http from "node:http";
import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const distDir = path.resolve(here, "..", "..", "internal", "ui", "dist");

const CSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'";
const MIME = { ".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8", ".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon", ".txt": "text/plain; charset=utf-8", ".md": "text/markdown; charset=utf-8" };

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const wtId = (p) => "wt_" + crypto.createHash("sha256").update(p).digest("hex").slice(0, 10);
const now = () => new Date().toISOString();
const base = (p) => p.split("/").filter(Boolean).pop();

// ---------------------------------------------------------------- state
let S;
let scenario;
let nonce;
let nonceAt;
let sessions;
let sse;
let stats;

const SERVICES = [
  { name: "web", image: "shop/web:dev", build: "./web", ports: [{ host_ip: "", published: "3000", target: 3000, protocol: "tcp" }], depends_on: ["db", "cache"], networks: ["default"], mounts: [{ type: "bind", source: "./web/src", target: "/app/src" }] },
  { name: "db", image: "postgres:16", ports: [{ host_ip: "127.0.0.1", published: "5432", target: 5432, protocol: "tcp" }], networks: ["default"], mounts: [{ type: "volume", source: "pgdata", target: "/var/lib/postgresql/data" }] },
  { name: "cache", image: "redis:7", ports: [{ host_ip: "127.0.0.1", published: "6379", target: 6379, protocol: "tcp" }], networks: ["default"] },
];

// THEO_MOCK_STALE=n adds n inactive worktrees to "shop" (screenshots of the cleanup picker); off by default.
function staleWorktrees() {
  const n = Number(process.env.THEO_MOCK_STALE || 0);
  return Array.from({ length: n }, (_, i) => mkWorktree("shop", `shop-stale-${i}`, `stale-${i}`, { state: "absent", dirty: i % 3 === 2 }));
}
function mkWorktree(project, dir, branch, o = {}) {
  const p = `/home/ana/code/${dir}`;
  return {
    id: wtId(p), path: p, branch, head: crypto.createHash("sha1").update(p).digest("hex").slice(0, 7), main: !!o.main, kind: o.main ? "main" : "worktree",
    dirty: !!o.dirty, detached: !!o.detached, compose_project: dir.replace(/[^a-z0-9]/g, "-"), env_file: o.env ?? { path: "/home/ana/code/" + project + "/.env", source: "main worktree" },
    // runtime
    rt: { state: o.state ?? "absent", services: o.services ?? {}, hostPorts: o.ports ?? { web: 3000, db: 5432, cache: 6379 }, volumes: o.volumes ?? "kept", purged: false, risky: o.risky ?? false },
    env: o.vars ?? {}, settings: { neutralize_container_names: false }, history: o.history ?? [],
  };
}

function reset() {
  scenario = { docker: true, gap: false, slow: 1 };
  nonce = "mocknonce" + crypto.randomBytes(8).toString("hex");
  nonceAt = Date.now();
  sessions = new Map();
  sse = { global: new Set(), count: 0, max: 0 };
  stats = { requests: 0 };
  const shopMain = mkWorktree("shop", "shop", "main", { main: true, state: "running", services: { web: "running", db: "running", cache: "running" }, volumes: "in use", vars: { DATABASE_URL: { value: "postgres://shop:s3cretpass@db/shop", secret: true }, LOG_LEVEL: { value: "debug" }, FEATURE_X: { value: "on", services: ["web"] } }, history: [{ time: "2026-10-28T09:12:00Z", user: "ana", operation: "up", result: "ok" }, { time: "2026-10-27T17:40:00Z", user: "ana", operation: "down", result: "error", error: "port is already allocated" }] });
  // 45 older events so that the history needs several pages
  for (let i = 0; i < 45; i++) shopMain.history.push({ time: new Date(Date.UTC(2026, 9, 26, 12, 0, 0) - i * 3600e3).toISOString(), user: "ana", operation: i % 3 === 0 ? "down" : "up", result: i % 7 === 0 ? "error" : "ok", ...(i % 7 === 0 ? { error: "port is already allocated" } : {}) });
  const shopFix = mkWorktree("shop", "shop-fix-123", "fix-123", { dirty: true, state: "partial", services: { web: "running", db: "running", cache: "exited" }, ports: { web: 3001, db: 5433, cache: 6380 }, volumes: "in use" });
  const shopFeat = mkWorktree("shop", "shop-feature-x", "feature-x", { state: "stopped", services: { web: "exited", db: "exited", cache: "exited" }, risky: true, volumes: "kept" });
  shopFix.owner = "agent-1";
  shopMain.backups = [{ project: "shop", instance: "shop", volume: "pgdata", docker_volume: "shop_pgdata", archive: "shop_pgdata-20261030T091500Z.tar.gz", size: 18_345_678, created_at: "2026-10-30T09:15:00Z", file: "/home/ana/.local/share/theo/backups/shop/shop_pgdata-20261030T091500Z.tar.gz" }];
  const shopOld = mkWorktree("shop", "shop-old-spike", "old-spike", { dirty: true, state: "absent", env: { source: "none" } });
  const shopDetached = mkWorktree("shop", "shop-detached", "", { detached: true, state: "unhealthy", services: { web: "unhealthy", db: "running", cache: "running" }, ports: { web: 3002, db: 5434, cache: 6381 } });
  const blogMain = mkWorktree("blog", "blog", "main", { main: true, state: "stopped", services: { web: "exited", db: "exited", cache: "exited" } });
  const blogDraft = mkWorktree("blog", "blog-draft", "draft-post", { state: "absent" });
  blogMain.compose_project = "blog"; blogDraft.compose_project = "blog-draft";
  S = {
    projects: [
      { name: "shop", path: "/home/ana/code/shop", env_file: "", added_at: "2026-09-01T10:00:00Z", wts: [shopMain, shopFix, shopFeat, shopOld, shopDetached, ...staleWorktrees()] },
      { name: "blog", path: "/home/ana/code/blog", env_file: "/home/ana/code/blog.env", added_at: "2026-09-12T10:00:00Z", wts: [blogMain, blogDraft] },
    ],
    ops: new Map(),
    idem: new Map(),
    proxy: { running: false, managed: false, port: 7080, autostart: false, https: false, https_port: 7443 },
    authority: null, // created the first time HTTPS is switched on
    trusted: false,
    opSeq: 0,
    // Findings of the reconcile report; "forgettable" ones are Theo's own records.
    findings: [
      { kind: "worktree missing", project: "shop", subject: "shop-gone-branch", worktree: "/home/ana/code/shop-gone-branch", detail: "Git lists the worktree but its folder no longer exists", fix: "Run `git worktree prune` in the main repository, or restore the folder." },
      { kind: "orphan containers", project: "shop", subject: "shop-old-feature-web-1", detail: "containers of an instance whose worktree no longer exists", fix: "Remove them with `docker rm -f shop-old-feature-web-1` if you do not need them." },
      { kind: "orphan volumes", project: "shop", subject: "shop-old-feature_pgdata", detail: "volume of an instance whose worktree no longer exists", fix: "Delete it with `docker volume rm shop-old-feature_pgdata` after checking its data." },
      { kind: "stale settings", project: "shop", subject: "/home/ana/code/shop-old-feature", worktree: "/home/ana/code/shop-old-feature", detail: "Theo's settings for a worktree that no longer exists", fix: "Forget the record.", forgettable: true },
      { kind: "stale ports", project: "shop", subject: "/home/ana/code/shop-old-feature", worktree: "/home/ana/code/shop-old-feature", detail: "ports 3003, 5435 are reserved for a worktree that no longer exists", fix: "Forget the record to free the ports.", forgettable: true },
      { kind: "stale ports", project: "blog", subject: "/home/ana/code/blog-old", worktree: "/home/ana/code/blog-old", detail: "port 3010 is reserved for a worktree that no longer exists", fix: "Forget the record to free the port.", forgettable: true },
    ],
    forgotten: [],
  };
  // Shared services: shop has them (db, used by two instances); blog has none.
  S.projects[0].shared = { from: shopMain.path, publish: false, services: { db: { image: "postgres:16", state: "running" } }, links: new Map([[shopMain.id, new Set(["db"])], [shopFix.id, new Set(["db"])]]) };
  S.projects[0].fixed = { web: "shop-web" };
  if (scenario.sharedEmpty) S.projects[0].shared = null;
  if (scenario.empty) S.projects = [];
}
reset();

const FS = {
  "/home/ana": ["code", "notes", "downloads"],
  "/home/ana/code": ["shop", "blog", "api-gateway", "playground"],
  "/home/ana/notes": [], "/home/ana/downloads": [],
  "/home/ana/code/shop": ["web", "docs"], "/home/ana/code/blog": [], "/home/ana/code/api-gateway": [], "/home/ana/code/playground": [],
  "/home/ana/code/shop/web": [], "/home/ana/code/shop/docs": [],
};
const GIT = new Set(["/home/ana/code/shop", "/home/ana/code/blog", "/home/ana/code/api-gateway"]);

const findProject = (n) => S.projects.find((p) => p.name === n);
const findWt = (p, id) => p?.wts.find((w) => w.id === id);

function derive(w) {
  const vals = Object.values(w.rt.services);
  if (!scenario.docker) return "unknown";
  if (vals.length === 0) return "absent";
  if (vals.some((v) => v === "unhealthy" || v === "dead")) return "unhealthy";
  const running = vals.filter((v) => v === "running").length;
  if (running === vals.length) return "running";
  if (running > 0) return "partial";
  return "stopped";
}
const activeOp = (w) => [...S.ops.values()].find((o) => o.worktree === w.id && ["queued", "running", "cancelling"].includes(o.state));

function portsOf(w) {
  return SERVICES.flatMap((s) => (s.ports ?? []).map((p) => ({ svc: s.name, target: p.target, configured: Number(p.published), ip: p.host_ip })));
}
const heldPorts = (except) => { const m = new Map(); for (const p of S.projects) for (const w of p.wts) if (w !== except && ["running", "partial", "unhealthy"].includes(derive(w))) for (const [svc, hp] of Object.entries(w.rt.hostPorts)) if (w.rt.services[svc] === "running" || w.rt.services[svc] === "unhealthy") m.set(hp, w); return m; };
const externalPorts = new Set([5432]); // something else on this machine already uses 5432 when nobody holds it (see ports plan)

function urlsOf(w) {
  if (!["running", "partial", "unhealthy"].includes(derive(w))) return [];
  return Object.entries(w.rt.hostPorts).filter(([svc]) => svc === "web" && w.rt.services[svc] === "running").map(([svc, hp]) => ({ service: svc, port: hp, url: `http://127.0.0.1:${hp}` }));
}
function conflictsOf(w) {
  if (derive(w) === "running" || !scenario.docker) return [];
  const held = heldPorts(w);
  const out = [];
  for (const p of portsOf(w)) if (held.has(p.configured) && !["absent"].includes(derive(w)) && derive(w) !== "stopped") out.push({ service: p.svc, port: p.configured });
  return out;
}

function portStatuses(w) {
  const held = heldPorts(w);
  return portsOf(w).map((p) => {
    const mine = w.rt.services[p.svc] === "running" || w.rt.services[p.svc] === "unhealthy";
    const hp = mine ? w.rt.hostPorts[p.svc] : p.configured;
    const status = !scenario.docker ? "free" : mine ? "running" : held.has(p.configured) ? "in use" : "free";
    return { service: p.svc, container_port: p.target, protocol: "tcp", host_port: hp, url: p.svc === "web" ? `http://localhost:${hp}` : undefined, status };
  });
}
function warningsOf(w) {
  const out = [];
  if (w.env_file?.source === "none") out.push({ kind: "missing-env-file", message: "no .env file was found for this worktree", fix: "Create a .env file in the worktree or set a shared one with `theo project set`." });
  return out;
}
function listItem(w) {
  const op = activeOp(w);
  const total = Object.keys(w.rt.services).length;
  const running = Object.values(w.rt.services).filter((v) => v === "running").length;
  return {
    id: w.id, path: w.path, branch: w.branch || undefined, head: w.head + "0123456789abcdef0123456789abcdef0123", detached: w.detached, locked: false, prunable: false, bare: false, main: w.main, kind: w.kind, dirty: w.dirty, owner: w.owner,
    proxy_urls: proxyLinks(w),
    compose_project: w.compose_project, env_file: w.env_file, state: derive(w), containers: { running, total },
    ports: portStatuses(w), warnings: warningsOf(w),
    busy: op ? { operation_id: op.id, kind: op.kind } : null,
  };
}
function proxyLinks(w) {
  if (!S.proxy.running) return undefined;
  const project = S.projects.find((p) => p.wts.includes(w));
  const inst = w.main ? "main" : w.compose_project.replace(new RegExp("^" + project.name + "-"), "");
  const host = `${inst}.${project.name}.localhost:${S.proxy.port}`;
  const running = w.rt.services.web === "running";
  const tls = S.proxy.https ? `${inst}.${project.name}.localhost:${S.proxy.https_port}` : "";
  return [{ service: "web", url: `http://${host}`, https_url: tls ? `https://${tls}` : undefined, default: true, running },
    ...SERVICES.map((x) => ({ service: x.name, url: `http://${x.name}.${host}`, https_url: tls ? `https://${x.name}.${tls}` : undefined, running: w.rt.services[x.name] === "running" }))];
}
function proxyStatus() {
  const authority = S.authority ? { ...S.authority, trust: { system: S.trusted }, trusted: S.trusted, can_trust_here: true, command: "theo proxy trust" } : undefined;
  return { ...S.proxy, pid: S.proxy.running ? 4242 : undefined, url: S.proxy.running ? `http://localhost:${S.proxy.port}/` : undefined, authority };
}
function containersOf(w) {
  // Same shape as the real server: raw `docker compose ps` records (capitalised keys).
  return Object.entries(w.rt.services).map(([service, state]) => ({
    Name: `${w.compose_project}-${service}-1`, Service: service, State: state === "unhealthy" ? "running" : state, Health: state === "unhealthy" ? "unhealthy" : state === "running" ? "healthy" : "",
    Status: state === "running" ? "Up 2 hours" : "Exited (" + (state === "exited" ? 1 : 0) + ") 3 hours ago", ExitCode: state === "exited" ? 1 : 0,
    Publishers: SERVICES.find((x) => x.name === service).ports.map((p) => ({ URL: "0.0.0.0", TargetPort: p.target, PublishedPort: w.rt.hostPorts[service], Protocol: "tcp" })),
  }));
}

// ---------------------------------------------------------------- helpers
function send(res, status, body, headers = {}) {
  const isStr = typeof body === "string";
  res.writeHead(status, { "Content-Type": isStr ? "text/plain; charset=utf-8" : "application/json", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "Content-Security-Policy": CSP, "Referrer-Policy": "no-referrer", "Cross-Origin-Opener-Policy": "same-origin", ...headers });
  res.end(isStr ? body : JSON.stringify(body));
}
const err = (res, status, code, message, extra = {}) => send(res, status, { error: { code, message, ...extra } });
const parkRuns = new Map();
async function readJson(req) {
  const chunks = [];
  for await (const c of req) chunks.push(c);
  if (chunks.length === 0) return {};
  try { return JSON.parse(Buffer.concat(chunks).toString("utf8")); } catch { return null; }
}
function sseStart(res, req) {
  res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-store", Connection: "keep-alive", "X-Content-Type-Options": "nosniff", "Content-Security-Policy": CSP });
  sse.count++; sse.max = Math.max(sse.max, sse.count);
  let closed = false;
  req.on("close", () => { if (!closed) { closed = true; sse.count--; } });
  res.write(": open\n\n");
  return () => closed;
}
const ev = (res, event, data, id) => res.write(`${id !== undefined ? `id: ${id}\n` : ""}event: ${event}\ndata: ${JSON.stringify(data)}\n\n`);

// ---------------------------------------------------------------- operations
function publicOp(o) { const { lines, subs, timer, ...rest } = o; return rest; }
function emitGlobal(event, data) { for (const r of sse.global) ev(r, event, data); }
function opUpdate(o) { emitGlobal("operation.updated", publicOp(o)); }
function opLog(o, line, stream = "stdout") {
  const id = o.lines.length + 1 + o.dropped;
  const e = { id, event: "log", data: { stream, line, at: now() } };
  o.lines.push(e); if (o.lines.length > 5000) { o.lines.shift(); o.dropped++; }
  for (const r of o.subs) ev(r, "log", e.data, id);
}
function opState(o, state, extra = {}) {
  Object.assign(o, { state }, extra);
  const id = o.lines.length + 1 + o.dropped;
  const e = { id, event: "state", data: { state, at: now() } }; o.lines.push(e);
  for (const r of o.subs) ev(r, "state", e.data, id);
  opUpdate(o);
}

function startOp(project, w, kind, body, res) {
  if (!scenario.docker) return err(res, 412, "docker_unavailable", "Cannot connect to the Docker daemon", { hint: "Start Docker and try again." });
  const act = activeOp(w);
  if (act) return err(res, 409, "busy", `another operation (${act.kind}) is running on this instance`, { resource: { operation_id: act.id } });
  if ([...S.ops.values()].filter((o) => ["running", "queued"].includes(o.state)).length >= 4) return err(res, 429, "too_many_operations", "too many operations are running");
  const o = { id: "op_" + String(++S.opSeq).padStart(4, "0") + crypto.randomBytes(3).toString("hex"), kind, project: project.name, worktree: w.id, state: "running", started_at: now(), cancellable: true, lines: [], subs: new Set(), dropped: 0 };
  S.ops.set(o.id, o);
  opState(o, "running");
  runOp(o, project, w, body);
  return o;
}

async function runOp(o, project, w, body) {
  const step = async (ms) => { await sleep(ms * scenario.slow); if (o.state === "cancelling") throw new Error("cancelled"); };
  const names = Object.keys(w.rt.services).length ? Object.keys(w.rt.services) : SERVICES.map((s) => s.name);
  try {
    if (["up", "restart", "start", "recreate"].includes(o.kind)) {
      if (o.kind === "restart" || o.kind === "recreate") { opLog(o, "Restarting containers"); await step(500); }
      opLog(o, `Using environment from ${w.env_file?.source ?? "none"}`);
      // Reserve ports
      const held = heldPorts(w);
      for (const p of portsOf(w)) {
        let hp = p.configured;
        if (held.has(hp) || (externalPorts.has(hp) && !held.size && false)) {
          if (body?.auto_ports) { hp = p.configured + 1; while (held.has(hp)) hp++; opLog(o, `port ${p.configured} is in use: ${p.svc} will use ${hp}`); }
          else { opLog(o, `Error: Bind for 0.0.0.0:${p.configured} failed: port is already allocated`, "stderr"); throw new Error(`port ${p.configured} is already allocated`); }
        }
        w.rt.hostPorts[p.svc] = hp;
      }
      for (const n of names) { opLog(o, ` Container ${w.compose_project}-${n}-1  Creating`); await step(350); w.rt.services[n] = w.rt.services[n] ?? "created"; }
      if (/fail/.test(w.branch)) { opLog(o, "Error response from daemon: driver failed programming external connectivity", "stderr"); w.rt.services[names[0]] = "running"; throw new Error("failed to start web: driver failed programming external connectivity"); }
      for (const n of names) { opLog(o, ` Container ${w.compose_project}-${n}-1  Starting`); await step(400); w.rt.services[n] = "running"; opLog(o, ` Container ${w.compose_project}-${n}-1  Started`); }
      w.rt.volumes = "in use";
      w.history.unshift({ time: now(), user: "ana", operation: o.kind, result: "ok" });
    } else if (o.kind === "stop") {
      for (const n of Object.keys(w.rt.services)) { opLog(o, ` Container ${w.compose_project}-${n}-1  Stopping`); await step(300); w.rt.services[n] = "exited"; }
      w.history.unshift({ time: now(), user: "ana", operation: "stop", result: "ok" });
    } else if (o.kind === "down") {
      for (const n of Object.keys(w.rt.services)) { opLog(o, ` Container ${w.compose_project}-${n}-1  Stopping`); await step(300); }
      w.rt.services = {}; w.rt.volumes = "kept";
      opLog(o, " Network default  Removed");
      w.history.unshift({ time: now(), user: "ana", operation: "down", result: "ok" });
    }
    opState(o, "succeeded", { finished_at: now(), result: { ok: true, summary: `${o.kind} finished` } });
  } catch (e) {
    const cancelled = e.message === "cancelled";
    w.history.unshift({ time: now(), user: "ana", operation: o.kind, result: "error", error: cancelled ? "cancelled" : e.message });
    opState(o, cancelled ? "cancelled" : "failed", { finished_at: now(), error: cancelled ? undefined : { code: "port_conflict", message: e.message, original: e.message + "\n\nError response from daemon: " + e.message }, result: { ok: false } });
  }
  for (const r of o.subs) { ev(r, "result", { ok: o.state === "succeeded", summary: o.state, error: o.error?.message }, o.lines.length + 1 + o.dropped); r.end(); }
  o.subs.clear();
  emitGlobal("state.changed", { project: project.name, worktree: w.id });
}


// ---------------------------------------------------------------- 0.2 additions
// ---------------------------------------------------------------- lifecycle (ADR-0016)
const daysAgo = (d) => new Date(Date.now() - d * 864e5).toISOString();
function lifeOf(proj, w) {
  if (w.main) return { idle_days: 0, ahead: 0, behind: 0, unpushed: 0, changes: w.dirty ? 1 : 0 };
  const running = ["running", "partial", "unhealthy"].includes(derive(w));
  const k = (S.kept ??= new Map()).get(w.id);
  const base = { base: "main", base_recorded: true, behind: 3, origin: "theo", created_at: daysAgo(30), changes: w.dirty ? 3 : 0, running, size_bytes: 220 * 2 ** 20 };
  let l;
  if (w.branch === "draft-post") l = { ...base, state: "merged", merged_by: "content", ahead: 2, unpushed: 0, upstream: "origin/draft-post", upstream_gone: true, activity_at: daysAgo(12), activity_source: "commit", idle_days: 12, reason: "its changes are in the base (squash or rebase merge)" };
  else if (/^stale-/.test(w.branch)) {
    const n = Number(w.branch.slice(6)), pushed = n % 3 === 0, gone = n % 3 === 1;
    l = { ...base, state: "inactive", ahead: 2, changes: n % 3 === 2 ? 1 : 0, unpushed: pushed || gone ? 0 : 2, upstream: pushed || gone ? `origin/${w.branch}` : undefined, upstream_gone: gone || undefined,
      activity_at: daysAgo(8 + n * 4), activity_source: "commit", idle_days: 8 + n * 4, size_bytes: (40 + n * 30) * 2 ** 20, reason: "no activity" };
  } else if (w.branch === "old-spike") l = { ...base, state: "inactive", ahead: 5, unpushed: 5, activity_at: daysAgo(19), activity_source: "files", idle_days: 19, reason: "no activity for 19 days (threshold 7)" };
  else if (w.branch === "feature-x") l = { ...base, state: "unpushed", ahead: 2, unpushed: 2, activity_at: daysAgo(0.1), activity_source: "files", idle_days: 0, reason: "2 commit(s) on no remote branch" };
  else if (running) l = { ...base, state: "in-use", ahead: 2, unpushed: 2, activity_at: daysAgo(0.003), activity_source: w.owner ? "mcp" : "instance", idle_days: 0, reason: "its instance is running" };
  else l = { ...base, state: "pushed", ahead: 4, unpushed: 0, upstream: `origin/${w.branch}`, activity_at: daysAgo(3), activity_source: "commit", idle_days: 3, reason: "everything is pushed" };
  if (k) l = { ...l, state: "kept", kept: true, kept_until: k.until || undefined, keep_note: k.note || undefined, reason: "kept with `theo worktree keep`" };
  return l;
}
const lifeView = (proj, w) => ({ id: w.id, path: w.path, branch: w.branch || undefined, head: w.head, detached: w.detached, kind: w.kind, owner: w.owner, dirty: w.dirty, ...lifeOf(proj, w) });
function cleanOf(proj, applied = false) {
  const views = proj.wts.filter((w) => !w.main).map((w) => lifeView(proj, w));
  const entry = (v) => ({ path: v.path, branch: v.branch, owner: v.owner, state: v.state, merged_by: v.merged_by, idle_days: v.idle_days, note: v.keep_note, closed: false });
  return {
    project: proj.name, applied, inactive_after_days: proj.inactive_after_days ?? 7,
    close: views.filter((v) => v.state === "merged").map((v) => ({ ...entry(v), reason: v.running ? "its instance is running; add --down" : undefined })),
    inactive: views.filter((v) => v.state === "inactive").map(entry),
    kept: views.filter((v) => v.state === "kept").map(entry),
    leftovers: S.findings.filter((f) => f.project === proj.name && /orphan|stale/.test(f.kind) && !S.forgotten.includes(fid(f))).map(withId),
  };
}

async function lifecycleRoutes(req, res, proj, seg) {
  const m = req.method;
  if (seg[2] === "lifecycle" && m === "GET") {
    return send(res, 200, { project: proj.name, inactive_after_days: proj.inactive_after_days ?? 7, worktrees: proj.wts.map((w) => lifeView(proj, w)), parked: proj.parked ?? [], notes: [] }), true;
  }
  if (seg[2] === "clean" && seg.length === 3) {
    if (m === "GET") return send(res, 200, cleanOf(proj)), true;
    if (m === "POST") {
      const b = (await readJson(req)) ?? {};
      if (b.confirm !== proj.name) return send(res, 409, { error: { code: "confirmation_required", level: 3, message: "closing worktrees needs the project name as confirmation", confirm: { expected: proj.name } } }), true;
      const plan = cleanOf(proj, true);
      for (const e of plan.close) {
        if (e.reason && !b.down) continue;
        proj.wts = proj.wts.filter((w) => w.path !== e.path);
        e.closed = true; e.reason = undefined; e.branch_deleted = !!b.delete_branches;
      }
      emitGlobal("state.changed", { project: proj.name });
      return send(res, 200, plan), true;
    }
  }
  if (seg[2] === "park-inactive" && seg.length === 4 && m === "GET") {
    const r = parkRuns.get(seg[3]);
    if (!r) return err(res, 404, "operation_not_found", `no bulk park ${seg[3]}`), true;
    const done = Math.min(r.entries.length, Math.floor((Date.now() - r.at) / 600));
    const fin = done >= r.entries.length;
    const cur = r.entries[done];
    return send(res, 200, {
      operation: { id: seg[3], kind: "park-inactive", project: proj.name, state: fin ? "succeeded" : "running", result: fin ? { ok: true, summary: "done" } : undefined },
      total: r.entries.length, done, current: cur?.path, branch: cur?.branch, entries: r.entries.slice(0, done), report: fin ? { project: proj.name, applied: true, entries: r.entries, summary: {} } : undefined,
    }), true;
  }
  if (seg[2] === "park-inactive" && seg.length === 3) {
    const url = new URL(req.url, "http://x");
    const only = m === "GET" ? url.searchParams.getAll("path") : null;
    const b = m === "POST" ? ((await readJson(req)) ?? {}) : {};
    if (m === "POST" && b.confirm !== proj.name) return send(res, 409, { error: { code: "confirmation_required", level: 3, message: "parking needs the project name", confirm: { expected: proj.name } } }), true;
    const save = m === "POST" ? !!b.save_changes : ["1", "true"].includes(url.searchParams.get("save_changes"));
    const picked = m === "POST" ? b.paths ?? [] : only;
    const incl = m === "POST" ? !!b.include_ignored : ["1", "true"].includes(url.searchParams.get("include_ignored"));
    const entries = proj.wts.filter((w) => !w.main).map((w) => lifeView(proj, w)).filter((v) => (v.state === "inactive" && !(picked ?? []).length) || (picked ?? []).includes(v.path)).map((v) => {
      const safe = v.changes === 0 && v.unpushed === 0 && !!v.upstream && !v.upstream_gone;
      // Every second stale worktree has a folder Git ignores that cannot be rebuilt.
      const ignored_names = /^stale-\d+$/.test(v.branch) && Number(v.branch.slice(6)) % 2 === 0 ? ["certs/"] : [];
      const reason = v.changes > 0 && !save ? `${v.changes} uncommitted file(s); add --save-changes to keep them in a work-in-progress commit` : ignored_names.length > 0 && !incl ? "has files Git ignores that cannot be rebuilt and parking deletes (certs/); add --include-ignored" : undefined;
      return { path: v.path, branch: v.branch, idle_days: v.idle_days, changes: v.changes, unpushed: v.unpushed, upstream_gone: v.upstream_gone, ignored: ignored_names.length, ignored_names, safe, branch_deletable: safe, size_bytes: v.size_bytes, reason, reason_code: reason ? (ignored_names.length > 0 && !incl ? "ignored" : "changes") : undefined, saved: !reason && v.changes > 0 && m === "POST", parked: false };
    });
    if (m === "POST") {
      for (const e of entries) {
        if (e.reason) continue;
        proj.wts = proj.wts.filter((w) => w.path !== e.path);
        e.parked = true; e.branch_deleted = !!b.delete_branches && e.branch_deletable;
        if (!e.branch_deleted) (proj.parked ??= []).push({ branch: e.branch, path: e.path, base: "main", parked_at: now() });
      }
      emitGlobal("state.changed", { project: proj.name });
    }
    const ok = entries.filter((e) => !e.reason);
    const summary = { total: entries.length, parkable: ok.length, safe: entries.filter((e) => e.safe).length, with_changes: entries.filter((e) => e.changes > 0).length, with_unpushed: entries.filter((e) => e.unpushed > 0).length, upstream_gone: entries.filter((e) => e.upstream_gone).length, blocked: 0, size_bytes: ok.reduce((n, e) => n + (e.size_bytes ?? 0), 0) };
    if (m === "POST") {
      const id = `op_park${parkRuns.size + 1}`;
      parkRuns.set(id, { at: Date.now(), entries });
      return send(res, 202, { id, kind: "park-inactive", project: proj.name, state: "running" }), true;
    }
    return send(res, 200, { project: proj.name, applied: false, entries, summary }), true;
  }
  if (seg[2] === "resume" && m === "POST") {
    const b = (await readJson(req)) ?? {};
    const pk = (proj.parked ?? []).find((x) => x.branch === b.branch);
    if (!pk) return err(res, 404, "not_found", `${b.branch} is not parked`), true;
    proj.parked = proj.parked.filter((x) => x !== pk);
    const w = mkWorktree(proj.name, base(pk.path), pk.branch); w.owner = pk.owner; proj.wts.push(w);
    emitGlobal("state.changed", { project: proj.name });
    return send(res, 200, { id: w.id, path: w.path, branch: w.branch, kind: "worktree", env_file: w.env_file }), true;
  }
  if (seg[2] === "worktrees" && (seg[4] === "park" || seg[4] === "keep") && m === "POST") {
    const w = findWt(proj, seg[3]);
    if (!w) return err(res, 404, "worktree_not_found", "no such worktree"), true;
    const b = (await readJson(req)) ?? {};
    if (seg[4] === "keep") {
      if (b.clear) S.kept.delete(w.id); else (S.kept ??= new Map()).set(w.id, { until: b.until ? `${b.until}T23:59:59Z` : "", note: b.note ?? "" });
      emitGlobal("state.changed", { project: proj.name });
      return send(res, 200, { path: w.path, kind: w.kind, kept: !b.clear }), true;
    }
    if (w.dirty) return err(res, 400, "invalid_request", `${w.path} has 3 uncommitted or untracked file(s); commit them first`), true;
    if (b.confirm !== base(w.path)) return send(res, 409, { error: { code: "confirmation_required", level: 3, message: "files Git ignores would be lost", confirm: { expected: base(w.path) }, affected: [{ kind: "file", name: ".env.local", detail: "ignored" }] } }), true;
    proj.wts = proj.wts.filter((x) => x !== w);
    (proj.parked ??= []).push({ branch: w.branch, path: w.path, base: "main", owner: w.owner, note: b.note || undefined, parked_at: now() });
    emitGlobal("state.changed", { project: proj.name });
    return send(res, 200, { branch: w.branch, path: w.path, unpushed: 2 }), true;
  }
  return false;
}

function statusOf() {
  return {
    projects: S.projects.map((p) => ({
      name: p.name, path: p.path, warnings: [],
      worktrees: p.wts.map((w) => {
        const st = derive(w);
        const li = listItem(w);
        return {
          id: w.id, path: w.path, branch: w.branch || undefined, kind: w.kind, dirty: w.dirty, warnings: warningsOf(w), lifecycle: w.main ? undefined : lifeOf(p, w),
          instance: st === "absent" ? undefined : { compose_project: w.compose_project, instance_state: st, counts: li.containers, ports: portStatuses(w), proxy_urls: (proxyLinks(w) ?? []).map((l) => l.url) },
        };
      }),
    })),
    docker_error: scenario.docker ? undefined : "Cannot connect to the Docker daemon at unix:///var/run/docker.sock",
    warnings: [],
  };
}
function historyPage(res, all, q) {
  const end = q.get("before") !== null && Number(q.get("before")) < all.length ? Number(q.get("before")) : all.length;
  const limit = Number(q.get("limit") || 0);
  if (limit < 0 || Number.isNaN(limit)) return err(res, 400, "invalid_request", "limit must be a positive number");
  const start = limit > 0 && end - limit > 0 ? end - limit : 0;
  return send(res, 200, all.slice(start, end), { "Theo-History-Total": String(all.length), ...(start > 0 ? { "Theo-History-Next": String(start) } : {}) });
}
const eventsOf = (p, w) => [...w.history].reverse().map((h) => ({ project: p.name, workspace: w.path, compose_project: w.compose_project, ...h }));
const byTime = (a, b) => (a.time < b.time ? -1 : a.time > b.time ? 1 : 0);
/** An operation that is not one instance's lifecycle: stop-all and the volume operations. */
function genericOp(res, kind, project, wtId, steps, after) {
  if (!scenario.docker) return err(res, 412, "docker_unavailable", "Cannot connect to the Docker daemon", { hint: "Start Docker and try again." });
  const o = { id: "op_" + String(++S.opSeq).padStart(4, "0") + crypto.randomBytes(3).toString("hex"), kind, project, worktree: wtId, state: "running", started_at: now(), cancellable: false, lines: [], subs: new Set(), dropped: 0 };
  S.ops.set(o.id, o);
  opState(o, "running");
  (async () => {
    for (const line of steps) { opLog(o, line); await sleep(300 * scenario.slow); }
    after?.();
    opState(o, "succeeded", { finished_at: now(), result: { ok: true, summary: `${kind} finished` } });
    for (const r of o.subs) { ev(r, "result", { ok: true, summary: "succeeded" }, o.lines.length + 1); r.end(); }
    o.subs.clear();
    emitGlobal("state.changed", { project, worktree: wtId });
  })();
  return send(res, 202, publicOp(o), { Location: `/api/v1/operations/${o.id}` });
}
const MCP_ARGS = (q) => ["mcp", ...(q.get("read_only") === "1" ? ["--read-only"] : []), ...(q.get("owner") ? ["--owner", q.get("owner")] : [])];
function mcpInstall(q) {
  const args = MCP_ARGS(q);
  return {
    name: "theo", command: "theo", args, claude_code: ["claude", "mcp", "add", "theo", "--", "theo", ...args],
    claude_code_command: `claude mcp add theo -- theo ${args.join(" ")}`,
    mcpServers: { theo: { command: "theo", args } },
    snippet: `## Local environments (Theo)\n\nThis repository runs its Docker Compose instances with Theo. Use the theo MCP server${args.includes("--read-only") ? " (read-only)" : ""} to list worktrees, create one per task and start its instance; do not run docker compose directly.\n`,
  };
}

async function topLevel(req, res, seg, q) {
  const m = req.method;
  if (seg[0] === "status" && m === "GET") return send(res, 200, statusOf()), true;
  if (seg[0] === "history" && m === "GET") return historyPage(res, S.projects.flatMap((p) => p.wts.flatMap((w) => eventsOf(p, w))).sort(byTime), q), true;
  if (seg[0] === "mcp" && seg[1] === "install" && m === "GET") return send(res, 200, mcpInstall(q)), true;
  if (seg[0] === "stop-all" && m === "POST") {
    const b = (await readJson(req)) ?? {};
    const projects = b.project ? S.projects.filter((p) => p.name === b.project) : S.projects;
    if (b.project && projects.length === 0) return err(res, 404, "project_not_found", `project ${b.project} is not registered`), true;
    const targets = projects.flatMap((p) => p.wts.filter((w) => ["running", "partial", "unhealthy"].includes(derive(w))));
    genericOp(res, "stop-all", b.project ?? "", undefined, targets.map((w) => `Stopping ${w.compose_project}`), () => {
      for (const w of targets) { for (const n of Object.keys(w.rt.services)) w.rt.services[n] = "exited"; w.history.unshift({ time: now(), user: "ana", operation: "stop", result: "ok" }); }
    });
    return true;
  }
  return false;
}

async function projectExtras(req, res, proj, seg, q) {
  const m = req.method;
  if (seg[2] === "history" && m === "GET") return historyPage(res, proj.wts.flatMap((w) => eventsOf(proj, w)).sort(byTime), q), true;
  if (seg[2] === "rename" && m === "POST") {
    const b = (await readJson(req)) ?? {};
    const name = String(b.name ?? "").trim();
    if (!name) return err(res, 400, "invalid_request", "name is required"), true;
    if (findProject(name)) return err(res, 409, "invalid_request", `a project named ${name} already exists`), true;
    const old = proj.name; proj.name = name;
    emitGlobal("state.changed", { project: name });
    return send(res, 200, { old, new: name, records: proj.wts.length }), true;
  }
  if (seg[2] === "prune") {
    const merged = proj.wts.filter((w) => !w.main && ["feature-x", "old-spike"].includes(w.branch)).map((w) => ({ path: w.path, branch: w.branch, owner: w.owner, reason: Object.keys(w.rt.services).length ? "it still has containers; run down first" : w.dirty ? "it has uncommitted changes" : undefined, removed: false }));
    const notMerged = proj.wts.filter((w) => !w.main).length - merged.length;
    if (m === "GET") return send(res, 200, { project: proj.name, into: "main", applied: false, merged, not_merged: notMerged }), true;
    if (m === "POST") {
      const b = (await readJson(req)) ?? {};
      if (b.confirm !== proj.name) return send(res, 409, { error: { code: "confirmation_required", level: 3, message: "type the project name to remove the merged worktrees", confirm: { expected: proj.name, prompt: "Type the project name" } } }), true;
      for (const x of merged) if (!x.reason) { proj.wts = proj.wts.filter((w) => w.path !== x.path); x.removed = true; }
      emitGlobal("state.changed", { project: proj.name });
      return send(res, 200, { project: proj.name, into: "main", applied: true, merged, not_merged: notMerged }), true;
    }
  }
  return false;
}

async function volumeExtras(req, res, proj, w, seg) {
  const m = req.method;
  if (seg[4] !== "volumes") return false;
  w.backups ??= [];
  if (seg[5] === "backups" && m === "GET") return send(res, 200, w.backups), true;
  if (["backup", "restore", "clone"].includes(seg[5]) && m === "POST") {
    const b = (await readJson(req)) ?? {};
    const kind = seg[5];
    if (kind !== "backup") {
      if (Object.keys(w.rt.services).length) return err(res, 409, "instance_has_containers", "the instance still has containers; run down first"), true;
      if (b.confirm !== w.compose_project) return send(res, 409, { error: { code: "confirmation_required", level: 3, message: `this overwrites the data of ${w.compose_project}`, confirm: { expected: w.compose_project, prompt: "Type the instance name" } } }), true;
    }
    genericOp(res, `volumes.${kind}`, proj.name, w.id, [kind === "backup" ? `Backing up ${w.compose_project}_pgdata` : kind === "restore" ? `Restoring ${b.volume ?? "pgdata"} from ${b.backup}` : `Copying volumes from ${b.from}`], () => {
      if (kind === "backup") {
        const at = now();
        const archive = `${w.compose_project}_pgdata-${at.replace(/[-:]/g, "").replace(/\..*$/, "")}.tar.gz`;
        w.backups.unshift({ project: proj.name, instance: w.compose_project, volume: "pgdata", docker_volume: `${w.compose_project}_pgdata`, archive, size: 4_812_345, created_at: at, file: `/home/ana/.local/share/theo/backups/${proj.name}/${archive}` });
      }
      w.history.unshift({ time: now(), user: "ana", operation: `volumes ${kind}`, result: "ok" });
    });
    return true;
  }
  return false;
}

// ---------------------------------------------------------------- routing
async function api(req, res, url) {
  const m = req.method;
  const seg = url.pathname.replace(/^\/api\/v1\/?/, "").split("/").filter(Boolean).map(decodeURIComponent);
  const q = url.searchParams;

  if (seg[0] === "session" && m === "POST") {
    const b = await readJson(req);
    if (!b || !b.nonce || b.nonce !== nonce || Date.now() - nonceAt > 60000) return err(res, 401, "unauthorized", "the launch link is invalid, used or expired");
    nonce = ""; // single use
    const sid = crypto.randomBytes(16).toString("hex"); const csrf = crypto.randomBytes(16).toString("hex");
    sessions.set(sid, csrf);
    return send(res, 200, { api_version: "1", csrf_token: csrf }, { "Set-Cookie": `theo_session=${sid}; HttpOnly; SameSite=Strict; Path=/` });
  }
  const cookie = /theo_session=([a-f0-9]+)/.exec(req.headers.cookie ?? "")?.[1];
  const csrf = cookie ? sessions.get(cookie) : undefined;
  if (!csrf) return err(res, 401, "unauthorized", "no valid session");
  if (m !== "GET" && req.headers["x-theo-csrf"] !== csrf) return err(res, 403, "csrf", "missing or wrong CSRF token");
  if (m !== "GET" && !String(req.headers["content-type"] ?? "").startsWith("application/json")) return err(res, 400, "invalid_request", "Content-Type must be application/json");

  if (seg[0] === "session" && m === "GET") return send(res, 200, { api_version: "1", csrf_token: csrf });
  if (seg[0] === "meta") return send(res, 200, metaOf());

  if (seg[0] === "events" && seg[1] === "focus") { await readJson(req); return send(res, 204, ""); }
  if (seg[0] === "events" && m === "GET") {
    const closed = sseStart(res, req);
    sse.global.add(res);
    const hb = setInterval(() => res.write(": keep-alive\n\n"), 15000);
    req.on("close", () => { clearInterval(hb); sse.global.delete(res); });
    return closed;
  }

  if (seg[0] === "operations") {
    if (seg.length === 1) return send(res, 200, [...S.ops.values()].map(publicOp));
    const o = S.ops.get(seg[1]);
    if (!o) return err(res, 404, "operation_not_found", "operation not found");
    if (seg[2] === "cancel" && m === "POST") { if (o.state === "running") { opState(o, "cancelling"); } return send(res, 202, publicOp(o)); }
    if (seg[2] === "events") {
      const closed = sseStart(res, req);
      const last = Number(req.headers["last-event-id"] ?? 0);
      if (last && last < o.dropped) ev(res, "gap", { dropped: o.dropped - last });
      for (const e of o.lines) if (e.id > last) ev(res, e.event, e.data, e.id);
      if (!["queued", "running", "cancelling"].includes(o.state)) { ev(res, "result", { ok: o.state === "succeeded", summary: o.state }, 1e6); res.end(); return closed; }
      o.subs.add(res); req.on("close", () => o.subs.delete(res));
      return;
    }
    return send(res, 200, publicOp(o));
  }

  if (seg[0] === "fs" && seg[1] === "browse") {
    const p = (q.get("path") || "/home/ana").replace(/\/+$/, "") || "/";
    if (!(p in FS)) return err(res, 404, "invalid_request", `no such folder: ${p}`);
    return send(res, 200, { path: p, parent: p === "/home/ana" ? undefined : path.posix.dirname(p), home: "/home/ana", is_repo: GIT.has(p), truncated: false, entries: FS[p].map((n) => ({ name: n, path: `${p}/${n}`, is_repo: GIT.has(`${p}/${n}`) })) });
  }

  if (seg[0] === "doctor") return send(res, 200, doctor());
  if (seg[0] === "reconcile") return reconcileRoutes(req, res, seg, q);
  if (seg[0] === "health") return send(res, 200, healthOf());
  if (seg[0] === "diagnostics") return send(res, 200, diagnostics());
  if (seg[0] === "proxy") {
    if (!seg[1] && m === "GET") return send(res, 200, proxyStatus());
    if (seg[1] === "start" && m === "POST") { const b = await readJson(req); S.proxy = { running: true, managed: true, port: b.port || 7080, autostart: true, https: !!b.https, https_port: b.https_port || 7443 };
      if (b.https && !S.authority) S.authority = { path: "/home/dev/.theo/proxy/ca.pem", created: new Date().toISOString(), fingerprint: "3F2A…" }; emitGlobal("state.changed", {}); return send(res, 200, proxyStatus()); }
    if ((seg[1] === "trust" || seg[1] === "untrust") && m === "POST") { await readJson(req); S.trusted = seg[1] === "trust"; emitGlobal("state.changed", {}); return send(res, 200, proxyStatus()); }
    if (seg[1] === "stop" && m === "POST") { await readJson(req); S.proxy = { ...S.proxy, running: false, managed: false, autostart: false }; emitGlobal("state.changed", {}); return send(res, 200, proxyStatus()); }
  }
  if (await topLevel(req, res, seg, q)) return;
  if (seg[0] !== "projects") return err(res, 404, "not_found", "unknown resource");
  if (seg.length === 1) {
    if (m === "GET") return send(res, 200, S.projects.map(({ wts, ...p }) => p));
    if (m === "POST") {
      const b = await readJson(req);
      if (!b?.path) return err(res, 400, "invalid_request", "path is required");
      if (!GIT.has(b.path)) return err(res, 422, "engine_error", `${b.path} is not a Git repository`, { hint: "Choose the folder that contains the .git directory." });
      const name = b.name || base(b.path);
      if (findProject(name)) return err(res, 409, "invalid_request", `a project named ${name} already exists`);
      const p = { name, path: b.path, env_file: b.env_file ?? "", added_at: now(), wts: [mkWorktree(name, base(b.path), "main", { main: true })] };
      S.projects.push(p);
      const { wts, ...pub } = p;
      return send(res, 201, pub);
    }
  }
  const proj = findProject(seg[1]);
  if (!proj) return err(res, 404, "project_not_found", `project ${seg[1]} is not registered`);
  const { wts, ...pub } = proj;
  if (seg.length === 2) {
    if (m === "GET") return send(res, 200, pub);
    if (m === "PATCH") { const b = await readJson(req); for (const k of ["env_file", "worktree_dir"]) if (k in b) proj[k] = b[k] ?? ""; for (const k of ["compose_files", "copy_on_add"]) if (k in b) proj[k] = b[k] ?? undefined; if ("inactive_after_days" in b) proj.inactive_after_days = b.inactive_after_days ?? undefined; const { wts: _w, ...out } = proj; return send(res, 200, out); }
    if (m === "DELETE") { S.projects = S.projects.filter((p) => p !== proj); return send(res, 200, { files_kept: true, containers_kept: true }); }
  }
  if (await lifecycleRoutes(req, res, proj, seg)) return;
  if (await projectExtras(req, res, proj, seg, q)) return;
  if (seg[2] === "overview") return send(res, 200, { project: proj.name, path: proj.path, docker: scenario.docker ? { available: true } : { available: false, error: "Cannot connect to the Docker daemon at unix:///var/run/docker.sock" }, worktrees: wts.map(listItem), generated_at: now() });
  if (seg[2] === "shared") return sharedRoutes(req, res, proj, seg);
  if (seg[2] === "branches") {
    const out = wts.filter((w) => w.branch).map((w) => ({ name: w.branch, worktree: w.path }));
    out.push({ name: "develop" }, { name: "release-2" }, { name: "origin/release-3", remote: true });
    return send(res, 200, out);
  }
  if (seg[2] !== "worktrees") return err(res, 404, "not_found", "unknown resource");

  if (seg.length === 3) {
    if (m === "GET") return send(res, 200, wts.map(listItem));
    if (m === "POST") {
      const b = await readJson(req);
      if (!b?.branch) return err(res, 400, "invalid_request", "branch is required");
      if (wts.some((w) => w.branch === b.branch)) return err(res, 409, "branch_checked_out", `branch ${b.branch} is already checked out in another worktree`);
      const dir = base(b.path || `${proj.name}-${b.branch.replace(/[^a-z0-9]+/gi, "-")}`);
      const w = mkWorktree(proj.name, dir, b.branch, { state: "absent" });
      if (b.path) { w.path = b.path; w.id = wtId(b.path); }
      wts.push(w);
      const notes = /noenv/.test(b.branch) ? ["no .env file was found for this worktree; services may start without their variables"] : [];
      return send(res, 201, { path: w.path, branch: w.branch, id: w.id, kind: "worktree", env_file: w.env_file, notes });
    }
  }
  const w = findWt(proj, seg[3]);
  if (!w) return err(res, 404, "worktree_not_found", `worktree ${seg[3]} does not exist`);
  const sub = seg[4];
  if (!sub && m === "GET") { const li = listItem(w); return send(res, 200, { ...li, containers: containersOf(w), container_counts: li.containers, settings: w.settings, expected_services: SERVICES.map((x) => x.name) }); }
  if (sub === "open") {
    if (m === "GET") return send(res, 200, { options: ["terminal", "editor", "files"].map((target) => ({ target, available: true })) });
    if (m === "POST") { const b = (await readJson(req)) ?? {}; return send(res, 200, { opened: true, target: b.target, tool: ({ terminal: "Terminal", editor: "VS Code", files: "Files" })[b.target] ?? b.target }); }
  }
  if (sub === "services") return send(res, 200, { env_file: w.env_file, services: SERVICES.map((s) => (s.name === "web" && w.rt.risky ? { ...s, mounts: [...s.mounts, { type: "bind", source: "/var/run/docker.sock", target: "/var/run/docker.sock" }, { type: "bind", source: "../shared-data", target: "/data", outside_workspace: true }] } : s)) });
  if (sub === "ports") {
    const auto = q.get("auto_ports") === "1";
    const held = heldPorts(w);
    const st = derive(w);
    const ports = portsOf(w).map((p) => {
      const mine = ["running", "partial", "unhealthy"].includes(st) && w.rt.services[p.svc] === "running";
      let status = "free"; let effective = p.configured; let running;
      if (mine) { status = "running"; running = w.rt.hostPorts[p.svc]; effective = running; }
      else if (held.has(p.configured)) {
        if (auto) { effective = p.configured + 1; while (held.has(effective)) effective++; status = "reassigned"; } else status = "in use";
      }
      return { service: p.svc, target: p.target, protocol: "tcp", host_ip: p.ip || "0.0.0.0", configured: String(p.configured), effective, running, status, url: p.svc === "web" ? `http://127.0.0.1:${effective}` : undefined };
    });
    return send(res, 200, { ports });
  }
  if (sub === "config") return send(res, 200, `name: ${w.compose_project}\nservices:\n  web:\n    image: shop/web:dev\n    environment:\n      DATABASE_URL: "••••••"\n`);
  if (await volumeExtras(req, res, proj, w, seg)) return;
  if (sub === "settings") { if (m === "PATCH") { const b = (await readJson(req)) ?? {}; if ("owner" in b) { w.owner = b.owner || undefined; delete b.owner; } Object.assign(w.settings, b); } return send(res, 200, { auto_ports: false, profiles: [], ...w.settings, owner: w.owner, known_profiles: ["debug", "seed"], shared: [...(proj.shared?.links.get(w.id) ?? [])], join: [], fixed_container_names: proj.fixed ?? {} }); }
  if (sub === "shared") return sharedLink(req, res, proj, w, seg);
  if (sub === "history") {
    const all = [...w.history].reverse(); // oldest first, as the engine keeps it
    const end = q.get("before") !== null && Number(q.get("before")) < all.length ? Number(q.get("before")) : all.length;
    const limit = Number(q.get("limit") || 0);
    const start = limit > 0 && end - limit > 0 ? end - limit : 0;
    return send(res, 200, all.slice(start, end).map((h) => ({ project: proj.name, workspace: w.path, compose_project: w.compose_project, ...h })), { "Theo-History-Total": String(all.length), ...(start > 0 ? { "Theo-History-Next": String(start) } : {}) });
  }
  if (sub === "volumes" && !seg[5]) {
    if (!SERVICES.some((s) => s.name === "db")) return send(res, 200, []);
    const st = derive(w);
    const name = `${w.compose_project}_pgdata`;
    if (w.rt.purged) return send(res, 200, [{ name, volume: "pgdata", status: "kept", created: false }, { name: "shared-cache", volume: "cache", status: "external", containers: [], created: true }]);
    return send(res, 200, [{ name, volume: "pgdata", status: st === "absent" || st === "stopped" ? "kept" : "in use", containers: st === "absent" ? [] : [`${w.compose_project}-db-1`], created: true }, { name: "shared-cache", volume: "cache", status: "external", containers: [], created: true }]);
  }
  if (sub === "volumes" && seg[5] === "purge" && m === "POST") {
    const b = await readJson(req);
    if (Object.keys(w.rt.services).length) return err(res, 409, "instance_has_containers", "the instance still has containers; run down first");
    const exp = w.compose_project;
    const info = { code: "confirmation_required", level: 3, message: `this deletes the data of 1 volume(s) of ${exp} for good; nothing was deleted`, resource: { kind: "instance", project: proj.name, worktree: w.id }, confirm: { expected: exp, prompt: "Type the instance name to delete its data" }, affected: [{ kind: "volume", name: `${exp}_pgdata`, detail: "kept after down" }], preserved: ["external volumes", "other instances", "the branch and the worktree"] };
    if (b?.confirm !== exp) return send(res, 409, { error: info });
    w.rt.purged = true; w.history.unshift({ time: now(), user: "ana", operation: "volumes purge", result: "ok" });
    return send(res, 200, { deleted: [`${exp}_pgdata`] });
  }
  if (sub === "remove" && m === "POST") {
    const b = await readJson(req);
    if (w.main) return err(res, 409, "invalid_request", "the main worktree cannot be removed");
    if (Object.keys(w.rt.services).length) return err(res, 409, "instance_has_containers", `${w.compose_project} still has containers`, { hint: "Run down first." });
    if (w.dirty) {
      const name = base(w.path);
      if (b?.confirm !== name) return send(res, 409, { error: { code: "confirmation_required", level: 3, message: `${name} has uncommitted changes; removing it deletes them for good; nothing was removed`, confirm: { expected: name, prompt: "Type the folder name to remove the worktree and its changes" }, affected: [{ kind: "file", name: "M  src/server.ts", detail: "modified" }, { kind: "file", name: "?? notes.txt", detail: "untracked" }], preserved: ["the branch", "other worktrees", "the project's main folder"] } });
    }
    proj.wts = proj.wts.filter((x) => x !== w);
    return send(res, 200, { removed: w.path });
  }
  if (sub === "env") {
    const name = seg[5];
    if (!name && m === "GET") return send(res, 200, Object.entries(w.env).map(([n, v]) => ({ name: n, value: v.secret ? "********" : v.value, secret: !!v.secret, services: v.services ?? [] })));
    if (name && seg[6] === "reveal" && m === "POST") { const v = w.env[name]; if (!v) return err(res, 404, "invalid_request", "no such variable"); return send(res, 200, { name, secret: !!v.secret, value: v.value }); }
    if (name && m === "PUT") { const b = await readJson(req); w.env[name] = { value: b.value, secret: !!b.secret, services: b.services ?? [] }; return send(res, 200, { applies_on: "next_up" }); }
    if (name && m === "DELETE") { delete w.env[name]; return send(res, 200, { applies_on: "next_up" }); }
  }
  if (["up", "down", "restart", "stop", "start", "recreate"].includes(sub) && m === "POST") {
    const b = await readJson(req);
    const key = req.headers["idempotency-key"];
    if (key && S.idem.has(key)) return send(res, 202, publicOp(S.ops.get(S.idem.get(key))), { Location: `/api/v1/operations/${S.idem.get(key)}` });
    const o = startOp(proj, w, sub, b, res);
    if (!o || res.writableEnded) return;
    if (key) S.idem.set(key, o.id);
    return send(res, 202, publicOp(o), { Location: `/api/v1/operations/${o.id}` });
  }
  if (sub === "logs") return logs(req, res, w, q);
  return err(res, 404, "not_found", "unknown resource");
}

async function logs(req, res, w, q) {
  const follow = q.get("follow") === "1" || q.get("follow") === "true";
  const tailQ = q.get("tail");
  const services = q.getAll("service");
  const svcs = services.length ? services : SERVICES.map((s) => s.name);
  const total = tailQ === "all" ? 1000 : Math.min(Number(tailQ ?? 200), 60000);
  const mk = (i) => { const s = svcs[i % svcs.length]; return { service: s, line: `${s} | request ${i} handled in ${(i * 7) % 90}ms${i % 97 === 0 ? " \u001b[31mERROR\u001b[0m something odd happened" : ""}`, at: new Date(Date.now() - (total - i) * 1000).toISOString(), stream: i % 97 === 0 ? "stderr" : "stdout" }; };
  if (!follow) {
    let t = ""; for (let i = 0; i < total; i++) { const l = mk(i); t += `${l.at} ${l.service} | ${l.line}\n`; }
    return send(res, 200, t, { "Content-Disposition": `attachment; filename="${w.compose_project}.log"` });
  }
  const closed = sseStart(res, req);
  let id = 0;
  for (let i = 0; i < total; i += 1000) {
    let chunk = "";
    for (let j = i; j < Math.min(total, i + 1000); j++) chunk += `id: ${++id}\nevent: log\ndata: ${JSON.stringify(mk(j))}\n\n`;
    res.write(chunk);
    if (closed()) return;
    await sleep(1);
  }
  if (scenario.gap) ev(res, "gap", { dropped: 321 }, ++id);
  let n = total;
  const t = setInterval(() => { if (closed()) { clearInterval(t); return; } ev(res, "log", { ...mk(n), at: now(), line: `${svcs[n % svcs.length]} | live line ${n++}` }, ++id); }, 500);
  req.on("close", () => clearInterval(t));
}

function metaOf() {
  return { theo_version: "0.0.0-mock", api_version: "v1", os: "linux", arch: "amd64", data_dir: "/home/ana/.local/share/theo", idle: { timeout_seconds: 900 },
    git: { available: true, version: "2.43.0", min_version: "2.17.0" }, compose: { available: true, version: scenario.composeOld ? "2.20.0" : "2.29.7", min_version: "2.24.4" },
    docker: scenario.docker ? { available: true, version: "27.3.1" } : { available: false, error: "Cannot connect to the Docker daemon at unix:///var/run/docker.sock", hint: "Start Docker Desktop or the docker service, then press Refresh." } };
}
// ---------------------------------------------------------------- shared services, reconcile, doctor
const sharedName = (proj) => `${proj.name}-shared`;
function sharedStatus(proj) {
  const sh = proj.shared;
  return {
    project: proj.name, compose_project: sharedName(proj), from: sh.from, network: `theo-${proj.name}-shared`, publish_ports: sh.publish,
    services: Object.entries(sh.services).map(([name, v]) => ({ name, image: v.image, state: v.state })),
    consumers: [...sh.links.entries()].filter(([, set]) => set.size).map(([id, set]) => {
      const w = findWt(proj, id);
      return { workspace: w.path, branch: w.branch || undefined, compose_project: w.compose_project, services: [...set], active: ["running", "partial", "unhealthy"].includes(derive(w)) };
    }),
    warnings: scenario.sharedWarning ? ["this worktree defines db with image postgres:15, but the shared one runs postgres:16"] : undefined,
  };
}
const consumerList = (proj, onlyActive = false) => sharedStatus(proj).consumers.filter((c) => !onlyActive || c.active).map((c) => ({ kind: "instance", name: c.branch || base(c.workspace), detail: `${c.services.join(", ")} (${c.active ? "has running containers" : "stopped"})` }));
const wtOf = (proj) => proj.wts.find((w) => w.main) ?? proj.wts[0];

async function sharedRoutes(req, res, proj, seg) {
  const m = req.method;
  const sh = proj.shared;
  const need = () => err(res, 404, "shared_not_configured", `${proj.name} has no shared services`, { hint: `Declare one with: theo shared add ${proj.name} <service>` });
  if (seg.length === 3 && m === "GET") return sh ? send(res, 200, sharedStatus(proj)) : need();
  if (seg.length === 3 && m === "POST") {
    const b = await readJson(req);
    const names = (b?.services ?? []).filter((x) => /^[a-z][a-z0-9_-]*$/i.test(x));
    if (names.length === 0 || names.length !== (b.services ?? []).length) return err(res, 400, "invalid_request", "services must be Compose service names");
    const unknown = names.filter((n) => !SERVICES.some((x) => x.name === n));
    if (unknown.length) return err(res, 422, "engine_error", `the project has no service ${unknown[0]}`, { hint: "Pick one of: " + SERVICES.map((x) => x.name).join(", ") });
    if (!proj.shared) proj.shared = { from: wtOf(proj).path, publish: false, services: {}, links: new Map() };
    if (b.publish) proj.shared.publish = true;
    for (const n of names) proj.shared.services[n] = proj.shared.services[n] ?? { image: SERVICES.find((x) => x.name === n).image, state: "not created" };
    return send(res, 201, sharedStatus(proj));
  }
  if (!sh) return need();
  if (seg[3] === "up" && m === "POST") return sharedOp(res, proj, "up");
  if (seg[3] === "down" && m === "POST") {
    const b = await readJson(req);
    const users = consumerList(proj, true);
    if (users.length && b?.confirm !== sharedName(proj)) {
      return send(res, 409, { error: { code: "confirmation_required", level: 2, message: `${users.length} running instance(s) use the shared services; stopping them leaves those instances without the service. Nothing was stopped.`, confirm: { expected: sharedName(proj), prompt: `Type ${sharedName(proj)} to stop them anyway` }, affected: users, preserved: ["Volumes and data of the shared services"] } });
    }
    return sharedOp(res, proj, "down");
  }
  if (seg[3] === "services" && seg[4] && m === "DELETE") {
    if (!sh.services[seg[4]]) return err(res, 404, "not_found", `${seg[4]} is not a shared service`);
    const using = [...sh.links.values()].some((set) => set.has(seg[4]));
    if (using) return send(res, 409, { error: { code: "shared_in_use", message: `${seg[4]} is used by instances; stop using it there first`, affected: consumerList(proj) } });
    if (sh.services[seg[4]].state !== "not created") return err(res, 409, "shared_running", `${seg[4]} still has containers; stop the shared services first`);
    delete sh.services[seg[4]];
    if (Object.keys(sh.services).length === 0) proj.shared = null;
    return send(res, 200, { removed: seg[4] });
  }
  if (seg[3] === "purge" && m === "POST") {
    const b = await readJson(req);
    if (sharedStatus(proj).consumers.length) return send(res, 409, { error: { code: "shared_in_use", message: "instances are linked to the shared services; nothing was deleted", affected: consumerList(proj), hint: "Stop using them in those instances first." } });
    if (Object.values(sh.services).some((v) => v.state === "running")) return err(res, 409, "invalid_request", "the shared services are running; stop them first");
    const exp = sharedName(proj);
    if (b?.confirm !== exp) return send(res, 409, { error: { code: "confirmation_required", level: 3, message: `this deletes the data of the shared services of ${proj.name} for good; nothing was deleted`, confirm: { expected: exp, prompt: `Type ${exp} to delete the data` }, affected: [{ kind: "volume", name: `${exp}_pgdata`, detail: "database files" }], preserved: ["Worktrees, branches and the instances' own volumes"] } });
    return send(res, 200, { deleted: [`${exp}_pgdata`] });
  }
  return err(res, 404, "not_found", "unknown resource");
}

async function sharedLink(req, res, proj, w, seg) {
  const sh = proj.shared;
  if (!sh) return err(res, 404, "shared_not_configured", `${proj.name} has no shared services`);
  const set = sh.links.get(w.id) ?? new Set();
  if (req.method === "PUT") {
    const b = await readJson(req);
    for (const n of b?.use ?? []) { if (!sh.services[n]) return err(res, 422, "engine_error", `${n} is not a shared service of ${proj.name}`); set.add(n); }
    sh.links.set(w.id, set);
    emitGlobal("state.changed", { project: proj.name, worktree: w.id });
    return send(res, 200, { use: [...set], join: b?.join ?? [] });
  }
  if (req.method === "DELETE" && seg[5]) { set.delete(seg[5]); sh.links.set(w.id, set); emitGlobal("state.changed", { project: proj.name, worktree: w.id }); return send(res, 200, { use: [...set] }); }
  return err(res, 404, "not_found", "unknown resource");
}

function sharedOp(res, proj, kind) {
  if (!scenario.docker) return err(res, 412, "docker_unavailable", "Cannot connect to the Docker daemon", { hint: "Start Docker and try again." });
  const act = [...S.ops.values()].find((o) => o.project === proj.name && !o.worktree && ["queued", "running", "cancelling"].includes(o.state));
  if (act) return err(res, 409, "busy", `another operation (${act.kind}) is running on the shared services`, { operation: { id: act.id, kind: act.kind } });
  const o = { id: "op_" + String(++S.opSeq).padStart(4, "0") + crypto.randomBytes(3).toString("hex"), kind: `shared.${kind}`, project: proj.name, state: "running", started_at: now(), cancellable: true, lines: [], subs: new Set(), dropped: 0 };
  S.ops.set(o.id, o);
  opState(o, "running");
  (async () => {
    try {
      for (const [n, v] of Object.entries(proj.shared.services)) {
        opLog(o, ` Container ${proj.name}-shared-${n}-1  ${kind === "up" ? "Starting" : "Stopping"}`);
        await sleep(350 * scenario.slow);
        v.state = kind === "up" ? "running" : "exited";
      }
      opState(o, "succeeded", { finished_at: now(), result: { ok: true, summary: `shared ${kind} finished` } });
    } catch (e) { opState(o, "failed", { finished_at: now(), error: { code: "internal", message: String(e) } }); }
    for (const r of o.subs) { ev(r, "result", { ok: o.state === "succeeded", summary: o.state }, o.lines.length + 1 + o.dropped); r.end(); }
    o.subs.clear();
    emitGlobal("state.changed", { project: proj.name });
  })();
  return send(res, 202, publicOp(o), { Location: `/api/v1/operations/${o.id}` });
}

const fid = (f) => "fd_" + crypto.createHash("sha1").update([f.kind, f.project ?? "", f.subject, f.worktree ?? ""].join("|")).digest("hex").slice(0, 12);
const withId = (f) => ({ id: fid(f), ...f });
function reportFor(proj) {
  let findings = S.findings.filter((f) => !proj || f.project === proj);
  if (!scenario.docker) return { findings: findings.filter((f) => !/^orphan|containers removed/.test(f.kind)).map(withId), docker_error: "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?" };
  if (scenario.healthy) findings = [];
  return { findings: findings.map(withId) };
}
function healthOf() {
  const r = reportFor("");
  return { status: r.docker_error ? "docker_unavailable" : r.findings.length ? "attention" : "ok", findings: r.findings.length, forgettable: r.findings.filter((f) => f.forgettable).length, ...(r.docker_error ? { docker_error: r.docker_error } : {}) };
}
async function reconcileRoutes(req, res, seg, q) {
  if (seg[1] === "apply" && req.method === "POST") {
    const b = await readJson(req);
    if (!b?.all && !(b?.ids ?? []).length) return err(res, 400, "invalid_request", "say which findings to forget: ids, or all: true");
    const before = reportFor(b.project ?? "").findings;
    const want = new Set(b.ids ?? []);
    const hit = before.filter((f) => f.forgettable && (b.all || want.has(f.id)));
    const applied = hit.map((f) => f.id);
    const skipped = (b.ids ?? []).filter((id) => !applied.includes(id));
    S.findings = S.findings.filter((f) => !applied.includes(fid(f)));
    S.forgotten.push(...applied);
    emitGlobal("state.changed", {});
    return send(res, 200, { findings: hit, forgotten: hit.length, applied, skipped });
  }
  if (req.method !== "GET") return err(res, 404, "not_found", "unknown resource");
  return send(res, 200, { ...reportFor(q.get("project") ?? ""), forgotten: 0 });
}

function diagnostics() {
  const doc = doctor();
  const report = `Theo 0.0.0-mock (API v1)\nPlatform: linux/amd64\nData directory: ${doc.data_dir}\nProjects: ${S.projects.length}\nDocker: ${scenario.docker ? "27.3.1" : "not available"}\nDocker Compose: ${scenario.composeOld ? "2.20.0" : "2.29.7"} (Theo needs 2.24.4 or later)\nGit: 2.43.0 (Theo needs 2.17.0 or later)\n\nChecks:\n` + doc.checks.map((c) => `- [${c.status}] ${c.name}${c.detail ? ": " + c.detail : ""}`).join("\n") + "\n";
  return { meta: metaOf(), doctor: doc, projects: S.projects.length, report };
}

function doctor() {
  const checks = [
    { id: "git", name: "Git", status: "ok", detail: "git version 2.43.0" },
    { id: "docker", name: "Docker CLI", status: "ok", detail: "Docker version 27.3.1" },
    scenario.docker ? { id: "daemon", name: "Docker engine", status: "ok", detail: "27.3.1 (linux containers)" } : { id: "daemon", name: "Docker engine", status: "fail", detail: "Cannot connect to the Docker daemon", fix: "Start the Docker service (systemctl start docker) or Docker Desktop." },
    scenario.composeOld ? { id: "compose", name: "Docker Compose", status: "fail", detail: "2.20.0 is older than the minimum 2.24.4", fix: "Upgrade the Docker Compose plugin to 2.24.4 or later." } : { id: "compose", name: "Docker Compose", status: "ok", detail: "2.29.7 (minimum 2.24.4)" },
    { id: "datadir", name: "Theo data directory", status: "ok", detail: "writable" },
    scenario.windows ? { id: "wsl", name: "Project location", status: "warn", detail: "projects outside WSL 2 mount slowly", fix: "Keep projects in the WSL 2 file system." } : { id: "state", name: "Theo state files", status: "ok" },
  ];
  return { ok: !checks.some((c) => c.status === "fail"), platform: scenario.windows ? "windows" : "linux", data_dir: "/home/ana/.local/share/theo", checks };
}

// ---------------------------------------------------------------- server
const server = http.createServer(async (req, res) => {
  stats.requests++;
  const url = new URL(req.url, "http://x");
  const port = server.address().port;
  if (url.pathname.startsWith("/__mock/")) {
    if (url.pathname === "/__mock/reset") { reset(); return send(res, 200, { ok: true, launch: launchUrl(port) }); }
    if (url.pathname === "/__mock/scenario") { Object.assign(scenario, await readJson(req)); if (scenario.empty) S.projects = []; if (scenario.sharedEmpty && S.projects[0]) S.projects[0].shared = null; return send(res, 200, scenario); }
    if (url.pathname === "/__mock/stats") return send(res, 200, { sse: sse.count, maxSse: sse.max, ...stats });
    if (url.pathname === "/__mock/launch") { nonce = "mocknonce" + crypto.randomBytes(8).toString("hex"); nonceAt = Date.now(); return send(res, 200, { url: launchUrl(port) }); }
    return err(res, 404, "not_found", "unknown mock hook");
  }
  if (req.headers.host !== `127.0.0.1:${port}`) return err(res, 403, "forbidden_origin", "unexpected Host header");
  if (url.pathname.startsWith("/api/")) {
    const o = req.headers.origin;
    if (o && o !== `http://127.0.0.1:${port}`) return err(res, 403, "forbidden_origin", "unexpected Origin");
    const sfs = req.headers["sec-fetch-site"];
    if (sfs && sfs !== "same-origin" && sfs !== "none") return err(res, 403, "forbidden_origin", "cross-site request");
    try { return await api(req, res, url); } catch (e) { console.error(e); return err(res, 500, "internal", "internal error", { request_id: "mock" }); }
  }
  // Static files (no cookie needed, they contain no data)
  let rel = url.pathname === "/" ? "/index.html" : url.pathname;
  const file = path.join(distDir, path.normalize(rel).replace(/^(\.\.[/\\])+/, ""));
  if (!file.startsWith(distDir) || !fs.existsSync(file) || fs.statSync(file).isDirectory()) return err(res, 404, "not_found", "not found");
  res.writeHead(200, { "Content-Type": MIME[path.extname(file)] ?? "application/octet-stream", "Content-Security-Policy": CSP, "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "Cross-Origin-Opener-Policy": "same-origin", "Cache-Control": "no-cache" });
  fs.createReadStream(file).pipe(res);
});

const launchUrl = (port) => `http://127.0.0.1:${port}/#n=${nonce}`;
const argPort = process.argv.includes("--port") ? Number(process.argv[process.argv.indexOf("--port") + 1]) : 0;
server.listen(argPort, "127.0.0.1", () => {
  const port = server.address().port;
  console.log(JSON.stringify({ url: launchUrl(port), port, pid: process.pid }));
});
