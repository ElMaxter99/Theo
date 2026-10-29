package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// Compose compatibility (B-005, ADR-0004): fixtures with the Compose features
// real projects use, run through Theo's generated file with the real docker
// compose. The tests with the prefix t3 use project, Compose and image names
// that start with "t3" so they never meet other tests on the same daemon.

// freePorts returns n host ports that are free now, at least 4 apart from each
// other, and each with the following one free too.
func freePorts(t *testing.T, n int) []int {
	t.Helper()
	var out []int
	for len(out) < n {
		p := testutil.FreePort(t)
		ok := true
		for _, q := range out {
			if p-q < 4 && q-p < 4 {
				ok = false
			}
		}
		if ok {
			out = append(out, p)
		}
	}
	return out
}

// hold makes a host port busy until the test ends.
func hold(t *testing.T, port int, proto string) {
	t.Helper()
	addr := ":" + strconv.Itoa(port)
	if proto == "udp" {
		c, err := net.ListenPacket("udp", addr)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
}

// compatProject creates the repository of a fixture, with a linked worktree
// feature/x, and registers it as the Theo project name. Theo's data goes to a
// temporary directory.
func compatProject(t *testing.T, name string, files map[string]string) {
	t.Helper()
	main, _ := testutil.NewRepo(t, files)
	t.Setenv(registry.EnvHome, t.TempDir())
	if _, stderr, code := run("project", "add", main, "--name", name); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
}

// plannedPorts returns the ports Theo plans for the linked instance with
// --auto-ports, by service.
func plannedPorts(t *testing.T, project string, extra ...string) map[string][]engine.PortMapping {
	t.Helper()
	args := append([]string{"ports", project, "-w", "feature/x", "--auto-ports", "--json"}, extra...)
	stdout, stderr, code := run(args...)
	if code != ExitOK {
		t.Fatalf("ports --auto-ports: %s", stderr)
	}
	var doc struct {
		Ports []engine.PortMapping `json:"ports"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("ports --json: %v\n%s", err, stdout)
	}
	out := map[string][]engine.PortMapping{}
	for _, m := range doc.Ports {
		out[m.Service] = append(out[m.Service], m)
	}
	return out
}

// resolved returns the configuration the linked instance would run with.
func resolved(t *testing.T, project string, extra ...string) string {
	t.Helper()
	args := append([]string{"instance", "config", project, "-w", "feature/x", "--auto-ports"}, extra...)
	stdout, stderr, code := run(args...)
	if code != ExitOK {
		t.Fatalf("instance config: %s", stderr)
	}
	return stdout
}

func mustContain(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%s lacks %q:\n%s", what, w, got)
		}
	}
}

// TestComposeCompatPorts reassigns ports in every way Compose can spell them
// and in every way a project can assemble its files. Each case holds the
// ports of the main instance busy, so --auto-ports has to move all of them.
func TestComposeCompatPorts(t *testing.T) {
	testutil.RequireCompose(t)
	const img = "image: alpine:3.20\n"

	t.Run("include", func(t *testing.T) {
		p := freePorts(t, 2)
		hold(t, p[0], "tcp")
		hold(t, p[1], "tcp")
		compatProject(t, "t3-include", map[string]string{
			"compose.yaml":  fmt.Sprintf("name: t3-include\ninclude:\n  - path: infra/db.yaml\nservices:\n  web:\n    %s    ports: [\"%d:80\"]\n    depends_on: [db]\n", img, p[0]),
			"infra/db.yaml": fmt.Sprintf("services:\n  db:\n    %s    ports: [\"%d:5432\"]\n", img, p[1]),
		})
		got := plannedPorts(t, "t3-include")
		if got["db"][0].Effective == p[1] || got["db"][0].Status != engine.PortReassigned || got["web"][0].Effective == p[0] {
			t.Errorf("ports = %+v", got)
		}
		mustContain(t, "config", resolved(t, "t3-include"), fmt.Sprintf("published: %q", strconv.Itoa(got["db"][0].Effective)))
	})

	t.Run("extends", func(t *testing.T) {
		p := freePorts(t, 2)
		hold(t, p[0], "tcp")
		hold(t, p[1], "tcp")
		compatProject(t, "t3-extends", map[string]string{
			"compose.yaml": fmt.Sprintf("name: t3-extends\nservices:\n  web:\n    extends:\n      file: common.yaml\n      service: base\n    ports: [\"%d:81\"]\n", p[1]),
			"common.yaml":  fmt.Sprintf("services:\n  base:\n    %s    ports: [\"%d:80\"]\n    environment: [A=1]\n", img, p[0]),
		})
		// The extended port and the service's own are both published; both move.
		got := plannedPorts(t, "t3-extends")
		if len(got["web"]) != 2 || got["web"][0].Effective == p[0] || got["web"][1].Effective == p[1] {
			t.Fatalf("ports = %+v", got)
		}
		cfg := resolved(t, "t3-extends")
		mustContain(t, "config", cfg, "A: \"1\"")
		if strings.Contains(cfg, fmt.Sprintf("published: %q", strconv.Itoa(p[0]))) || strings.Contains(cfg, fmt.Sprintf("published: %q", strconv.Itoa(p[1]))) {
			t.Errorf("an original port is still published:\n%s", cfg)
		}
	})

	t.Run("anchors and merge keys", func(t *testing.T) {
		p := freePorts(t, 2)
		hold(t, p[0], "tcp")
		hold(t, p[1], "tcp")
		compatProject(t, "t3-anchors", map[string]string{
			"compose.yaml": fmt.Sprintf(`name: t3-anchors
x-base: &base
  image: alpine:3.20
  environment: &env
    COMMON: shared
  ports: ["%d:80"]
services:
  a:
    <<: *base
  b:
    <<: *base
    ports: ["%d:80"]
    environment:
      <<: *env
      OWN: x
`, p[0], p[1]),
		})
		got := plannedPorts(t, "t3-anchors")
		if got["a"][0].Effective == p[0] || got["b"][0].Effective == p[1] || got["a"][0].Effective == got["b"][0].Effective {
			t.Errorf("ports = %+v", got)
		}
		cfg := resolved(t, "t3-anchors")
		mustContain(t, "config", cfg, "COMMON: shared", "OWN: x")
	})

	t.Run("override file, any extension", func(t *testing.T) {
		p := freePorts(t, 2)
		hold(t, p[0], "tcp")
		hold(t, p[1], "tcp")
		// Compose loads compose.override.yml next to compose.yaml; Theo must
		// keep it when it adds its own file.
		compatProject(t, "t3-override", map[string]string{
			"compose.yaml":         fmt.Sprintf("name: t3-override\nservices:\n  web:\n    %s    ports: [\"%d:80\"]\n", img, p[0]),
			"compose.override.yml": fmt.Sprintf("services:\n  web:\n    environment:\n      LAYER: override\n  extra:\n    %s    ports: [\"%d:80\"]\n", img, p[1]),
		})
		got := plannedPorts(t, "t3-override")
		if len(got["extra"]) != 1 || got["extra"][0].Effective == p[1] {
			t.Fatalf("ports = %+v", got)
		}
		mustContain(t, "config", resolved(t, "t3-override"), "LAYER: override")
	})

	t.Run("COMPOSE_FILE with several files", func(t *testing.T) {
		p := freePorts(t, 2)
		hold(t, p[0], "tcp")
		hold(t, p[1], "tcp")
		compatProject(t, "t3-composefile", map[string]string{
			".env":       "COMPOSE_FILE=base.yaml:ports.yaml\n",
			"base.yaml":  "name: t3-composefile\nservices:\n  web:\n    " + img,
			"ports.yaml": fmt.Sprintf("services:\n  web:\n    ports: [\"%d:80\"]\n    environment:\n      FROM_SECOND: yes\n  other:\n    %s    ports: [\"%d:80\"]\n", p[0], img, p[1]),
			// The default file names must not be picked up when COMPOSE_FILE names others.
			"compose.yaml": "services:\n  decoy:\n    " + img,
		})
		got := plannedPorts(t, "t3-composefile")
		if got["web"][0].Effective == p[0] || got["other"][0].Effective == p[1] {
			t.Fatalf("ports = %+v", got)
		}
		cfg := resolved(t, "t3-composefile")
		mustContain(t, "config", cfg, "FROM_SECOND", "other:")
		if strings.Contains(cfg, "decoy") {
			t.Errorf("compose.yaml is not part of COMPOSE_FILE:\n%s", cfg)
		}
	})

	t.Run("reset and override in the user's files", func(t *testing.T) {
		p := freePorts(t, 3)
		for _, q := range p {
			hold(t, q, "tcp")
		}
		compatProject(t, "t3-tags", map[string]string{
			"compose.yaml": fmt.Sprintf(`name: t3-tags
services:
  web:
    image: alpine:3.20
    ports: ["%d:80"]
    environment: [A=1]
    extra_hosts: ["x:127.0.0.1"]
  api:
    image: alpine:3.20
    ports: ["%d:81"]
`, p[0], p[1]),
			"compose.override.yaml": fmt.Sprintf(`services:
  web:
    ports: !override ["%d:80"]
    extra_hosts: !reset []
  api:
    ports: !reset []
`, p[2]),
		})
		got := plannedPorts(t, "t3-tags")
		if len(got["api"]) != 0 || len(got["web"]) != 1 || got["web"][0].Effective == p[2] {
			t.Fatalf("ports = %+v", got)
		}
		cfg := resolved(t, "t3-tags")
		if strings.Contains(cfg, "extra_hosts") {
			t.Errorf("the user's !reset was lost:\n%s", cfg)
		}
	})

	t.Run("network modes", func(t *testing.T) {
		p := freePorts(t, 1)
		hold(t, p[0], "tcp")
		compatProject(t, "t3-netmode", map[string]string{
			"compose.yaml": fmt.Sprintf(`name: t3-netmode
services:
  web:
    image: alpine:3.20
    container_name: t3-netmode-web
    ports: ["%d:80"]
  hostnet:
    image: alpine:3.20
    network_mode: host
    container_name: t3-netmode-host
  side:
    image: alpine:3.20
    network_mode: "service:web"
    depends_on: [web]
`, p[0]),
		})
		if _, stderr, code := run("instance", "set", "t3-netmode", "-w", "feature/x", "--neutralize-container-names"); code != ExitOK {
			t.Fatal(stderr)
		}
		if _, stderr, code := run("env", "set", "t3-netmode", "-w", "feature/x", "MODE=x"); code != ExitOK {
			t.Fatal(stderr)
		}
		got := plannedPorts(t, "t3-netmode")
		if len(got["hostnet"]) != 0 || len(got["side"]) != 0 || got["web"][0].Effective == p[0] {
			t.Fatalf("ports = %+v", got)
		}
		cfg := resolved(t, "t3-netmode")
		// Theo gives network_mode services no networks and no ports, and does not change their mode.
		mustContain(t, "config", cfg, "network_mode: host", "network_mode: service:web", "MODE: x")
		if strings.Contains(cfg, "t3-netmode-host") || strings.Contains(cfg, "t3-netmode-web") {
			t.Errorf("fixed container names must be gone:\n%s", cfg)
		}
	})

	t.Run("port syntaxes", func(t *testing.T) {
		p := freePorts(t, 6)
		for _, q := range p {
			hold(t, q, "tcp")
		}
		hold(t, p[2], "udp")
		compatProject(t, "t3-syntax", map[string]string{
			"compose.yaml": fmt.Sprintf(`name: t3-syntax
services:
  web:
    image: alpine:3.20
    expose: ["3000", "4000-4001"]
    ports:
      - "%d:80"
      - "127.0.0.1:%d:81"
      - "%d:53/udp"
      - target: 82
        published: %d
        host_ip: "::1"
        protocol: tcp
      - target: 83
        published: "%d"
        mode: host
      - "84"
      - "%d-%d:90-91"
      - "%d-%d:95"
`, p[0], p[1], p[2], p[3], p[4], p[5], p[5]+1, p[5]+2, p[5]+3),
		})
		got := plannedPorts(t, "t3-syntax")
		byTarget := map[uint32]engine.PortMapping{}
		for _, m := range got["web"] {
			byTarget[m.Target] = m
		}
		// 82 is on ::1, which a host without IPv6 cannot test: it stays as it is.
		for target, orig := range map[uint32]int{80: p[0], 81: p[1], 53: p[2], 83: p[4]} {
			m := byTarget[target]
			if m.Status != engine.PortReassigned || m.Effective == orig {
				t.Errorf("target %d: %+v", target, m)
			}
		}
		if byTarget[53].Protocol != "udp" || byTarget[81].HostIP != "127.0.0.1" || byTarget[82].HostIP != "::1" {
			t.Errorf("protocol or host_ip lost: %+v", byTarget)
		}
		if byTarget[84].Status != engine.PortRandom {
			t.Errorf("no published port is left to Docker: %+v", byTarget[84])
		}
		if byTarget[95].Status != engine.PortRange {
			t.Errorf("a host port range is left alone: %+v", byTarget[95])
		}
		cfg := resolved(t, "t3-syntax")
		mustContain(t, "config", cfg, "host_ip: ::1", "protocol: udp", "mode: host", `"3000"`)
	})

	t.Run("profiles", func(t *testing.T) {
		p := freePorts(t, 2)
		hold(t, p[0], "tcp")
		hold(t, p[1], "tcp")
		compatProject(t, "t3-profiles", map[string]string{
			"compose.yaml": fmt.Sprintf("name: t3-profiles\nservices:\n  web:\n    %s    ports: [\"%d:80\"]\n  debug:\n    %s    profiles: [dbg]\n    ports: [\"%d:80\"]\n", img, p[0], img, p[1]),
			"other.env":    "COMPOSE_PROFILES=dbg\n",
		})
		if got := plannedPorts(t, "t3-profiles"); len(got["debug"]) != 0 || len(got["web"]) != 1 {
			t.Errorf("a service of a disabled profile must be left alone: %+v", got)
		}
		if got := plannedPorts(t, "t3-profiles", "--profile", "dbg"); len(got["debug"]) != 1 || got["debug"][0].Effective == p[1] {
			t.Errorf("--profile dbg: %+v", got)
		}
		t.Setenv("COMPOSE_PROFILES", "dbg")
		if got := plannedPorts(t, "t3-profiles"); len(got["debug"]) != 1 || got["debug"][0].Effective == p[1] {
			t.Errorf("COMPOSE_PROFILES=dbg: %+v", got)
		}
		cfg := resolved(t, "t3-profiles")
		mustContain(t, "config", cfg, "debug:")
	})

	t.Run("dependencies, env files and builds", func(t *testing.T) {
		p := freePorts(t, 1)
		hold(t, p[0], "tcp")
		compatProject(t, "t3-deps", map[string]string{
			"compose.yaml": fmt.Sprintf(`name: t3-deps
services:
  web:
    build: ./web
    image: t3-deps-web:dev
    container_name: t3-deps-web
    env_file:
      - web.env
      - path: missing.env
        required: false
    environment:
      FROM_COMPOSE: kept
    extra_hosts: ["host.docker.internal:host-gateway"]
    ports: ["%d:80"]
    depends_on:
      db: {condition: service_healthy}
      cache: {condition: service_started, required: false}
  db:
    image: alpine:3.20
    healthcheck: {test: ["CMD", "true"], interval: 1s}
  cache:
    image: alpine:3.20
    profiles: [cache]
`, p[0]),
			"web/Dockerfile": "FROM alpine:3.20\n",
			"web.env":        "FROM_FILE=1\nOVERRIDDEN=file\n",
		})
		for _, args := range [][]string{
			{"instance", "set", "t3-deps", "-w", "feature/x", "--neutralize-container-names"},
			{"env", "set", "t3-deps", "-w", "feature/x", "OVERRIDDEN=theo", "--service", "web"},
			{"env", "set", "t3-deps", "-w", "feature/x", "LITERAL=a$HOME ${X:-y} $$"},
		} {
			if _, stderr, code := run(args...); code != ExitOK {
				t.Fatalf("%v: %s", args, stderr)
			}
		}
		cfg := resolved(t, "t3-deps")
		mustContain(t, "config", cfg, "FROM_FILE: \"1\"", "FROM_COMPOSE: kept", "OVERRIDDEN: theo", "required: false", "condition: service_healthy", "host.docker.internal=host-gateway")
		// The $ of a value set with theo reaches the container as typed, not interpolated.
		mustContain(t, "config", cfg, "LITERAL: a$$HOME $${X:-y} $$$$")
		if strings.Contains(cfg, "container_name") {
			t.Errorf("fixed container name still there:\n%s", cfg)
		}
	})
}
