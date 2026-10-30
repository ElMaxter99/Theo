// Builds the interface into ../internal/ui/dist (committed; embedded by internal/ui).
//
// Output (deterministic, content-hashed names so the Go server can cache them forever):
//   dist/index.html                  generated here, references the hashed files below
//   dist/assets/app-<hash>.js        the application (ES module)
//   dist/assets/app-<hash>.css       the styles
//   dist/assets/theme-<hash>.js      tiny classic script run in <head> to apply the saved theme before first paint
//   dist/assets/THIRD-PARTY.txt      licence of the bundled runtime dependency
//   dist/brand/*                     provisional brand artwork (see public/brand/NOTICE.md)
//
// No inline script or style is ever produced: the page is served with a strict CSP.
import { build } from "esbuild";
import { cpSync, mkdirSync, readFileSync, rmSync, writeFileSync, readdirSync } from "node:fs";
import { dirname, join, basename } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(fileURLToPath(import.meta.url));
const dist = join(root, "..", "internal", "ui", "dist");

rmSync(dist, { recursive: true, force: true });
mkdirSync(join(dist, "assets"), { recursive: true });

const result = await build({
  entryPoints: { app: join(root, "src/main.tsx"), theme: join(root, "src/theme-init.ts") },
  outdir: join(dist, "assets"),
  entryNames: "[name]-[hash]",
  bundle: true,
  minify: true,
  format: "esm",
  target: "es2022",
  jsx: "automatic",
  jsxImportSource: "preact",
  legalComments: "none",
  metafile: true,
  logLevel: "warning",
});

const outputs = Object.keys(result.metafile.outputs).map((p) => basename(p));
const find = (re) => {
  const f = outputs.find((n) => re.test(n));
  if (!f) throw new Error(`build output ${re} not found in ${outputs.join(", ")}`);
  return f;
};
const appJs = find(/^app-.*\.js$/);
const appCss = find(/^app-.*\.css$/);
const themeJs = find(/^theme-.*\.js$/);

// The theme script is a classic (non-module) script: esbuild's ESM output of a file without
// imports/exports is plain statements, which is valid as a classic script.
const index = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<meta name="referrer" content="no-referrer">
<title>Theo</title>
<link rel="icon" type="image/png" sizes="32x32" href="brand/avatar-plum-32.png">
<link rel="icon" type="image/x-icon" href="brand/avatar-plum.ico">
<link rel="apple-touch-icon" href="brand/avatar-plum-180.png">
<link rel="stylesheet" href="assets/${appCss}">
<script src="assets/${themeJs}"></script>
</head>
<body>
<div id="app"><noscript><p>Theo needs JavaScript. It runs entirely on this computer and loads nothing from the internet.</p></noscript></div>
<script type="module" src="assets/${appJs}"></script>
</body>
</html>
`;
writeFileSync(join(dist, "index.html"), index);

cpSync(join(root, "public"), dist, { recursive: true });
rmSync(join(dist, "brand", "NOTICE.md"), { force: true });
cpSync(join(root, "public", "brand", "NOTICE.md"), join(dist, "brand", "NOTICE.md"));

const preactLicence = readFileSync(join(root, "node_modules", "preact", "LICENSE"), "utf8");
writeFileSync(
  join(dist, "assets", "THIRD-PARTY.txt"),
  `The bundle contains Preact (MIT).\n\n${preactLicence}`,
);

let total = 0;
for (const n of readdirSync(join(dist, "assets"))) total += readFileSync(join(dist, "assets", n)).length;
console.log(`built ${appJs} ${appCss} ${themeJs} (${(total / 1024).toFixed(1)} KiB in assets/)`);
