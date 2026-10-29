# Quickstart

Five minutes from an existing Compose project to two isolated copies running side by side. To try it on a small app built for the purpose, use [`examples/demo`](../../examples/demo/README.md).

You need Git and Docker with the Compose plugin in your `PATH`. Starting containers needs a running Docker daemon; reading a project does not.

## 1. Register the project

```sh
theo project add ~/code/my-api --name my-api
theo project list
```

From a linked worktree, Theo registers its main worktree. Registering touches nothing in the repository.

## 2. See what it runs

```sh
theo worktrees my-api      # branch, uncommitted changes and path of each worktree
theo services my-api       # services as `docker compose config` resolves them
```

`services` shows image or build, ports, mounts (bind mounts outside the worktree are flagged), dependencies, networks and profiles. Services behind profiles are included.

Git creates linked worktrees without ignored files such as `.env`. Theo resolves the env file in this order: `--env-file`; the worktree's own `.env`; the project's env file; the main worktree's `.env`. It is used by reference, never copied. To pin one for the project:

```sh
theo project set my-api --env-file ~/secrets/my-api.env   # or --no-env-file
```

## 3. Start the main worktree

```sh
theo up my-api [--build] [--profile worker]
theo ps my-api                  # which worktrees have containers
theo logs my-api -f --tail 100 [service...]
theo restart my-api
theo down my-api                # removes containers and networks, never volumes
```

Each worktree is its own Compose project: the main worktree keeps the name Compose gives it, and linked worktrees get `<project>-<directory>`, so containers, networks and volumes never mix.

## 4. Add a second worktree and run both

```sh
theo branches my-api                                   # local and remote branches, and which have a worktree
theo worktree add my-api feature/x                     # existing branch (or a single remote one)
theo worktree add my-api fix/bug-1 --new --from main   # new branch
theo worktree add my-api feature/x --path ~/trees/x    # choose the directory
theo project set my-api --worktree-dir ~/trees/my-api  # default directory for this project
theo up my-api -w feature/x --auto-ports
```

Without `--path` or `--worktree-dir`, worktrees go to `<Theo data dir>/worktrees/<project>/<branch>`. Theo never forces: a branch checked out in another worktree is refused. Worktrees made with plain `git worktree add` show up in `theo worktrees` too.

If both worktrees publish the same fixed port, `up` warns and `--auto-ports` picks another one; see [ports](ports.md). Per-instance variables and the operation history are in [instance settings](instance-settings.md).

## 5. Remove a worktree

```sh
theo worktree remove my-api feature/x
```

Refused while the worktree has containers (run `theo down` first) or uncommitted or untracked files, which are listed. To lose them on purpose:

```sh
theo worktree remove my-api feature/x --discard-changes --confirm <directory-name>
```

The main worktree is never removed, and neither are branches or volumes.

## Volumes

`down` never deletes volumes; deleting data is a separate, explicit step.

```sh
theo volumes my-api -w feature/x   # in use, or kept with their data after down; external ones listed
theo volumes purge my-api -w feature/x --confirm <compose-project>
```

`purge` is refused while the instance has containers, and without the matching `--confirm` (it lists what would be deleted and gives the exact command). Volumes declared `external` are never touched, and neither are other instances' volumes.

## Unregister

```sh
theo project remove my-api
```

Files, containers and volumes are not touched.

## Scripts and agents

`project list`, `worktrees` and `services` accept `--json`.
