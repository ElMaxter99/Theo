# `--explain`: see the commands before they run (FR-122)

`--explain` is a global flag. Put it **before** the command:

```
theo --explain up shop --service api
theo --explain worktree add shop feature/login --new
```

Theo prints, in order, every `git` and `docker` command the operation runs,
with the directory it runs in. Commands that only read still run, because
Theo needs their output to plan the next step (which worktrees exist, what the
Compose file resolves to). Commands that **change** something are listed and
**not executed**.

## Output format

Everything goes to standard output. The listing is meant to be read by people
and is also easy to parse:

```
# theo --explain: the Git and Docker commands this operation runs, in order.
# read   = runs now, because planning needs its output
# change = NOT run; this is what would change Git or Docker
1. read   /work/shop $ git worktree list --porcelain
2. read   /work/shop $ docker compose --profile '*' config --format json
3. read   /work/shop $ docker compose -p shop --profile '*' ps --all --format json
4. change /work/shop $ docker compose -p shop up --detach api
# 4 command(s): 3 read, 1 change. Nothing was changed.
```

* Lines starting with `#` are comments: the legend and the closing summary.
* A command line is `<n>. <read|change> <directory> $ <command>`. `n` counts
  from 1. The directory is absolute: the working directory of the command.
* `read` ran; `change` did not. The command is quoted for a POSIX shell, so
  you can copy it, `cd` to the directory and run it yourself.
* What the operation would have printed (`Started shop.`, a table, JSON) is
  not printed: under `--explain` the listing is the output. Warnings and
  errors still go to standard error, and the exit code is the operation's
  (for example 1 when the project does not exist).

## What counts as a change

| Runs for real (`read`) | Listed, not run (`change`) |
|---|---|
| `git worktree list`, `status`, `rev-parse`, `for-each-ref`, `ls-files` | `git worktree add` / `remove`, `git clone` and any other verb that writes |
| `docker compose config`, `ps`, `ls`, `logs` | `docker compose up`, `down`, `restart`, `stop`, `start`, `exec` |
| `docker volume ls`, `docker ps` | `docker volume rm`, `docker network create` / `rm` |

Every call in Theo says which kind it is (`execx.Run` / `execx.Stream` read;
`execx.RunChange` / `StreamChange` / `StreamInteractive` change). As a safety
net, `Run` and `Stream` also refuse to execute a command whose verb is known
to change things when `--explain` is on, so a call that forgot to say so is
listed as a `change` instead of running.

## Theo's own state

`--explain` also changes nothing of Theo's: projects are not registered or
removed, settings (`env set`, `instance set`) and reserved ports are not
saved, the history gets no entry, `reconcile --apply` deletes nothing, and the
generated Compose files are not written (the listing names the files the
command would use). Directories `theo worktree add` would create are not
created.

Because the generated files do not exist, `--explain` does not validate the
configuration those files produce. Use `theo instance config` for that.

## Limits

* The listing is for one command. A command that decides its next step from
  what a change did (for example `worktree add`, which lists the worktrees
  again afterwards) shows the read, which sees the state without the change.
* `theo exec` and `theo shell` are listed with `-T` if standard input is not a
  terminal, as they would run.
* The commands are the ones Theo runs today. Any `docker` or `git` command
  that a future feature adds must go through `internal/execx`, or it will not
  be listed.
