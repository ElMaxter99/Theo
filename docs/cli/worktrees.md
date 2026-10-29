# Workspaces: worktrees, clones, copied files and owners

A *workspace* is a directory where a branch of a project is checked out and
can run as its own Compose project. Theo has three kinds:

| kind       | what it is                                                            | created by                      |
|------------|-----------------------------------------------------------------------|---------------------------------|
| `main`     | the project's own checkout (the path given to `project add`)          | -                               |
| `worktree` | a linked Git worktree (shares objects with the project)               | `theo worktree add`             |
| `clone`    | an independent clone (own `.git`, own submodules)                     | `theo worktree add --clone`     |

Everything that takes a workspace (`up`, `down`, `ps`, `services`, `volumes`,
`env`, `instance`, `history`, `ports`, `reconcile`, `worktree remove`) treats a
clone like a linked worktree. A workspace is selected by branch, directory
name or path, for example `-w feature/x`, `-w feature-x` or `-w /path/to/dir`.

## Copying unversioned files (`.env.local` and friends)

Git does not copy ignored or untracked files into a new workspace. Theo's
default is to copy nothing and to say what is missing (and, for `.env`, to share
the file by reference, see `project set --env-file`). Copying is opt-in:

```
theo project set myapp --copy-on-add .env.local --copy-on-add config/secrets.json
theo worktree add myapp feature/x --new --copy        # copies the listed files
theo worktree add myapp feature/y --new --copy-file local/dev.key   # one-off
theo project set myapp --no-copy-on-add               # empty the list again
```

Rules (decided in Q-014, alternative B of the open question, opt-in only):

- The list is per project, default empty. Repeating `--copy-on-add` sets the
  whole list; it does not append.
- Patterns are paths relative to the project root, with `/` as separator.
  `*`, `?` and `[...]` match within one directory (`config/*.json`). Absolute
  paths, any `..` component and anything inside `.git` are rejected when the
  pattern is set or used.
- `--copy` uses the project's list; `--copy-file <path>` (repeatable) adds
  files for this command only, and also works without `--copy`.
  `--copy` with an empty list and no `--copy-file` is an error, raised before
  anything is created.
- Files come from the **main worktree**. Only regular files that Git does not
  track are copied. Tracked files are skipped (the new workspace has its own
  checkout), directories are skipped, and so is a file that is, or sits behind,
  a symlink that resolves outside the main worktree.
- A file that already exists in the new workspace is **never overwritten**
  (the copy uses exclusive creation). A destination directory that is a
  symlink leading out of the workspace is not followed. Permissions of the
  source (for example `0600`) are kept.
- The output lists exactly what was copied and what was not, with the reason,
  and always warns that secrets now exist in two places, pointing to the
  by-reference alternative `theo project set <p> --env-file <path>`.

```
Created worktree for feature/x at /home/me/.config/theo/worktrees/myapp/feature-x
Copied from the main worktree: .env.local
Not copied: config/secrets.json (does not exist in the main worktree)
Note: Warning: .env.local may contain secrets, which now exist in two places. To share a file without duplicating it, use `theo project set myapp --env-file <path>`: Theo then uses it by reference.
```

## Independent clones and submodules (`--clone`)

`git worktree` support for submodules is experimental. When the main worktree
has a `.gitmodules`, `theo worktree add` still creates the worktree but adds a
note saying so and suggesting `--clone`.

```
theo worktree add myapp feature/x --new --clone [--from main] [--path <dir>] [--owner <name>]
theo worktree add myapp existing-branch --clone
```

What `--clone` does, in order:

1. `git clone --local --no-checkout <project path> <target>`. The target is
   `<worktree dir>/<branch>` like for worktrees (`--path` overrides it) and
   must not exist or be empty.
2. Sets `origin` of the clone to the project's own `origin` URL, so pushes go to
   the real remote and not to the local path. If the project has no `origin`,
   the clone's `origin` stays the project path and a note says so.
3. Checks out the branch (`--new` creates it from `--from`, default the
   project's HEAD). Without `--new` the branch must exist as a local branch of
   the project; the clone only knows those. Remote-tracking branches of the
   clone reflect the project's local branches until you run `git fetch` in it
   (Theo does not use the network).
4. Records the clone in Theo's registry (see below).
5. If there is a `.gitmodules`, runs `git submodule update --init --recursive`.
   If this fails the clone stays registered and the error says how to retry or
   remove it.

If a step before the registration fails, the half-made directory is deleted
(it only held what Theo just created).

The same branch cannot be checked out in two workspaces of a project, clones
included (the rule for worktrees, kept for clones so pushes do not collide).

The Compose project name is the same rule as for linked worktrees: the project
name plus the directory name (`myapp-feature-x`), with a short path hash only
when two workspaces share a directory name. The main worktree keeps its own
name.

### Removing a clone

`theo worktree remove <project> <w>` has the same protections as for a worktree:

- refused while the clone has containers (`theo down` first);
- refused when it has uncommitted or untracked files, and, only for clones,
  when a local branch has **commits that exist nowhere else**: not in any
  remote-tracking branch of the clone and not in any ref of the project. The
  listing says `commits only in the clone: branch x (abc1234)`. Push them, or
  `git fetch <clone> x` from the project, to save them;
- `--discard-changes --confirm <directory name>` overrides that.

Unlike a worktree, removing a clone deletes its whole directory and Git
history; only pushed work survives. Ignored files are lost without warning
(same as `git worktree remove`). Volumes are never touched. If the clone's
directory is already gone, the command only forgets the record.

### Where it is stored

Registry file (`registry.json`), per project:

```json
{
  "name": "myapp",
  "copy_on_add": [".env.local"],
  "workspaces": [
    {"path": "/abs/clone", "kind": "clone", "owner": "agent-7", "created_at": "2026-10-29T10:00:00Z"},
    {"path": "/abs/worktree", "kind": "worktree", "owner": "agent-8"}
  ]
}
```

Decision: a field on the project record, not a new store, because clones, like
worktrees, belong to exactly one project and are removed with it. Records of
kind `clone` make the workspace exist; records of kind `worktree`/`main` only
hold an owner label and are dropped when the owner is cleared or the worktree
is removed. `theo reconcile` reports a clone whose directory disappeared and
`theo worktree remove` forgets it.

## Owner labels

Any string (1 to 100 characters, no control characters), meant for the agent or
person working in a workspace.

```
theo worktree add myapp feature/x --new --owner claude-a
theo worktree owner myapp feature/x claude-b       # change
theo worktree owner myapp feature/x --clear        # remove
theo worktrees myapp --owner claude-b              # filter
```

`worktree owner` works on any workspace, the main one included. The label is
informational: Theo does not enforce it. It is shown in `theo worktrees`
(OWNER column), in `theo ps <project>` (an OWNER column appears when any
workspace has one), in the `theo: ...` line printed by commands that act on an
instance (`[owner: x]`, `[clone]`), in `ps -w --json`, and in `reconcile`
findings about a missing workspace.

## JSON schema (stable for the GUI)

Fields are only ever added. Absent optional fields mean "empty or false".
Lists print `[]`, never `null`, for `worktrees`.

### `theo worktrees <project> --json`

An array, main worktree first, then linked worktrees in Git's order, then
clones in registration order. `--owner <name>` filters it.

| field      | type    | notes                                                                |
|------------|---------|----------------------------------------------------------------------|
| `path`     | string  | absolute path                                                        |
| `kind`     | string  | `main`, `worktree` or `clone`. Always present                        |
| `owner`    | string  | omitted when there is none                                           |
| `branch`   | string  | short name; omitted when detached or bare                            |
| `head`     | string  | commit hash; omitted when unknown                                    |
| `dirty`    | boolean | uncommitted or untracked files. Always present; `false` if bare or missing |
| `detached` | boolean | omitted when false                                                   |
| `bare`     | boolean | omitted when false                                                   |
| `locked`   | boolean | omitted when false                                                   |
| `prunable` | boolean | the directory no longer exists (also for a clone); omitted when false |

```json
[
  {"path": "/src/myapp", "head": "9f2c...", "branch": "main", "dirty": false, "kind": "main"},
  {"path": "/data/worktrees/myapp/feature-x", "head": "1a7e...", "branch": "feature/x", "dirty": true, "kind": "clone", "owner": "claude-a"}
]
```

### `theo branches <project> --json`

An array: local branches first, then remote-tracking ones, each group sorted by
name.

| field      | type    | notes                                                                             |
|------------|---------|-----------------------------------------------------------------------------------|
| `name`     | string  | short name; remote branches are `origin/feature`                                  |
| `remote`   | boolean | omitted when false                                                                |
| `worktree` | string  | path of the workspace (worktree or clone) that has the branch checked out; omitted when none. A remote branch gets the path of the local branch of the same name |

A branch that exists only in a clone (not yet fetched into the project) is
listed as a local branch with that clone as `worktree`.

### `theo worktree add ... --json`

| field        | type     | notes                                                           |
|--------------|----------|-----------------------------------------------------------------|
| `path`       | string   |                                                                 |
| `branch`     | string   |                                                                 |
| `kind`       | string   | `worktree` or `clone`                                           |
| `owner`      | string   | omitted when none                                               |
| `env_file`   | object   | `{"path": ..., "source": "flag|worktree|project|main worktree|none"}` |
| `notes`      | string[] | things to know (submodules, secrets warning, missing `.env`)    |
| `copied`     | string[] | root-relative files copied                                      |
| `not_copied` | object[] | `{"path": ..., "reason": ...}`                                  |

### Other JSON that gained fields

- `theo ps <project> --json`: each item has `worktree`, `stacks` and now `kind`
  and `owner`.
- `theo ps <project> -w <w> --json` and instance objects: `kind`, `owner`.
- `theo worktree owner ... --json`: one object like an item of `worktrees`.

## Limits

- Verified on Linux with Git 2.43 and a Docker daemon. macOS and Windows are
  only covered by CI. On Windows, copying relies on symlink resolution that may
  differ, and read-only Git object files are made writable before a clone is
  deleted.
- Git 2.38.1+ blocks the `file` protocol for submodules by default. A project
  whose submodules are local paths needs `protocol.file.allow=always` in your
  Git configuration for `--clone` to initialize them (remote URLs are fine).
  Theo does not change your Git configuration.
- Submodule initialization uses the network when the submodules are remote.
- Clones copy objects with hard links when possible (`--local`), so they are
  cheap on one filesystem but not a free copy across filesystems.
- Owner labels are not an access-control mechanism.
