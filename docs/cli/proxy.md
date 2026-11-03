# Local domains: `theo proxy`

Every instance gets a name of its own instead of a host port that changes
with `--auto-ports`:

```sh
theo proxy                      # foreground; Ctrl+C stops it
theo proxy --https              # also https://…localhost:7443 (see HTTPS below)
theo proxy routes [project]     # the table of names
theo proxy trust | untrust      # trust Theo's local authority on this machine
```

```
http://main.shop.localhost:7080            the main worktree of "shop" (its web service)
http://web.main.shop.localhost:7080        the same, naming the service
http://api.feature-x.shop.localhost:7080   the api service of the worktree "feature-x"
```

## Names

`<service>.<instance>.<project>.localhost[:port]`, and the short form
`<instance>.<project>.localhost[:port]`.

* **project** is the registered project name.
* **instance** is `main` for the project's main worktree. For a linked
  worktree it is what Theo adds to the project name in the Compose project
  name: the worktree's directory name, plus a short hash when two worktrees
  share a directory name (`theo ps <project>` shows the Compose names). The
  branch works too, as an alias, when no other instance has that name:
  worktree `../shop-login` on branch `feature/login` is both
  `shop-login.shop.localhost` and `feature-login.shop.localhost`.
* **service** is the Compose service name.

Each part goes through Theo's slug rule (the one that makes Compose project
names): lowercase; anything other than a letter, digit, `-` or `_` becomes
`-`; leading and trailing `-`/`_` are dropped. Host names also turn `_` into
`-` and are cut at 63 characters. So `feature/Login_Form` is
`feature-login-form`.

**The short form** goes to the service named `web`, `app`, `api` or
`frontend`, in that order; otherwise to the first service by name.

**Which port.** Only TCP ports published with a host port are routed (a
random host port, `"3000"`, is not, nor UDP). A service that publishes
several goes to its lowest container port (80 before 443, 8080 before 9229);
reach the others on their host port as before (`theo ports`).

The target is the host port the instance's container publishes now (the
reassigned one with `--auto-ports`), read the way `theo status` reads it: Git,
Theo's records and Docker. The proxy keeps that table for 2 seconds; a name
it does not know, or an instance it thinks is stopped, makes it read again
(at most twice a second), so an instance you just started answers at once.

## What the proxy does

* Listens on `127.0.0.1:7080` (`--port`, `--addr`). It refuses an address
  that is not loopback unless you pass `--allow-remote`: then **anyone who can
  reach your machine on that port reaches every running instance, with no
  authentication**, including databases' admin pages and debug endpoints. Use
  it only on a network you trust, and prefer a firewall rule that limits who
  can connect.
* Forwards the request unchanged, with the `Host` header you sent (so the
  application builds links with the name you use) and `X-Forwarded-For`,
  `X-Forwarded-Host` and `X-Forwarded-Proto: http`.
* WebSocket and other `Upgrade` requests pass through (hot reload of
  Vite, webpack, Next.js...).
* An unknown name answers **404** with the list of routes; `localhost:7080`
  itself shows that list with 200. A stopped instance answers **502** with the
  `theo up` command that starts it; so does one whose container does not
  answer.
* It writes `proxy.json` in Theo's data directory while it runs. `theo status`
  then shows the names of each instance (`proxy main: http://…` lines, and
  `instance.proxy_urls` in `--json`); without a running proxy, `status` is
  unchanged.
* Ctrl+C (or SIGTERM) stops it, letting requests in flight finish for up to 3
  seconds.

Some development servers check the `Host` header and refuse names they do not
know (Vite's `server.allowedHosts`, webpack-dev-server's `allowedHosts`, Django's
`ALLOWED_HOSTS`, Rails' `config.hosts`). Add `.localhost` there.

## `theo proxy routes`

```
ROUTE                                 PROJECT  INSTANCE   SERVICE  TARGET          RUNNING
http://main.shop.localhost:7080       shop     main       web      127.0.0.1:8080  yes
http://db.main.shop.localhost:7080    shop     main       db       127.0.0.1:5432  yes
http://web.main.shop.localhost:7080   shop     main       web      127.0.0.1:8080  yes
http://feature-x.shop.localhost:7080  shop     feature-x  web      127.0.0.1:8081  no
```

The port in the URLs is the running proxy's, else 7080, else `--port`.
`--json` prints `{"port", "proxy_running", "routes": [...]}`, each route with
`host`, `url`, `default` (the short form), `project`, `instance`, `aliases`
(optional), `service`, `worktree`, `branch` (optional), `compose_project`
(optional), `container_port`, `host_port`, `target` and `running`. `--hosts`
prints hosts-file lines (below).

## How `*.localhost` resolves

`localhost` and every name under it are reserved for the loopback address
(RFC 6761), but whether a program knows it depends on who resolves the name:

| Client | `*.localhost` → 127.0.0.1 | |
|---|---|---|
| Chrome, Edge, Brave, Opera (Chromium) | yes, by the browser itself | all systems |
| Firefox | yes, by the browser itself | all systems |
| Safari | recent versions do; older ones ask the system | not verified by us |
| curl ≥ 7.78 | yes, by curl itself | verified here (Linux, curl 8.5) |
| The system resolver (`ping`, `getent`, most languages' HTTP clients) | Linux with systemd-resolved or `nss-myhostname`: yes. Plain glibc with `files dns`: **no**. macOS and Windows: generally **no** | Linux without systemd-resolved verified here: `getent hosts web.main.shop.localhost` and Go's resolver fail |

So a browser and curl work as they are. For anything else (a test runner, an
HTTP library, `ping`, a container), add the names to the hosts file:

```sh
theo proxy routes --hosts | sudo tee -a /etc/hosts       # macOS, Linux
theo proxy routes --hosts                                 # Windows: paste into
                                                          # C:\Windows\System32\drivers\etc\hosts (as administrator)
```

The hosts file has no wildcards, so run it again after you add a worktree or
service. Or skip names entirely: any client can send the request to the proxy
and set the header, `curl -H 'Host: main.shop.localhost' http://127.0.0.1:7080/`.

What we verified: on Linux (glibc, `hosts: files dns`, no systemd-resolved),
curl 8.5 resolves `*.localhost` by itself while `getent` and Go's resolver do
not. The browser rows come from the browsers' documented behaviour; the macOS,
Windows and Safari rows are not verified by Theo's tests.

## HTTPS

Browsers already treat `*.localhost` as a secure context over plain HTTP, so
most work needs nothing more. Some things need a real `https://`: OAuth
providers that only accept HTTPS callbacks, `SameSite=None; Secure` cookies
across domains, HSTS, or trying the production configuration. For those:

```sh
theo proxy --https [--https-port 7443]
theo proxy trust                # once per machine
```

```
http://main.shop.localhost:7080     as before
https://main.shop.localhost:7443    the same instance over HTTPS
```

* The first time, Theo creates its **local authority** in `proxy/` of its data
  directory (`ca.pem`, and `ca-key.pem` readable only by you). Its key never
  leaves that folder. The authority carries *name constraints*: it can only
  sign `localhost` and names under it, and no IP address, so even a leaked key
  could not vouch for any other site (Chrome, Edge, Go and curl enforce this).
* The proxy signs a certificate per name when a browser first asks for it,
  valid for 30 days, and makes new ones each time it starts. An address typed
  as an IP (`https://127.0.0.1:7443`) gets no valid certificate: use the names.
* HTTP keeps working on its port; HTTPS is an addition, never a replacement.
  The application sees `X-Forwarded-Proto: https` on HTTPS requests.
* `theo proxy routes` and `--json` add the HTTPS address of each route
  (`https_url`) while the proxy serves HTTPS.

Until the machine trusts the authority, browsers warn on every name.
`theo proxy trust` adds it, with the system's own tool, and the system asks
you to confirm:

| System | Where | What it runs |
|---|---|---|
| Windows | Your user's trusted roots (no administrator needed) | `certutil -user -addstore Root` |
| macOS | Your login keychain | `security add-trusted-cert -r trustRoot` |
| Linux | The system's store (needs `sudo`) and Chrome's store, `~/.pki/nssdb` | `update-ca-certificates` or `update-ca-trust`, and `certutil` (package `libnss3-tools` or `nss-tools`) |

Restart the browser afterwards. `theo proxy untrust` removes it again; the
files stay in the data directory, so trusting again later is the same
authority. To start over, run `theo proxy untrust` and delete the `proxy/`
folder.

### Firefox

Firefox keeps a store of its own on Linux and macOS (on Windows it trusts the
system's roots by default). Theo does not change it. To trust the authority
there: Settings → Privacy & Security → Certificates → View Certificates →
Authorities → Import, choose `ca.pem` from `proxy/` in Theo's data directory,
and tick "Trust this CA to identify websites".

Switching an instance to HTTPS changes its address. Applications that keep
their address in their data (WordPress and others) may keep sending you to
the old one: Theo warns about it (see
[`volumes.md`](volumes.md) and the `url-in-data` warning of `theo status`).

## With the web interface

The interface (`theo ui` and `theo-desktop`) can run the proxy itself:
**Settings → Local domains → Serve local domains**, with the port to use
(7080 by default). It is the same proxy, on loopback only, in the
interface's own process; it writes `proxy.json` like `theo proxy`, so
`theo status`, `theo open` and MCP see it. The switch is remembered in
`proxy-ui.json` in Theo's data directory and turned on again the next time
the interface starts. While it is on, the interface does not stop by itself
after the idle timeout, so the names keep answering with no tab open; switch
it off, or stop the interface, to stop them.

While any proxy runs (the interface's or one started with `theo proxy`), the
project panel shows each running instance's local domains above its
`localhost:<port>` links, and the Summary tab lists every name. A proxy
started with `theo proxy` is shown in Settings but can only be stopped in its
terminal.

**Also serve HTTPS on port** (7443 by default) does what `--https` does; it
is remembered with the switch. Below it, the local authority shows whether this
machine trusts it. On Windows and macOS, **Trust on this machine** and **Stop
trusting** run `theo proxy trust` and `untrust` (the system shows its own
confirmation); on Linux, where it needs `sudo`, the interface shows the
command to run in a terminal.

The two servers are separate: the interface listens on
a random `127.0.0.1` port, the proxy on 7080. Browsers treat each
`*.localhost` name as its own site, so cookies of `main.shop.localhost` do not
leak to `feature-x.shop.localhost`, nor to the interface (cookies are not
scoped by port, so they would on bare `localhost`).

## `theo open`

```
theo open <project> [-w <w>] [service] [--print]
```

opens a running service of an instance in the default browser: its local
domain while a proxy runs (`http://<instance>.<project>.localhost:7080`
without a service, `http://<service>.<instance>.<project>.localhost:7080`
with one), else `http://localhost:<port>`. Without a service it picks web,
app, api or frontend, else the first service with a running TCP port.
`--print` only prints the address. It fails, with the `theo up` command,
when the service publishes no running port.

## With agents

An agent working in a worktree can find its instance's address without
knowing which port `--auto-ports` gave it:

```sh
theo proxy routes shop --json | jq -r '.routes[] | select(.default and .branch=="feature/x") | .url'
```

and fetch it with curl (which resolves `*.localhost` itself), or with
`-H 'Host: …'` against `127.0.0.1:7080` from a client that does not. The name
of an instance never changes while the worktree exists, so it can be written in
a test configuration or a prompt once.
