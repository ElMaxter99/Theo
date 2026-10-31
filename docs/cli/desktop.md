# Theo Desktop: the web interface in a native window

`theo-desktop` is the [web interface](ui.md) in a window of its own instead of a browser tab. It is the same server
and the same page as `theo ui`; only the frame around them changes. It is a **separate program** from `theo`: the
`theo` command line stays a single static binary with no graphical dependency, and you can use either, or both.

```
theo-desktop            # opens the "Theo" window
theo-desktop version
```

## What it does

- Starts the same server as `theo serve` inside its own process, on `127.0.0.1`, and loads its launch URL straight
  into the window. The URL never goes through a command line, an environment variable or an external opener, so it
  is **not visible in the process list** (the limit described in [ui.md](ui.md#known-security-limits)).
- Opens **one** window titled "Theo" (1280x820, minimum 900x600).
- Uses the same `ui.lock` as `theo ui`. If a Theo server is already running for your data directory (started by
  `theo ui`, `theo serve` or another `theo-desktop`), the window opens onto that one and **leaves it running** when
  it closes. If not, closing the window stops the server it started and removes `ui.lock`.
- Links to your running containers (`http://localhost:8080`...) open in your **system browser**, never inside the
  window. The window cannot be navigated to any other address than the Theo server.
- Does not weaken anything in the [security model](ui.md#the-security-model-in-plain-words): same `Host`, `Origin`,
  CSRF and CSP checks, same one-time nonce and cookie. The window loads `http://127.0.0.1:PORT`, so the checks pass
  as they are; **no User-Agent or header exception was needed.**

The server of the app has no idle timeout: the window decides when it ends.

## Requirements

| System | What it needs |
|---|---|
| Windows 10/11, 64-bit | The **Microsoft Edge WebView2 Runtime**. It comes with Windows 11 and with current Windows 10 updates; if it is missing the app shows an error dialog, and the runtime is a free download from Microsoft. Nothing else to install. |
| macOS 11 or later | Nothing: it uses the system WebKit. |
| Linux, 64-bit | **GTK 3 and WebKitGTK 4.1** (`libwebkit2gtk-4.1`): Debian 12 / Ubuntu 22.04+: `sudo apt install libwebkit2gtk-4.1-0`; Fedora: `webkit2gtk4.1`; Arch: `webkit2gtk-4.1`. A graphical session (X11 or XWayland). |

Docker and Git are needed to *do* anything in Theo, exactly as with the command line. On macOS the app also looks for them in
`/opt/homebrew/bin`, `/usr/local/bin` and Docker Desktop's directory, because an app opened from Finder does not
inherit your shell's `PATH`.

## Install and run

Download the archive for your system from the [releases](https://github.com/ElMaxter99/Theo/releases) and check it
against `checksums.txt`.

- **Windows:** unzip, run `theo-desktop.exe`. The program has no console window; start errors appear in a dialog.
  The icon is embedded in the executable.
- **macOS:** unzip, drag `Theo.app` to Applications, open it. The app is only **ad-hoc signed** (no Apple Developer
  ID), so the first time macOS refuses it. Right-click > Open > Open, or run
  `xattr -dr com.apple.quarantine /Applications/Theo.app`. The `arm64` build is for Apple silicon, `amd64` for Intel.
- **Linux:** extract the archive and run `./theo-desktop`, or `./install.sh` to put it in `~/.local/bin` with a
  launcher entry ("Theo") and icon for your menu. Remove it by deleting `~/.local/bin/theo-desktop`,
  `~/.local/share/applications/theo-desktop.desktop` and `~/.local/share/icons/hicolor/128x128/apps/theo.png`.

The icon is the project's provisional app icon (`web/public/brand/`, [CC BY 4.0](../../web/public/brand/NOTICE.md)).

## Building it yourself

The desktop code sits behind the Go build tag `desktop` and needs cgo, a C/C++ compiler and the system web view
headers. Without the tag, `go build ./...`, `go vet ./...` and `go test ./...` skip all of it, and `go build
./cmd/theo` stays pure Go (`CGO_ENABLED=0`, cross-compilable).

```
go build -tags desktop -o theo-desktop ./cmd/theo-desktop
```

- **Linux:** `sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev`. The web view library (`webview_go`, pinned to an
  exact commit) asks `pkg-config` for `webkit2gtk-4.0`, which Ubuntu 24.04 and newer no longer ship. Run
  `export PKG_CONFIG_PATH="$(scripts/webkit40-shim.sh):$PKG_CONFIG_PATH"` first: it writes a `webkit2gtk-4.0.pc` that
  forwards to 4.1 (the part of the API the library uses is the same) and does nothing if a real 4.0 is installed.
- **macOS:** Xcode command line tools.
- **Windows:** a MinGW-w64 `gcc` in `PATH`; add `-ldflags "-H=windowsgui"` for a build without a console. For the
  icon, generate the resource first: `go run github.com/akavel/rsrc@v0.10.2 -ico web/public/brand/avatar-plum.ico
  -arch amd64 -o cmd/theo-desktop/rsrc_windows_amd64.syso` (the file is git-ignored). Windows already ships the
  WebView2 loader inside the library, so no DLL travels with the program.

Releases are built natively per system by `.github/workflows/release.yml`; `scripts/package-desktop.sh` makes the
archives (including the macOS `.app`).

## Troubleshooting

| Symptom | What to do |
|---|---|
| `cannot create the system web view` | Linux: no display (`DISPLAY`/`WAYLAND_DISPLAY` empty) or WebKitGTK missing. Windows: install the WebView2 Runtime. |
| `error while loading shared libraries: libwebkit2gtk-4.1.so.0` | Install the Linux package from the table above. |
| Blank or white window on Linux | A WebKitGTK rendering problem with some graphics drivers. Try `WEBKIT_DISABLE_COMPOSITING_MODE=1 theo-desktop` (and `WEBKIT_DISABLE_DMABUF_RENDERER=1`). Not reproduced here; this is the usual workaround. |
| macOS: "cannot be opened because the developer cannot be verified" | See Install above (ad-hoc signature). |
| Windows SmartScreen warns | The executable is not code-signed. "More info" > "Run anyway". |
| "the session expired" in the window | The server this window used went away (for example another `theo ui` was stopped). Close the window and open it again. |
| Docker shows as unavailable | Same as the web interface: start Docker. On macOS, make sure it is installed in one of the directories listed above. |
| A container link does nothing | It opens with the system's default browser (`xdg-open`, `open`, `rundll32`); check you have one configured. |

Diagnostics: `THEO_DESKTOP_REQUEST_LOG=/some/file` appends one line per request of the server the app starts
(method and route pattern only, like the server's own log: never paths, query strings or bodies). `THEO_HOME`
works as for the CLI.

## Known limits

- **Closing the window stops the server it started, and with it any operation (`up`, `down`...) still running in it.**
  The operation is cancelled the same way as pressing Ctrl+C in `theo ui`, and an interrupted `up` can leave the
  instance partly started. Wait for operations to finish before closing. If you opened the window onto a server
  started elsewhere, that server is not touched.
- If you started `theo-desktop` first and then a `theo ui` (which reuses that server for its browser tab), closing the
  window stops the server under that tab too.
- **The "stay inside the window" rule is enforced by a script injected into the page**, because the web view library
  has no navigation hook. It stops clicks, middle clicks, `target=_blank`, `window.open`, forms with another origin
  and files dropped on the window. It does not stop a script of the page itself from assigning `location`; Theo's
  page never does, and its CSP (no inline script, no external connections, no frames, `form-action 'none'`) leaves no
  way for project data to inject one. Addresses the page sends to be opened externally are checked again in Go:
  only `http` and `https`, no credentials in the address, never the Theo server's own address.
- One window per process. Starting `theo-desktop` twice gives two windows on the same server, which is supported.
- No developer tools, no menu bar, no tray icon, no auto-update, no installer (`.msi`, `.dmg`, `.deb`).
- The window icon is set on Windows (from the executable) and macOS/Linux take it from the bundle / launcher entry; running the bare
  Linux binary shows the toolkit's default icon in some shells.
- The Linux release is built against WebKitGTK 4.1 only. The 4.0 flavour was not tried.

## What has been verified

Be careful with the difference between *compiled* and *seen working*.

| | Verified how |
|---|---|
| Navigation filter, server reuse decision, stale `ui.lock`, shutdown on close, the PATH helper | Unit tests in `internal/desktop` (no cgo, no display; part of the normal `go test ./...`, which CI runs on all three systems). They start the real server with a fake window. |
| The injected script in a **real web view** (container links reach Go, `javascript:` and cross-origin forms do not, `target=_blank` to Theo stays inside, under the server's real CSP) | `go test -tags desktop ./internal/desktop` under `xvfb-run`: Linux, WebKitGTK 4.1 only. |
| Window opens, page loads, session established, one window titled "Theo", second app reuses the server, closing a window through the window manager stops the server (process gone, `ui.lock` removed, port closed) | `scripts/desktop-smoke.sh` under Xvfb + openbox on Linux; run by hand, and wired into CI. |
| Windows | **Compiled only**: cross-built once from Linux with MinGW (`-H=windowsgui`, icon resource), and the `Desktop (windows-latest)` CI and release jobs are set up to build it natively. **No Windows window was ever opened by anyone on this project**: WebView2 behaviour, the icon, the error dialog and the container-link opener are untested. |
| macOS | **The cgo/WebKit part was never compiled by the author** (no Mac available; only the pure-Go packages were type-checked for `darwin`). The `Desktop (macos-latest)` CI job and the release job are set up to compile it, and the `.app` packaging script has never been run. The Info.plist, the icon set and the ad-hoc signing are untested. The `amd64` build is cross-compiled on an Apple silicon runner and never run. |
| Wayland (without XWayland), HiDPI, a real GPU | Not tried. |
