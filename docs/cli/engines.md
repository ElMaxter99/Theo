# Container engines

Theo never talks to a container engine directly. It runs the `docker` CLI
(`docker compose ...`, `docker volume ...`, `docker network ...`) and parses
its output. So the question is not "which engine does Theo support" but
"which engine answers the docker CLI the way Docker does". `theo doctor` names
the engine it finds (the `engine` check) and lists what differs.

## Support matrix

| Engine | Linux | macOS | Windows | Status |
|---|---|---|---|---|
| Docker Engine 29 (dockerd) | **verified** | — | — | Theo's Docker tests run against it |
| Docker Engine, rootless | expected to work | — | — | same API; ports below 1024 need setup |
| Docker Desktop | expected to work | expected to work | expected to work (WSL 2 backend, Linux containers) | CI builds and unit tests only |
| Colima (docker runtime) | expected to work | expected to work | — | not tested |
| OrbStack | — | expected to work | — | not tested |
| Rancher Desktop, **dockerd (moby)** engine | expected to work | expected to work | expected to work | not tested |
| Rancher Desktop, **containerd** engine (nerdctl) | unsupported | unsupported | unsupported | no Docker API, no `docker` CLI |
| Podman, Docker's CLI + Compose pointed at Podman's socket | expected to work, with differences | expected to work, with differences | expected to work, with differences | not tested |
| Podman's `podman-docker` wrapper as `docker` | unsupported | unsupported | unsupported | output formats differ |
| `podman-compose` as `docker compose` | unsupported | unsupported | unsupported | not Docker Compose |
| Docker Desktop in Windows containers mode | — | — | unsupported | `theo doctor` fails `docker-os` |

**What has actually been verified:** Docker Engine 29 with Compose 5 on Linux
(the integration tests and the `examples/demo` guide). Everything else above is
"expected" from the engines' own Docker-API compatibility, and `theo doctor`'s
detection of each engine is tested only against hand-written `docker info`
samples (`internal/engine/testdata/engines`), not against real installs.
Reports from other engines are welcome.

## Pointing Theo at an engine

Theo passes its environment unchanged to every `docker` it runs (the command
line, `theo serve`/`theo ui` and the desktop app alike), so the docker CLI
picks the engine the usual way, in this order:

1. `DOCKER_HOST` (an address: `unix://`, `npipe://`, `tcp://`, `ssh://`);
2. `DOCKER_CONTEXT` (a context name);
3. the current context of `docker context use`, kept in `~/.docker/config.json`.

`theo doctor` shows which one chose the endpoint (`endpoint_from` in `--json`).

| Engine | Usual way |
|---|---|
| Docker Desktop | its own contexts `desktop-linux` / `default`, chosen at install |
| Colima | `colima start` creates and selects the `colima` context (`colima-<profile>` for other profiles); socket `~/.colima/<profile>/docker.sock` |
| OrbStack | the `orbstack` context; socket `~/.orbstack/run/docker.sock` |
| Rancher Desktop | Preferences > Container Engine > **dockerd (moby)**; context `rancher-desktop`, socket `~/.rd/docker.sock` (macOS/Linux) |
| Podman (Linux) | `systemctl --user enable --now podman.socket`, then `export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/podman/podman.sock` (or `docker context create podman --docker host=...`) |
| Podman (macOS/Windows) | `podman machine start`; use the socket path `podman machine inspect` prints (Podman Desktop can also expose it as the default Docker socket) |

The desktop app on macOS is started by Finder, which does not read your shell
profile: a `DOCKER_HOST` exported in `~/.zshrc` does not reach it. Prefer a
context (`docker context use colima`), which lives in `~/.docker/config.json`
and works from everywhere.

### THEO_DOCKER

`THEO_DOCKER` makes Theo run another program wherever it runs `docker`: an
absolute path (`/opt/docker/bin/docker`) or a command name looked up in `PATH`.
A relative path such as `./docker` is refused, because Theo runs commands in
other directories. Use it for a second Docker CLI that is not first in `PATH`.

It does **not** turn Podman's own command into a supported engine:
`THEO_DOCKER=podman` runs, but Theo passes Docker's arguments and parses
Docker's output (`docker volume ls --format '{{json .}}'`, `docker compose ls`,
`docker info`), and Podman prints some of these differently. Use Docker's CLI
with Podman's socket instead. `theo --explain` keeps printing `docker` in its
listing.

## Differences that affect Theo

| Theo feature | Docker Engine / Desktop | Colima, OrbStack, Rancher Desktop (moby) | Podman (Docker CLI + socket) |
|---|---|---|---|
| Published ports (`theo ports`, `--auto-ports`) | on the host | forwarded to `localhost` by the VM | rootless: user-space port forwarding (pasta/slirp4netns); ports below 1024 need `net.ipv4.ip_unprivileged_port_start` |
| `host.docker.internal` / `host-gateway` | supported | supported by dockerd; what it reaches depends on the VM's network | rootless containers see the host as `host.containers.internal`; `host-gateway` depends on the Podman version |
| Named volumes, `theo volumes` backup/restore/clone | yes | yes (inside the VM) | yes through the API; `docker volume ls` labels are read by Theo |
| Bind mounts of worktrees | any path (Docker Desktop: shared folders on macOS) | only folders shared with the VM: Colima shares `~` and `/tmp/colima`, Rancher Desktop `~`; a worktree elsewhere starts empty | any path on Linux, may need `:z` on SELinux; on macOS only folders the Podman machine mounts (`/Users`, `/private`, `/var/folders`) |
| External networks (`shared`, `docker network create`) | yes | yes | yes (netavark); Theo's label filter works through the API |
| `docker compose ls` (`theo ps`, `reconcile`) | yes | yes | yes, with Docker's Compose |
| Compose | the plugin, 2.24.4 or later | the plugin, 2.24.4 or later | **Docker's** Compose 2.24.4 or later (plugin, or the `docker-compose` v2 binary as Podman's compose provider); `podman-compose` fails `theo doctor` |

A remote engine (`DOCKER_HOST=ssh://...` or `tcp://` to another machine) is
reported as a warning whatever it is: bind mounts use that machine's paths and
published ports open there, so worktrees only work if the same paths exist on
it.

## What `theo doctor` reads

* `docker info --format '{{json .}}'`: `ServerVersion`, `OSType`,
  `OperatingSystem` (`Docker Desktop`, `OrbStack`, `Rancher Desktop WSL
  Distribution`), `Name` (`docker-desktop`, `orbstack`, `colima`,
  `lima-rancher-desktop`), `Labels` (`com.docker.desktop.*`), `DockerRootDir`
  (`…/containers/storage` is Podman's), `SecurityOptions` (`name=rootless`).
  From the podman-docker wrapper it gets Podman's own `podman info` document
  and recognises it.
* `docker version --format '{{json .Server}}'`: `Platform.Name` (`Docker
  Engine - Community`, `Docker Desktop 4.x`) and `Components[].Name` (`Podman
  Engine`).
* `docker context inspect`: the context name and its endpoint.
* `DOCKER_HOST`, `DOCKER_CONTEXT`, `THEO_DOCKER`.

Daemon facts win over context and socket names, which are only hints. When
the daemon does not answer, the hints still pick the advice: `colima start`,
start OrbStack, switch Rancher Desktop to dockerd (moby), start Podman's
socket or machine.
