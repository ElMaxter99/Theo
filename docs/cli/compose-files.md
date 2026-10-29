# Compose files and profiles

Theo does not read Compose files itself: it asks `docker compose config` what the
files mean. This page says which files it hands to Compose, and how to change
that (FR-011), how `COMPOSE_FILE` is handled, and how per-instance profiles work
(FR-013).

## Which Compose files an instance uses

For every worktree, in this order, the first that applies wins:

1. `--compose-file <path>` (repeatable), on the command you run. One call only.
2. The project setting `compose_files` (`theo project set <p> --compose-file ...`).
3. `COMPOSE_FILE`, from the process environment, or else from the env files Theo
   passes to Compose (see below).
4. Nothing chosen: Compose finds `compose.yaml`, `compose.yml`,
   `docker-compose.yaml` or `docker-compose.yml` and its `*.override.*` file by its
   own rules, as before.

When files are chosen, Theo passes them to every Compose call as repeated
`-f`, in the order given, and **Compose's own discovery is off**: the
`compose.override.yaml` is not added for you (the same as `docker compose -f A`).
So `theo instance config` shows exactly what `docker compose -f B -f A config`
shows. A later file overrides the earlier ones.

The selection is honored everywhere Compose files are used: `services`, `ports`,
`instance config`, `up`, `down`, `restart`, `logs`, `ps -w`, `env`, `volumes`, and
the base of the file Theo generates for `--auto-ports`, `theo env` and
`instance set --neutralize-container-names` (the generated file is always added
after the chosen ones).

```
$ theo project set shop --compose-file compose.yaml --compose-file deploy/dev.yaml
Project "shop": Compose files are compose.yaml, deploy/dev.yaml (in this order, relative to each worktree)

$ theo project set shop --default-compose-files      # back to Compose's own choice

$ theo instance config shop --compose-file deploy/dev.yaml --compose-file compose.yaml
$ theo up shop -w feature/x --compose-file compose.yaml --compose-file deploy/ci.yaml
```

### Rules for the paths

* Relative to the root of each worktree, so the one setting works in the main
  worktree and in every linked one. Absolute paths and paths that leave the
  worktree (`../x.yaml`) are refused: a file outside the worktree would be shared
  by all of them.
* No duplicates; the order is kept exactly as given, and stored with `/`
  separators so it means the same on every OS.
* `project set` checks that every file exists in the project's **main** worktree.
  Every command checks it again in the worktree it acts on, and fails naming the
  file, where the choice came from and the worktree:

  ```
  theo: Compose file deploy/dev.yaml (from project setting) does not exist in worktree /work/shop-fix1; change the setting with `theo project set shop --compose-file <file>` or go back to Compose's own choice with --default-compose-files
  ```
  A file that only exists on some branches therefore makes the other worktrees fail
  clearly instead of silently running a different configuration.

### The `.env` file

Compose reads the default `.env` from the directory of the **first** Compose file.
If your first file is in a subdirectory, Theo names the worktree's own `.env`
explicitly (`--env-file`) so interpolation keeps working as if you ran Compose at
the worktree root. An env file you choose (`--env-file`, `theo project set
--env-file`) is passed as it always was.

## COMPOSE_FILE

Projects that set `COMPOSE_FILE` used to fail with "does not support COMPOSE_FILE"
when you used `--auto-ports`, instance variables or container-name settings. Now
`COMPOSE_FILE` is understood:

* Looked up in the process environment first, and otherwise in the env files in
  the order Theo passes them to Compose (the last one that sets it wins), including
  the worktree's `.env`.
* Split on `:` on Linux and macOS and on `;` on Windows, or on the value of
  `COMPOSE_PATH_SEPARATOR` (environment or env file).
* Relative paths are resolved against the worktree root; absolute paths are kept.
* An explicit `compose_files` setting or `--compose-file` flag wins over it.
* An empty `COMPOSE_FILE` is treated as not set by Theo. (Compose itself treats an
  empty value as an error; unset it in your shell if you see that.)
* Variables inside the `COMPOSE_FILE` value (`${X}`) are not expanded by Theo.

`theo services --json` and `theo instance show` report the files in use and where
they came from (`compose_files`, `compose_files_source`).

## Profiles per instance

`docker compose` starts only the services without `profiles`, plus those whose
profile you enable. Theo can remember the profiles of one instance:

```
theo instance set shop -w feature/x --profile debug --profile metrics
theo instance set shop -w feature/x --no-profiles
theo instance show shop -w feature/x          # "profiles (theo): debug, metrics"
```

* Each name must be declared by some service; otherwise the command fails and lists
  the profiles that exist.
* Saved per instance (per worktree), outside the project's files.
* `theo up` starts the instance with the saved profiles **plus** the ones given with
  `--profile` (merged, no duplicates). `ports` and `instance config` use the same
  merged list, so what they show is what `up` will start.
* `down`, `restart`, `logs` and `ps` always act on every profile of the Compose
  project (they pass `--profile '*'`), so a service started under a profile is
  stopped, restarted, shown and logged consistently, whether the profile was saved
  or given once with `up --profile`.

## Decisions and limits

* Selecting files never edits them. It is a setting kept in Theo's registry.
* Several Compose files are merged by Compose, not by Theo; Theo shows Compose's
  own error message (plus the list of files used) when the combination is invalid.
* `--compose-file` is a per-call override; it is not remembered. Files in the
  generated overlay of an instance started with a one-off override stay as that run
  built them; the next `up` regenerates it for the files of that run.
* Only Linux was exercised against a real Docker daemon here. The Windows `;`
  separator and path handling are covered by unit tests and `go vet` for
  `GOOS=windows`, and are otherwise verified only by CI.
