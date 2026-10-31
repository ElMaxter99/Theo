# Theo

Theo is a cross-platform command-line tool (macOS, Linux, Windows) that manages **several isolated environments of the same project, based on Git worktrees and Docker Compose**. Each branch gets its own running copy, so a developer, or several AI agents, can work on different features and fixes at the same time, choosing which services are isolated and which are deliberately shared.

> **Status:** early. The features below work and are tested on Linux against a real Docker daemon; macOS and Windows are covered by CI only, and Docker-based tests are skipped there.

## Install

**Release binaries.** Download the archive for your system from the [releases page](https://github.com/ElMaxter99/Theo/releases) (`tar.gz` for Linux and macOS, `zip` for Windows, amd64 and arm64), check it against `checksums.txt`, and put `theo` in your `PATH`.

**Scoop (Windows).** Each release attaches a manifest, so no bucket is needed:

```powershell
scoop install https://github.com/ElMaxter99/Theo/releases/latest/download/theo.json
```

**Homebrew (macOS, Linux).** Each release attaches `theo.rb`, a formula for a tap of your own; no public tap is published yet.

**With Go** (1.24 or later):

```sh
go install github.com/ElMaxter99/Theo/cmd/theo@latest
```

**From source:**

```sh
git clone https://github.com/ElMaxter99/Theo
cd Theo
go build ./cmd/theo      # or scripts/build.sh, scripts/build.ps1
./theo help
```

You also need Git and Docker with the Compose plugin in your `PATH`. Reading a project's services runs `docker compose config`, which does not need the daemon; starting and inspecting containers does.

## Quickstart

```sh
theo project add ~/code/my-api --name my-api    # register a Git repository
theo services my-api                            # what Compose will run
theo up my-api                                  # start the main worktree
theo worktree add my-api feature/x --new       # a second worktree
theo up my-api -w feature/x --auto-ports        # run it next to the first
theo ps my-api
theo down my-api -w feature/x                   # volumes are kept
```

More in the [quickstart](docs/cli/quickstart.md). To try everything on a small app made for it, follow [`examples/demo`](examples/demo/README.md).

## Commands

Run `theo help` for the full list. Every command that prints data supports `--json`.

| Topic | Commands | Page |
|---|---|---|
| Quickstart | the basics, step by step | [quickstart](docs/cli/quickstart.md) |
| Projects | `project add\|set\|list\|remove` | [project](docs/cli/project.md) |
| Compose files | which files and env files are used | [compose-files](docs/cli/compose-files.md) |
| Worktrees | `worktrees`, `branches`, `worktree add\|remove`, `up`, `down`, `restart`, `ps`, `logs` | [worktrees](docs/cli/worktrees.md) |
| Host ports | `ports`, `up --auto-ports` | [ports](docs/cli/ports.md) |
| Instance settings | `env set\|unset\|list`, `instance set\|show\|config`, `history` | [instance-settings](docs/cli/instance-settings.md) |
| Volumes | `volumes`, `volumes purge\|backup\|restore\|clone` | [volumes](docs/cli/volumes.md) |
| Shared services | `shared add\|use\|up\|status\|down` | [shared](docs/cli/shared.md) |
| Status | `status` | [status](docs/cli/status.md) |
| Explain | `explain` | [explain](docs/cli/explain.md) |
| Reconcile | `reconcile` | [reconcile](docs/cli/reconcile.md) |
| Shell completion | `completion` | [completion](docs/cli/completion.md) |
| Output formats | `--json` and friends | [output](docs/cli/output.md) |
| Compose compatibility | supported Compose features and limits | [compose-compat](docs/cli/compose-compat.md) |
| Env precedence | which value wins | [env-precedence](docs/cli/env-precedence.md) |

## Safety principles

- **Orchestrate, don't reimplement.** Theo drives the `git` and `docker compose` you already use and relies on their validation.
- **Never touch your files.** Your Compose files and `.env` stay as they are; Theo writes its own generated files per instance, outside the project. Ignored files are never copied or overwritten.
- **No implicit deletion.** `down` never removes volumes. Deleting volumes, worktrees or data is a separate command that lists what it will delete and needs an explicit confirmation. Volumes declared `external` and other instances' volumes are never touched.
- **Reports before repairs.** `reconcile` only reports; `--apply` forgets Theo's own stale records, never containers, volumes or your files.
- **Secrets are masked, not encrypted.** Values set with `theo env --secret` are masked in Theo's output but stored in plain text in Theo's data files, readable only by your user. Passing a secret on the command line leaves it in your shell history.
- **Local engine first.** All logic is in a local engine; the command line is the first interface and a graphical one can be built on the same engine.

Theo keeps its registry and data in `theo/` inside your user configuration directory (`~/.config` on Linux, `~/Library/Application Support` on macOS, `%AppData%` on Windows). Set `THEO_HOME` to use another directory.

## Platform notes

- Linux: the reference platform for the Docker-based tests.
- macOS and Windows: CI builds, vets and runs the tests that need no Docker daemon. Anything that starts containers is unverified there, so please report problems.
- On Windows, use a Docker daemon running Linux containers (Docker Desktop with WSL 2). Paths with spaces and accents are supported.
- `--auto-ports` and per-instance settings need Docker Compose 2.24.4 or later.

## Development

```sh
gofmt -l .          # must print nothing
go vet ./...
go test ./...
```

Tests that use Git and Docker skip themselves when those are missing; they build tiny images, and the `examples/demo` guide test pulls `golang`, `alpine` and `redis`. See [CONTRIBUTING.md](CONTRIBUTING.md) for the layout and the rules, [SECURITY.md](SECURITY.md) to report a vulnerability and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). `scripts/build.sh` and `scripts/build.ps1` build for the current system or every release target; pushing a `v*` tag builds and publishes a release.

The repository is in English.

## License

Theo is open source under the [MIT License](LICENSE). The brand artwork (mascot, logo, icons) is separate and is licensed under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/); see [the notice](web/public/brand/NOTICE.md).
