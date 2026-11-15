# Tools (0.9)

Theo's **tools** are self-contained features with their own scope. The first is
the screen capture ("before and after"), planned in
`roadmap/plan-herramienta-capturas.md` of Theo-workspace. This page covers what
exists so far: starting an application (0.8) and capturing, comparing and
reporting its screens (0.9).

```sh
theo tool list [--json]                         # the tools, what works today and what is planned
theo tool capture detect [path] [--write] [--json]
theo tool capture serve <project> [worktree] [--yes] [--check] [--json]
theo tool capture validate [path] [--json]
theo tool capture init [path]
theo tool capture setup [--yes]
theo tool capture run <project> [worktree] [--label L] [--url U] [--mode M] [--again] [--yes] [--json]
theo tool capture run-pair <project> <before> <after> [--open] [--pr] [--again] [--yes] [--json]
theo tool capture sets <project> [--json]
theo tool capture compare <project> <before> <after> [--open] [--json]
theo tool capture report <project> <label> [<label>] [--corporate|--summary|--failed] [--json]
```

## The `run` section of `.theo.json`

A repository says how its application starts, so a tool can bring up a
deployment by itself:

```json
{
  "version": 1,
  "run": {
    "kind": "script",
    "install": ["npm ci"],
    "start": "npm run dev -- --port ${PORT}",
    "port": { "env": "PORT", "auto": true },
    "env": { "API_URL": "http://localhost:4000" },
    "ready": { "path": "/", "status": [200, 304], "timeout_s": 90 }
  }
}
```

- `kind`: `script` (one process in the worktree) or `compose` (Theo starts it
  with `theo up`; `service` names the one that answers on the web). Empty means
  `script` when `start` is set.
- `install`, `start`: shell commands. They run code from the repository, so
  Theo will ask before running them, as it does for `after_create`
  (`repoconfig.RunFingerprint` identifies what was approved; a change asks again).
- `${PORT}`, or the variable named in `port.env`, is replaced in `start` and in
  the values of `env`. With `port.auto` (the default) Theo picks a free port;
  with `"auto": false`, `port.fixed` is used.
- `ready`: the application is up when `GET <path>` answers one of `status`
  (default `/` and 200 or 304) within `timeout_s` (default 90).
- A mistake in the section is an error, not ignored: `theo doctor` reports it.

### Kind `static`: an application that only generates files

Some applications (an AngularJS one, for example) have no server: a script
builds a folder and a web server is expected to serve it under a path such as
`/portal`. Theo is that server:

```json
{
  "version": 1,
  "run": {
    "kind": "static",
    "install": ["npm ci"],
    "build": ["npm run build"],
    "dir": "dist",
    "base": "/portal",
    "spa": true,
    "proxy": { "/portal/api": "http://localhost:4000" },
    "ready": { "path": "/portal/" }
  }
}
```

- `build` (commands) and `dir` (the folder the build leaves, inside the
  worktree) are required with `base` (a path starting with `/`; `/` for the
  root). There is no `start`. Kind `static` is also assumed when `dir` or `build`
  is set without `start`.
- Theo serves `dir` under `base` on a free port of its own, so two branches run
  at once and both answer on `/portal/`. `GET /` redirects to `base/`, anything
  outside `base` is 404.
- `spa: true` answers a path without a file and without an extension (an HTML5
  route) with `index.html`.
- `proxy` sends the paths under a prefix to a server that already exists (the
  API), keeping the path.
- `env` is given to the build commands.
- If the built `index.html` has an absolute `<base href="https://...">`, Theo
  warns: the app would load its files from that address, not from Theo.
- Cookies are shared between ports of the same host, so two instances open in
  one browser can overwrite each other's; the `<branch>.<project>.localhost`
  addresses of the proxy will fix that later.
- `build` commands run code from the repository: they are approved like
  `install` and `start`.

## `theo tool capture detect`

Looks at the folder (the current one by default) and proposes a `run` section,
with fixed rules and no AI: a Compose file, then a `package.json` script (`dev`,
`start` or `serve`; Vite, SvelteKit, Next and Angular get the port as a flag; the
package manager comes from the lock file), then a Makefile target. An AngularJS 1.x project (`angular` dependency and a
`build` script) gets a `static` section; `base` comes from the `<base href>` of
its `index.html` (`src/`, `app/` or the root). It says its
confidence (`high`, `medium`, `low`) and why. Without `--write` nothing changes;
with it the section is added to `.theo.json` (other keys are kept, an existing
`run` section is never replaced).

## `theo tool capture serve`

Starts the application a worktree's `run` section describes (kind `script` or `static`):
runs `install`, then `start` on a free port (or `build`, and serves the folder
itself), waits until `ready` answers, prints the URL and keeps it until Ctrl-C, which stops the whole process tree.

- The commands run code from the repository, so Theo asks the first time and
  whenever the commands or `env` change (`--yes` approves without asking; without
  a terminal it refuses and says so). The approval is remembered per project.
- `--check` starts it, reports the URL and stops: use it to test a run section
  (for example one written by `detect --write`) before relying on it.
- A `compose` section is started with `theo up`, not with this command.

## Before and after screenshots (0.9)

### The inventory: `.theo/capturas/pantallas.json`

The file lists what to capture: viewports, screens (`pantallas`), their states
(`estados`, with the mock data and the actions that reach them) and flows
(`flujos`, a sequence of `pantalla#estado`). It lives in the repository, so each
branch says what its own screens are. `theo tool capture validate` checks it
against the schema (`internal/tools/capture/schema/pantallas.v1.json`) and says
where it is wrong (`screens[1].states[0].actions[0]`); `init` writes a starting
file. There is no AI generation yet: it arrives in 0.10.

```json
{
  "version": 1, "app": "Portal", "entrada": "panel",
  "viewports": [{ "id": "desktop", "width": 1280, "height": 720 }, { "id": "movil", "width": 390, "height": 800, "mobile": true }],
  "pantallas": [
    { "id": "panel", "titulo": "Panel", "ruta": "/", "origen": ["src/panel.html"], "prioridad": "alta", "estados": [{ "id": "base" }] }
  ],
  "flujos": [{ "id": "abrir", "pasos": ["panel#base"] }]
}
```

### The capture engine

Capturing needs Node 20 or newer, Playwright and Chromium. `theo tool capture setup`
installs them once in Theo's data folder (about 320 MB, needs the network);
nothing is installed in your projects. `theo doctor` checks the engine for
projects that have an inventory. Set `THEO_CAPTURE_CHROMIUM` to use a Chromium
you already have.

### Capture, compare, report

- `run` captures one deployment: every state at every viewport, **twice**, to
  find what is unstable (animations, clocks, data that changes). The clock is
  frozen, motion is off, and API calls are answered from the inventory's mocks
  (`--mode real` calls the real API, `--mode grabar` records it); an API call with
  no mock fails that capture and says so. The result is a *set*:
  `.theo/capturas/conjuntos/<label>-<commit>/` with `manifest.json` and `shots/`.
  A set of the same commit is reused unless the tree is dirty or `--again`.
- `run-pair` starts the two branches (their `run` section, approved like in
  `serve`), captures both, compares, writes the reports and stops them. `compare`
  does the same from two saved sets.
- The comparison pairs screens by id, then by route, then by the source files
  they share (`origen`), and classes each as `same`, `minor` (up to 1 % of the
  pixels, or a few hundred pixels), `changed`, `new`, `removed`, `moved`,
  `unstable` or `not_captured`. It also compares the flows and lists what it
  could not cover (screens with a changed source file and no capture, API calls
  with no mock).
- Reports go to `.theo/capturas/informes/<date>-<before>-vs-<after>/`:
  `corporativo.html` and `.pdf` (desktop and mobile side by side, a page per
  screen), `composiciones.html` with `composiciones/*.png` (before, after and
  difference for each state), `resumen.md` (totals, changed screens and the
  percentage of pixels, flows, what was not captured; paste it into a pull
  request with `--pr`) and `comparacion.json` (everything the others are made
  from). `report --failed` lists what failed and why.
- The reports are written in Theo's language: the `language` preference
  (`theo config set language en|es|system`), then `LC_ALL`/`LANG`.
- `.theo/capturas/.gitignore` keeps the sets and reports out of Git.

### The Tools page

The web interface has a **Tools** page (below Problems). It does what `run-pair`
does: choose two branches that have a worktree, start, follow the progress (the
same numbers the command prints), and open the compositions, the corporate report
and `resumen.md` of each report. It also installs the engine and lists the saved
sets. Reports made from the page use the interface's language. The API behind it
is under `/api/v1/projects/{p}/capture` (state, `compare`, `runs/{op}`,
`reports/{name}/{file}`) and `/api/v1/capture/setup`; the long actions are
operations.

## Not built yet

The generation of the inventory with AI (0.10). `theo up` does not start
`script` applications yet.
