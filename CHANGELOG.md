# Changelog

Notable changes per release. Each release on GitHub also lists every merged pull request.

## v0.2.0 (unreleased)

### New
- **`theo mcp`**: a Model Context Protocol server over stdio, so AI agents (Claude Code and others) can list projects, create worktrees with an owner label, start, stop and inspect instances, and dry-run operations. Operations that delete data stay human-only. `--read-only` and `--owner`. See [docs/cli/mcp.md](docs/cli/mcp.md).
- **`theo proxy`**: a local reverse proxy on `127.0.0.1:7080` that serves every instance at `http://<service>.<instance>.<project>.localhost:7080` (and `<instance>.<project>.localhost`), WebSockets included. `theo proxy routes` lists the URLs (`--json`, `--hosts`); `theo status` shows them while the proxy runs. See [docs/cli/proxy.md](docs/cli/proxy.md).
- **Container engines**: `theo doctor` identifies Docker Engine, Docker Desktop, Colima, OrbStack, Rancher Desktop and Podman, and explains what differs. `THEO_DOCKER` points Theo at another docker-compatible CLI. See [docs/cli/engines.md](docs/cli/engines.md).
- **Spanish interface**: the web interface and the desktop app follow the browser language (English or Spanish) with a selector next to the theme.
- **`theo project remove --delete-worktrees --confirm <name>`**: also deletes the project's other worktrees and clones, after the same checks as `worktree remove`. Without the flag nothing changes, but the listing says which worktrees stay on disk.

### Improved
- `theo worktree add` explains a leftover directory (usually from a removed project with the same name) instead of Git's bare "already exists".
- Accessibility: focus after removals and in the variables form, announcements of running operations, more contrast checks.
- The release workflow can be dry-run on pull requests and by hand.

### Testing
- `scripts/smoke.sh` and `scripts/smoke.ps1` walk the demo app through every feature against a real Docker daemon (about 110 checks, the v0.2 features included), and CI runs both on Linux. See [docs/testing.md](docs/testing.md).

## v0.1.0 (unreleased)

First version: projects, worktrees and clones, isolated instances per worktree, automatic host ports, per-instance variables and profiles, volumes with backup, restore and clone, shared services, `reconcile`, `doctor`, `--json` everywhere, the local web interface (`theo ui`) and the desktop app (`theo-desktop`).
