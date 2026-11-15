# Tools (0.8)

Theo's **tools** are self-contained features with their own scope. The first is
the screen capture ("before and after"), planned in
`roadmap/plan-herramienta-capturas.md` of Theo-workspace. This page covers what
exists so far.

```sh
theo tool list [--json]                         # the tools, what works today and what is planned
theo tool capture detect [path] [--write] [--json]
theo tool capture serve <project> [worktree] [--yes] [--check] [--json]
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

## Not built yet

The capture engine, the comparison, the reports and the AI generation are the
next steps of the plan. `theo up` does not start `script` applications yet.
