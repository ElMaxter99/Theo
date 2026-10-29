# Theo

> **Status:** early prototype. Theo can register a project, show its worktrees and the services Docker Compose resolves for each of them, and start, stop and inspect each worktree as its own Compose project. It does not yet reassign ports, so two worktrees that publish the same fixed port cannot run at the same time.

Theo is a cross-platform tool (macOS, Linux and Windows) for managing development projects and **multiple isolated environments based on Git and Docker Compose**. The goal is to let a developer, or several AI agents, work at the same time on different branches, features and fixes of the same project, each one with its own running environment, while choosing which services are isolated and which ones are deliberately shared.

## Design principles

- **Orchestrate, don't reimplement.** Theo drives the `git` and `docker compose` tools you already use and relies on their own behaviour and validation.
- **Never touch your files.** Your project's Compose files stay as they are; Theo adds its own configuration per instance.
- **Safe by default.** No destructive operation (deleting volumes, data or worktrees) happens implicitly.
- **Local engine first.** All logic lives in a local engine. The first interface is a minimal command-line tool; a graphical interface will be built later on top of the same engine.
- **Cross-platform from day one.** macOS, Linux and Windows are supported by design, not as an afterthought.

## Requirements

- [Go](https://go.dev/dl/) 1.24 or later, to build from source.
- Git and Docker with the Compose plugin, in your `PATH`. Reading a project's services runs `docker compose config`, which does not need the Docker daemon. Starting and inspecting containers does.

## Build and run

```sh
go build ./cmd/theo
./theo help
```

Or run it directly with `go run ./cmd/theo <command>`.

## Usage

```sh
# Register a Git repository. From a linked worktree, its main worktree is registered.
theo project add ~/code/my-api --name my-api
theo project list

# Worktrees of the project: branch, uncommitted changes and path.
theo worktrees my-api

# Services as `docker compose config` resolves them: image or build, ports,
# mounts (bind mounts outside the worktree are flagged), dependencies,
# networks and profiles. Services behind profiles are included.
theo services my-api                       # main worktree
theo services my-api --worktree feature/x  # by branch, directory name or path

# Git creates linked worktrees without untracked files such as .env, so they
# share an env file. In order: --env-file; the worktree's own .env; the
# project's env file; the main worktree's .env.
theo project set my-api --env-file ~/secrets/my-api.env   # or --no-env-file
theo services my-api -w feature/x --env-file other.env    # one-off override

# Branches and worktrees. Theo never forces: a branch that is checked out in
# another worktree is refused, and nothing is deleted without confirmation.
theo branches my-api                                   # local and remote, and which have a worktree
theo worktree add my-api feature/x                     # existing branch (or a single remote one)
theo worktree add my-api fix/bug-1 --new --from main   # new branch
theo worktree add my-api feature/x --path ~/trees/x    # choose the directory
theo project set my-api --worktree-dir ~/trees/my-api  # default directory for this project
# Without --path or --worktree-dir, worktrees go to <Theo data dir>/worktrees/<project>/<branch>.
# Git does not copy ignored files such as .env: Theo says which are missing and
# which env file the worktree will use by reference. Nothing is copied.
theo worktree remove my-api feature/x
# Refused while the worktree has containers (run `theo down` first) or
# uncommitted or untracked files, which are listed. To lose them on purpose:
theo worktree remove my-api feature/x --discard-changes --confirm <directory-name>
# The main worktree is never removed, and neither are branches or volumes.
# Worktrees created with plain `git worktree add` show up in `theo worktrees` too.

# Run a worktree. Each worktree is its own Compose project: the main worktree
# keeps the name Compose gives it, and linked worktrees get
# <project>-<directory>, so their containers, networks and volumes never mix.
theo up my-api -w feature/x [--build] [--profile worker] [--auto-ports]
theo ps my-api                  # which worktrees have containers
theo ps my-api -w feature/x     # containers, state and ports of one worktree
theo logs my-api -w feature/x -f --tail 100 [service...]
theo restart my-api -w feature/x
theo down my-api -w feature/x   # removes containers and networks, never volumes

# Host ports. Two worktrees that publish the same fixed port cannot run at
# once. `ports` shows what each service publishes and whether the port is free;
# `up` warns when one is taken. With --auto-ports, `up` publishes the services
# whose port is taken on another free one (the next free port after the
# configured one) and remembers it, so the address stays the same on restart.
# Your Compose files are not modified: Theo adds a generated Compose file for
# that instance (needs Docker Compose 2.24.4 or later). Ports of other
# instances Theo has assigned count as taken.
theo ports my-api -w feature/x [--auto-ports] [--profile worker]
theo up my-api -w feature/x --auto-ports

# Per-instance settings. They never touch your Compose files or .env: Theo
# generates a Compose file and an env file for the instance (outside the
# project, readable only by you) and runs it with them.
theo env set my-api -w feature/x LOG_LEVEL=debug [--service api]
theo env set my-api -w feature/x API_TOKEN=... --secret   # masked when shown
theo env list my-api -w feature/x [--show-secrets]
theo env unset my-api -w feature/x LOG_LEVEL
# A fixed container_name stops a second instance from starting (`up` warns).
theo instance set my-api -w feature/x --neutralize-container-names
theo instance show my-api -w feature/x
theo instance config my-api -w feature/x   # what it will run with; secrets masked
theo history my-api [-w feature/x] [--limit 20]   # up, down and restart, with result and user

# Unregister the project. Files, containers and volumes are not touched.
theo project remove my-api
```

`project list`, `worktrees` and `services` accept `--json` for scripts and agents.

Theo never changes your files or branches and never deletes volumes. Only `up`, `down` and `restart` change containers.

Values set with `theo env --secret` are stored in plain text in `instances.json` and in the generated files, which are readable only by your user; Theo masks them in its own output, but they are not encrypted. Passing a secret on the command line leaves it in your shell history.

`--auto-ports` does not support projects that set `COMPOSE_FILE` yet, and leaves random ports (`"3000"`) and ranges (`"8000-8010"`) as they are. The ports it assigned are in `ports.json`, next to the registry, and the generated Compose files in `overlays/`.

Theo keeps its registry in `theo/registry.json` inside your user configuration directory (for example `~/.config` on Linux, `~/Library/Application Support` on macOS, `%AppData%` on Windows). Set `THEO_HOME` to use another directory.

## Development

```sh
go vet ./...
go test ./...
```

Some tests use the real `git` and `docker compose` tools, and a Docker daemon that runs Linux containers. They are skipped when those are not available. They build a tiny image `FROM scratch`, so they never pull images.

## Project layout

| Path | Contents |
|---|---|
| `cmd/theo/` | Entry point of the `theo` command-line tool. |
| `internal/cli/` | Command-line interface: argument parsing and output. No domain logic. |
| `internal/engine/` | Local engine: domain logic and operations shared by every interface. |
| `internal/registry/` | Persistent registry of projects. |
| `internal/ports/` | Host port availability and the ports assigned to instances. |
| `internal/instances/` | Per-instance settings and the operation history. |
| `internal/git/`, `internal/compose/` | Read-only adapters for the `git` and `docker compose` tools. |
| `internal/execx/` | Runs external tools directly, without a shell. |
| `internal/testutil/` | Test helpers for tests that use real `git` and `docker compose`. |

## License

Theo is open source under the [MIT License](LICENSE).
