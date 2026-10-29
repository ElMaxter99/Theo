# theo volumes

Named volumes hold the data of an instance (databases, uploads). `theo down`
never deletes them. These commands list them, protect them, and move their data
between instances. Every command takes the project name and `-w <worktree>`
(branch, directory name or path; default: the main worktree).

```
theo volumes <project> [-w <w>] [--json]
theo volumes backup  <project> -w <w> [--volume <v>]... [--out <dir>] [--json]
theo volumes restore <project> -w <w> --from <backup.tar.gz> [--volume <v>] [--replace] --confirm <compose-project>
theo volumes clone   <project> --from <worktree> -w <worktree> [--volume <v>]... [--replace] --confirm <compose-project>
theo volumes purge   <project> -w <w> --confirm <compose-project> [--backup] [--out <dir>]
```

`--volume` is the volume's name in the Compose file (`data`) or its Docker name
(`myapp-feature_data`). `<compose-project>` is the instance's Compose project
name, shown by `theo instance show` and in the refusal messages: typing it is the
confirmation. Without it, nothing changes and the message tells you the exact
command to run. Every operation is recorded in `theo history`.

## The helper container

Theo reads and writes volume data with a short-lived helper container
(`docker run --rm --network none`). The image defaults to `alpine:3.20`; set
`THEO_HELPER_IMAGE` to use another one (it needs `sh`, `tar`, `find`, `ls` and
`head`; busybox-based images work). The image is pulled only if it is not
already on the machine. Backup and clone mount the source volume **read-only**.

## Backup

```
$ theo volumes backup myapp -w feature/login
Backed up data (1.2 MiB): ~/.config/theo/backups/myapp/myapp__myapp-feature__data__20261029T081500Z.tar.gz
  manifest: ~/.config/theo/backups/myapp/myapp__myapp-feature__data__20261029T081500Z.json
```

- One `.tar.gz` per volume, named `<project>__<instance>__<volume>__<UTC time>`,
  plus a `.json` manifest with the volume, instance, helper image, size and
  sha256 of the archive. Files are created readable by you only. Existing files
  are never overwritten.
- Default directory: `<Theo data dir>/backups/<project>/` (the data dir is
  `$THEO_HOME` if set, else the user config directory plus `theo`). Change it
  with `--out`.
- Works while the instance runs. Theo then warns that the data may be
  inconsistent: a database writing during the copy can produce a backup that
  does not open. For databases prefer their own dump tool, or run `theo down`
  first. Theo does not stop anything for you.
- Volumes Compose never created (no `up` yet) are skipped; external volumes are
  refused.

## Restore

```
theo volumes restore myapp -w feature/login --from backup.tar.gz --confirm myapp-feature
```

- The volume comes from the manifest next to the archive (same name, `.json`);
  without a manifest pass `--volume`, and the integrity check is skipped (Theo
  says so). With a manifest, the archive's sha256 is verified first and a
  mismatch refuses the restore.
- Refused while the instance has containers (running or stopped): `theo down`
  first. Refused when the volume already has data, unless `--replace`, which
  deletes the current data first. Take a backup of it before.
- If the volume does not exist (a fresh instance) Theo creates it with the
  labels Compose uses (`com.docker.compose.project` and `.volume`), so the next
  `theo up` adopts it without warnings.
- Only restore archives you trust: the archive is unpacked as root inside the
  helper container, into that volume only.

## Clone: seed a branch with the data of another

```
theo volumes clone myapp --from main -w feature/login --confirm myapp-feature
```

Copies the data of the volumes of the `--from` instance into the `-w` instance,
keeping permissions and ownership. The source is read-only and may be running
(with the same inconsistency warning). The target must have no containers; a
target volume that has data needs `--replace`. Without `--volume`, every
non-external volume that both instances declare and that exists in the source is
copied; the others are listed as skipped. Everything is checked before the first
copy starts.

Never touched: external volumes (declared `external: true`) and the volumes of
shared services (Compose project `<project>-shared`, used by several instances).
Both are refused with a message that says why.

## Purge with a backup

`theo volumes purge ... --backup [--out <dir>]` takes a backup of every volume it
is about to delete and deletes them only if all the backups succeeded. A purge
that would be refused (containers still exist, no `--confirm`) takes no backup.

## Limit: data that depends on the instance's port or URL

Theo copies bytes; it does not understand what is inside. Some applications
store their own address in the data. The classic case is WordPress, which keeps
the site URL (`siteurl` and `home` in the database) and may embed it in content:
a clone made on port `8081` and started on port `8082` still redirects to
`http://localhost:8081`. Theo does **not** rewrite data. Advice:

- Fix the address after cloning, with the application's own tool (for WordPress,
  `wp search-replace 'http://localhost:8081' 'http://localhost:8082'`, or define
  `WP_HOME` and `WP_SITEURL` from the instance's environment so it is not read
  from the database).
- Or keep the instance on a fixed port (see it with `theo ports`, set it with `theo env set`), so the address
  is the same everywhere.
- Prefer applications and images that read their public URL from an environment
  variable.
