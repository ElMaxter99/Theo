# theo doctor

`theo doctor [--json]` checks what Theo needs and says how to fix what is wrong.
It changes nothing except creating Theo's data directory when it does not exist
yet. Exit code 0 when everything needed works, 1 when a check failed; warnings
and notes never fail it.

| Check | Fails when | Fix it prints |
|---|---|---|
| git | missing, or older than 2.17 (`git worktree remove`) | install or upgrade Git |
| docker | the `docker` CLI is not in PATH (or `THEO_DOCKER` cannot be run) | install Docker (Engine, or Docker Desktop on macOS/Windows); fix or unset `THEO_DOCKER` |
| Docker daemon | `docker info` cannot reach it | for the engine the context points at: Linux `systemctl start docker` and the docker group, Docker Desktop, `colima start`, OrbStack, Rancher Desktop in dockerd mode, Podman's socket or machine; says when `DOCKER_HOST` is set |
| Docker containers | the engine runs Windows containers | switch to Linux containers |
| Container engine | never fails | names the engine behind docker (Docker Engine, Docker Desktop, Colima, OrbStack, Rancher Desktop, Podman), its context and endpoint; `ok` for Docker Engine and Docker Desktop, a note for Colima, OrbStack and Rancher Desktop, a warning for Podman and for a remote engine, with the differences as notes. See [engines.md](engines.md) |
| docker compose plugin | missing, older than **2.24.4** (`!override`, `!reset`), or `podman-compose` | install or upgrade the plugin; says if only the old `docker-compose` is found |
| git speed | never fails; warns when Git is older than **2.41** (listing many worktrees is slower) | upgrade Git |
| `.theo.json` | never fails; warns when the file of a registered project is not valid JSON; a note when projects use it | fix the JSON |
| ports per branch | never fails; a note when a registered project sets `COMPOSE_FILE`, a fixed `container_name` or `${VAR}` in `ports:` | see [ports.md](ports.md#limits) |
| Claude Code hooks | never fails; a note when Theo's hooks are in none of the user's or the projects' settings | `theo mcp install --hooks` |
| Theo data directory | `THEO_HOME` (or the user config dir) cannot be created or written | fix permissions or set `THEO_HOME` |
| Theo state files | `registry.json`, `ports.json`, `instances.json`, `history.jsonl` exist but are unreadable or not valid JSON | fix or move the file (Theo never deletes it) |

Platform notes: on macOS, a warning when a registered project lives outside the
folders the engine shares by default (Docker Desktop and OrbStack: `/Users`,
`/Volumes`, `/private`, `/tmp`, `/var/folders`; Colima: your home and
`/tmp/colima`; Rancher Desktop: your home and `/tmp/rancher-desktop`; Podman
machine: `/Users`, `/private`, `/var/folders`), left out for a remote engine; on Windows, whether long paths are enabled (warning if not, with
the command to enable them) and a note about keeping projects in the WSL 2 file
system.

`theo up`, `theo ports --auto-ports` and `theo instance config` print the same
minimum and point here when Compose is too old to read the file Theo generates.
The version is read once per process.

`--json` prints `{ok, platform, data_dir, checks: [{id, name, status, detail, fix, notes}], engine}`
with `status` one of `ok`, `warn`, `fail`, `info`. `notes` (a list of sentences)
is left out when empty. `engine` is left out when docker is not installed;
otherwise it is `{kind, name, version, operating_system, context, endpoint,
endpoint_from, cli, rootless, remote, support, notes}` with `kind` one of
`docker-engine`, `docker-desktop`, `colima`, `orbstack`, `rancher-desktop`,
`podman`, `unknown`, `endpoint_from` one of `DOCKER_HOST`, `DOCKER_CONTEXT`,
`context`, and `support` one of `verified`, `expected`, `unsupported` (empty
when the daemon did not answer). Empty fields are left out. Both are additions:
the earlier fields keep their names and meaning.

Only the Linux checks were run against real tools; the macOS and Windows branches
and every engine other than Docker Engine are covered by unit tests with fake
tools and hand-written `docker info` samples. See [engines.md](engines.md) for
the support matrix.
