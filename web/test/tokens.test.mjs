// Design tokens: dark blocks are identical and every text/background pair meets WCAG 2.2 AA.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const css = readFileSync(fileURLToPath(new URL("../src/styles/tokens.css", import.meta.url)), "utf8");

function vars(block) {
  const out = {};
  for (const m of block.matchAll(/--([a-z0-9-]+):\s*(#[0-9a-f]{6})\s*;/gi)) out[m[1]] = m[2].toLowerCase();
  return out;
}
const light = vars(css.slice(css.indexOf(":root {"), css.indexOf("@media")));
const mediaStart = css.indexOf("@media (prefers-color-scheme: dark)");
const forcedStart = css.indexOf(':root[data-theme="dark"]');
const darkMedia = vars(css.slice(mediaStart, forcedStart));
const darkForced = vars(css.slice(forcedStart));

function lum(hex) {
  const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
}
function ratio(a, b) {
  const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
  return (x + 0.05) / (y + 0.05);
}

test("the system-dark block and the forced-dark block are identical", () => {
  assert.deepEqual(darkMedia, darkForced);
  assert.ok(Object.keys(darkForced).length > 20);
});

// [foreground, background, minimum ratio]
const pairs = [
  ["text", "bg", 4.5],
  ["text", "bg-subtle", 4.5],
  ["text", "surface", 4.5],
  ["text", "surface-2", 4.5],
  ["text", "selected", 4.5],
  ["text-muted", "bg", 4.5],
  ["text-muted", "bg-subtle", 4.5],
  ["text-muted", "surface", 4.5],
  ["text-muted", "surface-2", 4.5],
  ["link", "bg", 4.5],
  ["link", "surface", 4.5],
  ["link", "bg-subtle", 4.5],
  ["on-accent", "accent", 4.5],
  ["on-accent", "accent-hover", 4.5],
  ["on-danger", "danger", 4.5],
  ["on-danger", "danger-hover", 4.5],
  ["ok-fg", "ok-bg", 4.5],
  ["warn-fg", "warn-bg", 4.5],
  ["error-fg", "error-bg", 4.5],
  ["info-fg", "info-bg", 4.5],
  ["neutral-fg", "neutral-bg", 4.5],
  ["ok-fg", "surface", 4.5],
  ["warn-fg", "surface", 4.5],
  ["error-fg", "surface", 4.5],
  ["info-fg", "surface", 4.5],
  ["neutral-fg", "surface", 4.5],
  ["log-text", "log-bg", 4.5],
  ["log-muted", "log-bg", 4.5],
  ["log-err", "log-bg", 4.5],
  // Non-text components and the focus ring need 3:1.
  ["border", "bg", 3],
  ["border", "surface", 3],
  ["focus", "bg", 3],
  ["focus", "surface", 3],
  ["accent", "bg", 3],
];

for (const [name, theme] of [["light", light], ["dark", darkForced]]) {
  for (const [fg, bg, min] of pairs) {
    test(`${name}: ${fg} on ${bg} >= ${min}:1`, () => {
      assert.ok(theme[fg] && theme[bg], `missing token ${fg} or ${bg}`);
      const r = ratio(theme[fg], theme[bg]);
      assert.ok(r >= min, `${theme[fg]} on ${theme[bg]} is ${r.toFixed(2)}:1`);
    });
  }
}
