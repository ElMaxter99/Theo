# Tools (0.8, in progress)

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

## `theo tool capture detect`

Looks at the folder (the current one by default) and proposes a `run` section,
with fixed rules and no AI: a Compose file, then a `package.json` script (`dev`,
`start` or `serve`; Vite, SvelteKit, Next and Angular get the port as a flag; the
package manager comes from the lock file), then a Makefile target. It says its
confidence (`high`, `medium`, `low`) and why. Without `--write` nothing changes;
with it the section is added to `.theo.json` (other keys are kept, an existing
`run` section is never replaced).

## `theo tool capture serve`

Starts the application a worktree's `run` section describes (kind `script`):
runs `install`, then `start` on a free port, waits until `ready` answers, prints
the URL and keeps it until Ctrl-C, which stops the whole process tree.

- The commands run code from the repository, so Theo asks the first time and
  whenever the commands or `env` change (`--yes` approves without asking; without
  a terminal it refuses and says so). The approval is remembered per project.
- `--check` starts it, reports the URL and stops: use it to test a run section
  (for example one written by `detect --write`) before relying on it.
- A `compose` section is started with `theo up`, not with this command.

## Not built yet

The capture engine, the comparison, the reports and the AI generation are the
next steps of the plan. `theo up` does not start `script` applications yet.
