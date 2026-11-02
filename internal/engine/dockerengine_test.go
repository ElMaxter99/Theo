package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// engineCase is one engine as the doctor sees it: a `docker info` sample from
// testdata/engines and what the other docker commands and variables say.
type engineCase struct {
	name       string
	goos       string
	sample     string            // file in testdata/engines; "" means the daemon does not answer
	cliVersion string            // `docker --version`; default Docker's
	server     string            // `docker version --format '{{json .Server}}'`; "" fails
	context    string            // context name; "" makes `docker context inspect` fail
	endpoint   string            // its docker endpoint
	env        map[string]string // DOCKER_HOST, DOCKER_CONTEXT, THEO_DOCKER

	kind     string
	status   string // of the "engine" check
	support  string
	version  string
	rootless bool
	remote   bool
	detail   []string // in the check's detail
	notes    []string // in one of the notes
	fix      string   // in the fix of the engine check
}

func readSample(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "engines", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (tc engineCase) tools(t *testing.T) fakeTools {
	tools := goodTools()
	if tc.cliVersion != "" {
		tools["docker --version"] = tc.cliVersion
	}
	if tc.sample == "" {
		tools["docker info"] = "!fail Cannot connect to the Docker daemon at " + tc.endpoint + ". Is the docker daemon running?"
	} else {
		tools["docker info"] = readSample(t, tc.sample)
	}
	tools["docker version"] = "!fail no server"
	if tc.server != "" {
		tools["docker version"] = tc.server
	}
	tools["docker context inspect"] = "!fail unknown command"
	if tc.context != "" {
		b, _ := json.Marshal([]map[string]any{{"Name": tc.context, "Endpoints": map[string]any{"docker": map[string]any{"Host": tc.endpoint}}}})
		tools["docker context inspect"] = string(b)
	}
	return tools
}

func (tc engineCase) run(t *testing.T, projects ...string) DoctorReport {
	t.Helper()
	goos := tc.goos
	if goos == "" {
		goos = "linux"
	}
	env := DoctorEnv{GOOS: goos, DataDir: t.TempDir(), Home: "/Users/ana", Projects: projects, Run: tc.tools(t).run,
		Getenv: func(k string) string { return tc.env[k] }}
	return RunDoctor(context.Background(), env)
}

func TestDetectEngine(t *testing.T) {
	const podmanServer = `{"Platform":{"Name":"linux/amd64/fedora-40"},"Components":[{"Name":"Podman Engine","Version":"5.2.5"},{"Name":"Conmon","Version":"2.1.12"}]}`
	tests := []engineCase{{
		name: "Docker Engine on Linux", sample: "docker-engine-linux.json",
		server:  `{"Platform":{"Name":"Docker Engine - Community"},"Components":[{"Name":"Engine","Version":"29.9.2"},{"Name":"containerd"}]}`,
		context: "default", endpoint: "unix:///var/run/docker.sock",
		kind: EngineDockerEngine, status: StatusOK, support: SupportVerified, version: "29.9.2",
		detail: []string{"Docker Engine 29.9.2", "context default (unix:///var/run/docker.sock)"},
	}, {
		name: "rootless Docker", sample: "docker-engine-rootless.json",
		context: "rootless", endpoint: "unix:///run/user/1000/docker.sock",
		kind: EngineDockerEngine, status: StatusOK, support: SupportVerified, rootless: true,
		detail: []string{"(rootless)", "context rootless"}, notes: []string{"below 1024"},
	}, {
		name: "Docker Desktop on macOS", goos: "darwin", sample: "docker-desktop-mac.json",
		server:  `{"Platform":{"Name":"Docker Desktop 4.35.1 (173168)"},"Components":[{"Name":"Engine","Version":"27.3.1"}]}`,
		context: "desktop-linux", endpoint: "unix:///Users/ana/.docker/run/docker.sock",
		kind: EngineDockerDesktop, status: StatusOK, support: SupportExpected, version: "27.3.1",
		detail: []string{"Docker Desktop 27.3.1", "context desktop-linux"},
	}, {
		name: "Docker Desktop in Windows containers mode", goos: "windows", sample: "docker-desktop-windows-containers.json",
		context: "desktop-windows", endpoint: "npipe:////./pipe/dockerDesktopWindowsEngine",
		kind: EngineDockerDesktop, status: StatusOK, support: SupportExpected,
	}, {
		name: "Colima", goos: "darwin", sample: "colima.json",
		context: "colima", endpoint: "unix:///Users/ana/.colima/default/docker.sock",
		kind: EngineColima, status: StatusInfo, support: SupportExpected, version: "27.1.1",
		detail: []string{"Colima 27.1.1 on Ubuntu", "context colima (unix:///Users/ana/.colima/default/docker.sock)"},
		notes:  []string{"/tmp/colima"},
	}, {
		name: "Colima profile through DOCKER_HOST only", goos: "darwin", sample: "docker-engine-linux.json",
		context: "default", endpoint: "unix:///var/run/docker.sock",
		env:  map[string]string{"DOCKER_HOST": "unix:///Users/ana/.colima/work/docker.sock"},
		kind: EngineColima, status: StatusInfo, support: SupportExpected,
		detail: []string{"DOCKER_HOST=unix:///Users/ana/.colima/work/docker.sock"},
	}, {
		name: "OrbStack", goos: "darwin", sample: "orbstack.json",
		context: "orbstack", endpoint: "unix:///Users/ana/.orbstack/run/docker.sock",
		kind: EngineOrbStack, status: StatusInfo, support: SupportExpected,
		detail: []string{"OrbStack 27.3.1 · context orbstack"}, notes: []string{"/Users"},
	}, {
		name: "Rancher Desktop (moby) on macOS", goos: "darwin", sample: "rancher-desktop-mac.json",
		context: "rancher-desktop", endpoint: "unix:///Users/ana/.rd/docker.sock",
		kind: EngineRancherDesktop, status: StatusInfo, support: SupportExpected,
		notes: []string{"dockerd (moby)", "containerd"},
	}, {
		name: "Rancher Desktop (moby) on Windows", goos: "windows", sample: "rancher-desktop-windows.json",
		context: "default", endpoint: "npipe:////./pipe/docker_engine",
		kind: EngineRancherDesktop, status: StatusInfo, support: SupportExpected,
		detail: []string{"on Rancher Desktop WSL Distribution"},
	}, {
		name: "Podman socket through DOCKER_HOST", sample: "podman-socket.json", server: podmanServer,
		context: "default", endpoint: "unix:///var/run/docker.sock",
		env:  map[string]string{"DOCKER_HOST": "unix:///run/user/1000/podman/podman.sock"},
		kind: EnginePodman, status: StatusWarn, support: SupportExpected, version: "5.2.5", rootless: true,
		detail: []string{"Podman 5.2.5 (rootless)", "DOCKER_HOST=unix:///run/user/1000/podman/podman.sock"},
		notes:  []string{"podman-compose", "below 1024", ":z"},
		fix:    "docs/cli/engines.md",
	}, {
		name: "Podman behind /var/run/docker.sock, no other hint", sample: "podman-socket.json",
		context: "default", endpoint: "unix:///var/run/docker.sock",
		kind: EnginePodman, status: StatusWarn, support: SupportExpected, rootless: true,
	}, {
		name: "podman-docker wrapper", sample: "podman-native.json", cliVersion: "podman version 5.2.5\n",
		kind: EnginePodman, status: StatusWarn, support: SupportUnsupported, version: "5.2.5", rootless: true,
		notes: []string{"podman-docker"}, fix: "DOCKER_HOST=unix://",
	}, {
		name: "Docker Engine on another machine", sample: "docker-engine-linux.json",
		env:  map[string]string{"DOCKER_HOST": "ssh://ana@build-server"},
		kind: EngineDockerEngine, status: StatusWarn, support: SupportExpected, remote: true,
		notes: []string{"another machine (ssh://ana@build-server)"}, fix: "machine that runs the engine",
	}, {
		name: "local tcp is not remote", sample: "docker-engine-linux.json",
		env:  map[string]string{"DOCKER_HOST": "tcp://127.0.0.1:2375"},
		kind: EngineDockerEngine, status: StatusOK, support: SupportVerified,
	}, {
		name: "DOCKER_CONTEXT names the context", sample: "docker-engine-linux.json",
		context: "build", endpoint: "unix:///run/docker-build.sock", env: map[string]string{"DOCKER_CONTEXT": "build"},
		kind: EngineDockerEngine, status: StatusOK,
		detail: []string{"DOCKER_CONTEXT=build (unix:///run/docker-build.sock)"},
	}, {
		name: "an engine Theo does not know", sample: "docker-engine-linux.json",
		server:  `{"Platform":{"Name":"Mirantis Container Runtime"},"Components":[{"Name":"Engine"}]}`,
		context: "default", endpoint: "unix:///var/run/docker.sock",
		kind: EngineUnknown, status: StatusInfo, support: SupportExpected,
		detail: []string{"Mirantis Container Runtime"}, notes: []string{"not in the tested list"},
	}, {
		name: "THEO_DOCKER is shown", sample: "docker-engine-linux.json", env: map[string]string{"THEO_DOCKER": "/opt/docker/bin/docker"},
		kind: EngineDockerEngine, status: StatusOK, detail: []string{"THEO_DOCKER=/opt/docker/bin/docker"},
	}, {
		name: "Rancher Desktop in containerd mode (no Docker socket)", goos: "darwin",
		context: "rancher-desktop", endpoint: "unix:///Users/ana/.rd/docker.sock",
		kind: EngineRancherDesktop, status: StatusWarn,
		detail: []string{"did not answer", "looks like Rancher Desktop"}, notes: []string{"containerd/nerdctl"}, fix: "dockerd (moby)",
	}, {
		name:    "daemon down, plain context",
		context: "default", endpoint: "unix:///var/run/docker.sock",
		kind: EngineUnknown, status: StatusInfo,
	}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.run(t)
			if r.Engine == nil {
				t.Fatal("no engine in the report")
			}
			e, c := *r.Engine, find(t, r, "engine")
			if e.Kind != tc.kind || c.Status != tc.status {
				t.Fatalf("kind %s, status %s, want %s %s\n%+v\n%+v", e.Kind, c.Status, tc.kind, tc.status, e, c)
			}
			if tc.support != "" && e.Support != tc.support {
				t.Errorf("support %q, want %q", e.Support, tc.support)
			}
			if tc.version != "" && e.Version != tc.version {
				t.Errorf("version %q, want %q", e.Version, tc.version)
			}
			if e.Rootless != tc.rootless || e.Remote != tc.remote {
				t.Errorf("rootless %v remote %v, want %v %v", e.Rootless, e.Remote, tc.rootless, tc.remote)
			}
			for _, want := range tc.detail {
				if !strings.Contains(c.Detail, want) {
					t.Errorf("detail %q lacks %q", c.Detail, want)
				}
			}
			all := strings.Join(c.Notes, "\n")
			if strings.Join(e.Notes, "\n") != all {
				t.Errorf("check notes and engine notes differ: %v / %v", c.Notes, e.Notes)
			}
			for _, want := range tc.notes {
				if !strings.Contains(all, want) {
					t.Errorf("notes lack %q:\n%s", want, all)
				}
			}
			if !strings.Contains(c.Fix, tc.fix) {
				t.Errorf("fix %q lacks %q", c.Fix, tc.fix)
			}
			if c.Status == StatusWarn && c.Fix == "" {
				t.Errorf("a warning needs a fix: %+v", c)
			}
			if c.Status == StatusFail {
				t.Errorf("the engine check never fails: %+v", c)
			}
			if tc.sample == "" {
				if d := find(t, r, "docker-daemon"); d.Status != StatusFail {
					t.Errorf("daemon: %+v", d)
				}
			}
		})
	}
}

// The Windows containers check keeps working on the JSON answer.
func TestDoctorWindowsContainersFromSample(t *testing.T) {
	tc := engineCase{goos: "windows", sample: "docker-desktop-windows-containers.json", context: "desktop-windows", endpoint: "npipe:////./pipe/dockerDesktopWindowsEngine"}
	r := tc.run(t)
	c := find(t, r, "docker-os")
	if r.OK || c.Status != StatusFail || !strings.Contains(c.Fix, "Linux containers") {
		t.Errorf("windows containers: %+v", c)
	}
	if d := find(t, r, "docker-daemon"); d.Status != StatusOK || d.Detail != "engine 27.3.1" {
		t.Errorf("daemon: %+v", d)
	}
}

func TestDoctorDaemonFixFollowsTheEngine(t *testing.T) {
	tests := []struct {
		goos, context, endpoint string
		env                     map[string]string
		want                    string
	}{
		{"darwin", "colima", "unix:///Users/ana/.colima/default/docker.sock", nil, "colima start"},
		{"darwin", "orbstack", "unix:///Users/ana/.orbstack/run/docker.sock", nil, "start OrbStack"},
		{"darwin", "rancher-desktop", "unix:///Users/ana/.rd/docker.sock", nil, "dockerd (moby)"},
		{"linux", "default", "unix:///var/run/docker.sock", map[string]string{"DOCKER_HOST": "unix:///run/user/1000/podman/podman.sock"}, "podman.socket"},
		{"darwin", "podman", "unix:///var/folders/x/podman/podman-machine-default-api.sock", nil, "podman machine start"},
		{"darwin", "desktop-linux", "unix:///Users/ana/.docker/run/docker.sock", nil, "Docker Desktop"},
		{"linux", "default", "unix:///var/run/docker.sock", nil, "systemctl start docker"},
		{"linux", "default", "", map[string]string{"DOCKER_HOST": "tcp://10.0.0.5:2375"}, "DOCKER_HOST is set to tcp://10.0.0.5:2375"},
	}
	for _, tt := range tests {
		tc := engineCase{goos: tt.goos, context: tt.context, endpoint: tt.endpoint, env: tt.env}
		c := find(t, tc.run(t), "docker-daemon")
		if c.Status != StatusFail || !strings.Contains(c.Fix, tt.want) {
			t.Errorf("%s %s: %+v, want fix with %q", tt.context, tt.env, c, tt.want)
		}
	}
}

func TestDoctorPodmanCompose(t *testing.T) {
	tools := goodTools()
	tools["docker compose version"] = "podman-compose version 1.2.0\npodman version 5.2.5\n"
	c := find(t, doctor(t, "linux", tools), "docker-compose")
	if c.Status != StatusFail || !strings.Contains(c.Detail, "podman-compose") || !strings.Contains(c.Fix, "2.24.4") {
		t.Errorf("podman-compose: %+v", c)
	}
}

func TestDoctorTheoDockerUnusable(t *testing.T) {
	tools := goodTools()
	tools["docker --version"] = "!missing"
	env := DoctorEnv{GOOS: "linux", DataDir: t.TempDir(), Run: tools.run,
		Getenv: func(k string) string { return map[string]string{"THEO_DOCKER": "/nope/docker"}[k] }}
	c := find(t, RunDoctor(context.Background(), env), "docker")
	if c.Status != StatusFail || !strings.Contains(c.Detail, "THEO_DOCKER=/nope/docker") || !strings.Contains(c.Fix, "THEO_DOCKER") {
		t.Errorf("THEO_DOCKER: %+v", c)
	}
}

func TestDoctorMacSharingFollowsTheEngine(t *testing.T) {
	colima := engineCase{goos: "darwin", sample: "colima.json", context: "colima", endpoint: "unix:///Users/ana/.colima/default/docker.sock"}
	c := find(t, colima.run(t, "/Users/ana/src/app", "/Volumes/work/app"), "macos-file-sharing")
	if c.Status != StatusWarn || !strings.Contains(c.Detail, "Colima") || !strings.Contains(c.Detail, "/Volumes/work/app") ||
		strings.Contains(c.Detail, "/Users/ana/src/app") || !strings.Contains(c.Fix, "colima") {
		t.Errorf("colima: %+v", c)
	}
	desktop := engineCase{goos: "darwin", sample: "docker-desktop-mac.json", context: "desktop-linux", endpoint: "unix:///Users/ana/.docker/run/docker.sock"}
	if c := find(t, desktop.run(t, "/Volumes/work/app"), "macos-file-sharing"); c.Status != StatusInfo || !strings.Contains(c.Detail, "Docker Desktop") {
		t.Errorf("docker desktop: %+v", c)
	}
	remote := engineCase{goos: "darwin", sample: "docker-engine-linux.json", env: map[string]string{"DOCKER_HOST": "ssh://build"}}
	for _, c := range remote.run(t, "/opt/x").Checks {
		if c.ID == "macos-file-sharing" {
			t.Errorf("remote engine: no file-sharing note expected, got %+v", c)
		}
	}
}

// The JSON of the report only gains fields: the old ones keep their names.
func TestDoctorJSONShape(t *testing.T) {
	tc := engineCase{sample: "podman-socket.json", context: "default", endpoint: "unix:///var/run/docker.sock",
		env: map[string]string{"DOCKER_HOST": "unix:///run/user/1000/podman/podman.sock"}}
	data, err := json.Marshal(tc.run(t))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"ok", "platform", "data_dir", "checks", "engine"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("no %q in %s", k, data)
		}
	}
	eng := doc["engine"].(map[string]any)
	for k, want := range map[string]any{"kind": "podman", "name": "Podman", "version": "5.2.5", "support": "expected",
		"context": "default", "endpoint": "unix:///run/user/1000/podman/podman.sock", "endpoint_from": "DOCKER_HOST", "rootless": true} {
		if eng[k] != want {
			t.Errorf("engine.%s = %v, want %v", k, eng[k], want)
		}
	}
	if notes, _ := eng["notes"].([]any); len(notes) == 0 {
		t.Errorf("engine.notes: %v", eng["notes"])
	}
	var sawEngine bool
	for _, raw := range doc["checks"].([]any) {
		c := raw.(map[string]any)
		for _, k := range []string{"id", "name", "status"} {
			if _, ok := c[k]; !ok {
				t.Errorf("check without %q: %v", k, c)
			}
		}
		if c["id"] == "engine" {
			sawEngine = true
			if _, ok := c["notes"].([]any); !ok {
				t.Errorf("engine check without notes: %v", c)
			}
		} else if _, ok := c["notes"]; ok {
			t.Errorf("notes must be left out when empty: %v", c)
		}
	}
	if !sawEngine {
		t.Error("no engine check")
	}

	// Without docker there is no engine to describe.
	tools := goodTools()
	tools["docker --version"] = "!missing"
	data, _ = json.Marshal(doctor(t, "linux", tools))
	if strings.Contains(string(data), `"engine"`) {
		t.Errorf("engine without docker: %s", data)
	}
}
