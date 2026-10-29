# Single services, recreate, exec and shell

Everything here acts on the instance of one worktree (`-w <worktree>`, the main
worktree when omitted), like `up` and `down`. Service names are checked against
the project's Compose configuration first; an unknown one is refused and the
error lists the valid names.

## Start only some services (FR-046)

```
theo up shop -w feature/login --service api --service db
```

Starts only those services **and the services they depend on** (Compose
`depends_on`). The instance's settings (environment variables, neutralized
container names, ports from `--auto-ports`) still apply: they go through the
same generated files as a plain `up`. `--build` and `--profile` work as usual.

## Stop, start and restart some services

```
theo stop shop api            # api stops; db, cache... keep running
theo start shop api           # starts the stopped container again
theo restart shop api db      # restarts those two
theo stop shop                # no service named: all of them
```

* `stop` keeps the containers (and volumes); it does not remove anything.
  `down` is what removes containers and networks.
* `start` only starts containers that exist. For a service that was never
  created, use `theo up shop --service <name>`.
* `restart` without services is the whole-instance restart it always was.
* `stop`, `start` and `restart` do not regenerate the instance's files; they
  act on the containers as they are.

## Recreate containers (FR-044)

```
theo recreate shop                  # every container, from current image and config
theo recreate shop api              # only api; its dependencies are left alone
theo up shop --force-recreate       # same as recreate, with up's semantics
```

Containers are removed and created again; **named volumes are never touched**,
so their data survives (the integration test writes a file into a named
volume, recreates, and reads it back, both through Theo and with a separate
`docker run -v`). Anonymous volumes (a `volumes: [/path]` entry with no name)
are not kept by Compose, as usual. `recreate` accepts `--build`, `--profile`
and `--auto-ports`, and applies the instance's settings like `up`.

`recreate <service>` passes `--no-deps`, so only the named services are
recreated. `up --force-recreate` has no `--no-deps`: with `--service` it
recreates the services and their dependencies.

## Run a command in a container (FR-072)

```
theo exec shop -w feature/login api -- ls -la /app
theo exec shop -T db -- psql -U app -c 'select 1' < query.sql
theo shell shop api
```

* Everything after `--` is the command, with its own flags.
* Standard input, output and error pass through unchanged.
* A terminal (TTY) is requested **only when standard input is a terminal**.
  `-T` turns it off even then. With a pipe or a file as input, no TTY is
  requested, so binary data and scripts are not mangled.
* **The exit code of `theo exec` is the command's exit code.** Theo prints
  nothing of its own about it. If Docker itself fails (the service is not
  running), `docker compose` exits with 1 and its message is on standard
  error, so 1 cannot be told from a command that exits with 1.
* `theo shell <service>` opens a shell: one `exec` of
  `sh -c 'command -v bash >/dev/null 2>&1 && exec bash || exec sh'`, that is
  `bash` when the image has it and `sh` otherwise. An image with neither (a
  `FROM scratch` image) cannot open a shell.
* `exec` and `shell` need the container to be running (`theo status`).
  They are not recorded in the history.
