# Theo

> **Status:** early prototype. Theo can register a project and show its worktrees and the services Docker Compose resolves for each of them. It cannot start or create environments yet.

Theo is a cross-platform tool (macOS, Linux and Windows) for managing development projects and **multiple isolated environments based on Git and Docker Compose**. The goal is to let a developer, or several AI agents, work at the same time on different branches, features and fixes of the same project, each one with its own running environment, while choosing which services are isolated and which ones are deliberately shared.

## Design principles

- **Orchestrate, don't reimplement.** Theo drives the `git` and `docker compose` tools you already use and relies on their own behaviour and validation.
- **Never touch your files.** Your project's Compose files stay as they are; Theo adds its own configuration per instance.
- **Safe by default.** No destructive operation (deleting volumes, data or worktrees) happens implicitly.
- **Local engine first.** All logic lives in a local engine. The first interface is a minimal command-line tool; a graphical interface will be built later on top of the same engine.
- **Cross-platform from day one.** macOS, Linux and Windows are supported by design, not as an afterthought.

## Requirements

- [Go](https://go.dev/dl/) 1.24 or later, to build from source.
- Git and Docker with the Compose plugin, in your `PATH`. Reading a project's services runs `docker compose config`, which does not need the Docker daemon to be running.

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

# Linked worktrees don't get untracked files such as .env. Use another env file:
theo services my-api -w feature/x --env-file ~/code/my-api/.env

# Unregister the project. Files, containers and volumes are not touched.
theo project remove my-api
```

`project list`, `worktrees` and `services` accept `--json` for scripts and agents.

These commands only read your repositories and Compose files: they never change files, branches, containers or volumes.

Theo keeps its registry in `theo/registry.json` inside your user configuration directory (for example `~/.config` on Linux, `~/Library/Application Support` on macOS, `%AppData%` on Windows). Set `THEO_HOME` to use another directory.

## Development

```sh
go vet ./...
go test ./...
```

Some tests use the real `git` and `docker compose` tools and are skipped when they are not installed.

## Project layout

| Path | Contents |
|---|---|
| `cmd/theo/` | Entry point of the `theo` command-line tool. |
| `internal/cli/` | Command-line interface: argument parsing and output. No domain logic. |
| `internal/engine/` | Local engine: domain logic and operations shared by every interface. |
| `internal/registry/` | Persistent registry of projects. |
| `internal/git/`, `internal/compose/` | Read-only adapters for the `git` and `docker compose` tools. |
| `internal/execx/` | Runs external tools directly, without a shell. |
| `internal/testutil/` | Test helpers for tests that use real `git` and `docker compose`. |

## License

Theo is open source under the [MIT License](LICENSE).
