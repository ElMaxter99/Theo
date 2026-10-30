# Theo local HTTP API (v1)

This is the truth about what `theo serve` / `theo ui` answers. It follows the design contract
(`gui-contract.md`) and lists where the real shapes differ from it. Every example below was
captured from a running server (real Git, real Docker; `go test -run TestDumpExamples ./internal/api`
with `UB_DOCS_DIR` set regenerates them). Paths of the example machine are temporary directories.

- Base: `http://127.0.0.1:<port>/api/v1`. JSON, UTF-8, `snake_case`, RFC 3339 times. The interface
  (static files) is served from `/` without a session.
- Fields the server does not know yet may appear later: clients must ignore unknown fields.
- Empty lists are always `[]`, never `null`.
- See `docs/cli/ui.md` for how to start the server and what the security model is.

## 1. Security: how a client talks to the server

1. `theo serve --print-launch-url` prints one line: `{"url":"http://127.0.0.1:PORT/#n=<nonce>","port":PORT,"pid":N}`.
   The nonce lives in the URL fragment, works **once** and expires after **60 s**.
2. The page reads the nonce from `location.hash`, then
   `POST /api/v1/session` with `{"nonce": "..."}` (header `Content-Type: application/json`). The answer
   sets the cookie `theo_session` (`HttpOnly; SameSite=Strict; Path=/`, no `Domain`) and returns
   `{"csrf_token": "...", "api_version": "1"}`. Then it removes the fragment with `history.replaceState`.
   `GET /api/v1/session` returns the CSRF token again for a valid cookie (after a page reload).
3. Every other request sends the cookie (automatic). Every request that changes something
   (`POST`, `PUT`, `PATCH`, `DELETE`) must also send `X-Theo-Csrf: <csrf_token>` and
   `Content-Type: application/json` (even when it has no body).
4. The server checks, in this order: `Host` is exactly `127.0.0.1:<port>` (else 403 `forbidden_origin`);
   `Origin`, when present, is exactly `http://127.0.0.1:<port>` (else 403 `forbidden_origin`);
   `Sec-Fetch-Site`, when present, is `same-origin` (else 403); the session cookie (else 401 `unauthorized`);
   the CSRF header for changes (else 403 `csrf`).
5. No CORS headers are ever sent. Responses carry the CSP of the contract, `nosniff`,
   `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin`; API responses are `no-store`.
6. `POST /api/v1/launch` (header `X-Theo-Control: <secret from ui.lock>`, no cookie, no `Origin`) returns a
   new launch URL: it is how a second `theo ui` opens another tab on the running server. A browser
   session is not enough to call it.

```json
// HTTP 401 (error-unauthorized)
{
  "error": {
    "code": "unauthorized",
    "message": "no valid session; open the launch URL that `theo ui` printed"
  }
}
```

## 2. Errors

Every non-2xx answer is `{"error": {...}}` with: `code` (stable), `message` (the engine's own English text),
and optionally `hint`, `level` (2 or 3 for destructive actions), `resource`, `confirm`
(`expected`, `prompt`), `affected[]`, `preserved[]`, `original` (output of git/docker, at most 8 KiB),
`operation` (for `busy`), `request_id` (for `internal`).

| HTTP | `code` | Meaning |
|---|---|---|
| 400 | `invalid_request`, `invalid_name`, `unknown_field` | validation (unknown JSON fields are rejected) |
| 401 | `unauthorized` | no session, wrong control secret, nonce used or expired |
| 403 | `forbidden_origin`, `csrf` | Host/Origin/Sec-Fetch-Site or CSRF checks |
| 404 | `project_not_found`, `worktree_not_found`, `operation_not_found`, `env_not_found`, `not_found` | |
| 409 | `busy`, `confirmation_required`, `instance_has_containers`, `volume_in_use`, `project_has_containers`, `branch_checked_out`, `port_conflict`, `already_exists`, `cancelled`, `shared_in_use` | protections of the engine |
| 412 | `docker_unavailable`, `git_unavailable`, `compose_too_old`, `git_too_old` | |
| 422 | `compose_invalid`, `engine_error` | the engine said no; `message` is its text; `original` has the rest |
| 500 | `internal` | with `request_id`; details only in the local log |

Mapping engine errors to codes is one table in `internal/api/errors.go`; a test fails when the engine
gains an error type that is not in it. The engine's typed errors also have an optional `Code()`.

## 3. Identifiers

Projects are addressed by their registered name. A worktree (= its single instance) is `wt_<10 hex>`:
the first 10 hex digits of the SHA-256 of its canonical absolute path (symlinks resolved; lower-cased on Windows).
Ids are computed from `git worktree list` on every request; nothing is stored. Paths never appear in URLs.

## 4. System

### `GET /meta`
`docker`, `compose` and `git` say whether the tool works and its version. If Docker is down the rest of the
API still serves the registry (NFR-014). Cached for 10 s.
```json
// HTTP 200 (meta)
{
  "theo_version": "0.0.0-dev",
  "api_version": "1",
  "os": "linux",
  "arch": "amd64",
  "docker": {
    "available": true,
    "version": "29.9.2"
  },
  "compose": {
    "available": true,
    "version": "5.6.1"
  },
  "git": {
    "available": true,
    "version": "2.43.0"
  },
  "data_dir": "/tmp/TestDumpExamples3241964665/003",
  "idle": {
    "timeout_seconds": 0
  }
}
```

### `GET /doctor`
Same JSON as `theo doctor --json`.

### `GET /events` (SSE)
Global channel. First event `hello` (`{"resync":true}`: read the current state again); then
`operation.created`, `operation.updated`, `operation.finished` (the operation object) and
`state.changed` (`{"project":"...","worktree":"wt_..."}`). Comment lines (`: keep-alive`) every 20 s.
Events carry an `id` but are not replayed: after a reconnect the client refetches.
The server also polls Docker every 5 s for changes made outside Theo, **only** while a client is connected
and a tab said it is visible: `POST /events/focus` with `{"visible": true|false}` (204).

### `GET /fs/browse?path=&hidden=1`
Directory picker for "add project" (decision for Q-027: server side listing, directories only).
Never returns file names or contents; default path is the user's home; any absolute path may be asked
explicitly; hidden directories are skipped unless `hidden=1`; at most 1000 entries (`truncated`);
`is_repo` is true when the directory has a `.git`.
```json
// HTTP 200 (browse)
{
  "entries": [
    {
      "name": "app",
      "path": "/tmp/TestDumpExamples3241964665/001/app",
      "is_repo": true
    },
    {
      "name": "app-feature",
      "path": "/tmp/TestDumpExamples3241964665/001/app-feature",
      "is_repo": true
    }
  ],
  "home": "/root",
  "is_repo": false,
  "parent": "/tmp/TestDumpExamples3241964665",
  "path": "/tmp/TestDumpExamples3241964665/001",
  "truncated": false
}
```

## 5. Projects

### `GET /projects`
Same JSON as `theo project list --json` (`name`, `path`, `env_file?`, `worktree_dir?`, `added_at`, ...).
```json
// HTTP 200 (projects)
[
  {
    "name": "ubd203455p",
    "path": "/tmp/TestDumpExamples3241964665/001/app",
    "added_at": "2026-10-30T06:53:03Z"
  }
]
```

### `POST /projects` `{path, name?, env_file?}` -> 201 (the project)
Errors: `already_exists` (409), engine text for "not a repository" (422).

### `PATCH /projects/{p}` `{env_file?: string|null, worktree_dir?: string|null}` -> 200 (the project)
`null` clears the setting; an absent key leaves it alone.

### `DELETE /projects/{p}[?forget_records=1]` -> 200
Only unregisters: `{"removed":true,"files_kept":true,"containers_kept":true,"records_dropped":false,"resources":{...}}`.
Refused with `project_has_containers` (409, `affected` lists the Compose projects) while its instances have
containers. There is no `yes`/force parameter.

### `GET /projects/{p}/overview[?refresh=1]`
One call for the panel. For every worktree: the Git data, the instance `state`
(`running`, `partial`, `unhealthy`, `stopped`, `absent`, `unknown`, from `engine.InstanceState`),
`containers {running,total}`, `ports[]` (with `url`, `status` of `theo ports`), `warnings[]`
(for instance `port-conflict`) and `busy` (`null` or `{operation_id, kind}`).
It runs `docker compose ps` once per worktree (in parallel) and never `docker compose ls`; the answer is cached
2 s per project. With Docker down, `docker.available` is false and states are `unknown`.
```json
// HTTP 200 (overview)
{
  "project": "ubd203455p",
  "path": "/tmp/TestDumpExamples3241964665/001/app",
  "docker": {
    "available": true
  },
  "worktrees": [
    {
      "id": "wt_e4a6ec17b2",
      "path": "/tmp/TestDumpExamples3241964665/001/app",
      "branch": "main",
      "head": "a7bdb1c515044a574276429390caa272e5684b7d",
      "detached": false,
      "locked": false,
      "prunable": false,
      "bare": false,
      "main": true,
      "kind": "main",
      "dirty": false,
      "compose_project": "ubd203455",
      "env_file": {
        "source": "none"
      },
      "state": "absent",
      "containers": {
        "running": 0,
        "total": 0
      },
      "ports": [
        {
          "service": "web",
          "container_port": 8080,
          "protocol": "tcp",
          "host_port": 25266,
          "url": "http://localhost:25266",
          "status": "in use"
        }
      ],
      "warnings": [
        {
          "kind": "port-conflict",
          "message": "host port 25266 (web) is already in use"
        }
      ],
      "busy": null
    },
    {
      "id": "wt_b98e6a680f",
      "path": "/tmp/TestDumpExamples3241964665/001/app-feature",
      "branch": "feature/x",
      "head": "a7bdb1c515044a574276429390caa272e5684b7d",
      "detached": false,
      "locked": false,
      "prunable": false,
      "bare": false,
      "main": false,
      "kind": "worktree",
      "dirty": false,
      "compose_project": "ubd203455p-app-feature",
      "env_file": {
        "source": "none"
      },
      "state": "running",
      "containers": {
        "running": 1,
        "total": 1
      },
      "ports": [
        {
          "service": "web",
  ...
```

### `GET /projects/{p}/branches`
Same JSON as `theo branches <p> --json`.

## 6. Worktrees and instances

An instance is the worktree (one per worktree today).

### `GET /projects/{p}/worktrees`
The `worktrees` array of the overview.

### `GET /projects/{p}/worktrees/{wt}`
The overview item plus `settings`, `containers[]` (raw `docker compose ps`, same as `theo ps -w --json`
`containers`), `expected_services[]` and `container_counts` (in the detail, `containers` is the array).
```json
// HTTP 200 (worktree)
{
  "id": "wt_b98e6a680f",
  "path": "/tmp/TestDumpExamples3241964665/001/app-feature",
  "branch": "feature/x",
  "head": "a7bdb1c515044a574276429390caa272e5684b7d",
  "detached": false,
  "locked": false,
  "prunable": false,
  "bare": false,
  "main": false,
  "kind": "worktree",
  "dirty": false,
  "compose_project": "ubd203455p-app-feature",
  "env_file": {
    "source": "none"
  },
  "state": "running",
  "ports": [
    {
      "service": "web",
      "container_port": 8080,
      "protocol": "tcp",
      "host_port": 25266,
      "url": "http://localhost:25266",
      "status": "running"
    }
  ],
  "warnings": [],
  "busy": null,
  "settings": {
    "neutralize_container_names": false,
    "auto_ports": true,
    "profiles": [],
    "shared": [],
    "join": [],
    "fixed_container_names": {}
  },
  "containers": [
    {
      "Name": "ubd203455p-app-feature-web-1",
      "Service": "web",
      "State": "running",
      "Health": "",
      "Status": "Up Less than a second",
      "ExitCode": 0,
      "Publishers": [
        {
          "URL": "0.0.0.0",
          "TargetPort": 8080,
          "PublishedPort": 25266,
          "Protocol": "tcp"
        }
      ]
    }
  ],
  "expected_services": [
    "web"
  ],
  "container_counts": {
    "running": 1,
    "total": 1
  }
}
```

### `POST /projects/{p}/worktrees` `{branch, new?, from?, path?, copy?}` -> 201
Creates the worktree synchronously (`theo worktree add`). `notes[]` are things to show (for example a missing `.env`).
Errors: `branch_checked_out` (409; the wizard disables such branches using `GET .../branches`, whose `worktree` is
set when a worktree has the branch).
```json
// HTTP 201 (worktree-create)
{
  "id": "wt_b7b7704df2",
  "path": "/tmp/TestDumpExamples3241964665/004/ub-demo",
  "branch": "ub-demo",
  "kind": "worktree",
  "env_file": {
    "source": "none"
  },
  "notes": []
}
```
```json
// HTTP 409 (worktree-create-conflict)
{
  "error": {
    "code": "branch_checked_out",
    "message": "branch \"ub-demo\" is already checked out in /tmp/TestDumpExamples3241964665/004/ub-demo (git allows a branch in one worktree only). Use that worktree: `theo services ubd203455p -w ub-demo`",
    "resource": {
      "kind": "project",
      "project": "ubd203455p"
    }
  }
}
```

### `POST /projects/{p}/worktrees/{wt}/remove` `{confirm?, discard_changes?}`
Two steps without server state. Without `confirm`, a worktree with uncommitted files answers 409
`confirmation_required` (level 3) with the files in `affected` and the directory name in `confirm.expected`.
Send the same request with `{"confirm": "<expected>"}` to remove it; the server asks the engine again and
refuses a wrong text. A worktree with containers answers 409 `instance_has_containers` (stop it first). The main worktree
and the branch are never removed. There is no generic `force`.
```json
// HTTP 409 (worktree-remove-confirm)
{
  "error": {
    "code": "confirmation_required",
    "message": "worktree /tmp/TestDumpExamples3241964665/004/ub-demo has 1 uncommitted or untracked file(s); nothing was removed",
    "level": 3,
    "resource": {
      "kind": "instance",
      "project": "ubd203455p",
      "worktree": "wt_b7b7704df2"
    },
    "confirm": {
      "expected": "ub-demo",
      "prompt": "Type the directory name to discard these changes"
    },
    "affected": [
      {
        "kind": "file",
        "name": "notes.txt",
        "detail": "??"
      }
    ],
    "preserved": [
      "the branch",
      "the other worktrees",
      "all volumes"
    ]
  }
}
```
```json
// HTTP 200 (worktree-remove)
{
  "branch": "ub-demo",
  "kind": "worktree",
  "path": "/tmp/TestDumpExamples3241964665/004/ub-demo",
  "preserved": [
    "the branch",
    "all volumes",
    "the other worktrees"
  ],
  "removed": true
}
```

### Lifecycle operations
`POST .../{wt}/up` `{build?, profiles?, auto_ports?, env_files?, start_shared?}`, `POST .../{wt}/down`,
`POST .../{wt}/restart` -> **202** with the operation and `Location: /api/v1/operations/{id}`.
`up` does what `theo up` does (shared services, ports with optional `auto_ports` that is remembered by the
instance, generated overlay, `docker compose up --detach`). Send `Idempotency-Key: <uuid>`: the same key from the
same session within 10 minutes returns the same operation. A second change on the same instance answers 409 `busy`
with the running operation; different instances run in parallel, at most 4 at once (the others are `queued`).
```json
// HTTP 202 (worktree-up)
{
  "id": "op_sHsRzmKZZHY7cBAy",
  "kind": "up",
  "project": "ubd203455p",
  "worktree": "wt_b98e6a680f",
  "state": "queued",
  "started_at": "2026-10-30T06:53:03.485794164Z",
  "cancellable": true
}
```
```json
// HTTP 409 (worktree-up-busy)
{
  "error": {
    "code": "busy",
    "message": "another operation is running on this instance",
    "hint": "wait for it or cancel it",
    "operation": {
      "id": "op_sHsRzmKZZHY7cBAy",
      "kind": "up"
    }
  }
}
```

### `GET .../{wt}/services`, `GET .../{wt}/ports?profile=&auto_ports=1`, `GET .../{wt}/config`
`services` and `ports` return the same JSON as `theo services|ports -w ... --json` (`ports` adds the instance fields).
`auto_ports=1` is a preview: it reserves nothing. For the new-worktree wizard (before the worktree exists) preview
the ports of the main worktree. `config` is the resolved Compose YAML as `text/plain`, secrets masked.
```json
// HTTP 200 (ports-auto)
{
  "project": "ubd203455p",
  "workspace": "/tmp/TestDumpExamples3241964665/001/app-feature",
  "branch": "feature/x",
  "compose_project": "ubd203455p-app-feature",
  "main": false,
  "kind": "worktree",
  "env_file": {
    "source": "none"
  },
  "ports": [
    {
      "service": "web",
      "target": 8080,
      "protocol": "tcp",
      "configured": "25266",
      "effective": 25266,
      "running": 25266,
      "status": "running"
    }
  ]
}
```

### Environment variables
`GET .../{wt}/env` -> array sorted by name `[{name, value, secret, services[]}]`. Secret values are **always**
`********` (`engine.Mask`) in lists (difference from the CLI, which prints a map; the contract test compares the data).
`PUT .../{wt}/env/{name}` `{value, secret?, services?}` -> `{"name","applies_on":"next_up"}`;
`DELETE .../{wt}/env/{name}` (404 `env_not_found`);
`POST .../{wt}/env/{name}/reveal` -> `{name, value, secret}` for that ONE variable, `Cache-Control: no-store`.
Bodies are never logged. Hypothesis not implemented: noting `reveal` in the history (the engine has no public way).
```json
// HTTP 200 (env)
[
  {
    "name": "DB_URL",
    "value": "********",
    "secret": true,
    "services": []
  },
  {
    "name": "LOG_LEVEL",
    "value": "debug",
    "secret": false,
    "services": [
      "web"
    ]
  }
]
```
```json
// HTTP 200 (env-reveal)
{
  "name": "DB_URL",
  "secret": true,
  "value": "postgres://user:hunter2@db/app"
}
```

### Settings
`GET|PATCH .../{wt}/settings` with `{neutralize_container_names?, auto_ports?}`.
```json
// HTTP 200 (settings)
{
  "neutralize_container_names": false,
  "auto_ports": true,
  "profiles": [],
  "shared": [],
  "join": [],
  "fixed_container_names": {}
}
```

### Volumes
`GET .../{wt}/volumes` same JSON as `theo volumes --json`.
`POST .../{wt}/volumes/purge` `{confirm?}`: level 3. Without the right text: 409 `confirmation_required` with
the volumes in `affected` and the Compose project name in `confirm.expected`; with containers: 409
`instance_has_containers`; on success `{"compose_project","deleted":[...]}`.
```json
// HTTP 200 (volumes)
[
  {
    "name": "ubd203455p-app-feature_data",
    "volume": "data",
    "status": "kept",
    "created": true
  }
]
```
```json
// HTTP 409 (volumes-purge-confirm)
{
  "error": {
    "code": "confirmation_required",
    "message": "this deletes the data of 1 volume(s) of ubd203455p-app-feature for good; nothing was deleted",
    "level": 3,
    "resource": {
      "kind": "instance",
      "project": "ubd203455p",
      "worktree": "wt_b98e6a680f"
    },
    "confirm": {
      "expected": "ubd203455p-app-feature",
      "prompt": "Type the instance name to delete its data"
    },
    "affected": [
      {
        "kind": "volume",
        "name": "ubd203455p-app-feature_data",
        "detail": "kept"
      }
    ],
    "preserved": [
      "external volumes",
      "other instances",
      "the branch and the worktree"
    ]
  }
}
```

### `GET .../{wt}/history?limit=`
Same JSON as `theo history --json` (oldest first). The engine records `up`, `down`, `restart` and `volumes purge`
exactly as the CLI does; the API keeps no history of its own.
```json
// HTTP 200 (history)
[
  {
    "time": "2026-10-30T06:53:04.53006155Z",
    "project": "ubd203455p",
    "workspace": "/tmp/TestDumpExamples3241964665/001/app-feature",
    "compose_project": "ubd203455p-app-feature",
    "operation": "up",
    "result": "ok"
  }
]
```

## 7. Operations

`GET /operations[?state=running]` (live ones and the last 50, newest first), `GET /operations/{id}`,
`POST /operations/{id}/cancel` (202; the final state arrives by SSE; cancelling does not undo anything:
an interrupted `up` can leave the instance `partial`).
`state`: `queued`, `running`, `cancelling`, `succeeded`, `failed`, `cancelled`. A failed operation carries
`error` with the code (for example `port_conflict`) and, in `original`, the last lines Docker printed.
```json
// HTTP 200 (operation)
{
  "id": "op_sHsRzmKZZHY7cBAy",
  "kind": "up",
  "project": "ubd203455p",
  "worktree": "wt_b98e6a680f",
  "state": "succeeded",
  "started_at": "2026-10-30T06:53:03.485794164Z",
  "finished_at": "2026-10-30T06:53:04.530175902Z",
  "result": {
    "ok": true,
    "summary": ""
  },
  "cancellable": false
}
```

### `GET /operations/{id}/events` (SSE)
Events (each with an incremental `id`): `state` `{state, at}`, `log` `{stream, line, at, truncated?}` (one line per event,
lines over 64 KiB are cut), `result` `{ok, summary, error?}` (once), `gap` `{dropped}`. The ring buffer keeps the last
5000 events; reconnect with `Last-Event-ID` to get only what came after; if it was discarded a `gap` comes first.
The stream ends after the final `state` event: **the client must close its EventSource when it sees `result`**
(otherwise the browser reconnects every few seconds). Operations survive the browser tab and the server never
idles out while one runs.
```
id: 1
event: state
data: {"at":"2026-10-30T06:53:03.485947759Z","state":"running"}

id: 2
event: log
data: {"at":"2026-10-30T06:53:03.725741712Z","line":" Image ubd203455p-app-feature-web Building ","stream":"stderr"}

id: 3
event: log
data: {"at":"2026-10-30T06:53:03.769988892Z","line":"#1 [internal] load local bake definitions","stream":"stdout"}

id: 4
event: log
data: {"at":"2026-10-30T06:53:03.872826518Z","line":"#1 reading from stdin 564B done","stream":"stdout"}

id: 5
event: log
data: {"at":"2026-10-30T06:53:03.872853646Z","line":"#1 DONE 0.0s","stream":"stdout"}

id: 6
event: log
data: {"at":"2026-10-30T06:53:03.872874494Z","line":"#2 [internal] load build definition from Dockerfile","stream":"stdout"}

id: 7
event: log
data: {"at":"2026-10-30T06:53:03.872877792Z","line":"#2 transferring dockerfile: 93B done","stream":"stdout"}

id: 34
event: log
data: {"at":"2026-10-30T06:53:04.28500762Z","line":" Volume ubd203455p-app-feature_data Created ","stream":"stderr"}

id: 35
event: log
data: {"at":"2026-10-30T06:53:04.285018713Z","line":" Volume ubd203455p-app-feature_data Created ","stream":"stderr"}

id: 36
event: log
data: {"at":"2026-10-30T06:53:04.311205507Z","line":" Network ubd203455p-app-feature_default Created ","stream":"stderr"}

id: 37
event: log
data: {"at":"2026-10-30T06:53:04.311218105Z","line":" Network ubd203455p-app-feature_default Created ","stream":"stderr"}

id: 39
event: log
data: {"at":"2026-10-30T06:53:04.395075024Z","line":" Container ubd203455p-app-feature-web-1 Created ","stream":"stderr"}

id: 41
event: log
data: {"at":"2026-10-30T06:53:04.527068749Z","line":" Container ubd203455p-app-feature-web-1 Started ","stream":"stderr"}

id: 42
event: log
data: {"at":"2026-10-30T06:53:04.530167756Z","line":"Started ubd203455p-app-feature.","stream":"stdout"}

id: 43
event: result
data: {"ok":true,"summary":""}

id: 44
event: state
data: {"at":"2026-10-30T06:53:04.530179268Z","state":"succeeded"}

```

## 8. Logs

`GET .../{wt}/logs?follow=1&tail=200&service=web&service=db` -> SSE, one `docker compose logs` process per stream,
killed when the client leaves (tested). Events: `log` `{service, stream, line, at, truncated?}` (`at` is when the
server received the line), `gap` `{dropped}` when the client does not keep up (lines are dropped, never buffered
without limit), `end` `{ok, error?}` when the process ends. `tail=all` for everything.
Without `follow=1` it is a text download (`Content-Disposition: attachment`).
```
id: 1
event: log
data: {"at":"2026-10-30T06:53:06.295378551Z","line":"server ready","service":"web","stream":"stdout"}

```

## 9. Differences from the design contract

- `GET /projects/{p}/worktrees` items have `containers` as `{running,total}`; the detail uses `containers` for the
  raw array and `container_counts` for the numbers.
- Secrets are masked as `********` (the CLI's mask), not `••••••`.
- `env` is an array; the CLI's `env list --json` is a map.
- `GET /session` was added; `POST /events/focus` as in the contract.
- `reconcile`, `shared`, `doctor` screens (V5, V6) are not built; `GET /doctor` exists because it is one call.
- Logs do not carry Docker's own timestamps (`LogOptions` has no `--timestamps`); `at` is the receive time.
- Idle shutdown counts SSE streams (global events, operation events, logs) and operations.
