// Project-level actions: settings (PATCH) and unregister (a plain dialog that says nothing is deleted); add-project errors.
import { test, before, after, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { chromium, startMock, openApp } from "./helpers.mjs";

let browser, mock;
before(async () => { mock = await startMock(); browser = await chromium.launch(); });
after(async () => { await browser?.close(); mock?.stop(); });
beforeEach(async () => { await mock.reset(); });

test("project settings are saved and shown again", async () => {
  const page = await openApp(browser, mock);
  await page.getByText("5 worktrees").waitFor();
  await page.getByRole("button", { name: "Project settings blog" }).click();
  await page.fill("#ps-env", "/home/ana/code/shared.env");
  await page.getByRole("button", { name: "Save" }).click();
  await page.locator("dialog[open]").waitFor({ state: "detached" });
  await page.getByRole("button", { name: "Project settings blog" }).click();
  assert.equal(await page.inputValue("#ps-env"), "/home/ana/code/shared.env");
  await page.keyboard.press("Escape");
  await page.ctx.close();
});

test("unregister says that nothing is deleted, has no mascot and removes only the registration", async () => {
  const page = await openApp(browser, mock);
  await page.getByText("5 worktrees").waitFor();
  await page.getByRole("button", { name: "Unregister blog" }).click();
  const dlg = page.locator("dialog[open]");
  await dlg.locator("text=Nothing is deleted").waitFor();
  assert.equal(await dlg.locator("img").count(), 0);
  assert.ok(await dlg.getByRole("button", { name: "Cancel" }).evaluate((el) => el === document.activeElement), "Cancel has the initial focus");
  await dlg.getByRole("button", { name: "Unregister project" }).click();
  await page.locator("h2 a", { hasText: "blog" }).waitFor({ state: "detached" });
  await page.ctx.close();
});

test("adding a folder that is not a repository shows the engine's message and hint, and keeps the form", async () => {
  const page = await openApp(browser, mock, { route: "#/projects/new", wait: "#ap-path" });
  await page.fill("#ap-path", "/home/ana/code/playground");
  await page.getByRole("button", { name: "Add project", exact: true }).click();
  await page.locator("[role=alert] >> text=is not a Git repository").waitFor();
  assert.ok(await page.locator("text=Choose the folder that contains the .git directory").count() > 0);
  assert.equal(await page.inputValue("#ap-path"), "/home/ana/code/playground");
  await page.fill("#ap-path", "");
  await page.getByRole("button", { name: "Add project", exact: true }).click();
  await page.locator("#ap-path-err").waitFor();
  assert.equal(await page.evaluate(() => document.activeElement.id), "ap-path", "focus goes to the first invalid field");
  await page.ctx.close();
});

test("adding a repository goes to its panel", async () => {
  const page = await openApp(browser, mock, { route: "#/projects/new", wait: "#ap-path" });
  await page.fill("#ap-path", "/home/ana/code/api-gateway");
  assert.equal(await page.inputValue("#ap-name"), "api-gateway", "the name is suggested from the folder");
  await page.getByRole("button", { name: "Add project", exact: true }).click();
  await page.waitForSelector("table.data");
  assert.match(page.url(), /#\/p\/api-gateway$/);
  await page.ctx.close();
});
