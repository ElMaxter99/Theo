# Theo demo app

A tiny app you can break freely, to try every Theo feature the way a normal user would. Nothing here needs an account or touches real data.

| Service | What it is |
|---|---|
| `web` | Small Go server (multi-stage build). Published on the **fixed host port 8080**. Needs `GREETING` from a `.env`. Bind mount `./config:/etc/demo:ro`, named volume `data`. Shows its greeting, instance name, branch and a **visit counter stored in redis**. `/healthz` for the healthcheck. |
| `cache` | `redis:7-alpine` with a **fixed `container_name: cache`** and named volume `cachedata`. This is the service you share between instances. |
| `worker` | Same image as `web`, behind the profile `worker`, waits for `web` to be healthy. Counts a tick in redis every 5 seconds. |

`web` waits for `cache` to be healthy (`depends_on` with a healthcheck).

What the page shows (`curl http://localhost:8080/`):

```
greeting: Hello from Theo
instance: main
branch:   feature-x
visits:   3
note:     This note is read from the bind mount ./config.
```

## Before you start

You need Git, Docker with the Compose plugin (a running daemon) and `theo` in your `PATH` (`theo version` prints a version). The first build pulls `golang:1.24-alpine`, `alpine:3.20` and `redis:7-alpine`. Host port 8080 must be free.

Each step says what to run, what you should see, and what it proves. Outputs are abridged; paths and timings differ. Commands are given for **bash** (Linux, macOS, Git Bash) and **PowerShell** (Windows). `up --build` prints a lot of Docker build output; only its last line matters.

Steps 0 to 12 are exercised against a real Docker daemon by `internal/acceptance/demo_test.go` and `demo_shared_test.go` (the commands and flags in this guide are the ones those tests run). The outputs shown are what to expect; if something differs on your machine, `theo doctor` checks your Git and Docker setup first.

A real `.env` is never committed. `.env.example` is the template; the real file lives only in the main worktree and the other worktrees use it by reference.

## 0. Make a repository of your own

Theo works on Git repositories, so copy the example out of the Theo checkout into a new one. Do not register Theo's own repository.

bash:

```sh
cp -r examples/demo ~/theo-demo
```

PowerShell:

```powershell
Copy-Item -Recurse examples\demo $HOME\theo-demo
```

bash:

```sh
cd ~/theo-demo && git init -q -b main && git add -A && git commit -q -m "demo"
```

PowerShell:

```powershell
cd $HOME\theo-demo; git init -q -b main; git add -A; git commit -q -m "demo"
```

Check the commit has no `.env`:

```sh
git ls-files
```

You should see `.env.example` and `.gitignore`, and no `.env`.

## 1. Register the project

bash:

```sh
theo project add . --name theo-demo
```

PowerShell:

```powershell
theo project add . --name theo-demo
```

Expect `Registered project "theo-demo" at ...`. This proves registering changes nothing in the repository (`git status` stays clean).

## 2. See the services before the `.env` exists

```sh
theo services theo-demo
```

Expect an error about the required variable (`set GREETING in .env`). This proves Theo relies on Compose's own validation and does not start anything half configured.

Create the `.env` (it is ignored by Git):

bash:

```sh
cp .env.example .env
```

PowerShell:

```powershell
Copy-Item .env.example .env
```

```sh
theo services theo-demo
```

Expect `cache`, `web` and `worker`: `web` with `8080 -> 8080/tcp`, a `bind ./config -> /etc/demo (read-only)` and `volume data`, `depends on: cache`; `worker` with `profiles: worker`; and `Env file .../.env (this worktree)`. This proves services, mounts, dependencies and profiles are read from Compose.

## 3. Ports

```sh
theo ports theo-demo
```

Expect one row: `web  8080/tcp  8080  8080  free  http://localhost:8080`. If it says `in use`, something else uses 8080: free it first.

## 4. Start the main worktree

```sh
theo up theo-demo --build
```

Expect `Started theo-demo.` at the end. Then:

bash:

```sh
curl -s localhost:8080/
```

PowerShell:

```powershell
curl.exe -s localhost:8080/
```

Expect `greeting: Hello from Theo`, `instance: main`, `branch:   unknown`, `visits:   1`. Run it again: `visits:   2`. This proves the stack runs, the `.env` reaches the container, the bind mount is mounted and `web` reaches `cache`.

Try the worker (a profile):

```sh
theo up theo-demo --profile worker
```

```sh
theo logs theo-demo --tail 3 worker
```

Expect `worker tick 1`, then more over time.

## 5. A second worktree

```sh
theo worktree add theo-demo feature/x --new
```

Expect `Created worktree for feature/x at ...` and a note that `.env` is missing there and Theo will use the main one by reference, copying nothing. This proves ignored files are never copied or overwritten.

```sh
theo worktrees theo-demo
```

Expect two worktrees, both `clean`.

## 6. Start it: conflict, warning, `--auto-ports`, container names

bash:

```sh
BRANCH=feature-x theo up theo-demo -w feature/x --build
```

PowerShell:

```powershell
$env:BRANCH = "feature-x"; theo up theo-demo -w feature/x --build
```

Expect it to **fail**, after two warnings:

```
theo: warning: host port 8080 (web, container port 8080/tcp) is already in use. Free it, or start with --auto-ports.
theo: warning: cache set a fixed container_name, so another instance of this project cannot run at the same time. ...
```

and Docker's `Conflict. The container name "/cache" is already in use`. This proves conflicts are detected and explained before they bite, and that nothing of the first instance was touched.

Move the port:

```sh
theo up theo-demo -w feature/x --auto-ports
```

It still fails on the container name. `--auto-ports` solves only ports.

Neutralize the fixed name for this instance (your Compose file is not changed):

```sh
theo instance set theo-demo -w feature/x --neutralize-container-names
```

Expect `... will start without fixed container names (the project's files are not changed).`

```sh
theo up theo-demo -w feature/x --auto-ports
```

Expect `Started theo-demo-feature-x.` Check the port it got:

```sh
theo ports theo-demo -w feature/x
```

Expect host port `8081`. Then:

bash:

```sh
curl -s localhost:8081/
```

PowerShell:

```powershell
curl.exe -s localhost:8081/
```

Expect `branch:   feature-x` and `visits:   1`. The second instance has its **own** redis, so its counter starts from 1 while main's keeps growing. (`branch` is baked at build time from the `BRANCH` variable of your shell; it reads `unknown` when you build without it. Start the instance again with `--auto-ports` every time: a plain `up` goes back to port 8080.)

## 7. Per-instance variables, including a secret

```sh
theo env set theo-demo -w feature/x INSTANCE_NAME=second
```

```sh
theo env set theo-demo -w feature/x API_TOKEN=not-a-real-secret --secret
```

```sh
theo env list theo-demo -w feature/x
```

Expect `API_TOKEN  ********` and `INSTANCE_NAME  second`. The secret is masked; `--show-secrets` shows it. This proves per-instance settings never edit `.env` or `compose.yaml` (check with `git status` in both worktrees).

```sh
theo up theo-demo -w feature/x --auto-ports
```

Expect `theo: using generated file ...` lines. Then `curl` port 8081 again: `instance: second`.

```sh
theo instance config theo-demo -w feature/x
```

Shows what the instance runs with; the token is masked.

## 8. `ps`, `status`, `ports`, history

```sh
theo ps theo-demo
```

Expect both worktrees `running`.

```sh
theo ps theo-demo -w feature/x
```

Expect `cache`, `web` (and `worker` if you started it) with their state.

```sh
theo status theo-demo
```

A summary of every instance: running state, URLs and ports (see [status](../../docs/cli/status.md)).

```sh
theo history theo-demo
```

Expect your `up` operations with result (`ok` or the error of the failed ones), instance and user.

## 9. Share the cache between instances

Until now each instance had its own redis. Stop both instances first, so no copy of the cache is left running, then share one:

```sh
theo down theo-demo
```

```sh
theo down theo-demo -w feature/x
```

```sh
theo shared add theo-demo cache
```

```sh
theo shared up theo-demo
```

Expect the shared `cache` to start once, in its own Compose project (`theo-demo-shared`), outside both instances and without a host port. Then point both instances at it:

```sh
theo shared use theo-demo -w main cache
```

```sh
theo shared use theo-demo -w feature/x cache
```

Restart both, letting Theo start the shared service if needed:

```sh
theo up theo-demo --start-shared
```

```sh
theo up theo-demo -w feature/x --auto-ports --start-shared
```

Now `curl` ports 8080 and 8081 alternately: the counter keeps growing across both instances (`visits: 7`, then `8` from the other, and so on). This proves a shared service is one deliberate container used by several instances, not a port trick.

```sh
theo shared status theo-demo
```

Expect `cache` running, and which instances use it.

```sh
theo shared down theo-demo
```

Expect it to be **refused** while instances use it, naming them. Stop the instances first (`theo down theo-demo`, `theo down theo-demo -w feature/x`), and only then is `theo shared down theo-demo` allowed. Its data is kept.

See [shared services](../../docs/cli/shared.md) for the details and limits.

## 10. Volumes: kept after `down`, deleted only on purpose

Make sure both instances are down:

```sh
theo down theo-demo -w feature/x
```

```sh
theo volumes theo-demo -w feature/x
```

Expect `cachedata` and `data` as `kept`, with a hint about `purge`. This proves `down` never deletes data.

```sh
theo volumes purge theo-demo -w feature/x
```

Expect a refusal listing what would be deleted and the exact command with `--confirm theo-demo-feature-x`. Nothing was deleted.

```sh
theo volumes purge theo-demo -w feature/x --confirm theo-demo-feature-x
```

Expect `Deleted 2 volume(s) of theo-demo-feature-x`. Main's volumes are untouched (`docker volume ls`).

Backup, restore and clone of an instance's data (see [volumes](../../docs/cli/volumes.md) for every flag). The first time you run these, Docker pulls the small helper image `alpine:3.20`:

```sh
theo volumes backup theo-demo -w main --volume data --out ./backups
```

Expect one `.tar.gz` and a `.json` manifest in `./backups`. Restoring over a volume that already has data is refused unless you say `--replace` and confirm:

```sh
theo volumes restore theo-demo -w main --from ./backups/<the-file>.tar.gz --replace --confirm theo-demo
```

Copy the data of `main` into the second instance (the way to seed a new branch with real data; the target must be stopped):

```sh
theo volumes clone theo-demo --from main -w feature/x --volume data --replace --confirm theo-demo-feature-x
```

The shared cache's data is deleted separately, and only when it is stopped:

```sh
theo shared purge theo-demo --confirm theo-demo-shared
```

```sh
theo shared unuse theo-demo -w main
```

```sh
theo shared unuse theo-demo -w feature/x
```

```sh
theo shared remove theo-demo
```

## 11. Reconcile after deleting a worktree by hand

Bring the second instance down, then delete its directory yourself (not with Theo). First find the path:

```sh
theo worktrees theo-demo
```

bash:

```sh
rm -rf <path-of-feature-x>
```

PowerShell:

```powershell
Remove-Item -Recurse -Force <path-of-feature-x>
```

```sh
theo reconcile theo-demo
```

Expect `worktree missing` (with the `git worktree prune` command), plus `stale settings`, `stale ports` and `stale files` for the records Theo kept, and `orphan volumes` if you did not purge. Nothing changed yet.

```sh
theo reconcile theo-demo --apply
```

Expect `Forgot N of Theo's own record(s). Nothing else was touched.` Theo forgets only its own records; containers, volumes and your files are never removed here.

## 12. Clean up

```sh
theo down theo-demo
```

The volumes of main stay. To remove them on purpose:

```sh
theo volumes purge theo-demo --confirm theo-demo
```

If you shared the cache, also run `theo shared down theo-demo` (now allowed).

```sh
theo project remove theo-demo
```

Expect Theo to forget the project; files and volumes are not touched. Finally delete `~/theo-demo` yourself and, if you want, the images (`docker image rm theo-demo-web theo-demo-worker`).

## Running it without Theo

The app is plain Compose: `GREETING=hi docker compose up -d --build` from this directory serves the same page on port 8080.
