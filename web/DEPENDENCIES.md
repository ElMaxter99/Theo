# Frontend dependencies

Pinned exactly in `package.json` and `package-lock.json` (install with `npm ci`). Only `preact` ends up in the shipped
bundle (`internal/ui/dist`); everything else is a development tool and is never distributed with `theo`.

| Package | Version | Licence | Used for | Shipped |
|---|---|---|---|---|
| `preact` | 11.2.0 | MIT | UI runtime (about 5 KiB gzipped) | yes, bundled; its licence is copied to `dist/assets/THIRD-PARTY.txt` |
| `esbuild` | 0.28.2 | MIT | bundler (`npm run build`); installs its platform binary `@esbuild/<os>-<arch>` (same version, MIT) | no |
| `typescript` | 7.0.2 | Apache-2.0 | type check (`npm run check`) | no |
| Fredoka (typeface, weights 600 and 700, latin subset, woff2 from `@fontsource/fredoka`) | 5.2.8, copied into `src/fonts` | **SIL OFL 1.1** | headings; not an npm dependency, the two files are committed | yes, bundled; the OFL is copied to `dist/assets/THIRD-PARTY.txt` |
| `playwright` | 1.65.0 | Apache-2.0 | end-to-end tests (`web/e2e`); run `npx playwright install chromium` once to download the browser | no |
| `axe-core` | 4.14.0 | **MPL-2.0** | accessibility checks inside the Playwright tests (`web/e2e`) | no |

The Chromium browser used by Playwright is not an npm package: install it once with `npx playwright install chromium`
(already preinstalled in CI).

## Licence note on `axe-core`

The brief for this round allows MIT, Apache-2.0, ISC and BSD, and at the same time asks for `axe-core` as a development
dependency. `axe-core` is MPL-2.0 (file-level copyleft). It is used unmodified, only by the tests, is never bundled into
`internal/ui/dist` and never reaches users, so the MPL obligations (publish changes to MPL files) do not apply to Theo.
If the owner prefers a strict allow-list, drop `axe-core` and the axe checks in `e2e/*.test.mjs` are the only things lost
(a decision to be taken by the lead; nothing else depends on it).

## Rules

- No new runtime dependency without a reason written here.
- No CDN, no web fonts from outside (Fredoka is bundled and served by Theo itself), no telemetry: the interface makes no request outside its own origin (tested in
  `e2e/security.test.mjs`).
- The provisional brand artwork in `public/brand/` is **not** covered by the MIT licence (see `public/brand/NOTICE.md`).
