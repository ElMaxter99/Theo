# Host ports

Two worktrees that publish the same fixed host port cannot run at the same time. Theo shows the problem before it happens and can resolve it without touching your Compose files.

```sh
theo ports my-api -w feature/x [--auto-ports] [--profile worker] [--json]
theo up my-api -w feature/x --auto-ports
```

## What you see

`ports` lists, for each published port, the service, the container port, the configured host port, the host port the instance will use, whether it is free and the URL:

```
SERVICE  CONTAINER  CONFIGURED  HOST PORT  STATUS  URL
web      8080/tcp   8080        8080       in use  http://localhost:8080
```

`up` prints a warning when a port it needs is taken ("host port 8080 ... is already in use. Free it, or start with --auto-ports"). Without `--auto-ports` it still tries to start, and Docker will refuse if the port is really taken.

## --auto-ports

With `--auto-ports`, `up` publishes the services whose port is taken on another free one (the next free port after the configured one) and remembers it, so the address stays the same when you start the instance again. Ports of other instances that Theo has assigned count as taken, even while those instances are stopped.

Your Compose files are not modified. Theo adds a generated Compose file for that instance (this needs Docker Compose 2.24.4 or later). The assigned ports are in `ports.json`, next to the registry, and the generated files in `overlays/`.

The first `up --auto-ports` turns the behaviour on **for that instance**, and it stays on: a later plain `theo up` keeps the remembered port (FR-063). Turn it off with `theo instance set <project> -w <worktree> --no-auto-ports` (and on without starting with `--auto-ports`); the instance then goes back to the configured port. `theo ports` previews with the current setting.

## Limits

- Projects that set `COMPOSE_FILE` are not supported by `--auto-ports` yet.
- Random ports (`"3000"`) and ranges (`"8000-8010"`) are left as they are.
- A fixed `container_name` is a different conflict that `--auto-ports` does not solve; see `instance set --neutralize-container-names` in [instance settings](instance-settings.md).
