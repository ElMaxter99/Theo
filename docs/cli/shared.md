# Shared services

Nothing is shared unless you say so. By default every instance (worktree) runs all the services of the project with
its own containers, network and volumes. Sharing is a deliberate, visible and reversible choice for a few services,
typically a database that several branches should use at once.

## How it works

- A project declares which services are shared: `theo shared add <project> <service>...`.
- One dedicated Compose project, `<project>-shared`, runs them, using the Compose files of one worktree (`--from`,
  default the main worktree). It has its own lifecycle and its own volumes: stopping a branch never stops it.
- Theo creates one Docker network per project, `theo-<project>-shared`, labelled as Theo's. The shared services and
  the instances that use them join it. A service reaches the shared one by its service name, as before.
- An instance opts in with `theo shared use <project> -w <worktree> <service>...`. At its next `up` it **does not
  start its own copy** (the service gets a profile that is never enabled), services that depended on it lose only that
  dependency (their other dependencies stay), and they join the shared network. Your Compose files are not changed:
  everything goes into the generated file Theo already uses for instance settings.

```sh
theo shared add my-api db                 # declare; nothing runs yet
theo shared use my-api db                 # main worktree uses it
theo shared use my-api -w bug-1 db        # so does bug-1
theo up my-api --start-shared             # starts <project>-shared first, then the instance
theo up my-api -w bug-1
theo shared status my-api                 # state, image, who uses it
```

## Commands

| Command | What it does |
|---|---|
| `shared add <p> <service>... [--from <w>] [--publish]` | Declare services as shared. `--publish` keeps the host ports the project publishes; by default the shared services publish nothing on the host. |
| `shared up <p>` | Create the network if needed and start the shared services (and what they depend on). |
| `shared use <p> -w <w> <service>... [--join <svc>]` | Make an instance use them. `--join` adds services of the instance to the shared network besides the ones that depended on the shared service. |
| `shared unuse <p> -w <w> [<service>...]` | Run its own copy again at the next `up`. |
| `shared status <p> [--json]` | State and consumers. |
| `shared down <p> [--confirm <p>-shared]` | Stop them; volumes are kept. Refused while running instances use them, listing those instances; `--confirm` overrides. |
| `shared remove <p> [<service>...]` | Stop sharing. Refused while instances are linked or the shared containers exist. Removes the network when the last service goes. |
| `shared purge <p> --confirm <p>-shared` | Delete the shared services' data. Needs them stopped and no instance linked to them (`shared unuse` first); otherwise it lists the linked instances and changes nothing. |
| `up --start-shared` | Start the shared services if they are stopped. Without it, `up` refuses and says how to start them: Theo never starts them in silence. |

## Protections

- `shared down` and `shared remove` never proceed silently while instances use the services (FR-092).
- The volumes of shared services are never deleted as an effect of anything else: not by `theo down`, not by removing
  a consumer, not by `shared remove`. Only `shared purge --confirm`.
- The worktree whose files define the shared services cannot be removed until you stop sharing them.
- `theo reconcile` reports a shared set whose defining worktree is gone, and instances linked to services that are no
  longer shared. `theo up` of such an instance fails with the exact fix instead of starting without the dependency.
- `theo up` warns when the image your worktree defines for a shared service differs from the one running.

## Limits and risks

- Branches that run incompatible migrations against the same database will interfere. Share a database server only
  when that is acceptable; isolate it for destructive tests (Q-018: Theo warns, it does not isolate logical databases).
- The consumer reaches the shared service by its service name on the shared network. If the application hard-codes
  another host name or port, set it with `theo env set` (FR-080); Theo cannot guess which variables hold endpoints.
- Services of consumer instances that join the shared network are visible to each other by service name there.
  Only the services that need the shared one join.
- A shared service that depends on non-shared services starts them too, inside `<project>-shared`.
- A fixed `container_name` of a shared service is removed in the shared project, so it cannot collide with the same
  service of a worktree that still runs its own copy.
- `shared down --confirm` leaves consumers without the service; they fail or retry until it is back.
- Sharing needs Docker Compose 2.24.4 or later (`!override` / `!reset`).
