# Theo

> **Status:** early development. Nothing usable yet: this repository only contains the project skeleton.

Theo is a cross-platform tool (macOS, Linux and Windows) for managing development projects and **multiple isolated environments based on Git and Docker Compose**. The goal is to let a developer, or several AI agents, work at the same time on different branches, features and fixes of the same project, each one with its own running environment, while choosing which services are isolated and which ones are deliberately shared.

## Design principles

- **Orchestrate, don't reimplement.** Theo drives the `git` and `docker compose` tools you already use and relies on their own behaviour and validation.
- **Never touch your files.** Your project's Compose files stay as they are; Theo adds its own configuration per instance.
- **Safe by default.** No destructive operation (deleting volumes, data or worktrees) happens implicitly.
- **Local engine first.** All logic lives in a local engine. The first interface is a minimal command-line tool; a graphical interface will be built later on top of the same engine.
- **Cross-platform from day one.** macOS, Linux and Windows are supported by design, not as an afterthought.

## Requirements

- [Go](https://go.dev/dl/) 1.24 or later, to build from source.
- Git and Docker with the Compose plugin will be required once the first features land.

## Build and run

```sh
go build ./cmd/theo
./theo version
```

Or run it directly:

```sh
go run ./cmd/theo help
```

## Development

```sh
go vet ./...
go test ./...
```

## Project layout

| Path | Contents |
|---|---|
| `cmd/theo/` | Entry point of the `theo` command-line tool. |
| `internal/cli/` | Command-line interface: argument parsing and output. No domain logic. |
| `internal/engine/` | Local engine: domain logic and operations shared by every interface. |

## License

Theo is open source under the [MIT License](LICENSE).
