# Changelog

Notable changes per release. Each release on GitHub also lists every merged pull request.

## v0.3.0 (unreleased)

### New
- **HTTPS for the local domains:** `theo proxy --https` (and **Also serve HTTPS** in Settings → Local domains) also serves every instance at `https://<instance>.<project>.localhost:7443`. Certificates come from a local authority Theo creates in its data directory, limited with name constraints to `localhost` and the names under it, so its key cannot vouch for any other site. `theo proxy trust` and `untrust` add it to or remove it from this machine's trusted roots (on Windows and macOS also from the interface; on Linux with `sudo`, plus Chrome's own store). Routes, `--json`, the API and the interface show the HTTPS address (`https_url`) while it runs. See [docs/cli/proxy.md](docs/cli/proxy.md#https).
- **MCP, knowing when things change:** `up`, `down`, `restart`, `stop` and `start` send `notifications/progress` with Docker's latest line when the client asks for progress (Claude Code does); the new read-only tool **`wait_for`** waits until an instance is running, stopped or changed (at most 120 s) and returns early when it fails; and `theo mcp --resources` (experimental) serves `theo://status` resources with subscriptions. The paragraph of `theo mcp install` tells agents to use `wait_for`. See [docs/cli/mcp.md](docs/cli/mcp.md#knowing-when-things-change).
- **Address stored in the data (Q-020):** for applications that keep their own address in their data (WordPress, Ghost, Drupal, Joomla, Matomo, Nextcloud, or any project with `theo project set --url-in-data on`), Theo warns (`url-in-data`) when an instance answers on another address than the one its data was made for: after `volumes clone`, after `up`, in `theo status`, MCP and the interface (a card on the instance summary with the old and the new address and what to do). Theo never changes data. `theo instance set --data-address-updated` records the new address once the data is updated.

### Changed
- **Desktop, missing WebView2 (Windows):** the dialog now explains in Spanish what is missing, offers to open Microsoft's download page (Yes / No) and points to `theo ui` in the meantime. The technical detail stays at the end for bug reports.

## v0.2.0

### New
- **Redesigned interface.** The web interface and the desktop app are reorganised around what you do most:
  - a **home** screen with what is running, what needs attention and the recent activity;
  - each project as tabs (Instances, Shared services, Activity, Settings);
  - a **side panel to create an instance** in one screen instead of the four-step wizard;
  - five tabs per instance (Summary, Logs, Configuration, Data, Activity) instead of seven;
  - **Problems** (instance warnings, the reconcile report and the cleanup of merged worktrees) instead of Health;
  - settings in sections.

  It also adds a command palette (Ctrl+K or ⌘K) with keyboard shortcuts, per-service actions, state filters, bulk start and
  pause, backups, restores and copies of volumes, renaming a project, the MCP registration, "Copy command" next to each
  action, and toasts when an operation ends. Turquoise has two tones (one for marks, one for text) and headings use the
  Fredoka typeface, bundled with the app. Addresses of 0.1 keep working. See [docs/cli/ui-web.md](docs/cli/ui-web.md#screens-02-redesign).
- **API for the interface**:
  - `GET /status` and `GET /history` for every project;
  - `POST /stop-all`, `GET /mcp/install`, `POST /projects/{p}/rename` and `GET|POST /projects/{p}/prune`;
  - volume backups, restores and clones as operations;
  - `services` on start, stop and restart, and the new operations `start` and `recreate`;
  - more fields in the project, worktree and settings changes.

  See [docs/api.md](docs/api.md).
- **CLI**:
  - `theo` with no arguments prints a summary of every project;
  - the project can be left out (or given as `.`) inside one of its folders;
  - `theo help <command>` and `theo help all`;
  - `--color auto|always|never` and `NO_COLOR`;
  - errors end with the next step to take.

  See [docs/cli/output.md](docs/cli/output.md).
- **`theo mcp`**: a Model Context Protocol server over stdio, so AI agents (Claude Code and others) can list projects, create worktrees with an owner label, start, stop and inspect instances, and dry-run operations. Operations that delete data stay human-only. `--read-only` and `--owner`. See [docs/cli/mcp.md](docs/cli/mcp.md).
- **`theo proxy`**: a local reverse proxy on `127.0.0.1:7080` that serves every instance at `http://<service>.<instance>.<project>.localhost:7080` (and `<instance>.<project>.localhost`), WebSockets included. `theo proxy routes` lists the URLs (`--json`, `--hosts`); `theo status` shows them while the proxy runs. See [docs/cli/proxy.md](docs/cli/proxy.md).
- **Container engines**: `theo doctor` identifies Docker Engine, Docker Desktop, Colima, OrbStack, Rancher Desktop and Podman, and explains what differs. `THEO_DOCKER` points Theo at another docker-compatible CLI. See [docs/cli/engines.md](docs/cli/engines.md).
- **Spanish interface**: the web interface and the desktop app follow the browser language (English or Spanish), with a selector in Settings.
- **Proxy from the interface**: the web interface and the desktop app can run the proxy themselves (Settings, or the line above the worktrees) and remember the switch; each worktree shows its local domains while a proxy runs. See [docs/cli/proxy.md](docs/cli/proxy.md#with-the-web-interface).
- **`theo open <project> [-w <w>] [service]`**: opens a running service in the browser, at its local domain while a proxy runs. `--print` only prints the address.
- **`theo mcp install`**: prints (or with `--apply` runs) the command that registers Theo in Claude Code, plus a paragraph for `AGENTS.md`; `--owner`, `--read-only`, `--scope`.
- **MCP**: new read-only tool `proxy_routes`; `ports`, `up` and `status` include the proxy addresses while a proxy runs.
- **Compose changed warning**: `theo status`, the interface and MCP warn (`compose-changed`) when the Compose configuration of a running instance changed since its last `up` or `recreate`, with the command that applies it.
- **`theo worktree prune <project> --merged`**: lists the worktrees whose branch is merged into the main branch (or `--into`), and with `--confirm <project>` removes the ones with no containers and no uncommitted files. Branches are never deleted.
- **`theo stop --all [project]`**, "Stop everything" on the home screen and "Pause every instance" in a project stop every running instance at once without removing anything.
- The interface shows the **owner** of each worktree (set by agents through MCP) and filters by it.
- **`theo project remove --delete-worktrees --confirm <name>`**: also deletes the project's other worktrees and clones, after the same checks as `worktree remove`. Without the flag nothing changes, but the listing says which worktrees stay on disk.

### Improved
- `theo worktree add` explains a leftover directory (usually from a removed project with the same name) instead of Git's bare "already exists".
- Accessibility: focus after removals and in the variables form, announcements of running operations, more contrast checks.
- The release workflow can be dry-run on pull requests and by hand.

### Testing
- `scripts/smoke.sh` and `scripts/smoke.ps1` walk the demo app through every feature against a real Docker daemon (about 125 checks, the v0.2 features included), and CI runs both on Linux. See [docs/testing.md](docs/testing.md).

## v0.1.0 (unreleased)

First version: projects, worktrees and clones, isolated instances per worktree, automatic host ports, per-instance variables and profiles, volumes with backup, restore and clone, shared services, `reconcile`, `doctor`, `--json` everywhere, the local web interface (`theo ui`) and the desktop app (`theo-desktop`).
