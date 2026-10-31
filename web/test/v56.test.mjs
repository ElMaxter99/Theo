// Pure logic of the V5/V6 screens: routes, findings, report text, view preferences, strings.
import { test } from "node:test";
import assert from "node:assert/strict";
import { parseHash, href } from "../src/lib/router.ts";
import { findingId, groupFindings } from "../src/lib/findings.ts";
import { reportText } from "../src/lib/report.ts";
import { normKind } from "../src/ops/kind.ts";
import { t } from "../src/strings.ts";

test("routes of shared services, health and settings parse and build consistently", () => {
  assert.deepEqual(parseHash("#/health"), { name: "health" });
  assert.deepEqual(parseHash("#/settings"), { name: "settings" });
  assert.deepEqual(parseHash(href.shared("my shop")), { name: "shared", project: "my shop" });
  assert.deepEqual(parseHash("#/p/shop/shared"), { name: "shared", project: "shop" });
  assert.deepEqual(parseHash("#/health/extra"), { name: "notfound" });
  assert.deepEqual(parseHash("#/p/shop/shared/extra"), { name: "notfound" });
});

test("finding ids are stable, prefer the server's id and group by kind in order", () => {
  const a = { kind: "stale ports", project: "shop", subject: "/x", worktree: "/x", detail: "d", forgettable: true };
  assert.equal(findingId(a), "stale ports|shop|/x|/x");
  assert.equal(findingId({ ...a, id: "f_1" }), "f_1");
  assert.equal(findingId({ kind: "orphan volumes", subject: "v", detail: "d" }), "orphan volumes||v|");
  const g = groupFindings([a, { kind: "orphan volumes", subject: "v", detail: "d" }, { ...a, subject: "/y" }]);
  assert.deepEqual(g.map((x) => [x.kind, x.items.length]), [["stale ports", 2], ["orphan volumes", 1]]);
  assert.deepEqual(groupFindings([]), []);
});

test("every finding kind the engine reports has a readable title", () => {
  for (const k of ["project missing", "worktree missing", "started outside Theo", "containers removed", "orphan containers", "orphan volumes", "stale settings", "stale ports", "stale files", "shared services broken"]) {
    assert.ok(t.reconcile.kinds[k], k);
  }
});

test("the report for support has versions and checks but no paths", () => {
  const text = reportText(
    { theo_version: "1.2.3", api_version: "v1", os: "linux", arch: "amd64", data_dir: "/home/ana/.local/share/theo", docker: { available: true, version: "27.3.1" }, git: { available: true, version: "2.43.0" } },
    { ok: false, platform: "linux", data_dir: "/home/ana/.local/share/theo", checks: [{ id: "compose", name: "Docker Compose", status: "fail", detail: "2.20.0 is older than 2.24.4", fix: "Upgrade" }] },
  );
  assert.match(text, /Theo 1\.2\.3 \(API v1\)/);
  assert.match(text, /\[fail\] Docker Compose: 2\.20\.0 is older than 2\.24\.4/);
  assert.doesNotMatch(text, /\/home\/ana/);
  assert.match(reportText(undefined, undefined), /Theo unknown/);
});

test("paths inside the details of a check never reach the report", () => {
  const text = reportText(undefined, { ok: true, platform: "windows", data_dir: "C:\\Users\\Ana\\theo", checks: [
    { id: "data-dir", name: "Theo data directory", status: "ok", detail: "/home/ana/.local/share/theo" },
    { id: "w", name: "Windows long paths", status: "warn", detail: "the data directory path is long (C:\\Users\\Ana\\AppData\\Roaming\\theo)" },
    { id: "m", name: "macOS file sharing", status: "warn", detail: "projects outside the folders: /opt/a, /srv/b" },
    { id: "d", name: "Docker daemon", status: "fail", detail: "cannot reach the Docker daemon: unix:///var/run/docker.sock; see https://docs.docker.com/engine/install/" },
  ] });
  assert.doesNotMatch(text, /ana|Ana|\/opt|\/srv|docker\.sock/);
  assert.match(text, /https:\/\/docs\.docker\.com\/engine\/install\//);
  assert.match(text, /\[ok\] Theo data directory: <path>/);
});

test("operation kinds of the shared services get labels whatever the separator", () => {
  assert.equal(normKind("shared_up"), "shared-up");
  assert.equal(normKind("shared.down"), "shared-down");
  assert.ok(t.ops.kind["shared-up"] && t.ops.kind["shared-down"]);
});

test("texts that carry the safety rules are present", () => {
  assert.match(t.reconcile.fixedNote, /does not delete containers, volumes or worktrees/);
  assert.match(t.wizard.neutralize, /Neutralize fixed container names for this worktree/);
  assert.equal(t.nav.healthCount(0), "Health: no findings");
  assert.equal(t.nav.healthCount(2), "Health: 2 findings");
});
