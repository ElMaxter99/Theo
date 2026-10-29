# Contributing to Theo

Thanks for helping. Theo is small and its rules are few; this page is the whole of them.

## Language

The public repository is in English: code, comments, commit messages, issues, pull requests and documentation. Please write in English here, even if you discuss the project elsewhere in another language.

## Sign your work (DCO, no CLA)

Theo is MIT licensed and has no contributor license agreement. Instead, every commit must carry a `Signed-off-by` line, which means you certify the [Developer Certificate of Origin](https://developercertificate.org/): that you wrote the change or have the right to submit it under the project's license.

```sh
git commit -s -m "Explain why the change is needed"
```

`-s` adds `Signed-off-by: Your Name <you@example.com>` using your Git identity. Forgot it? `git commit --amend -s` (last commit) or `git rebase --signoff main` (a branch).

## Build and test

You need Go 1.24 or later. Git and Docker (with the Compose plugin) are needed for the tests that use them; those tests skip themselves when a tool or a Docker daemon is missing.

```sh
go build ./cmd/theo
gofmt -l .            # must print nothing
go vet ./...
go test ./...
```

Theo also runs on Windows and macOS, so check `GOOS=windows go vet ./...` and `GOOS=darwin go vet ./...` too, use `filepath` for paths, and never assume a shell: call `git` and `docker` directly.

Tests that start containers share one Docker daemon. Give your Theo project, Compose project and volume names a unique prefix, take host ports from `testutil.FreePort`, and always clean up in `t.Cleanup`.

## Layout

| Path | Contents |
|---|---|
| `cmd/theo/` | Entry point. |
| `internal/cli/` | Argument parsing and output only. New commands go in their own file and register themselves. |
| `internal/engine/` | Domain logic shared by every interface. |
| `internal/git/`, `internal/compose/`, `internal/execx/` | Adapters for `git` and `docker compose`; external tools run directly, without a shell. |
| `internal/registry/`, `internal/ports/`, `internal/instances/` | Persistent state. |
| `internal/acceptance/` | End-to-end tests through the command line, including the `examples/demo` guide. |
| `internal/testutil/` | Helpers for tests that use real tools. |
| `docs/cli/` | One page per feature. |
| `examples/demo/` | A small app to try Theo on. |

## Rules that are not negotiable

- **Theo never changes the user's files without confirmation.** Project Compose files and `.env` stay as they are; Theo writes its own generated files elsewhere. Ignored or untracked files are never overwritten.
- **Theo never deletes data implicitly.** Volumes, worktrees and branches are removed only by a separate, explicit, confirmed command that says what it will delete. `down` keeps volumes.
- **Orchestrate, do not reimplement.** Drive `git` and `docker compose`; do not parse what they can validate for you.
- **No secrets** in code, tests, logs or docs. Output that can contain a secret masks it.
- Every command that prints data supports `--json`.

## Pull requests

Keep a change focused, explain why in the commit message and the description, and add or update tests and the matching page in `docs/cli/`. Say what you ran and on which system; if something is untested (for example Windows), say so. CI must pass.

Report bugs and ideas with the issue templates. For security problems, see [SECURITY.md](SECURITY.md).
