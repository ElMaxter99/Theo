# theo doctor

`theo doctor [--json]` checks what Theo needs and says how to fix what is wrong.
It changes nothing except creating Theo's data directory when it does not exist
yet. Exit code 0 when everything needed works, 1 when a check failed; warnings
and notes never fail it.

| Check | Fails when | Fix it prints |
|---|---|---|
| git | missing, or older than 2.17 (`git worktree remove`) | install or upgrade Git |
| docker | the `docker` CLI is not in PATH | install Docker (Engine, or Docker Desktop on macOS/Windows) |
| Docker daemon | `docker info` cannot reach it | Linux: `systemctl start docker`, docker group; others: start Docker Desktop |
| Docker containers | the engine runs Windows containers | switch to Linux containers |
| docker compose plugin | missing, or older than **2.24.4** (`!override`, `!reset`) | install or upgrade the plugin; says if only the old `docker-compose` is found |
| Theo data directory | `THEO_HOME` (or the user config dir) cannot be created or written | fix permissions or set `THEO_HOME` |
| Theo state files | `registry.json`, `ports.json`, `instances.json`, `history.jsonl` exist but are unreadable or not valid JSON | fix or move the file (Theo never deletes it) |

Platform notes: on macOS, a warning when a registered project lives outside the
folders Docker Desktop shares by default (`/Users`, `/Volumes`, `/private`, `/tmp`,
`/var/folders`); on Windows, whether long paths are enabled (warning if not, with
the command to enable them) and a note about keeping projects in the WSL 2 file
system.

`theo up`, `theo ports --auto-ports` and `theo instance config` print the same
minimum and point here when Compose is too old to read the file Theo generates.
The version is read once per process.

`--json` prints `{ok, platform, data_dir, checks: [{id, name, status, detail, fix}]}`
with `status` one of `ok`, `warn`, `fail`, `info`.

Only the Linux checks were run against real tools; the macOS and Windows branches
are covered by unit tests with fake tools.
