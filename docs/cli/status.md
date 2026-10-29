# `theo status`: one dashboard

```
theo status                 # every registered project
theo status shop            # one project
theo status shop --json     # the same data for the GUI and for scripts
```

For each project and each of its worktrees: branch, kind, whether the tree has
uncommitted changes, the state of its Compose instance, the services by
state, the published ports with their URLs, the settings Theo keeps, and a list
of warnings. It only reads: Git, Docker and Theo's own records. A worktree
that cannot be read (a missing env file, a broken Compose file) shows up with
state `unknown` and a warning; it does not fail the command.

```
shop  /work/shop
  BRANCH     KIND    GIT    INSTANCE     SERVICES                PORTS                        SETTINGS
  main       main    clean  partial      1/2 running, 1 stopped  http://localhost:8080        1 env var(s); profiles: reports
  feature/x  linked  dirty  not created  0/2 running             http://localhost:8080 (free)  profiles: reports
  ! feature/x: host port 8080 (api, container port 80/tcp) is already in use [port-conflict]
      -> free it, or start with `theo up shop -w feature/x --auto-ports`
```

## JSON schema

`theo status --json` prints one object. Lists are always arrays, never `null`.
Fields marked *optional* are left out when they do not apply.

```jsonc
{
  "projects": [
    {
      "name": "shop",
      "path": "/work/shop",
      "error": "…",                       // optional: the project could not be read (path gone, not a repo)
      "worktrees": [
        {
          "path": "/work/shop",
          "branch": "main",               // optional: absent when detached or bare
          "head": "66092c0d…",            // optional
          "kind": "main",                 // "main" | "linked" | "bare"
          "detached": false,              // optional
          "dirty": false,                 // uncommitted or untracked files
          "locked": false,                // optional
          "missing": false,               // optional: Git lists it but the directory is gone
          "env_file": {                   // optional: absent for bare/missing worktrees
            "path": "/work/shop/.env",    //   optional: absent when "source" is "none"
            "source": "worktree"          //   "flag" | "worktree" | "project" | "main worktree" | "none"
          },
          "instance": {                   // optional: absent for bare/missing worktrees
            "compose_project": "shop",    //   empty when the configuration could not be resolved
            "state": "partial",           //   "not created" | "running" | "stopped" | "partial" | "unknown"
            "counts": {                   //   services by state
              "running": 1, "stopped": 1, "completed": 0, "not_created": 0, "total": 2
            },
            "services": [
              {
                "name": "api",
                "state": "running",       //   "running" | "stopped" | "completed" | "partial" | "not created"
                "containers": 1,          //   containers of the service (more than 1 when scaled)
                "running": 1,
                "health": "healthy",      //   optional: Compose health of a running container
                "profiles": ["reports"]   //   optional
              }
            ],
            "ports": [
              {
                "service": "api",
                "container_port": 80,
                "protocol": "tcp",
                "host_port": 8080,        //   optional: absent when Docker picks the port at start
                "url": "http://localhost:8080", // optional: TCP ports with a known host port
                "status": "running"       //   as in `theo ports`: free | running | in use | kept | reassigned | random | range
              }
            ]
          },
          "settings": {                   // optional: same condition as "instance"
            "env_vars": 1,                //   variables set with `theo env set`
            "secret_env_vars": 1,         //   how many of them are secret (values are never shown)
            "neutralize_container_names": false,
            "fixed_container_names": { "api": "shop-api" }, // optional: services that set container_name
            "profiles": ["reports"]       //   Compose profiles the project declares
          },
          "warnings": [
            { "kind": "port-conflict", "message": "…", "fix": "…" }  // "fix" is optional
          ]
        }
      ],
      "warnings": []                      // about the project as a whole (same shape)
    }
  ],
  "docker_error": "…",                    // optional: Docker could not be asked; states are "unknown"
  "warnings": []                          // records that belong to no registered project
}
```

### States

* **instance.state**: `not created` (no containers), `running` (every
  service runs; a service that finished with exit code 0, such as a migration
  job, does not count against it), `stopped` (containers exist and none
  runs), `partial` (some run and some do not, or some services were never
  created, for example after `theo up --service api`), `unknown` (Docker or
  the configuration could not be read).
* **services** lists the services without a profile and every service that has
  containers. `counts.total` is the length of that list. A service with
  several containers is `running` only when all run, `partial` when some do.
* `instance.ports` covers the services without a profile. A stopped instance
  lists the ports it would publish, with `status` `free` or `in use`.

### Warning kinds

`kind` is a stable identifier; `message` is for humans.

| kind | meaning |
|---|---|
| `port-conflict` | a host port the instance publishes is taken by something that is not this instance |
| `foreign-stack` | a Compose project runs from this worktree under a name Theo does not use (from `reconcile`) |
| `missing-env-file` | the project's env file no longer exists, or no `.env` is found and Compose needs one |
| `config-error` | `docker compose config` fails for this worktree (first line of the error) |
| `fixed-container-name` | a linked worktree's services set `container_name`, so a second instance collides |
| `docker-unavailable` | Docker could not be asked |
| `project missing`, `worktree missing`, `containers removed`, `orphan containers`, `orphan volumes`, `stale settings`, `stale ports`, `stale files` | findings of `theo reconcile`, reused as they are (`fix` carries its advice) |

## Notes for consumers

* `status` runs `docker compose config` and `ps` once per worktree, so with
  many worktrees it takes a moment; the cost is about 0.3 s per worktree.
* New fields may be added; existing ones keep their meaning. Ignore keys you do
  not know.
* Secret values are never in the output, only counts.
* `profiles` is what the project declares, not what is active: Compose does not
  keep which profiles an instance was started with.
