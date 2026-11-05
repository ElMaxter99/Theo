# Changelog

Notable changes per release. Each release on GitHub also lists every merged pull request.

## Unreleased

### New (v0.5: Theo in the agents' workflow)
- **`.theo.json`:** a committed file in the repository that declares `copy` (files a new worktree gets from the main one) and `after_create` (commands to run in it). Claude Code's `.worktreeinclude` is read too. See [agents](docs/cli/agents.md).
- **`after_create` commands** run after `theo worktree add`, and with `theo worktree setup`. They run code from the repository, so Theo asks the first time and whenever the list changes (`--yes` approves, `--no-setup` skips); without a terminal nothing runs.
- **Claude Code hooks:** `theo hook worktree-create | worktree-remove | session-start | agent-state`, and `theo mcp install --hooks` to add them to `.claude/settings.json` (local, project or user) without touching the rest of the file.
- **`theo env`:** `eval "$(theo env)"` (or `--format powershell|dotenv|json`) exports the project, worktree, ports and URLs; `session-start` hands them to the Claude Code session.
- **Agent state:** `working`, `waiting`, `done` or unknown per worktree, from the hooks (exact) or from agent processes on Linux (an estimate), in `status`, `worktrees --json`, the API and MCP; new status warnings `agent-waiting` and `agent-done`.
- **`--json`** in `up`, `down`, `restart`, `stop`, `start`, `recreate`, `open`, `env set|unset` and `theo --json`.
- **`theo log`** shows a diagnostic log of what hooks could not do.
- **`theo doctor`** warns about a Git older than 2.41, an invalid `.theo.json`, and hooks not installed.

- **`theo open --in terminal|editor|files`** opens a worktree's folder where you work; **`theo config`** sets the programs (`open.editor`, `open.terminal`, `open.files`).
- **`AGENT` column** in `theo worktrees` and an `Agents:` line in `theo`.
- **Web interface:** an **Open in** menu in each worktree row (editor, terminal, folder, proxy URL; the last one used goes first; a disabled option says why), an **agent chip** (with an *estimated* tag when it comes from processes), a **Waiting for you** card on Home, and **Close** blocked while an agent is alive (also in `theo worktree clean`). The API gets `GET|POST /projects/{p}/worktrees/{wt}/open` and `agent` in the overview.
- **New-version notice**, opt-in (`theo config set update-check on`), at most one request a day.

- **`theo worktree park-inactive`** (and *Park all the inactive* in the Cleanup view) parks every inactive worktree at once; `--save-changes` keeps uncommitted files in a local WIP commit on the branch, and nothing is parked without `--confirm`. `worktree clean` also lists merged worktrees that have uncommitted files, and the inactive ones show whether their remote branch is gone.

### Fixed
- **Windows:** matching worktrees with Theo's records compared every pair of paths and resolved symbolic links each time (about 70,000 file lookups for 52 worktrees); each path is now resolved once, so `theo worktrees` no longer loses seconds there.
- **Windows PowerShell 5.1:** the console showed `ÔåÆ` instead of `→` (and similar for `·`, `…`, `▶`...) because it reads Theo's UTF-8 with the old OEM code page. On Windows Theo now writes plain ASCII (`->`, `-`, `...`) to the console. `--json` output is left as it is. `THEO_ASCII=1` does the same on any system and `THEO_ASCII=0` turns it off.
- **Windows:** `theo worktrees` and `theo worktree clean` run up to 16 Git and Docker processes at once (twice the processors, at least 8; it was always 8), so a listing of 50 worktrees needs fewer waves of `git status`.

## v0.4.1

### Documentation
- **New README:** badges, screenshots of the interface (light and dark), a diagram of how worktrees, instances, shared services and the proxy fit together, an install table, a *What's new* section and sections for the interface and AI agents (MCP). It ships inside every release archive.
- **README in Spanish:** `README.es.md`, with Spanish screenshots of the interface and a language switch at the top of both.
- `cd web && npm run screenshots:readme` takes the README screenshots (English and Spanish interface) from the mock, without Docker.
- **CONTRIBUTING.md, Releasing:** every release reviews and improves the README in both languages; the pull request template reminds it.

No changes to the binaries since v0.4.0.

## v0.4.0

### New
- **Forgotten work (ADR-0016):** every worktree has a state (`missing`, `in-use`, `kept`, `merged`, `inactive`, `unpushed`, `new`, `pushed`) and its last activity, in `theo worktrees`, `theo status`, MCP, the API and the interface. Merged includes branches merged by squash or rebase on GitHub. `theo` lists the forgotten work.
- **`theo worktree clean`** closes the merged worktrees and the ones whose folder is gone, for one project or `--all`, after a typed confirmation; `--down`, `--delete-branches` and `--images` go further. Remote branches and volumes are never touched. The interface's Cleanup screen does the same.
- **`theo worktree park` / `resume`** put unfinished work away (folder removed, branch kept with a note) and bring it back; **`theo worktree keep`** leaves one alone until a date; **`theo worktree base`** shows or records the branch a worktree is compared with; **`theo project set --inactive-after <days>`** sets when a worktree counts as inactive (default 7).
- **MCP:** `finish_worktree` lets an agent close its own worktree once its branch is merged or pushed, `clean_worktrees` lists what could be closed, `list_worktrees` carries the state, and a server with `--owner` marks its owner's worktrees as in use while it works.
- **Interface:** the instances table shows the state, last activity and Git summary, groups by state or owner, and offers Park, Keep and Resume; home shows a Forgotten work card.
- New status warnings: `worktree-merged`, `worktree-inactive`, `instance-idle`.

### Fixed
- **Windows:** `theo worktrees` and `theo worktree clean` with many worktrees are several times faster: about one Git process per worktree instead of six, and in parallel (Git 2.41 or later compares every branch with its base in one command).
- **Windows:** `theo worktrees` asks Docker once (`docker compose ls`) while Git works, instead of also running `docker compose config` first, and `theo worktree clean` without `--confirm` looks for leftovers at the same time; with Docker Desktop each call costs a second or more.
- **Windows:** following logs in the interface no longer leaves `docker-compose.exe` running after the page closes the stream: Theo now ends the whole process tree.
- **Tests on Windows with Docker:** the tests that only failed there because of paths, line endings or `pgrep` pass.

## v0.3.1

### Fixed
- **Interface, row menus (⋯):** the menu of a table row opened inside the table and was cut by its border (Projects, the instances of a project, the services of an instance). It now opens over the page, upwards when there is no room below, and follows its button when something scrolls.
- **Interface, layout:** cards and sections that sat on top of each other (home, problems, shared services) are spaced again; the services table no longer hides its Actions column at normal widths (health moved under the container state) and port mappings, addresses and tags no longer break letter by letter; narrow inputs such as the proxy port keep their size.
- **Interface, shared services:** the screen shows the project head and its tabs like the other project screens.
- **Interface, conditions:** the bulk **Start** and **Pause** buttons are off when no selected instance can take that action, and unregistering a project from the list removes it from the sidebar at once.

## v0.3.0

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
