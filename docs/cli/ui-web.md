# The web interface: source, build and tests

`theo ui` / `theo serve` serve a web application that is embedded in the binary (`internal/ui/dist`, committed, so that
`go build` and `go install` need no Node). This page is for people who work on that application. For how to start it and its
security model see `docs/cli/ui.md`; for the HTTP API it talks to see `docs/api.md`.

The interface has no logic of its own about Git or Docker: it only calls the API and shows what the engine says.

## Layout

```
web/
  package.json, package-lock.json   exact versions; DEPENDENCIES.md lists names, versions and licences
  build.mjs                         esbuild script: writes ../internal/ui/dist (see "Build output")
  tsconfig.json                     strict TypeScript, typecheck only (esbuild does the bundling)
  src/
    main.tsx, App.tsx               entry, session start, shell
    strings.ts                      EVERY user-visible text (English; i18n-ready, Q-029)
    brand.ts                        the only place with paths to the (provisional) brand artwork
    styles/tokens.css               the only place with colour values: light, dark, semantic colours
    styles/app.css                  layout and components (plain CSS, forced-colors, reduced motion)
    api/        types.ts, client.ts (session + CSRF + errors), sse.ts (parser + reconnecting stream),
                events.ts (the one global event channel), focus.ts (the "max 2 streams per tab" rule)
    lib/        status.ts (state -> text + icon + tone, one table), ports.ts, overview.ts, logstore.ts,
                useLogStream.ts, router.ts (hash routes), hooks.ts, theme.ts, live.ts (the one live region), format.ts
    ops/        store.ts (operations list, start/cancel), Operations.tsx (header indicator and panel)
    components/ Shell, Icon (one icon set), Dialog (native <dialog>; plain and typed confirmation), Tabs (ARIA),
                VirtualLog (virtualized log view), RemoveFlow, ui.tsx
    pages/      Projects (+ welcome), AddProject, ProjectPanel, InstanceDetail, tabs/*, Wizard
  public/brand/                     provisional artwork, NOTICE.md (NOT under the MIT licence)
  mock/server.mjs                   mock API server (Node, no dependencies) for development and tests
  test/                             unit tests (`node --test`)
  e2e/                              Playwright tests against the mock; e2e/real/ against the real `theo serve`
  e2e/screenshots/                  PNGs of every screen (light, some dark, forced colours, zoom)
```

Rules that the code and the tests enforce: no CDN, no web fonts, no inline `<script>` or `<style>`, no external request,
no `innerHTML` (project data is always a text node), no Shadow DOM, no component library, native elements first
(`<button>`, `<dialog>`, `<table>`, `<details>`), at most two SSE connections per tab.

## Build

```
cd web
npm ci              # once; installs the pinned dev tools
npm run build       # writes ../internal/ui/dist  (commit the result)
npm run check       # strict typecheck
npm test            # unit tests (design-token contrast, SSE parser, status table, log store, routes...)
```

### Build output

Names carry a content hash so that the Go server can cache them forever; `index.html` is never cached.

| File | Content |
|---|---|
| `dist/index.html` | generated; references the files below |
| `dist/assets/app-<hash>.js` / `.css` | the application (ES module) and its styles |
| `dist/assets/theme-<hash>.js` | tiny classic script in `<head>` that applies a saved light/dark choice before first paint |
| `dist/assets/THIRD-PARTY.txt` | licence of the bundled runtime (Preact, MIT) |
| `dist/brand/*` | provisional brand artwork and its `NOTICE.md` |

The build is deterministic: running it twice gives the same bytes, so CI can check that the committed bundle matches the source
(`npm ci && npm run build && git diff --exit-code internal/ui/dist`).

## Shared services, health and settings (V5, V6)

| Route | Screen | Source |
|---|---|---|
| `#/p/<project>/shared` | Shared services of a project: provider state (Compose project, network, host ports published or not, per-service state), start / stop as operations, "Stop sharing" per service, the instances that use them (each with "Stop using"), a form to make another instance use a service, a form to declare a service as shared, "Delete data…". The empty state explains exclusive vs shared. | `pages/SharedServices.tsx` |
| `#/health` | The reconcile report grouped by kind, with what to do about each finding. Only Theo's own records (`forgettable`) have a checkbox; "Forget Theo's records" asks for confirmation and states that containers, volumes and worktrees are not touched. With `docker_error` a banner says why and which findings are missing. | `pages/Health.tsx`, `lib/health.ts` |
| `#/settings` | The `doctor` table (status in text + icon, detail, how to fix), versions and data directory, "Copy information for a report" (versions and check results only: no paths, no variables), view preferences (theme, density, reduced motion; stored only in this browser). | `pages/Settings.tsx`, `lib/report.ts`, `lib/prefs.ts` |

The sidebar has a **Health** entry whose number is the count of findings (text, never only a dot); with `docker_error` it also says
"Docker unavailable". The count is refreshed when the page opens and, debounced, on `state.changed` and `reconcile.stale`.

Confirmations follow the same rule as the rest of the app: the first request carries no `confirm`; the server answers
`409 confirmation_required` (level 2 for stopping shared services that running instances use, level 3 for purging their data) with
the expected text and the list to show; the dialog enables its button on an exact match and the server validates again. Purging while
instances are linked is refused by the server (`shared_in_use`) and the interface shows the consumers instead of a confirmation.

**History** is paginated: the tab asks for 20 events, and "Load older events" asks for the next page with `before=<time of the
oldest loaded event>`. The list is de-duplicated, so a refresh during paging does not repeat rows.

**Wizard, fixed container names.** When the settings of the main worktree report `fixed_container_names`, step 2 shows them and an
unchecked checkbox "Neutralize fixed container names for this worktree". It is never ticked for the user. When ticked, after the
worktree is created the app sends `PATCH .../settings {neutralize_container_names: true}` for the new worktree only, before starting
it; if that fails the instance is not started and the screen says why.

### API shapes the screens rely on

The shapes are the Go structs the CLI already serialises (`engine.SharedStatus`, `engine.Report`, `engine.DoctorReport`), see
`api/types.ts`. Where the backend track's `docs/api.md` does not say otherwise the app assumes:

- `GET /projects/{p}/shared` -> `SharedStatus`, or 404 `shared_not_configured`; `POST /projects/{p}/shared {services, from?, publish?}`;
  `POST .../shared/up` and `.../shared/down {confirm?}` -> 202 operations of kind `shared-up` / `shared-down` without `worktree`;
  `DELETE .../shared/services/{s}`; `POST .../shared/purge {confirm?}`; `PUT .../worktrees/{wt}/shared {use, join}`;
  `DELETE .../worktrees/{wt}/shared/{s}`.
- `GET /reconcile` -> `{findings:[{kind, project?, subject, worktree?, detail, fix?, forgettable?}], docker_error?}`;
  `POST /reconcile/forget {finding_ids}`. Finding ids are the server's `id` when it sends one, else `kind|project|subject|worktree`
  (`lib/findings.ts`).
- `GET /doctor` -> `{ok, platform, data_dir, checks:[{id, name, status, detail?, fix?}]}`.
- `GET .../history?limit=&before=` -> an array, oldest first.

## Design and accessibility decisions

- **Tokens.** `styles/tokens.css` has three blocks that must stay identical in meaning: light, dark by system preference and
  dark forced by the user (`data-theme="dark"`, stored in `localStorage` inside try/catch). `test/tokens.test.mjs` checks that the
  two dark blocks are identical and that every text/background pair meets WCAG 2.2 AA (4.5:1 text, 3:1 components and focus).
  Semantic colours (ok, warning, error, info) are independent of the brand palette: the brand coral is not the error red.
- **State is never colour alone.** `lib/status.ts` is the single table from API enums to text + distinct icon shape + tone.
- **Keyboard and screen readers.** Skip link, landmarks, one polite `aria-live` region (`lib/live.ts`), ARIA tabs with
  Left/Right/Home/End, focus moves to the page heading after navigation and to the step heading in the wizard, dialogs return focus
  to their opener, destructive dialogs focus *Cancel* first and Esc cancels, logs are not live regions.
- **Zoom and motion.** Relative units; at 640 CSS px (200 % zoom) there is no page-level horizontal scroll (wide tables scroll in
  their own focusable region); spinners stop under `prefers-reduced-motion`; `forced-colors` keeps all text and borders.
- **Logs.** `VirtualLog` renders a window of at most a few hundred line nodes whatever the number of lines (the limit is 10 000);
  100 000 lines are kept in memory at most; search, follow, pause, download (a plain link to the text endpoint) and `gap` markers.
- **Streams.** One global event channel plus one "focus" stream (a log view or an operation output); a new view that needs a
  stream preempts the previous one, which shows "another live view is using the connection" and a Resume button.
- **Destructive flows** (`components/Dialog.tsx`, `RemoveFlow.tsx`, `VolumesTab.tsx`): the first request never carries a
  confirmation; the server answers 409 `confirmation_required` with `affected`, `preserved` and `confirm.expected`; the dialog lists
  them, enables its button only on an exact match and sends the same request with `confirm`. The server validates again and its
  rejection is shown. There is no mascot in these dialogs (tested).

## Tests

All Playwright tests need Chromium and `require('playwright')` (preinstalled in the development environment; not a dependency).

| Command | What | Needs |
|---|---|---|
| `npm test` | unit tests: design-token contrast, SSE parser, status table, port clashes, log store, virtual window, routes | Node 22 |
| `npm run test:e2e` | (includes `e2e/v56.test.mjs`: shared services, health, settings, history paging, the wizard checkbox, each screen with axe in light and dark) every screen + axe (light and dark) + CSP, flows (start/stop/cancel, wizard, destructive dialogs, secrets, SSE budget), log viewer with 50 000 lines, keyboard walk, forced colours, zoom, security behaviour | the mock server (started by the tests) |
| `npm run test:e2e:real` | the same story against the real `theo serve`: add project with the folder picker, start with real Docker, logs, UC-02 port clash with automatic ports, remove with typed confirmation, secrets, purge, security negatives, loopback-only socket | Docker, Go, `THEO_BIN` or a tree that has `internal/api` |

`e2e/real` builds `./cmd/theo` itself when `internal/api` exists; otherwise set `THEO_BIN` to a binary that embeds the current
`internal/ui/dist`. It uses a temporary `THEO_HOME`, a temporary Git repository, names prefixed `uf`, a fixed host port from
a free range, and removes its containers, volumes, networks and images at the end.

Screenshots are written to `web/e2e/screenshots/` by the tests (never with real data: the data is the mock's or the test repository's).

### What is and is not verified

Verified by automated tests: no critical or serious axe violations on every screen in light and dark; the keyboard walk;
ARIA tabs; focus management; forced-colors / dark mode keep all text; reduced motion; no page-level horizontal scroll at
200 % zoom; no CSP violation or console error; no request outside the origin; no token in any URL after the exchange.

**Not verified** (say so in any accessibility claim): real screen readers (NVDA, VoiceOver, Orca), Firefox and WebKit, macOS and
Windows, browser zoom in a desktop browser (it is emulated with a 640 px viewport), Windows high-contrast themes (emulated with
`forced-colors: active`), and any manual review of reading order beyond the automated checks. The contrast of the tokens is
computed, not measured on screen.

## Mock server

`node mock/server.mjs [--port N]` prints `{"url","port","pid"}` like `theo serve --print-launch-url` and implements the API with
believable data (including shared services with consumers, a reconcile report with Theo's own records and others, a `doctor` report, a history of 47 events): two projects, worktrees in every state (running, partial, failing, stopped, absent, detached, uncommitted
changes), conflicting ports, slow `up` operations that stream log lines, a failing start (branch names containing `fail`), logs of
any length, confirmation flows, secrets. It enforces the same Host, Origin, cookie, CSRF and CSP rules as the real server.
Test hooks (mock only): `POST /__mock/reset`, `POST /__mock/scenario {docker, empty, gap, slow, sharedEmpty, sharedWarning, healthy, composeOld, windows}`, `GET /__mock/stats`, `GET /__mock/launch`.
The mock is a development tool: it can drift from the real API, which is why `e2e/real` exists. `docs/api.md` is the truth.
