# Reconcile

Theo keeps records (instance settings, reserved ports, generated files). `reconcile` compares them with what Git and Docker really have.

```sh
theo reconcile [my-api] [--apply] [--json]
```

## What it reports

- A worktree removed outside Theo (its directory is gone but Git still lists it).
- Containers or a Compose stack left behind by a deleted worktree, or started by hand under another name.
- Volumes kept with their data after a worktree was deleted.
- Theo's own stale records: settings, reserved ports and generated files of a worktree that no longer exists.

Each finding says what to do next, for example `git worktree prune` or the exact `docker compose -p ... down`.

With `--json` every finding has an `id` (stable between runs: a hash of what it is about); the web interface uses it to forget exactly the records it showed.

## What it changes

Nothing, unless you pass `--apply`. Even then it only forgets Theo's own stale records (settings, reserved ports, generated files). It never removes containers, volumes, worktrees or your files; for those it prints the command and you decide.

```
Forgot 4 of Theo's own record(s). Nothing else was touched.
```
