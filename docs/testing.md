# Testing

Theo has four kinds of tests. The first three run with `go test` or `npm`; the
smoke battery is a script that walks the demo app end to end, the way a user
would. Tests that need Git, Docker or a browser skip themselves when the tool
is missing, so a green run on a machine without Docker proves less: read the
skips (`go test -v` prints them).

| Kind | Where | Needs | Run |
|---|---|---|---|
| Unit | `internal/**/*_test.go` | Go; some use Git | `go test ./...` |
| Acceptance (UC-01 to UC-08, the demo guide) | `internal/acceptance/`, plus the CLI tests that call `testutil.RequireDaemon` | Git, Docker with Compose, a running daemon (Linux containers) | `go test -count=1 -v ./internal/acceptance` |
| Web interface | `web/test/` (unit), `web/e2e/` (Playwright against the mock), `web/e2e/real/` (against the real `theo serve`) | Node 22; Playwright with Chromium for e2e; Docker for `real` | `cd web && npm test`, `npm run test:e2e`, `npm run test:e2e:real` |
| Smoke battery | `scripts/smoke.sh`, `scripts/smoke.ps1` | Git, Docker with Compose and a running daemon, curl, free host ports 8080 and 8081 | `scripts/smoke.sh ./theo` or `.\scripts\smoke.ps1 -Theo .\theo.exe` |

## Unit tests

```sh
gofmt -l .        # must print nothing
go vet ./...
go test ./...
```

Engine tests use fakes for Git and Compose (`internal/engine/engine_test.go`);
CLI tests run `cli.Run` in process with a temporary `THEO_HOME`
(`registry.EnvHome`) and real temporary Git repositories from `internal/testutil`.

## Acceptance tests

`internal/acceptance` runs the built command line against a real Docker daemon:
the use cases UC-01 to UC-08 and steps 0 to 12 of the
[demo guide](../examples/demo/README.md). CI prints which ran and which were
skipped on each system:

```sh
go test -count=1 -v ./internal/acceptance 2>&1 | grep -E '^\s*(---|[a-z_]+_test.go:[0-9]+:)'
```

Tests that start containers share the daemon: give projects, Compose projects
and volumes a unique prefix, take host ports from `testutil.FreePort`, and clean
up in `t.Cleanup`.

## Web interface

See [ui-web.md](cli/ui-web.md#tests) for what each suite covers. In short:

```sh
cd web
npm ci
npm run check && npm test          # typecheck and unit tests
npm run test:e2e                   # Playwright against the mock API
THEO_BIN=/path/to/theo npm run test:e2e:real   # against the real theo serve (Docker)
```

After changing `web/`, run `npm run build` and commit `internal/ui/dist`; CI
fails when it is out of date.

## Evidence for a pull request

`scripts/evidence.sh` runs every check above (including the real e2e) and writes `evidence/report.md`
and the screenshots; CI runs it too and uploads the `e2e-evidence` artifact. See CLAUDE.md for the rule
that a change is not handed over until it exits 0.

## Smoke battery

`scripts/smoke.sh` (bash: Linux, macOS, Git Bash) and `scripts/smoke.ps1`
(Windows PowerShell 5.1 and PowerShell 7) run the same sequence of about 100
checks, with the same names, against a real Docker daemon: project add/show,
services, ports, up/stop/start/restart/recreate, profiles, exec and logs,
worktrees and clones with owners and copied files, a second instance with
`--auto-ports` and neutralized container names, per-instance variables and
secrets, shared services, volume purge, backup, restore and clone, worktree
removal, reconcile, `theo serve`, completion, doctor, project rename, and finally
`project remove --delete-worktrees`. Each check prints `OK` or `FAIL` (with the
end of its output), and a summary closes the run.

```sh
go build -o theo ./cmd/theo
scripts/smoke.sh ./theo                      # or: scripts/smoke.sh  (theo from PATH)
```

```powershell
go build -o theo.exe .\cmd\theo
.\scripts\smoke.ps1 -Theo .\theo.exe
```

What it touches, and what it does not:

* Theo's data goes to a temporary `THEO_HOME`; your own projects are not seen.
* The demo is copied into a temporary Git repository and its Compose `name:` is
  set to `theo-smoke` (`THEO_SMOKE_PROJECT` changes it), so a `theo-demo` of
  your own is never touched.
* On exit, even after a failure or Ctrl+C, it deletes the containers, networks
  and volumes of the Compose projects `theo-smoke`, `theo-smoke-feature-x`,
  `theo-smoke-feature-c` and `theo-smoke-shared`, the images built for them
  (`THEO_SMOKE_KEEP_IMAGES=1` keeps them) and the temporary directories. Its
  last check asserts that no container or volume of those projects remains.
* The demo publishes the fixed host port 8080 and its second instance gets 8081,
  and its `cache` service has the fixed `container_name: cache`. The script
  checks up front and stops with a clear message (exit code 2) when a port is in
  use or a container called `cache` exists.

`THEO_SMOKE_VERBOSE=1` prints the output of every check. The exit code is 0 when
every check passed and 1 otherwise.

CI runs both scripts on `ubuntu-latest` in the job "Smoke (demo, real Docker)"
(`smoke.ps1` under PowerShell 7). Windows PowerShell 5.1 is not run in CI: the
GitHub Windows runners have no Linux containers.
