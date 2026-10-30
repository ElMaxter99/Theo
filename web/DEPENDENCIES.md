# Frontend dependencies

Pinned exactly in `package.json` and `package-lock.json` (install with `npm ci`). Only `preact` ends up in the shipped
bundle (`internal/ui/dist`); everything else is a development tool and is never distributed with `theo`.

| Package | Version | Licence | Used for | Shipped |
|---|---|---|---|---|
| `preact` | 10.29.8 | MIT | UI runtime (about 5 KiB gzipped) | yes, bundled; its licence is copied to `dist/assets/THIRD-PARTY.txt` |
| `esbuild` | 0.28.2 | MIT | bundler (`npm run build`); installs its platform binary `@esbuild/<os>-<arch>` (same version, MIT) | no |
| `typescript` | 5.9.3 | Apache-2.0 | type check (`npm run check`) | no |
| `axe-core` | 4.14.0 | **MPL-2.0** | accessibility checks inside the Playwright tests (`web/e2e`) | no |

Not dependencies of this package: **Playwright** and Chromium come preinstalled in the development environment and in CI
(`require('playwright')`), they are not listed in `package.json`.

## Licence note on `axe-core`

The brief for this round allows MIT, Apache-2.0, ISC and BSD, and at the same time asks for `axe-core` as a development
dependency. `axe-core` is MPL-2.0 (file-level copyleft). It is used unmodified, only by the tests, is never bundled into
`internal/ui/dist` and never reaches users, so the MPL obligations (publish changes to MPL files) do not apply to Theo.
If the owner prefers a strict allow-list, drop `axe-core` and the axe checks in `e2e/*.test.mjs` are the only things lost
(a decision to be taken by the lead; nothing else depends on it).

## Rules

- No new runtime dependency without a reason written here.
- No CDN, no web fonts, no telemetry: the interface makes no request outside its own origin (tested in
  `e2e/security.test.mjs`).
- The provisional brand artwork in `public/brand/` is **not** covered by the MIT licence (see `public/brand/NOTICE.md`).
