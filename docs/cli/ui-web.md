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
    strings.ts                      EVERY user-visible text: the English catalog `en`, `Catalog` (its type) and `t`
    strings.es.ts                   the Spanish catalog, typed `Catalog` (a missing or extra key fails `npm run check`)
    brand.ts                        the only place with paths to the (provisional) brand artwork
    styles/tokens.css               the only place with colour values: light, dark, semantic colours
    styles/app.css                  layout and components (plain CSS, forced-colors, reduced motion)
    api/        types.ts, client.ts (session + CSRF + errors), sse.ts (parser + reconnecting stream),
                events.ts (the one global event channel), focus.ts (the "max 2 streams per tab" rule)
    lib/        status.ts (state -> text + icon + tone, one table), ports.ts, overview.ts, logstore.ts,
                useLogStream.ts, router.ts (hash routes, 0.1 addresses rewritten), hooks.ts, theme.ts, live.ts (the one
                live region), format.ts, locale.ts (language preference, `num`, `plural`), focusguard.ts (focus never
                falls back to <body>), statusStore.ts (GET /status, shared by the sidebar, home and the palette)
    ops/        store.ts (operations list, start/cancel, stop-all, volume operations), Operations.tsx (header indicator
                and panel, shown only while something runs)
    components/ Shell, Icon (one icon set), Dialog (native <dialog>; plain and typed confirmation; side panel), Tabs (ARIA),
                Menu (APG menu button), Palette (command palette and keyboard shortcuts), Toasts, CopyCommand,
                VirtualLog (virtualized log view), RemoveFlow, ui.tsx
    pages/      Home (+ welcome), Projects, AddProject, ProjectHeader, ProjectPanel (instances), NewInstance (side panel),
                SharedServices, ProjectActivity, ProjectSettings, InstanceDetail, tabs/*, Problems (+ Health, cleanup),
                Settings
  src/fonts/                        Fredoka 600 and 700 (woff2, SIL OFL 1.1, licence in OFL.txt), headings only
  public/brand/                     provisional artwork, NOTICE.md (NOT under the MIT licence)
  mock/server.mjs                   mock API server (Node, no dependencies) for development and tests
  test/                             unit tests (`node --test`)
  e2e/                              Playwright tests against the mock; e2e/real/ against the real `theo serve`
  e2e/screenshots/                  PNGs of every screen (light, some dark, forced colours, zoom)
```

Rules that the code and the tests enforce: no CDN, no font other than the bundled Fredoka (served from the app itself,
`font-src 'self'`), no inline `<script>` or `<style>`, no external request,
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
| `dist/assets/fredoka-latin-<weight>-normal-<hash>.woff2` | the heading typeface |
| `dist/assets/THIRD-PARTY.txt` | licences of the bundled runtime (Preact, MIT) and typeface (Fredoka, SIL OFL 1.1) |
| `dist/brand/*` | provisional brand artwork and its `NOTICE.md` |

The build is deterministic: running it twice gives the same bytes, so CI can check that the committed bundle matches the source
(`npm ci && npm run build && git diff --exit-code internal/ui/dist`).

## Screens (0.2 redesign)

The design lives in the Theo-workspace repository (the design canvas); every new screen is drawn there first. The 0.2
structure replaces the 0.1 one: a home screen, the project as tabs, a side panel instead of the four-step wizard, five tabs
per instance instead of seven, and *Problems* instead of *Health*.

| Route | Screen | Source |
|---|---|---|
| `#/` | Home: what is running (with its address and Pause), what needs attention, recent activity of every project, "Stop everything". With no project it is the welcome screen, with the Git and Docker checks. | `pages/Home.tsx` |
| `#/projects`, `#/projects/new` | Projects as a table (running, attention, New instance, ⋯ menu); adding a project. | `pages/Projects.tsx`, `pages/AddProject.tsx` |
| `#/p/<p>` | Instances: state chips with counts, search, owner filter, multi-select with bulk start and pause, one row per instance with the action that fits its state and a ⋯ menu. | `pages/ProjectPanel.tsx` |
| `#/p/<p>/new` | The same, with the *New instance* side panel open: new or existing branch, port check while typing, "More options" (owner, folder, copy files, clone volumes, fixed container names), a one-line summary, *Create and start* / *Create only* / *Copy command*. | `pages/NewInstance.tsx` |
| `#/p/<p>/shared`, `/activity`, `/settings` | Shared services; the history of every instance of the project; the project's settings (Compose files, env file, worktree folder, files copied into new worktrees, rename, the AGENTS.md paragraph, unregister). | `pages/SharedServices.tsx`, `ProjectActivity.tsx`, `ProjectSettings.tsx` |
| `#/p/<p>/w/<id>/<tab>` | Instance: Summary (warnings, services with their own actions, ports, facts), Logs, Configuration (profiles, owner, ports, variables, the Compose preview), Data (volumes, backups, restore, copy from another instance, delete), Activity. | `pages/InstanceDetail.tsx`, `pages/tabs/*` |
| `#/problems`, `#/problems/cleanup` | Instance warnings with what to do, the reconcile report (below); cleanup of worktrees whose branch is merged, after typing the project name. | `pages/Problems.tsx`, `pages/Health.tsx` |
| `#/settings/<section>` | Preferences (theme, language, density, motion), Local domains, AI agents (MCP registration), Diagnostics (doctor and the report), About. | `pages/Settings.tsx` |

Addresses of 0.1 keep working and are rewritten in place: `#/health` goes to `#/problems`, and the old instance tabs go to the
new ones (`services` and `ports` to `summary`, `variables` to `config`, `volumes` to `data`, `history` to `activity`).

**Keyboard.** Ctrl+K (⌘K) or `/` opens the command palette (projects, instances and actions, filtered as you type; arrows and
Enter); `?` lists the shortcuts; `g i`, `g p`, `g x`, `g s` go to home, projects, problems and settings. Row and page menus
follow the APG menu-button pattern (arrows, Home/End, Escape returns focus to the button).

**Colour.** Turquoise has two tones: `--brand` (#168783, the logo teal) for marks that are not text, such as the current tab
and active borders, and `--accent` (#0e6b67) for buttons and links, where text needs 4.5:1. Headings use Fredoka.

## Shared services, problems and settings

| Route | Screen | Source |
|---|---|---|
| `#/p/<project>/shared` | Shared services of a project: provider state (Compose project, network, host ports published or not, per-service state), start / stop as operations, "Stop sharing" per service, the instances that use them (each with "Stop using"), a form to make another instance use a service, a form to declare a service as shared, "Delete data…". The empty state explains exclusive vs shared. | `pages/SharedServices.tsx` |
| `#/problems` | The reconcile report grouped by kind, with what to do about each finding. Only Theo's own records (`forgettable`) have a checkbox; "Forget Theo's records" asks for confirmation and states that containers, volumes and worktrees are not touched. With `docker_error` a banner says why and which findings are missing. | `pages/Health.tsx`, `lib/health.ts` |
| `#/settings/diagnostics`, `/preferences`, `/about` | The `doctor` table (status in text + icon, detail, how to fix), versions and data directory, "Copy information for a report" (versions and check results only: no paths, no variables), view preferences (theme, language, density, reduced motion; stored only in this browser). | `pages/Settings.tsx`, `lib/report.ts`, `lib/prefs.ts` |

The sidebar has a **Problems** entry whose number is the count of findings (text, never only a dot); with `docker_error` it also says
"Docker unavailable". The count is refreshed when the page opens and, debounced, on `state.changed` and `reconcile.stale`.

Confirmations follow the same rule as the rest of the app: the first request carries no `confirm`; the server answers
`409 confirmation_required` (level 2 for stopping shared services that running instances use, level 3 for purging their data) with
the expected text and the list to show; the dialog enables its button on an exact match and the server validates again. Purging while
instances are linked is refused by the server (`shared_in_use`) and the interface shows the consumers instead of a confirmation.

**History** is paginated: the tab asks for 20 events, and "Load older events" asks for the next page with `before=<time of the
oldest loaded event>`. The list is de-duplicated, so a refresh during paging does not repeat rows.

**New instance, fixed container names.** When the settings of the main worktree report `fixed_container_names`, "More options"
shows them and an unchecked checkbox "Neutralize fixed container names for this worktree". It is never ticked for the user. When ticked, after the
worktree is created the app sends `PATCH .../settings {neutralize_container_names: true}` for the new worktree only, before starting
it; if that fails the instance is not started and the screen says why.

### API shapes the screens rely on

They are the ones in `docs/api.md` (sections 9 to 11 of the backend track), checked against the real server:

- `GET|POST /projects/{p}/shared`, `POST .../shared/up`, `POST .../shared/down {confirm?}` (operations of kind `shared.up` /
  `shared.down`, without `worktree`; with running consumers and no `confirm`: `409 confirmation_required`, level 2, consumers in
  `affected`), `DELETE .../shared/services/{s}` (`shared_in_use`, `shared_running`), `POST .../shared/purge {confirm?}`
  (`shared_in_use` while instances are linked, then level 3), `PUT|DELETE .../worktrees/{wt}/shared[/{s}]`.
  A project without shared services answers `404 shared_not_configured`, which the page turns into its empty state.
- `GET /reconcile` (findings carry a stable `id`), `POST /reconcile/apply {ids}` (only forgettable findings; others come back in
  `skipped`), `GET /health` `{status, findings, forgettable}` for the sidebar number.
- `GET /diagnostics` `{meta, doctor, projects, report}`: the settings screen and the text of "Copy information for a report"
  (`lib/report.ts` is only the fallback). The report contains no secrets but does include the data directory; the page says so.
- `GET .../history?limit=&before=`: the body is the plain array (oldest first); the cursor of the older page is in the response header
  `Theo-History-Next` (absent on the oldest page).
- `GET .../settings` has `fixed_container_names`, used by the checkbox of the new instance panel, and `known_profiles` and
  `owner` for the Configuration tab.
- `GET /status` (every project, with worktree ids), `GET /history` and `GET /projects/{p}/history` (same paging as above),
  `POST /stop-all`, `GET /mcp/install`, `POST /projects/{p}/rename`, `GET|POST /projects/{p}/prune`,
  `GET .../volumes/backups` and `POST .../volumes/backup|restore|clone`: the 0.2 additions in `docs/api.md`.

## Design and accessibility decisions

- **Tokens.** `styles/tokens.css` has three blocks that must stay identical in meaning: light, dark by system preference and
  dark forced by the user (`data-theme="dark"`, stored in `localStorage` inside try/catch). `test/tokens.test.mjs` checks that the
  two dark blocks are identical and that every text/background pair meets WCAG 2.2 AA (4.5:1 text, 3:1 components and focus).
  Semantic colours (ok, warning, error, info) are independent of the brand palette: the brand coral is not the error red.
- **State is never colour alone.** `lib/status.ts` is the single table from API enums to text + distinct icon shape + tone.
- **Keyboard and screen readers.** Skip link, landmarks, one polite `aria-live` region (`lib/live.ts`), ARIA tabs with
  Left/Right/Home/End, focus moves to the page heading after navigation and to the name field in the new instance panel, dialogs return focus
  to their opener, destructive dialogs focus *Cancel* first and Esc cancels, logs are not live regions.
- **Language (Q-029).** `t` is the catalog of the language resolved once when the page loads (`lib/locale.ts`: stored
  preference `theo.lang`, else `navigator.language`, es* -> Spanish); choosing another language saves it and reloads the page,
  so components import a plain `t` and never subscribe to a language signal. `<html lang>` is set before the first render;
  the language names in the selector carry their own `lang`. Counts go through `plural` (Intl.PluralRules) and `num`
  (Intl.NumberFormat), dates through `format.ts`, all with the active locale. Server texts stay in English.
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
| `npm test` | unit tests: design-token contrast, both catalogs have the same keys and non-empty texts (`test/i18n.test.mjs`), SSE parser, status table, port clashes, log store, virtual window, routes | Node 22 |
| `npm run test:e2e` | (includes `e2e/i18n.test.mjs`: Spanish from the browser, the selector, `lang`, axe in Spanish; `e2e/a11y-v02.test.mjs`: axe inside open dialogs, focus after removals and form errors, live-region announcements; `e2e/v56.test.mjs`: shared services, health, settings, history paging, the wizard checkbox, each screen with axe in light and dark) every screen + axe (light and dark) + CSP, flows (start/stop/cancel, wizard, destructive dialogs, secrets, SSE budget), log viewer with 50 000 lines, keyboard walk, forced colours, zoom, security behaviour | the mock server (started by the tests) |
| `npm run test:e2e:real` | the same story against the real `theo serve`: add project with the folder picker, start with real Docker, logs, UC-02 port clash with automatic ports, remove with typed confirmation, secrets, purge, security negatives, loopback-only socket | Docker, Go, `THEO_BIN` or a tree that has `internal/api` |

`e2e/real` builds `./cmd/theo` itself when `internal/api` exists; otherwise set `THEO_BIN` to a binary that embeds the current
`internal/ui/dist`. It uses a temporary `THEO_HOME`, a temporary Git repository, names prefixed `uf`, a fixed host port from
a free range, and removes its containers, volumes, networks and images at the end.

Screenshots are written to `web/e2e/screenshots/` by the tests (never with real data: the data is the mock's or the test repository's).

`e2e/real/v56.test.mjs` (prefix `uf5`) covers the new screens against the real server and Docker: declare, start and stop shared
services, purge refused while an instance is linked and then the typed text, a worktree deleted by hand reported by Health and
forgetting only Theo's stale settings (the worktree finding stays), the diagnostics screen and the history header.
Run it with `THEO_BIN=/path/to/theo npm run test:e2e:real`.

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

## Open in and the agent (0.5)

- **Open in…** (row of a worktree, Project › Instances): Editor, Terminal, Folder and Proxy URL. The programs are the
  ones of `theo config` (`open.editor`, `open.terminal`, `open.files`) or the first usual one found. The last one used in
  each project goes first (kept in this browser). An option that cannot work is disabled and says why next to it:
  tool missing, folder gone, or instance not running (no URL).
- **Agent chip:** Working, Waiting for you, Finished, with the tag *estimated* when the state comes from processes and
  not from Claude Code's hooks. Home shows a **Waiting for you** card next to the forgotten work.
- **Close** (and `theo worktree clean`) is blocked while an agent is alive in the worktree.
