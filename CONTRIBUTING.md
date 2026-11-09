# Contributing to Theo

Thanks for helping. Theo is small and its rules are few; this page is the whole of them.

## Language

The public repository is in English: code, comments, commit messages, issues, pull requests and documentation. Please write in English here, even if you discuss the project elsewhere in another language. The one exception is `README.es.md`, the Spanish version of the README, kept in step with `README.md`.

## Sign your work (DCO, no CLA)

Theo is MIT licensed and has no contributor license agreement. Instead, every commit must carry a `Signed-off-by` line, which means you certify the [Developer Certificate of Origin](https://developercertificate.org/): that you wrote the change or have the right to submit it under the project's license.

```sh
git commit -s -m "Explain why the change is needed"
```

`-s` adds `Signed-off-by: Your Name <you@example.com>` using your Git identity. Forgot it? `git commit --amend -s` (last commit) or `git rebase --signoff main` (a branch).

## Build and test

You need Go 1.26 or later. Git and Docker (with the Compose plugin) are needed for the tests that use them; those tests skip themselves when a tool or a Docker daemon is missing.

```sh
go build ./cmd/theo
gofmt -l .            # must print nothing
go vet ./...
go test ./...
```

Theo also runs on Windows and macOS, so check `GOOS=windows go vet ./...` and `GOOS=darwin go vet ./...` too, use `filepath` for paths, and never assume a shell: call `git` and `docker` directly.

### Testing

There are four kinds of tests; [docs/testing.md](docs/testing.md) explains each and what it needs:

- **Unit**: `go test ./...` (fakes for Git and Compose, real temporary Git repositories in the CLI tests).
- **Acceptance**: `go test -count=1 -v ./internal/acceptance`, the use cases and the demo guide against a real Docker daemon.
- **Web interface**: `cd web && npm test`, `npm run test:e2e` (Playwright against the mock) and `npm run test:e2e:real` (against the real `theo serve`).
- **Smoke battery**: `scripts/smoke.sh ./theo` (or `.\scripts\smoke.ps1 -Theo .\theo.exe` on Windows) walks `examples/demo` through every feature, about 100 checks, with an isolated `THEO_HOME` and full cleanup. It needs a Docker daemon and free host ports 8080 and 8081. CI runs it on Linux.

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

## Releasing

Versions are `vX.Y.Z` (digits only); release candidates are `vX.Y.Z-rc.N`. Nobody creates tags by hand: `.github/workflows/release.yml` and `release-on-merge.yml` do it.

1. **Open the release.** Create the branch `release/X.Y.Z` (for example `release/0.6.0`) and open a pull request to `main`, with the changes listed below.
2. **Every push to `release/X.Y.Z` is a release candidate.** It tests Theo, builds every system and publishes the pre-release `vX.Y.Z-rc.1`, then `-rc.2` for the next push. A pre-release has every archive and `checksums.txt`, no Scoop or Homebrew files, and is never "latest". Try its binaries.
3. **Merge the pull request.** The merge commit is tagged `vX.Y.Z` and the same workflow publishes the release: the archives plus the Scoop and Homebrew files, as a draft that is published once every file is attached. `CHANGELOG.md` must have a `## vX.Y.Z` section (the run fails otherwise); it is the release's notes.

Before anything is published, the Release workflow unpacks the CLI archive on Linux, macOS and Windows and checks that `theo --version` prints the tag, and it signs a build provenance attestation for every archive (`gh attestation verify <archive> --repo ElMaxter99/Theo` checks one).

A branch `release/` with anything but `X.Y.Z`, or a version that already has a tag, fails before building. Pushing a `vX.Y.Z` or `vX.Y.Z-rc.N` tag by hand, or running the workflow by hand with a tag, still works (to redo a release whose files are missing). A pull request that touches the workflow or the packaging scripts, or a manual run with no tag, is a dry run that builds as `v0.0.0-dryrun` and publishes nothing.

The `Release rules` check on the release pull request runs `scripts/check-release-pr.sh` (you can run it yourself): the version is new, `CHANGELOG.md` has the `## vX.Y.Z` section and nothing is left under `## Unreleased`, the same for `CHANGELOG.es.md` once it exists, and `README.md` and `README.es.md` change together. It fails the pull request on a mistake that would otherwise show only after the merge has tagged the release.

Before merging, on the release pull request:

1. **CHANGELOG.md** has a section for the new version.
2. **The README is reviewed and improved, in English and in Spanish. Every release, no exceptions.** It is the first page people see and it ships inside every archive, so it must describe the release being tagged, not the previous one. `README.md` and `README.es.md` say the same thing: change one, change the other in the same pull request.
   - **What's new** describes this version; the previous one moves to a single line under *Earlier releases*.
   - New commands and flags appear in **Quickstart** or **Commands**, with a link to their page in `docs/cli/`; removed or renamed ones are gone.
   - **Install**, **Platform notes** and the badges match what the release really ships and what was really tested.
   - If the interface changed, refresh the screenshots: `cd web && npm ci && npm run screenshots:readme` writes `docs/images/*.png` from the mock, with the English interface for `README.md` and the Spanish one (`*-es.png`) for `README.es.md`.
   - Improve at least one thing beyond keeping it current: a clearer example, a better explanation, a missing link. Say what in the pull request.
3. Every page in `docs/cli/` touched by the release is up to date.

Report bugs and ideas with the issue templates. For security problems, see [SECURITY.md](SECURITY.md).
