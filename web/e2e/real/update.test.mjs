// Updating Theo from its own interface, against the REAL `theo serve`: a copy of the real program is "installed" in a
// folder of its own, a local server plays GitHub (the list of releases, the archive, checksums.txt), and the page
// checks, offers release candidates only when asked, downloads, verifies and replaces the file on disk.
// It needs no Docker. What it cannot do is restart the desktop window (that is the desktop app's part).
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import crypto from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, copyFileSync, readdirSync, chmodSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { chromium, openApp, shot, startServer, rmrf, webDir } from "./real-helpers.mjs";

const repoRoot = path.resolve(webDir, "..");
const skip = process.platform === "win32" ? "the archive of this test is a .tar.gz; Windows uses a .zip" : false;
let browser, server, fake, root, installed, newProgram;
const adapter = { launchUrl: () => server.launch() };
const requests = [];

before(async () => {
  if (skip) return;
  root = mkdtempSync(path.join(tmpdir(), "theo-upd-"));
  // The real program, built as version 0.11.0 and installed where the update can replace it.
  const bin = path.join(root, "build", "theo");
  mkdirSync(path.dirname(bin), { recursive: true });
  execFileSync("go", ["build", "-ldflags", "-X github.com/ElMaxter99/Theo/internal/engine.Version=0.11.0", "-o", bin, "./cmd/theo"], { cwd: repoRoot, stdio: "inherit" });
  const dir = path.join(root, "installed");
  mkdirSync(dir);
  installed = path.join(dir, "theo");
  copyFileSync(bin, installed);
  chmodSync(installed, 0o755);

  // The "new version": a tiny program, packed the way a release packs it.
  newProgram = "#!/bin/sh\necho theo v0.11.1 (replaced by the update)\n";
  const pack = path.join(root, "pack", "theo_v0.11.1_" + (process.platform === "darwin" ? "darwin" : "linux") + "_" + (process.arch === "arm64" ? "arm64" : "amd64"));
  mkdirSync(pack, { recursive: true });
  writeFileSync(path.join(pack, "theo"), newProgram, { mode: 0o755 });
  const name = path.basename(pack) + ".tar.gz";
  execFileSync("tar", ["-C", path.dirname(pack), "-czf", path.join(root, name), path.basename(pack)]);
  const archive = readFileSync(path.join(root, name));
  const sum = crypto.createHash("sha256").update(archive).digest("hex");

  fake = http.createServer((req, res) => {
    requests.push(req.url);
    const origin = `http://127.0.0.1:${fake.address().port}`;
    if (req.url.startsWith("/releases")) {
      res.setHeader("Content-Type", "application/json");
      return res.end(JSON.stringify([
        { tag_name: "v0.12.0-rc.2", prerelease: true, html_url: origin + "/notes-rc", assets: [] },
        { tag_name: "v0.11.1", html_url: origin + "/notes", assets: [
          { name, browser_download_url: `${origin}/dl/${name}`, size: archive.length },
          { name: "checksums.txt", browser_download_url: `${origin}/dl/checksums.txt` },
        ] },
        { tag_name: "v0.11.0" },
      ]));
    }
    if (req.url === `/dl/${name}`) return res.end(archive);
    if (req.url === "/dl/checksums.txt") return res.end(`${sum}  ${name}\n`);
    res.statusCode = 404; res.end();
  });
  await new Promise((r) => fake.listen(0, "127.0.0.1", r));
  browser = await chromium.launch();
  server = await startServer(installed, path.join(root, "home"), { THEO_UPDATE_API: `http://127.0.0.1:${fake.address().port}/releases` });
});

after(async () => {
  await browser?.close();
  server?.stop();
  fake?.close();
  if (root) rmrf(root);
});

test("release candidates are only offered after choosing the test channel", { skip }, async () => {
  const page = await openApp(browser, adapter, { route: "#/settings/updates" });
  await page.waitForSelector("#upd-rc");
  assert.equal(await page.isChecked("#upd-rc"), false);
  await page.click("#upd-rc");
  await page.waitForFunction(() => document.querySelector("#upd-rc").checked);
  await page.click("button:has-text('Check now')");
  await page.waitForSelector("text=Test version 0.12.0-rc.2 is available");
  await shot(page, "v17-real-rc");
  await page.click("#upd-rc");
  await page.locator(".banner.info", { hasText: "Test version" }).waitFor({ state: "detached" });
  await page.click("button:has-text('Check now')");
  await page.ctx.close();
});

test("a real Theo finds the new version, downloads it, verifies it and replaces itself", { skip }, async () => {
  const page = await openApp(browser, adapter, { route: "#/settings/updates" });
  assert.match(await page.locator("main").innerText(), /Installed version\s+0\.11\.0/);
  await page.click("button:has-text('Check now')");
  await page.locator(".banner.info", { hasText: "Theo 0.11.1 is available (you have 0.11.0)" }).waitFor();
  await shot(page, "v17-real-available");
  // The test version is not offered: release candidates are off by default.
  assert.doesNotMatch(await page.locator("body").innerText(), /0\.12\.0-rc/);

  await page.click("button:has-text('Update to 0.11.1')");
  await page.waitForSelector("text=Theo 0.11.1 is installed", { timeout: 30000 });
  await shot(page, "v17-real-installed");
  assert.equal(readFileSync(installed, "utf8"), newProgram, "the file on disk is the new program");
  assert.deepEqual(readdirSync(path.dirname(installed)).filter((f) => f !== "theo"), [], "no leftovers beside the program");
  // In the browser there is no window to restart: it says to reopen Theo.
  assert.match(await page.locator("main").innerText(), /Close Theo and open it again/);
  assert.equal(await page.locator("button:has-text('Restart Theo')").count(), 0);
  assert.deepEqual(page.problems, []);
  await page.ctx.close();
});
