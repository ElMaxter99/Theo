# Agents in the workflow (0.5)

Theo can create, use and close worktrees for a coding agent without manual
steps, and tell you what each agent is doing. Everything here works from the
command line; the interface parts (the "Open in..." menu, the agent badge and the
"Waiting for you" card) follow the design approved on the canvas (V-13, V-14).

## The repository declares what a new worktree needs: `.theo.json`

A file in the root of the repository, committed, so the whole team and every
agent get the same worktree:

```json
{
  "version": 1,
  "copy": [".env", ".env.local", "config/*.key"],
  "after_create": ["npm ci", "npm run generate"]
}
```

- `copy`: files Git does not track that `theo worktree add --copy` copies from
  the main worktree (same rules as `project set --copy-on-add`: files only,
  never overwritten, never outside the worktree). Claude Code's
  `.worktreeinclude` is read too, as a read-only input (one file or glob per
  line, `#` comments; negations and directories are ignored with a note). The
  project's own `--copy-on-add` list still applies.
- `after_create`: commands run in the new worktree, one after the other,
  stopping at the first that fails. They run code from the repository, so
  Theo asks the first time, and again whenever the list changes. The answer is
  remembered per project for those exact commands.
  - `theo worktree add` asks on a terminal; `--yes` approves, `--no-setup` skips.
  - Without a terminal (a hook, a script) nothing runs and Theo says how to
    approve: `theo worktree setup <project> <worktree>` (`--yes` to approve
    without asking). The commands are read from the new worktree's own
    `.theo.json`, so a branch that changes them asks again.
- An invalid `.theo.json` is an error, not silently ignored. `theo doctor`
  checks the file of every registered project.

## Claude Code hooks: `theo hook`

```sh
theo mcp install --hooks                 # .claude/settings.local.json (yours)
theo mcp install --hooks --scope project # .claude/settings.json (the team's)
theo mcp install --hooks --scope user    # ~/.claude/settings.json
theo mcp install --hooks --print         # only show the settings
```

Existing settings are kept (the keys come out sorted); a hook already there is
not added twice. The hooks call:

| Event | Command | What it does |
|---|---|---|
| `WorktreeCreate` | `theo hook worktree-create` | creates a worktree and branch `worktree-<name>` (owner `claude`), copies the files, prints the path and nothing else on stdout. The same name gives the same worktree |
| `WorktreeRemove` | `theo hook worktree-remove` | removes the worktree if it has no changes; never discards anything |
| `SessionStart` | `theo hook session-start` | tells the session where it runs (ports and URLs) and exports the `THEO_*` variables through `CLAUDE_ENV_FILE` |
| `UserPromptSubmit`, `PostToolUse`, `Notification`, `Stop`, `SessionEnd` | `theo hook agent-state` | records whether the agent works, waits for you or is done |

A hook never gets in the agent's way: apart from `worktree-create`, which must
return a path or say why not, failures are written to `theo log`.

## `theo env`

```sh
eval "$(theo env)"                                    # POSIX shells
theo env --format powershell | Invoke-Expression      # PowerShell
theo env --format dotenv > .env.theo
theo env --json
```

Exports `THEO_PROJECT`, `THEO_WORKTREE`, `THEO_WORKTREE_PATH`,
`THEO_COMPOSE_PROJECT` and, for each service that publishes a TCP port,
`THEO_PORT_<SERVICE>` and `THEO_URL_<SERVICE>`; `THEO_URL` is the default
service's. Inside a project folder the project and worktree are inferred.
`theo env set|unset|list` still manage an instance's variables.

## Agent state

`working`, `waiting` (it needs you), `done` or unknown. It shows as `agent` in
`theo status --json`, `theo worktrees --json`, the API and MCP, and as the
warnings `agent-waiting` and `agent-done` in `status`.

- From the hooks above it is exact. A state older than 12 hours is not
  believed (a crashed session never sends its last event). After you approve a
  permission the state goes back to `working` with the next tool call.
- Without hooks, Theo looks for agent programs (`claude`, `codex`, `aider`,
  `gemini`, `goose`, `opencode`, `amp`, `cursor-agent`, `copilot`) whose
  working folder is the worktree and reports `working` with source `process`:
  an estimate, never a warning. Linux reads `/proc`. macOS asks `ps` and `lsof`
  for each agent's working folder. Windows gives no working folder of another
  process, so Theo reads the command lines through PowerShell and only finds an
  agent started with the worktree's path in its arguments; for any other, only
  hooks give a state. The macOS and Windows detection is covered by tests on
  sample output and has not been run on those systems.

## Open a worktree where you work: `theo open --in`

```sh
theo open shop -w feature-x --in editor      # Opened feature-x in editor (code)
theo open shop -w feature-x --in terminal    # Windows Terminal, Terminal.app or the system's terminal
theo open shop -w feature-x --in files       # explorer, open or xdg-open
theo config set open.editor "code -n"        # your own program (arguments allowed)
```

Without `--in`, `theo open` still opens a running service in the browser. If the
program is not found, Theo says how to set one (`theo config set open.editor
<command>`) and exits with 1; a worktree whose folder is gone is refused.
`theo config list|get|set|unset` handles `open.editor`, `open.terminal`,
`open.files` and `update-check`.

## Agents in the listings

`theo worktrees` has an `AGENT` column (`working`, `waiting`, `done`, `-`;
`working (estimated)` when it comes from a process). `theo` ends with
`Agents: 1 working, 1 waiting` and, for each agent waiting for you, the command to
open its worktree.

## New-version notice

Off by default. `theo config set update-check on` makes `theo` (the summary) ask
GitHub's releases API at most once a day, with a two-second limit, and print the
release link when a newer version exists. Nothing else is sent, a failure is
silent, and a development build never asks. `theo config set update-check off`
turns it off.

## More

- `--json` is available on `up`, `down`, `restart`, `stop`, `start`,
  `recreate`, `open`, `env set|unset` and `theo --json`. With it Compose's own
  output goes to stderr so stdout holds only the JSON document.
- `theo log` shows Theo's diagnostic log (`theo log --path` says where it is).
- `theo doctor` also warns about a Git older than 2.41 (slower listings),
  reports `.theo.json` problems and whether the hooks are installed.
