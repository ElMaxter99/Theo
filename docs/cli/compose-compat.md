# Compose compatibility

Theo runs every worktree as its own Compose project (`docker compose -p <name>`).
When an instance needs a change (a host port that is taken, a variable set with
`theo env`, a fixed `container_name`), Theo writes one extra Compose file outside
your project, passes it last with `-f`, and checks the result with
`docker compose config` before starting anything. Your files are never edited.

This page says what was verified against real Compose, what Theo does for each
feature, and where the limits are. Verified on Linux with Docker 29.9.2 and
Compose v5.6.1 (`internal/cli/compat_test.go`, `compat_run_test.go`). Older
Compose versions, macOS and Windows are only covered by what the Compose
documentation says, not by a run: they are marked "unverified".

## Requirements

- `docker compose` plugin **2.24.4 or later** (the generated file uses `!override`
  and `!reset`). Older versions fail with a message that points to `theo doctor`,
  which prints the version found and how to upgrade.
- `include:` needs Compose 2.20, `depends_on.required` 2.20, `env_file.required`
  2.24 (Compose documentation; the 2.24.4 minimum covers all of them).

## What the generated file contains

| Setting | Generated when | Compose spelling |
|---|---|---|
| Ports | `--auto-ports` and a host port is taken | `ports: !override` with the complete list of the service |
| Variables | `theo env set` | `environment:` of each target service, plus a generated env file |
| Container name | `--neutralize-container-names` | `container_name: !reset null` |

Nothing else is generated. In particular Theo never adds `networks` or `ports` to
a service that does not need them, and never touches `network_mode`.

## Feature matrix

Each row was run through Theo with the main instance holding the ports, so the
second instance had to move every one of them. "Run" means the containers were
started and inspected; "config" means the resolved configuration was checked.

| Feature | Result | Verified by |
|---|---|---|
| `include:` (ports of an included service) | Reassigned; the included file is untouched | config, run |
| `extends:` (ports inherited from the extended file) | Inherited and own ports are both reassigned | config, run |
| YAML anchors and `<<:` merge keys | Reassigned; anchors in `x-` blocks are left as they are | config, run |
| `compose.override.yaml` | Kept (see "Override files") | config, run |
| `compose.yaml` plus `docker-compose.override.yml` (any mix of names) | Kept | config |
| `COMPOSE_FILE=a.yaml:b.yaml` in the environment or `.env` | Supported (was refused before) | config |
| `!override` / `!reset` already in your files | Respected; a port list you `!reset` stays empty | config |
| `network_mode: host`, `service:x`, `container:x`, `none` | Never given `ports`/`networks`; ports declared on them are ignored (Docker does not publish them); the mode is not touched | config, run |
| Ports, short syntax `"8080:80"`, `"127.0.0.1:8080:80"`, `"80"` | Reassigned / host IP kept / left to Docker | config, run |
| Ports, long syntax (`target`, `published`, `host_ip`, `protocol`, `mode`) | All fields kept | config, run |
| IPv6 `host_ip: "::1"` | Kept. On a host without IPv6 the port cannot be tested and counts as free | config |
| UDP (`"53:53/udp"`) | Tested and reassigned as UDP, independently of the TCP port with the same number | config, run |
| Port ranges `"9100-9102:90-92"` | Compose 5.6 expands them into single ports and each moves; a host range with a single container port (`"8000-8010:80"`) is left to Compose | config, run |
| `expose:` | Not a host port; untouched | config, run |
| `profiles:` | Services of a profile that is not enabled are not planned. `--profile`, `COMPOSE_PROFILES` in the shell or `.env` are all honoured | config |
| `depends_on` with `service_healthy`, `service_started`, `required: false` | Untouched | config, run |
| `container_name` | Warned about; `--neutralize-container-names` removes it for the instance | config, run |
| `extra_hosts`, `expose`, `env_file` (including `required: false`) | Untouched | config, run |
| `env_file:` plus `theo env set` | The value set with Theo wins inside the container | config, run |
| `build:` plus `image:` | Warns on `up --build` (see "Image names") | run |
| `build:` without `image:` | One image per instance (`<project>-<service>`); nothing to do | run |

The shared-service pieces of the overlay (`profiles: !override`,
`depends_on: !override/!reset`, an extra `networks` entry and a top-level external
network) live in the shared-services work, not in this branch, so they are
**not covered here**. When that work is merged, the same fixtures should be run
through it; nothing in this page contradicts it.

## Bugs found and fixed while verifying

1. **Override files with another name were dropped.** Compose loads an override
   file whatever the extension: `compose.yaml` with `docker-compose.override.yml`
   works. Theo only looked for the same extension, so adding its file would
   silently remove your override. It now searches `compose.override.yml`,
   `compose.override.yaml`, `docker-compose.override.yml`,
   `docker-compose.override.yaml` (the first that exists, as Compose does).
2. **Theo no longer guesses which files Compose loads.** Before writing its file
   it asks Compose for the configuration with and without the file list it
   worked out, and stops with a clear error if they differ. A wrong guess can no
   longer drop a file silently.
3. **`COMPOSE_FILE` is supported** (process environment, `--env-file`, or `.env`;
   `COMPOSE_PATH_SEPARATOR` too). `--auto-ports` and `theo env` used to refuse.
4. **A `$` in a value set with `theo env` was interpolated** by Compose when
   reading the generated file (`A='pre$HOME'` reached the container as `pre/root`).
   It is now written as `$$` and arrives as typed.
5. **The generated env file replaced the worktree's own `.env`.** Any `--env-file`
   makes Compose skip `.env`, so `theo env set` silently removed the interpolation
   variables of a worktree that had its own `.env`. Theo now names the `.env`
   first, then the generated file.
6. **An address this host does not have** (an IPv6 `host_ip` on a machine without
   IPv6) was treated as "port taken", so `--auto-ports` searched 1000 ports and
   failed with "no free host port". It now counts as not testable, like a
   privileged port: Docker reports a real conflict.
7. **`COMPOSE_PROFILES`** was ignored when planning ports, so a service enabled
   only that way kept a taken port.
8. **Services with `network_mode: host/service:x`** that declared ports were
   planned and "reassigned" although Docker does not publish them. They are
   skipped.
9. Theo now also checks that the resolved configuration really applies the
   variables and the removed `container_name`, not just the ports.

## Override files and `-f`

Passing any `-f` turns off Compose's own file search, so Theo must name the files
itself. It uses `COMPOSE_FILE` if set, else the first of `compose.yaml`,
`compose.yml`, `docker-compose.yaml`, `docker-compose.yml` plus an override file
(order above), then its own file last. Compose also searches parent directories
when the current one has no Compose file; Theo does not, and says so. Put
`COMPOSE_FILE=...` in the `.env` if your files live elsewhere.

## Image names (B-009)

With several instances of one project (worktrees), the image name decides whether
they share an image:

| Compose file | Image name | Instances |
|---|---|---|
| `build: .` only | `<project>-<service>` | one image each |
| `build: .` and `image: myapp:dev` | `myapp:dev` | **one tag shared**: the last build wins |
| `build: .` and `image: myapp-${COMPOSE_PROJECT_NAME}` | `myapp-<project>` | one image each |
| `image: redis:7` (no build) | `redis:7` | shared, which is what you want |

With a shared tag, building in one worktree retags the image: containers already
running keep the old image, but the next recreate of any other instance uses the
new one, built from a different branch. `theo up --build` warns and names the
services (`theo: warning: services built into a fixed image name ... web
(myapp:dev)`) when the project has more than one worktree. Theo finds them by
resolving the configuration under two project names: an image that changes with
the name is per instance. Workarounds: drop `image:`, or use
`image: myapp-${COMPOSE_PROJECT_NAME}` (Compose sets that variable to the
instance name).

## Limits

- Ports of services in `network_mode: host` bind directly on the host; Theo
  cannot move them and does not check them.
- A port that is "random" (`"80"` without a host port) is never planned.
- A host port range is left to Compose.
- Ports on an address this host does not have cannot be tested before `up`.
- Each reassigned service gets its complete `ports:` list rewritten with long
  syntax; comments in your file are irrelevant (it is not edited), but
  `docker compose config` shows the long form.
- Compose 5.6.1 prints a literal `$` as `$$` in `config` output; Theo accepts
  both spellings when it checks the result, in case other versions differ
  (unverified).
