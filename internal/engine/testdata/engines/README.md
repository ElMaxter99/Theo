# Engine samples

Hand-written samples of `docker info --format '{{json .}}'` for the engine
detection tests (`dockerengine_test.go`). They follow the shape each engine
prints but were **not** captured from real installs, except that the field
names and the Docker Engine sample match Docker 29 on Linux.

Fields the detection relies on (everything else is ignored and only makes the
samples realistic):

| Field | Used for |
|---|---|
| `ServerVersion`, `OSType` | the daemon answered; its version; Linux or Windows containers |
| `OperatingSystem` | `Docker Desktop`, `OrbStack`, `Rancher Desktop WSL Distribution` |
| `Name` | `docker-desktop`, `orbstack`, `colima`/`colima-<profile>`, `lima-rancher-desktop` |
| `Labels` | `com.docker.desktop.*` (Docker Desktop) |
| `DockerRootDir` | `…/containers/storage` is Podman's storage (a Podman socket behind `/var/run/docker.sock`) |
| `SecurityOptions` | `name=rootless` |
| `ServerErrors` | why the daemon did not answer (old CLIs exit 0) |
| `ClientInfo.Context` | the CLI context, when `docker context inspect` is not available |
| `host.os`, `host.security.rootless`, `version.Version` | `podman-native.json`: Podman's own `podman info`, printed when `docker` is the podman-docker wrapper |

The tests add what the doctor learns from `docker version --format '{{json
.Server}}'` (`Platform.Name`, `Components[].Name`, e.g. `Podman Engine`),
`docker context inspect` (`Name`, `Endpoints.docker.Host`) and the
`DOCKER_HOST` / `DOCKER_CONTEXT` variables.
