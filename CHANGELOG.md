# Changelog

Notable changes per release. Each release on GitHub also lists every merged pull request.

## v0.2.0 (unreleased)

### New
- **`theo mcp`**: a Model Context Protocol server over stdio, so AI agents (Claude Code and others) can list projects, create worktrees with an owner label, start, stop and inspect instances, and dry-run operations. Operations that delete data stay human-only. `--read-only` and `--owner`. See [docs/cli/mcp.md](docs/cli/mcp.md).
- **`theo proxy`**: a local reverse proxy on `127.0.0.1:7080` that serves every instance at `http://<service>.<instance>.<project>.localhost:7080` (and `<instance>.<project>.localhost`), WebSockets included. `theo proxy routes` lists the URLs (`--json`, `--hosts`); `theo status` shows them while the proxy runs. See [docs/cli/proxy.md](docs/cli/proxy.md).
- **Container engines**: `theo doctor` identifies Docker Engine, Docker Desktop, Colima, OrbStack, Rancher Desktop and Podman, and explains what differs. `THEO_DOCKER` points Theo at another docker-compatible CLI. See [docs/cli/engines.md](docs/cli/engines.md).
- **Spanish interface**: the web interface and the desktop app follow the browser language (English or Spanish) with a selector next to the theme.
- **Proxy from the interface**: the web interface and the desktop app can run the proxy themselves (Settings, or the line above the worktrees) and remember the switch; each worktree shows its local domains while a proxy runs. See [docs/cli/proxy.md](docs/cli/proxy.md#with-the-web-interface).
- **`theo open <project> [-w <w>] [service]`**: opens a running service in the browser, at its local domain while a proxy runs. `--print` only prints the address.
- **`theo mcp install`**: prints (or with `--apply` runs) the command that registers Theo in Claude Code, plus a paragraph for `AGENTS.md`; `--owner`, `--read-only`, `--scope`.
- **MCP**: new read-only tool `proxy_routes`; `ports`, `up` and `status` include the proxy addresses while a proxy runs.
- **Compose changed warning**: `theo status`, the interface and MCP warn (`compose-changed`) when the Compose configuration of a running instance changed since its last `up` or `recreate`, with the command that applies it.
- **`theo worktree prune <project> --merged`**: lists the worktrees whose branch is merged into the main branch (or `--into`), and with `--confirm <project>` removes the ones with no containers and no uncommitted files. Branches are never deleted.
- **`theo stop --all [project]`** and a **Pause all** button in the interface stop every running instance at once without removing anything.
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
