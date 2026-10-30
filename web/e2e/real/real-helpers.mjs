// Helpers for the tests that run against the REAL `theo serve` (no mock): they start the real binary with a
// temporary THEO_HOME, go through the real launch URL, and use a real Git repository and real Docker.
//
// Binary: set THEO_BIN to a theo built with the interface embedded, or let this file build ../../../cmd/theo
// (which only works on a tree that contains internal/api, that is, after the backend track is integrated).
import { spawn, execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, existsSync, rmSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import crypto from "node:crypto";
import { webDir } from "../helpers.mjs";

export * from "../helpers.mjs";

const repoRoot = path.resolve(webDir, "..");

export function theoBinary() {
  if (process.env.THEO_BIN) return process.env.THEO_BIN;
  if (!existsSync(path.join(repoRoot, "internal", "api"))) return null;
  const out = path.join(mkdtempSync(path.join(tmpdir(), "uf-bin-")), "theo");
  execFileSync("go", ["build", "-o", out, "./cmd/theo"], { cwd: repoRoot, stdio: "inherit" });
  return out;
}

export function dockerAvailable() {
  const r = spawnSync("docker", ["info", "--format", "{{.OSType}}"], { encoding: "utf8" });
  return r.status === 0 && r.stdout.trim() === "linux";
}

const sh = (cmd, args, opts = {}) => execFileSync(cmd, args, { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"], ...opts }).trim();

/** A unique name prefix for everything this run creates in Docker (track prefix `uf`). */
export const newPrefix = () => "uf" + crypto.randomBytes(3).toString("hex");

/**
 * Creates a Git repository with a Compose file that builds a tiny static HTTP server (FROM scratch, nothing is
 * pulled). Returns { dir, port }. The published host port is fixed on purpose, so a second instance clashes.
 */
export function makeRepo(root, name, port) {
  const dir = path.join(root, name);
  mkdirSync(dir, { recursive: true });
  const src = mkdtempSync(path.join(tmpdir(), "uf-srv-"));
  writeFileSync(path.join(src, "go.mod"), "module server\n\ngo 1.24\n");
  writeFileSync(path.join(src, "main.go"), `package main

import (
	"fmt"
	"net/http"
	"time"
)

func main() {
	fmt.Println("server ready")
	go func() {
		for i := 0; ; i++ {
			time.Sleep(time.Second)
			fmt.Println("tick", i)
		}
	}()
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	http.ListenAndServe(":8080", nil)
}
`);
  execFileSync("go", ["build", "-o", path.join(dir, "server"), "."], { cwd: src, env: { ...process.env, CGO_ENABLED: "0", GOOS: "linux" } });
  writeFileSync(path.join(dir, "Dockerfile"), 'FROM scratch\nCOPY server /server\nENTRYPOINT ["/server"]\n');
  writeFileSync(path.join(dir, "compose.yaml"), `services:\n  web:\n    build: .\n    ports:\n      - "${port}:8080"\n    volumes:\n      - data:/data\nvolumes:\n  data:\n`);
  writeFileSync(path.join(dir, ".gitignore"), "server\n");
  sh("git", ["init", "-q", "-b", "main"], { cwd: dir });
  sh("git", ["config", "user.email", "uf@example.com"], { cwd: dir });
  sh("git", ["config", "user.name", "uf"], { cwd: dir });
  sh("git", ["add", "."], { cwd: dir });
  sh("git", ["commit", "-q", "-m", "init"], { cwd: dir });
  return { dir, port };
}

export function freePort(start = 24000) {
  // The caller passes a high base; probing is done by trying to bind with node's net.
  return start + Math.floor(Math.random() * 3000) * 2;
}

/** Starts `theo serve --print-launch-url` and returns { url, port, pid, stop(), launch() }. */
export async function startServer(bin, home) {
  const child = spawn(bin, ["serve", "--print-launch-url"], { env: { ...process.env, THEO_HOME: home }, stdio: ["ignore", "pipe", "pipe"] });
  let stderr = "";
  child.stderr.on("data", (d) => (stderr += d));
  const info = await new Promise((resolve, reject) => {
    let buf = "";
    child.stdout.on("data", (d) => {
      buf += d;
      const i = buf.indexOf("\n");
      if (i >= 0) { try { resolve(JSON.parse(buf.slice(0, i))); } catch (e) { reject(e); } }
    });
    child.on("exit", (c) => reject(new Error(`theo serve exited ${c}: ${stderr}`)));
    setTimeout(() => reject(new Error("theo serve did not print the launch URL: " + stderr)), 20000);
  });
  return {
    ...info,
    origin: `http://127.0.0.1:${info.port}`,
    pid: child.pid,
    // A fresh one-time launch URL for another tab: the same call a second `theo ui` makes (secret from ui.lock).
    launch: async () => {
      const lock = JSON.parse(readFileSync(path.join(home, "ui.lock"), "utf8"));
      const r = await fetch(`http://127.0.0.1:${info.port}/api/v1/launch`, { method: "POST", headers: { "X-Theo-Control": lock.control_secret } });
      if (!r.ok) throw new Error("launch failed: " + r.status);
      return (await r.json()).url;
    },
    stop: () => { child.kill("SIGTERM"); },
    child,
  };
}

/** Removes the Docker resources whose names start with the prefix (containers, volumes, networks, images). */
export function dockerCleanup(prefix) {
  const quiet = (args) => { try { return sh("docker", args); } catch { return ""; } };
  const ids = quiet(["ps", "-aq", "--filter", `name=${prefix}`]).split("\n").filter(Boolean);
  if (ids.length) quiet(["rm", "-f", ...ids]);
  const vols = quiet(["volume", "ls", "-q", "--filter", `name=${prefix}`]).split("\n").filter(Boolean);
  if (vols.length) quiet(["volume", "rm", "-f", ...vols]);
  const nets = quiet(["network", "ls", "-q", "--filter", `name=${prefix}`]).split("\n").filter(Boolean);
  if (nets.length) quiet(["network", "rm", ...nets]);
  const imgs = quiet(["images", "-q", `${prefix}*`]).split("\n").filter(Boolean);
  if (imgs.length) quiet(["rmi", "-f", ...imgs]);
}

export const docker = (...args) => sh("docker", args);
export const rmrf = (p) => rmSync(p, { recursive: true, force: true });
