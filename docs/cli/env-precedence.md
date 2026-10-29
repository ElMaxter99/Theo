# Environment variables: who wins

Compose reads variables in two different places that are easy to confuse:

- **Interpolation**: `${VAR}` and `${VAR:-default}` inside your Compose files
  (and inside `env_file:` files). These come from the *project environment*.
- **Container environment**: what the process in the container sees. These come
  from `env_file:`, `environment:` and, for pass-through entries, the project
  environment.

Everything below was checked with `docker compose config` (Compose v5.6.1,
Linux) by `TestEnvPrecedence` in `internal/cli/envprecedence_test.go`, and by hand
with the plain `docker compose` commands in the "Plain Compose" table.

## Plain Compose

### Interpolation (project environment), strongest first

| # | Source | Notes |
|---|---|---|
| 1 | Variables of the process that runs `docker compose` | Also an *empty* variable: `V=` shadows every file |
| 2 | `--env-file`, last one given | Several allowed; later files win key by key |
| 3 | `--env-file`, earlier ones | |
| 4 | `.env` of the project directory | **Only when no `--env-file` is given**: any `--env-file` replaces it. Pass `--env-file .env` first to keep both |
| 5 | `${VAR:-default}` in the file | |

Example (`.env` has `W=dotenv X=dotenv`, `e1.env` has `W=e1 X=e1`, `e2.env` has `X=e2`):

| Command | `${W}` | `${X}` |
|---|---|---|
| `docker compose config` | dotenv | dotenv |
| `--env-file e1.env` | e1 | e1 (`.env` ignored) |
| `--env-file e1.env --env-file e2.env` | e1 | e2 |
| `--env-file e2.env --env-file e1.env` | e1 | e1 |
| `W=process docker compose --env-file e1.env config` | process | e1 |

### Container environment, strongest first

| # | Source |
|---|---|
| 1 | `environment:` of the service (a `KEY: value` or `KEY=value` entry) |
| 2 | `env_file:` of the service, later files win over earlier ones |
| 3 | A pass-through entry (`environment: [KEY]` or `KEY:` with no value) takes the value from the project environment (process, then env files, then `.env`); if none has it, the variable is not set |

- `environment:` beats `env_file:` for the same key (`T=svcenvironment` over `T=svcfile`).
- The process environment does **not** override a value in a service's `env_file:`
  (`V=process docker compose config` still gives the file's `V=svcfile`); it only
  feeds interpolation and pass-through entries.
- `env_file:` files are interpolated with the project environment
  (`FROMENVFILE=${INTERP:-none}` becomes the value of `INTERP`).
- Variables of `--env-file` / `.env` are **not** passed to containers by
  themselves; they must be referenced (`${VAR}`) or listed as pass-through.

## With Theo

Theo runs `docker compose -p <instance> [-f ...] [--env-file ...]` and adds two
things for `theo env set` (all under `$THEO_HOME/overlays/`, outside your project):

1. an overlay file with `environment:` for the target services (default: every
   service, or the ones given with `--service`), and
2. an env file passed as the **last** `--env-file`.

So the statement in `theo env`'s help, "applied at up through a generated file",
means both: the first changes the container, the second changes `${VAR}`.

| Where | Value set with `theo env set X=theo` |
|---|---|
| Container environment | `theo` — it wins over the service's `environment:`, its `env_file:` and a pass-through entry, because the overlay's `environment:` is merged last |
| Interpolation `${X}` | `theo` — the generated env file is the last `--env-file`, so it wins over `.env`, your `--env-file`s... |
| ...**except** a variable exported in the shell that runs `theo up` | The process environment is stronger than any env file: `${X}` is the shell's value, while the container's `X` is still `theo`. This is the only place they differ |

Details Theo guarantees (and the test covers):

- The worktree's own `.env` keeps working. Naming an env file makes Compose skip
  `.env`; Theo names the worktree's `.env` first and its generated file last. (An
  earlier version lost the `.env` as soon as one variable was set.)
- Your `--env-file` options (one or several, any order) are kept, in the order given,
  and the generated file comes after them.
- Values are written exactly as given: a `$` in the value reaches the container as
  typed, not interpolated (overlay: `$$`; env file: single quotes).
- `theo env list` masks secrets; so does `theo instance config`. The generated files
  contain secrets in clear text with mode 0600 (`$THEO_HOME/overlays/`).
- `theo env unset` plus the next `up` removes the settings: the files are deleted
  and the instance runs with your own files again.

## Which env file does a worktree use?

For Theo's own reading (service list, ports) and for the run, in order: `--env-file`
given to the command (repeatable); the worktree's own `.env`; the project's env
file (`theo project set --env-file`); the main worktree's `.env`. The last two are
passed as `--env-file`, so the same rule applies: they *replace* the default `.env`.

## COMPOSE_* variables

`COMPOSE_FILE`, `COMPOSE_PATH_SEPARATOR` and `COMPOSE_PROFILES` are read by Compose
from the same places (process first, then `--env-file`s, else `.env`). Theo reads
them the same way to plan ports and to name the files for its generated file. See
[compose-compat.md](compose-compat.md). `COMPOSE_PROJECT_NAME` is irrelevant:
Theo always passes `-p`, which wins.
