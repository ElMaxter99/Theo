package engine

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/ElMaxter99/Theo/internal/execx"
)

// The container engines the doctor tells apart. Theo only ever runs the
// `docker` CLI (or THEO_DOCKER); the engine is whatever answers that CLI.
const (
	EngineDockerEngine   = "docker-engine"
	EngineDockerDesktop  = "docker-desktop"
	EngineColima         = "colima"
	EngineOrbStack       = "orbstack"
	EngineRancherDesktop = "rancher-desktop"
	EnginePodman         = "podman"
	EngineUnknown        = "unknown"
)

// Support levels of an engine, as in docs/cli/engines.md.
const (
	SupportVerified    = "verified"    // Theo's Docker tests run against it
	SupportExpected    = "expected"    // implements the Docker API Theo uses; not tested
	SupportUnsupported = "unsupported" // known not to work
)

// ContainerEngine is what the doctor found behind the docker CLI. It is the
// "engine" field of `theo doctor --json`.
type ContainerEngine struct {
	Kind    string `json:"kind"` // one of the Engine* constants
	Name    string `json:"name"` // for people: "Docker Desktop", "Podman"...
	Version string `json:"version,omitempty"`
	// OperatingSystem is the daemon's own description of where it runs
	// (`docker info` OperatingSystem).
	OperatingSystem string `json:"operating_system,omitempty"`
	// Context is the docker CLI context in use; Endpoint is the address it
	// talks to (unix://, npipe://, tcp://, ssh://).
	Context  string `json:"context,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	// EndpointFrom says what chose the endpoint: "DOCKER_HOST",
	// "DOCKER_CONTEXT" or "context" (the CLI's current context).
	EndpointFrom string `json:"endpoint_from,omitempty"`
	// CLI is THEO_DOCKER when it is set.
	CLI      string   `json:"cli,omitempty"`
	Rootless bool     `json:"rootless,omitempty"`
	Remote   bool     `json:"remote,omitempty"` // the endpoint is another machine
	Support  string   `json:"support"`
	Notes    []string `json:"notes,omitempty"`
}

// dockerInfo is the part of `docker info --format '{{json .}}'` the doctor
// reads. Every field here is one detection relies on; the rest of the
// document is ignored, so engines that leave fields out still parse.
type dockerInfo struct {
	ServerVersion   string   `json:"ServerVersion"`
	OSType          string   `json:"OSType"`
	OperatingSystem string   `json:"OperatingSystem"`
	Name            string   `json:"Name"`
	DockerRootDir   string   `json:"DockerRootDir"`
	Labels          []string `json:"Labels"`
	SecurityOptions []string `json:"SecurityOptions"`
	ServerErrors    []string `json:"ServerErrors"`
	ClientInfo      *struct {
		Context string `json:"Context"`
	} `json:"ClientInfo"`

	// When `docker` is Podman's podman-docker wrapper, the same command
	// prints Podman's own `podman info` document instead, which has these.
	PodmanHost *struct {
		OS       string `json:"os"`
		Security struct {
			Rootless bool `json:"rootless"`
		} `json:"security"`
	} `json:"host"`
	PodmanVersion *struct {
		Version string `json:"Version"`
	} `json:"version"`
}

// dockerServer is the part of `docker version --format '{{json .Server}}'`
// the doctor reads.
type dockerServer struct {
	Platform struct {
		Name string `json:"Name"`
	} `json:"Platform"`
	Components []struct {
		Name string `json:"Name"`
	} `json:"Components"`
}

// dockerContext is the part of `docker context inspect` the doctor reads.
type dockerContext struct {
	Name      string `json:"Name"`
	Endpoints struct {
		Docker struct {
			Host string `json:"Host"`
		} `json:"docker"`
	} `json:"Endpoints"`
}

// engineProbe is everything the doctor learned from the docker CLI about the
// engine. The detection itself (detectEngine) is a pure function of it.
type engineProbe struct {
	CLIVersion string // `docker --version`
	CLI        string // THEO_DOCKER

	Info    dockerInfo
	InfoOK  bool   // the daemon answered
	InfoErr string // why not, when it did not
	Version string // daemon version
	OSType  string // linux or windows

	ServerPlatform   string
	ServerComponents []string

	Context       string
	Endpoint      string
	DockerHost    string // the DOCKER_HOST variable
	DockerContext string // the DOCKER_CONTEXT variable
}

// probeEngine asks the docker CLI about the daemon, its version and its
// context. Each call may fail; what it could not learn stays empty.
func probeEngine(ctx context.Context, env DoctorEnv, cliVersion string) engineProbe {
	p := engineProbe{
		CLIVersion: cliVersion, CLI: env.getenv(execx.EnvDocker),
		DockerHost: env.getenv("DOCKER_HOST"), DockerContext: env.getenv("DOCKER_CONTEXT"),
	}
	out, err := env.Run(ctx, "docker", "info", "--format", "{{json .}}")
	p.Info, p.Version, p.OSType, p.InfoOK = parseDockerInfo(out)
	switch {
	case err != nil:
		p.InfoOK, p.InfoErr = false, lastLine(err.Error())
	case !p.InfoOK && len(p.Info.ServerErrors) > 0:
		p.InfoErr = p.Info.ServerErrors[0]
	case !p.InfoOK:
		p.InfoErr = "unexpected answer to docker info: " + shortLine(out)
	}
	if p.Info.ClientInfo != nil {
		p.Context = p.Info.ClientInfo.Context
	}
	if p.InfoOK {
		if out, err := env.Run(ctx, "docker", "version", "--format", "{{json .Server}}"); err == nil {
			var s dockerServer
			if json.Unmarshal([]byte(strings.TrimSpace(out)), &s) == nil {
				p.ServerPlatform = s.Platform.Name
				for _, c := range s.Components {
					p.ServerComponents = append(p.ServerComponents, c.Name)
				}
			}
		}
	}
	if out, err := env.Run(ctx, "docker", "context", "inspect"); err == nil {
		var list []dockerContext
		if json.Unmarshal([]byte(strings.TrimSpace(out)), &list) == nil && len(list) > 0 {
			p.Context = list[0].Name
			p.Endpoint = list[0].Endpoints.Docker.Host
		}
	}
	if p.DockerHost != "" {
		p.Endpoint = p.DockerHost
	}
	return p
}

// parseDockerInfo reads `docker info --format '{{json .}}'`, from Docker's
// CLI or from Podman's wrapper. ok is false when the daemon did not answer
// (Docker's CLI then prints the document with an empty ServerVersion).
func parseDockerInfo(out string) (info dockerInfo, version, osType string, ok bool) {
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &info) != nil {
		return dockerInfo{}, "", "", false
	}
	if info.ServerVersion != "" {
		return info, info.ServerVersion, info.OSType, info.OSType != ""
	}
	if info.PodmanHost != nil && info.PodmanVersion != nil && info.PodmanVersion.Version != "" {
		osType = info.PodmanHost.OS
		if osType == "" {
			osType = "linux"
		}
		return info, info.PodmanVersion.Version, osType, true
	}
	return info, "", "", false
}

// detectEngine names the engine from what the probe found. Daemon facts win
// over context and socket names, which are only hints (a context can be
// called anything); with no daemon, the hints are all there is.
func detectEngine(p engineProbe) ContainerEngine {
	e := ContainerEngine{
		Version: p.Version, OperatingSystem: p.Info.OperatingSystem,
		Context: p.Context, Endpoint: p.Endpoint, CLI: p.CLI,
	}
	switch {
	case p.DockerHost != "":
		e.EndpointFrom = "DOCKER_HOST"
	case p.DockerContext != "":
		e.EndpointFrom = "DOCKER_CONTEXT"
	case p.Context != "" || p.Endpoint != "":
		e.EndpointFrom = "context"
	}
	e.Remote = remoteEndpoint(p.Endpoint)
	for _, o := range p.Info.SecurityOptions {
		if strings.Contains(o, "name=rootless") {
			e.Rootless = true
		}
	}
	if p.Info.PodmanHost != nil && p.Info.PodmanHost.Security.Rootless {
		e.Rootless = true
	}

	lower := func(s string) string { return strings.ToLower(s) }
	osName, name := lower(p.Info.OperatingSystem), lower(p.Info.Name)
	ctxName, endpoint := lower(p.Context), lower(p.Endpoint)
	podman := strings.Contains(lower(p.CLIVersion), "podman") || p.Info.PodmanHost != nil ||
		strings.Contains(lower(p.ServerPlatform), "podman") || strings.Contains(endpoint, "podman") ||
		strings.Contains(ctxName, "podman") || strings.Contains(p.Info.DockerRootDir, "containers/storage")
	for _, c := range p.ServerComponents {
		if strings.Contains(lower(c), "podman") {
			podman = true
		}
	}
	desktopLabel := false
	for _, l := range p.Info.Labels {
		if strings.HasPrefix(l, "com.docker.desktop.") {
			desktopLabel = true
		}
	}

	switch {
	case podman:
		e.Kind, e.Name = EnginePodman, "Podman"
	case strings.Contains(osName, "rancher desktop") || strings.Contains(name, "rancher-desktop") ||
		ctxName == "rancher-desktop" || strings.Contains(endpoint, "/.rd/") || strings.Contains(endpoint, "rancher-desktop"):
		e.Kind, e.Name = EngineRancherDesktop, "Rancher Desktop"
	case strings.Contains(osName, "orbstack") || name == "orbstack" || ctxName == "orbstack" || strings.Contains(endpoint, "/.orbstack/"):
		e.Kind, e.Name = EngineOrbStack, "OrbStack"
	case name == "colima" || strings.HasPrefix(name, "colima-") || ctxName == "colima" || strings.HasPrefix(ctxName, "colima-") ||
		strings.Contains(endpoint, "/.colima/"):
		e.Kind, e.Name = EngineColima, "Colima"
	case osName == "docker desktop" || name == "docker-desktop" || desktopLabel ||
		strings.HasPrefix(lower(p.ServerPlatform), "docker desktop") ||
		(!p.InfoOK && strings.HasPrefix(ctxName, "desktop-")):
		e.Kind, e.Name = EngineDockerDesktop, "Docker Desktop"
	case p.InfoOK && (p.ServerPlatform == "" || strings.Contains(lower(p.ServerPlatform), "docker engine")):
		e.Kind, e.Name = EngineDockerEngine, "Docker Engine"
	default:
		e.Kind, e.Name = EngineUnknown, "an unrecognised engine"
		if p.ServerPlatform != "" {
			e.Name = p.ServerPlatform
		}
	}
	return e
}

// remoteEndpoint reports whether the docker CLI talks to another machine:
// ssh://, or tcp:// to an address other than this one.
func remoteEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "ssh":
		return true
	case "tcp", "http", "https":
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1", "":
			return false
		}
		return true
	}
	return false
}

// Notes shared by several engines.
const (
	noteComposePlugin = "`docker compose` must be Docker's Compose (the plugin or the docker-compose v2 binary), " +
		"not podman-compose: Theo relies on Compose's labels, `config --format json` and `ls`"
	noteVMMounts = "the engine runs in a virtual machine: a bind mount only shows files of folders shared with that VM; " +
		"a worktree outside them starts empty in the container"
)

// checkEngine turns the detection into the "engine" check and the report's
// engine field.
func checkEngine(env DoctorEnv, p engineProbe) (ContainerEngine, Check) {
	e := detectEngine(p)
	c := Check{ID: "engine", Name: "Container engine", Status: StatusInfo}

	where := ""
	switch {
	case e.EndpointFrom == "DOCKER_HOST":
		where = "DOCKER_HOST=" + e.Endpoint
	case e.Context != "" && e.Endpoint != "":
		where = "context " + e.Context + " (" + e.Endpoint + ")"
		if e.EndpointFrom == "DOCKER_CONTEXT" {
			where = "DOCKER_CONTEXT=" + e.Context + " (" + e.Endpoint + ")"
		}
	case e.Context != "":
		where = "context " + e.Context
	}
	summary := e.Name
	if e.Version != "" {
		summary += " " + e.Version
	}
	if e.Rootless {
		summary += " (rootless)"
	}
	if e.OperatingSystem != "" && !strings.EqualFold(e.OperatingSystem, e.Name) {
		summary += " on " + e.OperatingSystem
	}
	if where != "" {
		summary += " · " + where
	}
	if e.CLI != "" {
		summary += " · " + execx.EnvDocker + "=" + e.CLI
	}
	c.Detail = summary

	native := p.Info.PodmanHost != nil || strings.Contains(strings.ToLower(p.CLIVersion), "podman")
	switch e.Kind {
	case EngineDockerEngine:
		c.Status, e.Support = StatusOK, SupportVerified
		if env.GOOS != "linux" || e.Remote {
			e.Support = SupportExpected
		}
		if e.Rootless {
			e.Notes = append(e.Notes, "rootless Docker cannot publish host ports below 1024 unless net.ipv4.ip_unprivileged_port_start allows it")
		}
	case EngineDockerDesktop:
		c.Status, e.Support = StatusOK, SupportExpected
	case EngineColima:
		e.Support = SupportExpected
		e.Notes = append(e.Notes,
			noteVMMounts+" (Colima shares your home directory and /tmp/colima by default; `colima start --edit` changes the mounts)",
			"published ports are forwarded to localhost by Colima; file-change events in bind mounts may be slower or missing (sshfs/virtiofs)")
	case EngineOrbStack:
		e.Support = SupportExpected
		e.Notes = append(e.Notes,
			"OrbStack shares the Mac's file system with its VM; keep projects under /Users to be safe",
			"published ports are on localhost as with Docker Desktop; OrbStack's own *.orb.local names are not used by Theo")
	case EngineRancherDesktop:
		e.Support = SupportExpected
		e.Notes = append(e.Notes,
			"only the dockerd (moby) container engine works; the containerd/nerdctl engine has no Docker API and is not supported",
			noteVMMounts+" (on macOS and Linux Rancher Desktop shares your home directory by default)")
	case EnginePodman:
		c.Status, e.Support = StatusWarn, SupportExpected
		if native {
			e.Support = SupportUnsupported
			e.Notes = append(e.Notes, "`docker` is Podman's own command (podman-docker): its output formats differ from Docker's in places Theo parses (volume and container listings)")
			c.Fix = "install Docker's CLI and Compose plugin and point them at Podman's socket: DOCKER_HOST=unix://$XDG_RUNTIME_DIR/podman/podman.sock (Linux, after `systemctl --user enable --now podman.socket`) or the socket `podman machine inspect` shows"
		} else {
			c.Fix = "nothing to change if your projects start; see docs/cli/engines.md for the differences"
		}
		e.Notes = append(e.Notes,
			noteComposePlugin,
			"rootless Podman publishes ports through a user-space proxy (pasta/slirp4netns): ports below 1024 need net.ipv4.ip_unprivileged_port_start, and containers see the host as host.containers.internal; `host-gateway` in extra_hosts depends on the Podman version",
			"on SELinux systems bind mounts may need the :z option")
	default:
		e.Support = SupportExpected
		e.Notes = append(e.Notes, "Theo only talks to the docker CLI, so an engine with a Docker-compatible API should work, but this one is not in the tested list")
	}
	if !p.InfoOK {
		// Nothing more than the hints of the context: the daemon check says why it failed.
		e.Support = ""
		switch e.Kind {
		case EngineRancherDesktop:
			c.Status = StatusWarn
			c.Fix = "start Rancher Desktop with Preferences > Container Engine set to dockerd (moby); the containerd engine has no Docker socket"
		default:
			c.Status = StatusInfo
			c.Fix = ""
		}
		if e.Kind == EngineUnknown || e.Kind == EngineDockerEngine {
			e.Kind, e.Name = EngineUnknown, "unknown (the daemon did not answer)"
		}
		c.Detail = "not identified: the daemon did not answer"
		if where != "" {
			c.Detail += " · " + where
		}
		if e.Kind != EngineUnknown {
			c.Detail += " · looks like " + e.Name
		}
		e.Notes = nil
		if e.Kind == EngineRancherDesktop {
			e.Notes = []string{"only the dockerd (moby) container engine works; the containerd/nerdctl engine has no Docker API and is not supported"}
		}
	}
	if e.Remote {
		e.Notes = append(e.Notes, "the engine is on another machine ("+e.Endpoint+"): bind mounts use that machine's paths, so worktree files are not in the containers unless the same paths exist there, and published ports open on that machine")
		if c.Status == StatusOK || c.Status == StatusInfo {
			c.Status = StatusWarn
		}
		if c.Fix == "" {
			c.Fix = "run Theo on the machine that runs the engine, or use a local engine"
		}
	}
	if c.Status == StatusWarn && c.Fix == "" {
		c.Fix = "see docs/cli/engines.md for what differs with " + e.Name
	}
	c.Notes = e.Notes
	return e, c
}

// daemonFix is the advice when the daemon does not answer, for the engine
// the context points at.
func daemonFix(goos string, e ContainerEngine, dockerHost string) string {
	fix := ""
	switch e.Kind {
	case EngineColima:
		fix = "start Colima (`colima start`)"
	case EngineOrbStack:
		fix = "start OrbStack"
	case EngineRancherDesktop:
		fix = "start Rancher Desktop and set Preferences > Container Engine to dockerd (moby)"
	case EnginePodman:
		if goos == "linux" {
			fix = "start Podman's socket (`systemctl --user enable --now podman.socket`)"
		} else {
			fix = "start the Podman machine (`podman machine start`)"
		}
	case EngineDockerDesktop:
		fix = "start Docker Desktop and wait until it says it is running"
	default:
		switch goos {
		case "linux":
			fix = "start it (`sudo systemctl start docker`) and, if the error is about permissions, add your user to the docker group (`sudo usermod -aG docker $USER`, then log in again)"
		default:
			fix = "start Docker Desktop and wait until it says it is running"
		}
	}
	if dockerHost != "" {
		fix += "; DOCKER_HOST is set to " + dockerHost + ": unset it, or point it at a running engine"
	}
	return fix
}

// macSharedFor returns the folders the engine shares with its VM by default
// on macOS, and the name to show.
func macSharedFor(e ContainerEngine, home string) ([]string, string, string) {
	switch e.Kind {
	case EngineColima:
		if home != "" {
			return []string{home, "/tmp/colima"}, "Colima", "add them to Colima's mounts (`colima start --edit`), or keep the project (and its worktrees) under your home directory"
		}
	case EngineRancherDesktop:
		if home != "" {
			return []string{home, "/tmp/rancher-desktop"}, "Rancher Desktop", "add them to Rancher Desktop's mounts (override.yaml of its Lima VM), or keep the project (and its worktrees) under your home directory"
		}
	case EnginePodman:
		return []string{"/Users", "/private", "/var/folders"}, "the Podman machine", "add them to the Podman machine's volumes (`podman machine init -v`), or keep the project (and its worktrees) under your home directory"
	case EngineOrbStack:
		return macShared, "OrbStack", "keep the project (and its worktrees) under your home directory"
	}
	return macShared, "Docker Desktop", "add them in Docker Desktop > Settings > Resources > File sharing, or keep the project (and its worktrees) under your home directory"
}

// shortLine is the first line of s, cut to 120 characters, for quoting
// unexpected output.
func shortLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}
