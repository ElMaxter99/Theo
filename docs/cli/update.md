# Installing and updating Theo

## Installers

Every release carries one installer per system, next to the plain archives. They install for **your user**
(no administrator), in a folder Theo itself can write to, so later versions install from inside the app.

| System | File | Installs into | Remove it with |
|---|---|---|---|
| Windows 10/11, 64-bit | `theo-setup_vX.Y.Z_windows_amd64.exe` | `%LOCALAPPDATA%\Programs\Theo`: `theo.exe` and `theo-desktop.exe`, a Start menu entry, optionally a desktop shortcut and `theo` in your `PATH` | Settings > Apps, or `uninstall.exe` |
| macOS 11+ | `Theo_vX.Y.Z_darwin_arm64.dmg` (Apple silicon) or `..._darwin_amd64.dmg` (Intel) | Drag `Theo.app` to Applications. The `theo` command is inside the app: `sudo ln -s /Applications/Theo.app/Contents/MacOS/theo /usr/local/bin/theo` | Delete `Theo.app` |
| Linux, any distribution, 64-bit | `theo-installer_vX.Y.Z_linux_amd64.sh` | `~/.local/bin/theo`, `~/.local/bin/theo-desktop`, the "Theo" entry of your applications menu | `theo-uninstall` |
| Debian, Ubuntu | `theo_vX.Y.Z_linux_amd64.deb` | `/usr/bin` (needs `sudo`; Theo cannot replace itself there) | `sudo apt remove theo` |

Check any of them against `checksums.txt` of the release. The macOS app is only ad-hoc signed, so the first
time: right-click > Open (see [desktop](desktop.md)). The window needs WebView2 (Windows) or WebKitGTK 4.1 (Linux);
the installers say so when it is missing.

## Update from the app

The desktop app looks for a new version when it starts and every 6 hours while it is open. When one exists:

- a banner appears at the top of the window and a tag next to the search box,
- one desktop notification is shown per version (turn it off with `update-notify`),
- **Update now** (in the banner or in Settings > Updates) downloads the archive for your system, checks its
  SHA-256 against the release's `checksums.txt`, replaces `theo` and `theo-desktop`, and offers **Restart Theo**.
  Nothing is installed without you pressing the button. Containers and projects are not touched.

If the download does not match its checksum, or a file cannot be replaced, the previous version stays in place
and the app says why. A copy that cannot replace itself (for example the `.deb`) opens the release page instead.

## Release candidates

Release candidates (`vX.Y.Z-rc.N`, test versions) are never offered by default. To receive them:

```
theo config set update-channel rc        # or the checkbox in Settings > Updates
theo config set update-channel stable    # back to final releases only
```

Versions compare as a whole: `0.12.0-rc.2` is newer than `0.11.1`, and `0.12.0` is newer than any `0.12.0-rc.N`.

## Settings

| Setting | Values | Default |
|---|---|---|
| `update-check` | `on` / `off`: look for new versions by itself | `on` in the installed desktop app; `off` for the command line alone |
| `update-channel` | `stable` / `rc` | `stable` |
| `update-notify` | `on` / `off`: desktop notification for a new version | `on` |

Looking only asks GitHub's releases API for the list of releases; nothing about you or your projects is sent.
A development build (`0.0.0-dev`) never looks and is never offered an update.

## `theo update`

```
theo update --check     # say whether a newer version exists
theo update             # download it, verify it and replace the programs
theo update --json
```

It replaces `theo` and, when it sits next to it, `theo-desktop`; inside the macOS app it replaces the whole
`Theo.app`. Restart Theo afterwards if it is open. A copy that was not installed from a release (a `go install`
or a development build) is not replaced: it says where to download.

## For packagers and tests

`THEO_UPDATE_API` replaces the address of the list of releases (GitHub's `releases` JSON); with it set, the
files may be served from that host too. Without it Theo only downloads from
`https://github.com/ElMaxter99/Theo/releases/download/`.

The web interface uses `GET /api/v1/update`, `POST /api/v1/update/check`, `/update/settings`, `/update/dismiss`,
`/update/install` and `/update/restart` ([api](../api.md)).
