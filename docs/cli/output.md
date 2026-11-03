# Output and exit codes

The contract for scripts, the GUI and agents that call `theo`.

## Streams

* **Standard output** carries the result: a table, the JSON of `--json`, the
  output of `logs` and `exec`, or the `--explain` listing.
* **Standard error** carries everything else: every error (`theo: <message>`),
  warnings, and context lines such as `theo: main · /work/shop (compose
  project "shop")` and the progress of `docker compose up`.
* An error never goes to standard output, and a failed command prints nothing
  on standard output (the exception is `--explain`, which prints its listing
  first). With `--json`, errors are still plain text on standard error: parse
  standard output only when the exit code is 0.

## Exit codes

| code | meaning |
|---|---|
| 0 | success |
| 1 | the operation failed: unknown project or worktree, Docker or Git failed, a safety check refused (uncommitted files, containers still exist, no confirmation) |
| 2 | wrong usage: unknown command or flag, missing or extra argument. The error is followed by `  → theo help <command>` on standard error |
| *n* | only `theo exec` and `theo shell`: the exit code of the command run in the container. Theo prints nothing of its own. Docker's own failures in `exec` (service not running) are 1, so `exec` cannot tell them from a command that exits with 1 |

`theo help` (the commands by topic), `theo help <command>`, `theo help all`
(the full reference) and `theo <command> -h` print the help on standard output
and exit 0.

## `theo` with no arguments

Since 0.2, `theo` alone prints a summary of what runs, in every project, and
exits 0 (it used to print the help and exit 2). Each worktree is one line, with
the state symbol the interface uses, the services running out of the total, and
the address of a running instance or its first warning; running and failing
instances come first, and a project with many worktrees shows five and counts the
rest. The last line counts the warnings and points to `theo status` and `theo
help`. Scripts should keep using `theo status --json`.

| symbol | state |
|---|---|
| `▶` | running |
| `◐` | partial: some services are not running |
| `▲` | unhealthy |
| `■` | stopped |
| `○` | not created |
| `?` | unknown (Docker could not be asked) |

## The project from the current folder

When a command that takes a project leaves it out, or gives `.`, Theo takes
the project and the worktree from the current folder: `theo up` inside a
linked worktree starts that worktree, and `theo logs . web -f` follows its
`web` service. Theo says so on standard error with a context line such as
`theo: shop · fix-123 (from the current folder)`. An explicit project name is
never changed: `theo up shop` still means the main worktree, as before. `ps`,
`worktrees` and `branches` only take the project. Outside a registered project
nothing is filled in, and the command fails as it always did.

## Errors and the next step

An error is `theo: <message>`; when Theo knows what to do next, a second line
starts with `→` (Docker not running, a host port in use, an unknown project or
worktree, a missing tool).

## Colors

`--color auto|always|never`, before the command, chooses ANSI colors; `auto`
(the default) colors only a terminal and honors
[`NO_COLOR`](https://no-color.org) and `TERM=dumb`. `--no-color` is
`--color never`. `--json` output is never colored.

## `--json`

Every command that prints data takes `--json`. The output is one JSON
document, indented, followed by a newline.

| command | `--json` document |
|---|---|
| `version` | `{"version": "…"}` |
| `project list` | array of projects |
| `project add`, `project set`, `project remove` | the project |
| `worktrees`, `branches` | array |
| `worktree add` | the new worktree (path, branch, env file, notes) |
| `worktree remove` | `{"path", "branch"}` of the removed worktree |
| `services` | the resolved services of a worktree |
| `ps` | without `-w`: worktrees with their Compose projects; with `-w`: the instance and its containers |
| `ports`, `volumes`, `env list`, `instance show`, `history`, `reconcile` | as documented by each command |
| `volumes purge` | `{"compose_project", "deleted": […]}` |
| `instance config` | the instance and `"config"`: the resolved Compose configuration as text |
| `status` | see [status.md](status.md) |
| `proxy routes` | `{"port", "proxy_running", "routes": […]}`, see [proxy.md](proxy.md) |

Rules:

* A list is `[]` when empty, never `null`.
* Optional fields are left out, not `null`.
* Secrets are masked (`env list`, `instance config`) or only counted (`status`).

Commands that act and report only success (`up`, `down`, `stop`, `start`,
`restart`, `recreate`, `env set`, `env unset`, `instance set`, `project …` aside
from the three above) print one confirmation line and have no `--json`; their
exit code is the result. `logs` and `exec` stream and have none either.

## Under `--explain`

See [explain.md](explain.md). Standard output is the listing and nothing else;
what the command would have printed is dropped.
