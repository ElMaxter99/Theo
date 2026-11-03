# `theo mcp`: Theo for AI agents (Model Context Protocol)

`theo mcp` serves Theo's operations to AI agents (Claude Code and any other
[MCP](https://modelcontextprotocol.io) client) on stdin and stdout. An agent
can then create a worktree for its task, start that worktree's own Compose
environment, read its URLs and logs, and stop it again, without colliding
with you or with other agents.

```
theo mcp [--read-only] [--owner <name>] [--resources]
```

| flag            | effect                                                                                  |
|-----------------|-----------------------------------------------------------------------------------------|
| `--read-only`   | only the tools that change nothing are listed and can be called                         |
| `--owner <name>`| owner label of the worktrees `add_worktree` creates when the call gives none            |
| `--resources`   | experimental: also serve `theo://status` resources with subscriptions (see below)        |

The client starts the process; you do not run it by hand. It reads JSON-RPC
2.0 messages, one per line, and answers on stdout. Nothing else is ever
written to stdout; the output of Git and Docker is captured and returned in
the tool results.

## Registering it

`theo mcp install` prints everything below for this machine, with the right
path to `theo`, and a paragraph for the repository's `AGENTS.md` or
`CLAUDE.md` that tells agents when and how to use the server:

```
theo mcp install [--owner <name>] [--read-only] [--scope local|user|project] [--name theo]
theo mcp install --apply           # runs the claude mcp add command for you
theo mcp install --snippet >> AGENTS.md
theo mcp install --json            # the command, the mcpServers entry and the paragraph
```

It writes nothing except through `--apply`, which runs `claude mcp add` (it
must be on `PATH`). It names the binary `theo` when that is the `theo` on
`PATH`, so the registration survives upgrades, and its absolute path
otherwise.

By hand, Claude Code:

```
claude mcp add theo -- theo mcp
claude mcp add theo-ro -- theo mcp --read-only          # a read-only one
claude mcp add theo -- theo mcp --owner claude          # label what it creates
```

Or in a project's `.mcp.json` (checked in, so the whole team gets it):

```json
{
  "mcpServers": {
    "theo": {
      "command": "theo",
      "args": ["mcp", "--owner", "claude"]
    }
  }
}
```

Use the full path of `theo` if it is not on the `PATH` the client sees. The
server uses the same data directory as the CLI (`THEO_HOME`, see
[project](project.md)), so it sees the projects you registered with
`theo project add`. Registering projects is left to you.

## Tools

Every tool that works on an instance takes `project` (a registered project)
and `worktree` (branch, directory name or path; empty means the main
worktree), the same selection as `-w` in the CLI.

| tool              | other arguments                                                                 | changes | notes |
|-------------------|---------------------------------------------------------------------------------|---------|-------|
| `list_projects`   | -                                                                               | no      | `project list --json` |
| `project_show`    | -                                                                               | no      | `project show --json` |
| `list_worktrees`  | `owner`                                                                         | no      | `worktrees --json [--owner]` |
| `list_branches`   | -                                                                               | no      | `branches --json` |
| `add_worktree`    | `branch`*, `new`, `from`, `path`, `owner`, `clone`, `copy`, `copy_files`        | yes     | `worktree add`; `owner` defaults to `--owner` |
| `remove_worktree` | `worktree`*, `discard_changes`, `confirm`                                       | yes, destructive | `worktree remove`, same refusals |
| `services`        | -                                                                               | no      | `services --json` |
| `ports`           | `profiles`, `auto_ports`                                                        | no      | `ports --json`, plus a `url` per port, and `proxy_urls` while a proxy runs |
| `up`              | `build`, `auto_ports`, `profiles`, `services`, `force_recreate`, `start_shared` | yes     | returns ports with URLs, `proxy_urls`, warnings and the end of Docker's output |
| `down`            | -                                                                               | yes, destructive | volumes are always kept |
| `restart`         | `services`                                                                      | yes     | |
| `stop`            | `services`                                                                      | yes     | nothing is removed |
| `start`           | `services`                                                                      | yes     | only existing containers |
| `ps`              | -                                                                               | no      | without `worktree`: every worktree |
| `logs`            | `services`, `tail`                                                              | no      | never follows; bounded (see Limits) |
| `status`          | `project` (optional)                                                            | no      | `status --json`, with `proxy_urls` while a proxy runs and the `compose-changed` warning |
| `wait_for`        | `until` (`running`, `stopped`, `changed`), `timeout_seconds` (default 60, at most 120) | no | waits for the instance instead of polling `status`; returns `reached`, `state`, `starting`, `failed`, `timed_out`. Waiting for `running` returns early, with `reached: false`, when a container exits with an error or is unhealthy |
| `proxy_routes`    | `project` (optional)                                                            | no      | `proxy routes --json`: every local domain, its target and whether it runs, and `proxy_running` |
| `env_list`        | -                                                                               | no      | secrets always masked |
| `env_set`         | `name`*, `value`*, `secret`, `services`                                         | yes     | applies at the next `up` |
| `env_unset`       | `name`*                                                                         | yes     | |
| `instance_show`   | -                                                                               | no      | `instance show --json` |
| `instance_config` | `profiles`, `auto_ports`                                                        | no      | secrets masked; reserves no port |
| `history`         | `worktree`, `limit`                                                             | no      | `history --json` |
| `reconcile`       | `project` (optional), `apply`                                                   | only with `apply` | `apply` forgets Theo's own stale records only |
| `volumes_list`    | -                                                                               | no      | `volumes --json` |
| `explain`         | `operation`* (`up`, `down`, `restart`, `stop`, `start`) and the arguments of `up` | no   | dry run, like `theo --explain` |

`*` marks required arguments.

Each result has a short text for the model and `structuredContent` with the
same JSON as the CLI's `--json` output. Where the CLI prints a list, it is
wrapped in an object (`{"projects": [...]}`, `{"worktrees": [...]}`,
`{"branches": [...]}`, `{"events": [...]}`, `{"volumes": [...]}`), because MCP
structured content is an object. The same JSON also comes as a second text
block, for clients that only read text.

Each tool carries MCP annotations: `readOnlyHint` for the ones that change
nothing; `destructiveHint` for `remove_worktree`, `down`, `env_unset` and
`reconcile` (with `apply`); `idempotentHint` where calling twice does no more
than once (`up`, `down`, `stop`, `start`, `env_set`, `env_unset`,
`reconcile`). `openWorldHint` is false everywhere: the tools only touch this
machine's Git and Docker. Clients use them to decide what to ask you before
running.

## Knowing when things change

* **Progress.** When a `tools/call` carries `_meta.progressToken` (Claude
  Code sends one), `up`, `down`, `restart`, `stop` and `start` send
  `notifications/progress` while Docker works: one at the start, then the
  latest line of Docker's output at most five times a second (secrets
  masked), and one at the end. `wait_for` sends the state it sees.
* **`wait_for`** is the way that works with every client: the agent calls it
  after `up` and gets an answer when the instance is ready, fails, or the
  time runs out.
* **Resources (experimental, `--resources`).** `theo://status` and
  `theo://status/<project>` hold the JSON of `theo status --json`. A client
  can subscribe to them; Theo then reads the state every 2 seconds and sends
  `notifications/resources/updated` when it changed. Checked on 2026-11-03:
  Claude Code 2.1 sends a progress token and lists resources but does not
  subscribe, so prefer `wait_for`.

## Safety model

* **Destructive data operations stay with you.** Deleting, restoring or
  cloning volume data (`volumes purge|restore|clone`), `shared purge`,
  `shared down` and `project remove` are not exposed at all, not even with
  confirmation. Neither are `exec` and `shell`. Use the CLI or the web
  interface for those.
* **The same protections as the CLI.** The tools call the engine, never the
  `theo` binary, so every check applies: `up` refuses a Compose project name
  another instance uses; `worktree remove` is refused while the instance has
  containers (or when Docker cannot be asked) and while there are
  uncommitted or untracked files. Those files are only discarded with
  `discard_changes: true` and `confirm` set to the worktree's directory name,
  exactly like `--discard-changes --confirm <dirname>`; the refusal tells the
  model to ask you first. Branches and volumes are never deleted. Theo's state
  files are written under the same cross-process lock as the CLI and the web
  interface.
* **One operation per instance at a time.** A second mutating call on an
  instance (or a second `add_worktree`/`remove_worktree` on a project) while
  one runs through the same server is refused with the code `busy`, not
  queued, so that the agent knows.
* **Secrets never leave.** `env_list` always masks secret values (there is
  no `show_secrets`), `env_set` never echoes the value, and the output of
  `instance_config`, `logs`, `up` and friends, and their errors, have the
  instance's secret values replaced with `********`.
* **`explain` changes nothing.** It runs with Theo's writes discarded and
  Docker and Git changes listed but not executed, like `theo --explain`.
  While it runs, other tool calls wait (explain mode is process-wide).
* `--read-only` removes every tool that changes something; `reconcile` stays
  as a report without `apply`.

## Errors

* Protocol errors are JSON-RPC errors: `-32700` parse error (also for a line
  over 4 MiB), `-32600` invalid request (batches are not supported),
  `-32601` unknown method, `-32602` invalid params (including an unknown
  tool, or a tool hidden by `--read-only`).
* Tool errors are results with `isError: true`, the message as text, and
  `structuredContent.error` with a `code` (`not_found`,
  `invalid_arguments`, `busy`, `confirmation_required`,
  `instance_has_containers`, `cancelled`, or `error`) and the `message`.
  A refused `remove_worktree` adds `changes` and `confirm`.
* A call cancelled with `notifications/cancelled` stops its Docker command
  and gets no answer.

## Limits

* `logs`: `tail` lines per container (default 200, at most 2000), and at
  most 64 KiB in all; the most recent lines are kept and `truncated` says
  so. It never follows.
* Docker output of `up`, `down`, `restart`, `stop`, `start`: the last 16 KiB
  (`output`, `output_truncated`).
* `instance_config`: the first 256 KiB.
* `history`: `limit` from 1 to 500 (default 20).
* Messages larger than 4 MiB are rejected.
* No `--env-file` or `--compose-file` overrides: the tools use the
  project's and the worktree's settings, as `theo up` does without flags.
* Tools, and with `--resources` the status resources; no prompts or
  sampling. The tool list does not change while the server runs.
* Supported protocol versions: `2025-06-18` (preferred), `2025-03-26`,
  `2024-11-05`. For any other, the server answers with `2025-06-18`.
