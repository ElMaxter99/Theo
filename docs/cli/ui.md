# `theo ui` and `theo serve`: the local web interface

`theo ui` starts a small web server inside the `theo` binary and opens the interface in your default browser.
`theo serve` is the same server without opening anything (for tests, scripts and agents). The interface shows
your projects, worktrees and instances, and starts, stops, restarts and inspects them. It uses the same engine as the
command line and **adds no permission**: every protection of the CLI (typed confirmations before data is deleted,
no removal of worktrees with containers, ...) is checked again by the server.

```
theo ui                                # start (or reuse) the server and open the browser
theo ui --no-open                      # print the launch URL instead of opening a browser
theo serve                             # same server, prints the launch URL
theo serve --print-launch-url          # one JSON line: {"url": "...", "port": 41873, "pid": 9120}
theo ui --idle-timeout 30m             # stop after 30 minutes without use (default 15m, 0 = never)
```

Stop it with Ctrl+C. The resources it serves are documented in [../api.md](../api.md).

## How it starts

1. It listens on `127.0.0.1` at a random port. It never listens on `0.0.0.0`, `localhost` or any other address,
   and there is no option to change that.
2. It prints (or opens) `http://127.0.0.1:PORT/#n=<nonce>`. The nonce is a one-time secret: it works **once** and
   only for **60 seconds**. The page reads it from the part after `#` (which a browser never sends to the server),
   exchanges it for a session cookie and removes it from the address bar. From then on no URL contains any secret.
   If you see "the launch link was already used or has expired", run `theo ui` again.
3. Only one server runs per Theo data directory. The server writes `ui.lock` there (readable only by you) with its
   process id, port and a control secret. A second `theo ui` finds it, asks the running server for a new launch URL and
   opens another tab instead of starting another server. If the process in `ui.lock` is gone, the stale file is replaced.

## The security model, in plain words

The server controls Docker for you, so it is closed on purpose. All of these apply together:

- **Local only.** Only programs on your computer can connect.
- **A one-time link, then a cookie.** Without the cookie every request is refused (401). The cookie is `HttpOnly`,
  `SameSite=Strict` and not readable by scripts.
- **Other web pages cannot use it.** The server refuses requests whose `Host` is not exactly `127.0.0.1:PORT`
  (this defeats DNS rebinding), whose `Origin` is another site, or that the browser says come from another site.
  Every change also needs a secret header (`X-Theo-Csrf`) that only the page you opened knows. There are no CORS
  headers, so no other site can read an answer.
- **A strict content policy.** The page may only load its own scripts and styles; no inline code, no external
  requests, no frames. Data from your projects (branch names, logs, variables) is always shown as text.
- **Secrets stay masked.** Variables marked secret are never in a list; "show" asks for one value at a time, the answer is not
  cached, and the server never writes request bodies to any log.
- **The server never builds shell commands.** It calls the engine, which runs `git` and `docker` directly.

What it does **not** protect against: another program running as your own user can already run `docker` itself.
The trust boundary is "the same operating system user". Do not paste the launch URL anywhere before using it.
The browser's own command line (`xdg-open`, `open`, `rundll32`) briefly shows the one-time URL; after the first use or
60 seconds it is useless.

## Long operations and shutdown

`up`, `down` and `restart` run as jobs in the server, so closing the browser tab does not interrupt them; open the page
again and the operation is still listed. At most 4 run at once (the rest wait), and only one change per instance
at a time. Cancelling stops the `docker compose` process and **does not undo** what it already did: an interrupted `up` can
leave the instance partially started, and the interface says so.

There is no reliable way to know that a browser tab was closed, so the server stops by itself only after
`--idle-timeout` (default 15 minutes) with no open stream (live updates, logs, operation output) **and** no running
operation. It never stops while an operation runs. Without any client it does not poll Docker at all; with a visible tab it
looks for changes made outside Theo every 5 seconds.

## Limits

- The interface reads the state of Docker through `docker compose`; with Docker stopped it still shows your projects and
  worktrees, with the state "unknown" and the reason.
- Changes made in another terminal show up within about 5 seconds, and only while a tab is visible.
- Directory browsing (`Browse...` when adding a project) lists directory names only, never files or their contents, and
  hides hidden directories by default.
- Operations are kept in memory (the last 50); the persistent record is the history (`theo history`).
- The server exposes shared services, reconcile, health, diagnostics and paginated history (see `docs/api.md`, sections 9 to 11). Every protection of the CLI applies: stopping shared services that running instances use needs the typed text, purging their data is refused while any instance is linked to them, and reconcile only forgets Theo's own records, never containers, volumes or worktrees.
- macOS and Windows are covered by CI only; nothing in the server is platform specific besides opening the browser.

## Troubleshooting

| Message | What to do |
|---|---|
| `the launch link was already used or has expired` | Run `theo ui` again. |
| `could not open a browser` | Open the printed URL yourself within 60 seconds, or use `--no-open`. |
| `cannot lock .../ui.start.lock` | Another `theo ui` is starting; wait a moment. |
| The page says the session expired | The server restarted; run `theo ui` again. |
