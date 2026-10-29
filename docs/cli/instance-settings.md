# Instance settings and history

Settings that belong to one instance (one worktree) never touch your Compose files or `.env`. Theo generates a Compose file and an env file for the instance, outside the project and readable only by your user, and runs the instance with them. Theo prints `using generated file ...` when it does.

## Variables

```sh
theo env set my-api -w feature/x LOG_LEVEL=debug [--service api]
theo env set my-api -w feature/x API_TOKEN=... --secret
theo env list my-api -w feature/x [--show-secrets] [--json]
theo env unset my-api -w feature/x LOG_LEVEL
```

Variables apply the next time the instance starts (`theo up`). Without `--service` they apply to every service. Secrets are masked (`********`) in Theo's output.

Limits: secrets are stored in plain text in `instances.json` and in the generated files, which are readable only by your user. They are not encrypted. Passing a secret on the command line leaves it in your shell history.

## Fixed container names

A fixed `container_name` stops a second instance from starting, and `up` warns about it. To start the instance without those names, for this instance only:

```sh
theo instance set my-api -w feature/x --neutralize-container-names
theo instance set my-api -w feature/x --keep-container-names   # undo
```

## Inspect

```sh
theo instance show my-api -w feature/x     # env file, variables, container names
theo instance config my-api -w feature/x   # what it will run with; secrets masked
```

## History

```sh
theo history my-api [-w feature/x] [--limit 20] [--json]
```

Lists `up`, `down` and `restart` with time, result and user. Failed operations show the error.
