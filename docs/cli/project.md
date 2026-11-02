# Managing projects

`theo project` registers a Git repository and keeps its settings. Registering,
editing and removing a project never changes the repository, its worktrees,
containers or volumes; the one exception is the opt-in
`project remove --delete-worktrees`, which deletes the linked worktrees and clones.

```
theo project add <path> [--name <name>] [--env-file <f>]
theo project list [--json]
theo project show <name> [--json]
theo project set <name> [--env-file <f> | --no-env-file]
                        [--worktree-dir <d> | --default-worktree-dir]
                        [--compose-file <f>... | --default-compose-files]
theo project rename <old> <new> [--yes] [--json]
theo project remove <name> [--yes] [--forget-records] [--json]
                           [--delete-worktrees --confirm <name> [--discard-changes]]
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
    (--delete-worktrees --confirm shop would also delete the other 1 worktree(s) and clone(s))
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
* When worktrees other than the main one remain, the listing says so and names
  `--delete-worktrees` (below). Without that flag nothing on disk is deleted.

### Deleting the worktrees too (`--delete-worktrees`)

Removing a project leaves its linked worktrees and the clones Theo made on disk,
and once the project is unregistered Theo can no longer remove them for you.
`--delete-worktrees` deletes them as part of the removal, opt-in and confirmed
with the project's name:

```
$ theo project remove shop --delete-worktrees --confirm shop
Project "shop" (/work/shop). With --delete-worktrees these worktrees and clones deleted from disk (their branches and volumes stay):
  /home/me/.config/theo/worktrees/shop/fix-1 (worktree, fix/1) clean
  /home/me/.config/theo/worktrees/shop/spike (clone, spike) clean
Everything else stays:
  worktrees on disk:
    /work/shop
  ...

Deleted 2 worktree(s) and clone(s) of "shop" from disk; its branches and volumes were kept.
Removed project "shop" from the registry. Its main worktree, containers and volumes were not touched (/work/shop).
```

* Every linked worktree (wherever it is) and every clone recorded by
  `theo worktree add --clone` is deleted, each one through the same code and checks
  as `theo worktree remove`. The **main worktree is never deleted**, nor any branch
  or volume. Theo's own worktree directory for the project
  (under `<data>/worktrees/`) is removed too once it is empty; a directory you set
  with `project set --worktree-dir` is left alone.
* `--confirm <name>` must be the project's exact name; without it, or with another
  value, the command prints what would be deleted and changes nothing.
* It is **refused while any instance has containers**, running or stopped, even with
  `--yes`: run `theo down` for each instance first. It is also refused when Docker
  cannot be asked, and when a worktree defines the project's shared services
  (`theo shared remove` first).
* A worktree with uncommitted or untracked files, or a clone with commits that
  exist nowhere else, refuses the whole command (the listing shows the files);
  `--discard-changes` deletes them anyway and they are lost. Ignored files (such as
  a copied `.env`) are lost without warning, as with `git worktree remove`.
* All checks run before anything is deleted. If deleting one worktree still fails
  (for example a locked worktree), the ones before it stay deleted, the project
  stays registered and the error says which were deleted; fix the cause and run the
  command again.
* It combines with `--forget-records`. In `--json`, `resources.to_delete` lists each
  worktree with its `kind`, `branch` and `changes`, and `resources.deleted` what was
  deleted.

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
