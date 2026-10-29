# Managing projects

`theo project` registers a Git repository and keeps its settings. Registering,
editing and removing a project never changes the repository, its worktrees,
containers or volumes.

```
theo project add <path> [--name <name>] [--env-file <f>]
theo project list [--json]
theo project show <name> [--json]
theo project set <name> [--env-file <f> | --no-env-file]
                        [--worktree-dir <d> | --default-worktree-dir]
                        [--compose-file <f>... | --default-compose-files]
theo project rename <old> <new> [--yes] [--json]
theo project remove <name> [--yes] [--forget-records] [--json]
```

## show (FR-004)

Prints every setting of a project: path, registration date, env file, Compose files
(see [compose-files.md](compose-files.md)) and the worktree directory (the one set,
or Theo's own, with its effective path in `--json`).

```
$ theo project show shop
Project        shop
Path           /work/shop
Registered     2026-10-29 09:12
Env file       (the main worktree's .env)
Compose files  compose.yaml, deploy/dev.yaml
Worktree dir   /home/me/.config/theo/worktrees/shop (Theo's own)
```

## rename (FR-004)

`theo project rename shop webshop` changes the name and migrates every record
Theo keeps under the old one:

* the instance settings (`Project` of each entry in `instances.json`);
* every event of the history (`history.jsonl` is rewritten to a temporary file and
  then renamed into place);
* any other store that keeps records by project name. A store takes part by
  implementing `RenameProject(old, new string) (int, error)` (engine interface
  `ProjectRenamer`), either because it is the settings or ports store, or because it
  is added with `Engine.AddProjectRenamer`. This is how shared sets can join without
  this command knowing about them; a store that does not exist simply is not called;
* Port reservations are keyed by worktree path and carry no project name, so there
  is nothing to migrate; they stay attributed through the settings and history;
* the default worktree directory is named after the project (`<data>/worktrees/<name>`).
  Git worktrees cannot be moved by Theo, so when that directory exists, the project
  keeps using it (it is saved as the project's worktree directory) and new worktrees
  go next to the existing ones. `theo project set <new> --default-worktree-dir`
  returns to the default under the new name.

Why it can be refused: the Compose project name of a **linked** worktree is
`<project>-<directory>`, so a rename changes it, and containers, networks and
volumes belong to the old name. (The main worktree's name comes from Compose, from
`name:`, `COMPOSE_PROJECT_NAME` or its directory, and does not change.) The rename
is refused, changing nothing, when:

* the new name is taken by another project (case-insensitive) or is invalid;
* a Compose project with the old name of any worktree still has containers (running
  or stopped). Run `theo down` first;
* Docker cannot be asked while some name would change;
* a Compose project with a new name already exists in Docker, or another registered
  instance already uses it;
* volumes kept by the old names exist: they are not moved or deleted, and the
  renamed instances start with new, empty ones. This one is allowed with `--yes`.

A change in letter case only (`shop` to `Shop`) changes no Compose name and is never
refused for containers.

## remove (FR-003)

`theo project remove` unregisters the project and first says what keeps existing:

```
$ theo project remove shop
Project "shop" (/work/shop). Removing it from the registry deletes nothing; this stays:
  worktrees on disk:
    /work/shop
    /home/me/.config/theo/worktrees/shop/fix-1
  Compose projects with containers: none
  volumes kept with their data:
    shop-fix-1_data
  generated files (kept): none
  Theo's records: 1 instance settings, 1 reserved ports, 12 history events kept (--forget-records drops the first two)

Removed project "shop" from the registry. Its files, containers and volumes were not touched (/work/shop).
```

* "Compose projects with containers" lists projects (running **or** stopped) whose
  files are in the project's worktrees. If any exists the command refuses, exit
  code 1, and prints the list; repeat with `--yes` to unregister anyway. The
  containers keep running and Theo stops managing them.
* "volumes kept" are the volumes of the Compose projects of the project's instances
  (found by their Compose labels; external volumes are not Compose's and are not
  listed).
* If Docker cannot be asked the list says so, and the command continues: nothing is
  deleted either way.
* `--forget-records` also drops Theo's own records for the project: the instance
  settings and reserved ports of its worktrees and the files Theo generated for them
  (`overlays/`). Those are Theo's, not yours, and everything else stays. The history
  stays (it also serves `theo reconcile`).
* `--json` prints `{ "removed", "records_dropped", "resources" }` and the refusal still
  exits with 1.

## Duplicate Compose project names (FR-040)

Two instances that run as the same Compose project would share containers, networks
and volumes. `theo up` checks every registered project's instances and refuses when
another registered instance, of any project, has the same Compose project name:

```
theo: the Compose project name "shop-fix1" of project "shop", fix1 · /work/shop-fix1 is also used by
project "api", main · /work/api. Two instances with the same name share ... so nothing was started.
Make one name different: set `name:` ... or `theo project rename`
```

`theo reconcile` reports the same situation as a finding of kind
`duplicate compose name` (also with a project in scope, for the names that involve
it) and never changes anything for it. Other commands (`ps`, `down`) are not
blocked. The name of a main worktree needs one `docker compose config` per other
project; projects whose configuration cannot be read are skipped in this check.
