// Accessibility pass of v0.2: what the screen-level checks in screens.test.mjs and a11y.test.mjs did not cover.
// Dialogs are checked with axe while open, focus never falls back to <body> when the focused control disappears,
// form errors move focus to the field, the typed confirmation does not read the same sentence twice, and operation
// progress is announced in readable words in the live region.
import { test, before, after, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { chromium, startMock, openApp, axeViolations, seriousOnly } from "./helpers.mjs";

let browser, mock;
before(async () => { mock = await startMock(); browser = await chromium.launch(); });
after(async () => { await browser?.close(); mock?.stop(); });
beforeEach(async () => { await mock.reset(); });

const wt = async (branch) => {
  const url = new URL(await mock.launchUrl());
  const r = await fetch(mock.origin + "/api/v1/session", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ nonce: url.hash.slice(3) }) });
  const cookie = r.headers.get("set-cookie").split(";")[0];
  const ov = await (await fetch(mock.origin + "/api/v1/projects/shop/overview", { headers: { Cookie: cookie } })).json();
  return ov.worktrees.find((w) => w.branch === branch).id;
};
const activeTag = (page) => page.evaluate(() => { const e = document.activeElement; return e ? `${e.tagName}|${e.id}|${(e.textContent || "").trim().slice(0, 40)}` : "none"; });
const row = (page, text) => page.locator("tbody tr", { hasText: text });

async function axeIn(page, selector, name) {
  const v = seriousOnly(await axeViolations(page, { include: selector }));
  assert.deepEqual(v, [], `${name}: ${JSON.stringify(v, null, 1)}`);
}

test("open dialogs have no serious axe violations: confirm, typed confirm, form, folder browser, operations panel", async () => {
  // Plain confirmation (unregister).
  let page = await openApp(browser, mock, { route: "#/projects", wait: "table.data" });
  await page.getByRole("button", { name: "More actions for blog" }).click();
  await page.getByRole("menuitem", { name: "Unregister" }).click();
  await page.locator("dialog[open]").waitFor();
  await axeIn(page, "dialog[open]", "unregister dialog");
  await page.keyboard.press("Escape");
  // Folder browser.
  await page.evaluate(() => { location.hash = "#/projects/new"; });
  await page.getByRole("button", { name: /Browse/ }).click();
  await page.locator("dialog[open]").waitFor();
  await page.waitForTimeout(300);
  await axeIn(page, "dialog[open]", "folder browser");
  await page.keyboard.press("Escape");
  await page.ctx.close();

  // Typed confirmation (remove a worktree with uncommitted changes).
  page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  await page.getByRole("button", { name: "More actions for old-spike" }).click();
  await page.getByRole("menuitem", { name: /Remove worktree/ }).click();
  await page.getByRole("button", { name: "Remove worktree" }).click();
  await page.locator("dialog[open] >> text=Will be deleted").waitFor();
  await axeIn(page, "dialog[open]", "typed confirmation");
  await page.keyboard.press("Escape");
  // Operations panel: in 0.2 it is only in the header while something runs.
  await mock.scenario({ slow: 3 });
  await page.getByRole("button", { name: /Start feature-x/ }).click();
  await page.locator(".ops-indicator button").click();
  await page.locator("#ops-panel").waitFor();
  await axeIn(page, "#ops-panel", "operations panel");
  await page.ctx.close();

  // Variable form, with a validation error shown.
  const id = await wt("main");
  page = await openApp(browser, mock, { route: `#/p/shop/w/${id}/config`, wait: "section[aria-labelledby=cfg-vars] table.data" });
  await page.getByRole("button", { name: "Add variable" }).click();
  await page.locator("dialog[open]").waitFor();
  await page.locator("dialog[open]").getByRole("button", { name: "Save" }).click();
  await page.locator("#var-name-err").waitFor();
  await axeIn(page, "dialog[open]", "variable form with an error");
  await page.ctx.close();
});

test("a form error moves focus to the field, which is marked invalid and described by the error", async () => {
  const id = await wt("main");
  const page = await openApp(browser, mock, { route: `#/p/shop/w/${id}/config`, wait: "section[aria-labelledby=cfg-vars] table.data" });
  await page.getByRole("button", { name: "Add variable" }).click();
  await page.locator("dialog[open]").waitFor();
  await page.fill("#var-name", "1bad");
  await page.locator("dialog[open]").getByRole("button", { name: "Save" }).focus();
  await page.keyboard.press("Enter");
  await page.locator("#var-name-err").waitFor();
  assert.match(await activeTag(page), /^INPUT\|var-name\|/, "focus moved to the invalid field");
  assert.equal(await page.locator("#var-name").getAttribute("aria-invalid"), "true");
  assert.match(await page.locator("#var-name").getAttribute("aria-describedby"), /var-name-err/);
  await page.ctx.close();
});

test("after unregistering a project with the keyboard, focus lands on the page heading, not on <body>", async () => {
  const page = await openApp(browser, mock, { route: "#/projects", wait: "table.data" });
  await page.getByRole("button", { name: "More actions for blog" }).focus();
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => document.activeElement?.getAttribute("role") === "menuitem");
  await page.keyboard.press("End");
  await page.keyboard.press("Enter");
  await page.locator("dialog[open]").waitFor();
  await page.getByRole("button", { name: "Unregister project" }).focus();
  await page.keyboard.press("Enter");
  await page.locator("tbody tr", { hasText: "blog" }).waitFor({ state: "detached" });
  await page.waitForFunction(() => document.activeElement && document.activeElement !== document.body, null, { timeout: 3000 }).catch(() => undefined);
  assert.match(await activeTag(page), /^H1\|/, "focus is on the heading");
  await page.ctx.close();
});

test("after removing a worktree from its row menu, focus lands on the page heading", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  await page.getByRole("button", { name: "More actions for old-spike" }).click();
  await page.getByRole("menuitem", { name: /Remove worktree/ }).click();
  await page.getByRole("button", { name: "Remove worktree" }).click();
  const dlg = page.locator("dialog[open]");
  await dlg.locator("text=Will be deleted").waitFor();
  await dlg.getByLabel(/Type the folder name/).fill("shop-old-spike");
  await dlg.getByRole("button", { name: "Remove worktree" }).focus();
  await page.keyboard.press("Enter");
  await row(page, "old-spike").waitFor({ state: "detached" });
  await page.waitForFunction(() => document.activeElement && document.activeElement !== document.body, null, { timeout: 3000 }).catch(() => undefined);
  assert.match(await activeTag(page), /^H1\|/);
  await page.ctx.close();
});

test("typed confirmation: the label and the description are different sentences, and without a server prompt there is no duplicate", async () => {
  const page = await openApp(browser, mock, { route: "#/p/shop", wait: "table.data" });
  const read = () => page.locator("dialog[open] input.mono").evaluate((el) => ({
    label: document.querySelector(`label[for="${el.id}"]`)?.textContent ?? "",
    desc: (el.getAttribute("aria-describedby") || "").split(" ").filter(Boolean).map((id) => document.getElementById(id)?.textContent ?? "").join(" "),
  }));
  await page.getByRole("button", { name: "More actions for old-spike" }).click();
  await page.getByRole("menuitem", { name: /Remove worktree/ }).click();
  await page.getByRole("button", { name: "Remove worktree" }).click();
  await page.locator("dialog[open] >> text=Will be deleted").waitFor();
  let r = await read();
  assert.match(r.label, /Type the folder name/);
  assert.match(r.desc, /Type shop-old-spike to confirm/);
  await page.keyboard.press("Escape");
  await page.locator("dialog[open]").waitFor({ state: "detached" });
  // Same flow with the server's prompt removed: the label says what to type and nothing repeats it.
  await page.route("**/remove", async (route) => {
    const res = await route.fetch();
    const json = await res.json();
    if (json?.error?.confirm) delete json.error.confirm.prompt;
    await route.fulfill({ response: res, json });
  });
  await page.getByRole("button", { name: "More actions for old-spike" }).click();
  await page.getByRole("menuitem", { name: /Remove worktree/ }).click();
  await page.getByRole("button", { name: "Remove worktree" }).click();
  await page.locator("dialog[open] >> text=Will be deleted").waitFor();
  r = await read();
  assert.equal(r.label, "Type shop-old-spike to confirm");
  assert.equal(r.desc, "", "no description that repeats the label");
  await page.ctx.close();
});

test("operation progress is announced in words in the single live region, from the instance page too", async () => {
  await mock.scenario({ slow: 2 });
  const id = await wt("feature-x");
  const page = await openApp(browser, mock, { route: `#/p/shop/w/${id}/summary`, wait: "[role=tablist]" });
  const live = page.locator("#live-region");
  const seen = new Set();
  const sample = setInterval(() => { live.textContent().then((s) => { if (s) seen.add(s); }, () => undefined); }, 40);
  await page.getByRole("button", { name: "Start", exact: true }).click();
  await page.waitForFunction(() => /Start feature-x: /.test(document.getElementById("live-region")?.textContent || ""), null, { timeout: 15000 });
  clearInterval(sample);
  seen.add(await live.textContent());
  const all = [...seen];
  assert.ok(all.some((s) => /^Starting feature-x/.test(s)), "start announced: " + JSON.stringify(all));
  assert.ok(all.some((s) => /^Start feature-x: (succeeded|failed)/.test(s)), "end announced: " + JSON.stringify(all));
  assert.ok(!all.some((s) => /^(up|down|restart) /.test(s)), "no raw operation kinds: " + JSON.stringify(all));
  assert.equal(await page.locator("[aria-live]").count(), 1);
  await page.ctx.close();
});
