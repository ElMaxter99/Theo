// Takes the screenshots shown in the top-level README, from the mock API (no Docker needed).
//
//   cd web && npm run screenshots:readme      writes ../docs/images/*.png
//
// Run it before each release when the interface changed (see "Releasing" in CONTRIBUTING.md).
// It is not a test: the e2e runner only picks up *.test.mjs.
import path from "node:path";
import { chromium, startMock, openApp, webDir } from "./helpers.mjs";

const out = path.resolve(webDir, "..", "docs", "images");
const shots = [
  ["ui-instances", "#/p/shop", "table.data"],
  ["ui-home", "#/", "main h1"],
];

const mock = await startMock();
const browser = await chromium.launch();
try {
  for (const scheme of ["light", "dark"]) {
    for (const [name, route, wait] of shots) {
      const page = await openApp(browser, mock, { route, wait, colorScheme: scheme, viewport: { width: 1280, height: 760 }, deviceScaleFactor: 2 });
      await page.waitForTimeout(500);
      const file = path.join(out, `${name}-${scheme}.png`);
      await page.screenshot({ path: file });
      await page.context().close();
      console.log(file);
    }
  }
} finally {
  await browser.close();
  mock.stop();
}
