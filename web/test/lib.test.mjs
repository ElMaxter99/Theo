// Pure logic: SSE parsing, status table, port clash derivation, log store and virtual window, routing, formatting.
import { test } from "node:test";
import assert from "node:assert/strict";
import { SseParser } from "../src/api/sse.ts";
import { stateView, portStatusView, volumeStatusView, operationStateView, containerCounts, isActiveOp, opErrorText, busyLabel } from "../src/lib/status.ts";
import { clashesForNewInstance, hasFixedPorts } from "../src/lib/ports.ts";
import { LogStore, visibleRange, MAX_STORED } from "../src/lib/logstore.ts";
import { parseHash, href } from "../src/lib/router.ts";
import { stripAnsi, folderForBranch, suggestName, validBranchName, validEnvName, parseList, splitMatches, basename } from "../src/lib/format.ts";
import { urlsOf, conflictsOf, shortSha } from "../src/lib/overview.ts";
import { t } from "../src/strings.ts";

test("SSE parser: events, ids, comments, multi-line data and chunk boundaries", () => {
  const p = new SseParser();
  const out = [];
  const feed = (s) => out.push(...p.push(s));
  feed(": keep-alive\n\nid: 1\nevent: log\ndata: {\"a\":1}\n\n");
  feed("event: state\ndata: line1\ndata: line2\n");
  feed("\nid: 7\ndata: x\r\n\r\n");
  feed("data: split");
  feed("ted\n\n");
  assert.deepEqual(out.map((e) => [e.event, e.id, e.data]), [
    ["log", "1", '{"a":1}'],
    ["state", "1", "line1\nline2"],
    ["message", "7", "x"],
    ["message", "7", "split" + "ted"],
  ]);
});

test("SSE parser: a lone CR at the end of a chunk waits for the LF", () => {
  const p = new SseParser();
  assert.deepEqual(p.push("data: a\r"), []);
  assert.equal(p.push("\n\r\n").length, 1);
});

test("every instance state has text, an icon and a tone; shapes are distinct", () => {
  const states = ["running", "partial", "unhealthy", "stopped", "absent", "unknown"];
  const views = states.map((s) => stateView(s));
  for (const v of views) { assert.ok(v.label.length > 0); assert.ok(v.icon); }
  assert.equal(new Set(views.map((v) => v.icon)).size, states.length, "distinct icon per state");
  assert.equal(new Set(views.map((v) => v.label)).size, states.length, "distinct text per state");
  assert.equal(stateView("partial", { running: 2, total: 3 }).label, "Partial (2/3)");
  assert.equal(stateView("whatever").label, "Unknown");
});

test("a busy instance shows the operation instead of the state", () => {
  assert.equal(stateView("stopped", null, "up").label, "Starting…");
  assert.equal(stateView("running", null, "down").label, "Stopping…");
  assert.equal(stateView("running", null, "restart").label, "Restarting…");
  assert.equal(busyLabel("build"), "Working…");
  assert.equal(stateView("stopped", null, "up").spin, true);
});

test("port, volume and operation states map to text and icons", () => {
  for (const s of ["free", "running", "in use", "reassigned", "kept", "random", "range"]) assert.ok(portStatusView(s).label.length > 0);
  assert.equal(portStatusView("in use").icon, "portbusy");
  assert.equal(portStatusView("in use").label, "In use by another program");
  assert.equal(volumeStatusView("external").label, "External");
  for (const s of ["queued", "running", "succeeded", "failed", "cancelling", "cancelled"]) assert.ok(operationStateView({ state: s }).label.length > 0);
  assert.equal(isActiveOp({ state: "cancelling" }), true);
  assert.equal(isActiveOp({ state: "failed" }), false);
  assert.equal(opErrorText({ error: { message: "boom" } }), "boom");
  assert.equal(opErrorText({ error: "plain" }), "plain");
  assert.equal(opErrorText({}), "");
});

test("containerCounts accepts counts and container lists", () => {
  assert.deepEqual(containerCounts({ containers: { running: 1, total: 2 } }), { running: 1, total: 2 });
  assert.deepEqual(containerCounts({ containers: [{ State: "running" }, { State: "exited" }, { State: "running" }] }), { running: 2, total: 3 });
  assert.equal(containerCounts({}), null);
  assert.deepEqual(containerCounts({ containers: [], container_counts: { running: 1, total: 4 } }), { running: 1, total: 4 }, "detail counts win");
});

test("port clashes for a new instance come from fixed host ports that are taken", () => {
  const plan = { ports: [
    { service: "web", target: 3000, configured: "3000", status: "running", running: 3000 },
    { service: "db", target: 5432, configured: "5432", status: "in use" },
    { service: "cache", target: 6379, configured: "6379", status: "free" },
    { service: "rnd", target: 80, configured: "", status: "random" },
    { service: "rng", target: 81, configured: "8000-8010", status: "in use" },
  ] };
  assert.deepEqual(clashesForNewInstance(plan), [
    { service: "web", port: 3000, who: "instance" },
    { service: "db", port: 5432, who: "program" },
  ]);
  assert.equal(hasFixedPorts(plan), true);
  assert.equal(hasFixedPorts({ ports: [{ service: "x", target: 1, configured: "", status: "random" }] }), false);
  assert.deepEqual(clashesForNewInstance(undefined), []);
});

test("urls and conflicts are derived from the overview ports", () => {
  const w = { state: "partial", ports: [
    { service: "web", container_port: 80, host_port: 3001, url: "http://localhost:3001", status: "running" },
    { service: "db", container_port: 5432, host_port: 5432, status: "in use" },
    { service: "x", container_port: 1, host_port: 9, url: "http://localhost:9", status: "free" },
  ] };
  assert.deepEqual(urlsOf(w), [{ service: "web", url: "http://localhost:3001", port: 3001 }]);
  // A running instance started with automatic ports reports "kept": still a live link. A partial one does not.
  const auto = { state: "running", ports: [{ service: "web", container_port: 80, host_port: 3003, url: "http://localhost:3003", status: "kept" }] };
  assert.equal(urlsOf(auto).length, 1);
  assert.equal(urlsOf({ ...auto, state: "partial" }).length, 0);
  assert.equal(urlsOf({ ...auto, state: "stopped" }).length, 0);
  assert.deepEqual(conflictsOf(w), [{ service: "db", port: 5432 }]);
  assert.deepEqual(conflictsOf({ ...w, state: "running" }), []);
  assert.equal(shortSha("0123456789abcdef"), "0123456");
});

test("log store: ANSI stripped, bounded memory, filtering", () => {
  const s = new LogStore();
  s.push({ text: "\u001b[31mERROR\u001b[0m boom", svc: "web" });
  s.push({ text: "all fine", svc: "db" });
  s.push({ text: "… dropped …", gap: true });
  assert.equal(s.lines[0].text, "ERROR boom");
  assert.deepEqual(s.filter("error", 100), [0, 2], "gap markers always stay visible");
  assert.deepEqual(s.filter("db", 100), [1, 2], "service name is searchable");
  assert.deepEqual(s.filter("", 2), [0, 1]);
  for (let i = 0; i < MAX_STORED + 5; i++) s.push({ text: "x" + i });
  assert.ok(s.lines.length <= MAX_STORED, "stored lines are bounded: " + s.lines.length);
  assert.ok(s.total > MAX_STORED);
});

test("virtual window never exceeds the maximum and follows the scroll position", () => {
  assert.deepEqual(visibleRange(0, 400, 20, 0), [0, 0]);
  assert.deepEqual(visibleRange(0, 400, 20, 1000, 0), [0, 20]);
  assert.deepEqual(visibleRange(2000, 400, 20, 1000, 5), [95, 125]);
  const [a, b] = visibleRange(0, 1e9, 20, 50000);
  assert.ok(b - a <= 400);
  const [, end] = visibleRange(10_000_000, 400, 20, 50000);
  assert.ok(end <= 50000);
});

test("routes parse and build consistently", () => {
  assert.deepEqual(parseHash(""), { name: "projects" });
  assert.deepEqual(parseHash("#/projects/new"), { name: "add" });
  assert.deepEqual(parseHash("#/p/shop"), { name: "project", project: "shop" });
  assert.deepEqual(parseHash("#/p/shop/new"), { name: "wizard", project: "shop" });
  assert.deepEqual(parseHash(href.detail("my shop", "wt_1", "logs")), { name: "detail", project: "my shop", wt: "wt_1", tab: "logs" });
  assert.deepEqual(parseHash("#/p/shop/w/wt_1/nonsense"), { name: "detail", project: "shop", wt: "wt_1", tab: "summary" });
  assert.deepEqual(parseHash("#/nope"), { name: "notfound" });
  assert.deepEqual(parseHash("#/p/%E0%A4%A"), { name: "project", project: "%E0%A4%A" }, "malformed escapes do not throw");
});

test("formatting helpers", () => {
  assert.equal(stripAnsi("\u001b[1;32mok\u001b[0m \u001b]0;title\u0007done"), "ok done");
  assert.equal(folderForBranch("feature/Login page"), "feature-Login-page");
  assert.equal(suggestName("/home/ana/code/My Shop/"), "my-shop");
  assert.equal(basename("C:\\code\\shop"), "shop");
  assert.equal(validBranchName("fix-123"), true);
  assert.equal(validBranchName("feature/login"), true);
  for (const bad of ["", "has space", "a..b", "-x", "a:b", "x.lock", "a//b", "/a", "a/"]) assert.equal(validBranchName(bad), false, bad);
  assert.equal(validEnvName("DATABASE_URL"), true);
  assert.equal(validEnvName("1X"), false);
  assert.deepEqual(parseList(" a, b ,,c"), ["a", "b", "c"]);
  assert.deepEqual(splitMatches("Hello hello", "hel"), [{ text: "Hel", match: true }, { text: "lo ", match: false }, { text: "hel", match: true }, { text: "lo", match: false }]);
  assert.deepEqual(splitMatches("x", ""), [{ text: "x", match: false }]);
});

test("the string catalog has no empty texts", () => {
  const walk = (o, path) => {
    for (const [k, v] of Object.entries(o)) {
      if (typeof v === "string") assert.ok(v.length > 0, `${path}.${k} is empty`);
      else if (Array.isArray(v)) v.forEach((x) => assert.ok(String(x).length > 0));
      else if (v && typeof v === "object") walk(v, `${path}.${k}`);
    }
  };
  walk(t, "t");
});
