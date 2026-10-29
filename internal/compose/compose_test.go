package compose

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ElMaxter99/Theo/internal/testutil"
)

const sample = `{
  "name": "app",
  "services": {
    "api": {
      "build": {"context": "/w/api", "dockerfile": "Dockerfile"},
      "ports": [
        {"mode": "ingress", "target": 80, "published": "8080", "protocol": "tcp"},
        {"mode": "ingress", "host_ip": "127.0.0.1", "target": 9229, "published": "9229", "protocol": "tcp"},
        {"mode": "ingress", "target": 3000, "protocol": "tcp"}
      ],
      "volumes": [
        {"type": "bind", "source": "/w/src", "target": "/app/src", "read_only": true, "bind": {}},
        {"type": "volume", "source": "data", "target": "/data", "volume": {}}
      ],
      "depends_on": {"db": {"condition": "service_started", "required": true}},
      "networks": {"default": null, "back": null}
    },
    "db": {"image": "postgres:16", "profiles": ["dev"]}
  }
}`

func TestParseConfig(t *testing.T) {
	p, err := ParseConfig([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "app" || len(p.Services) != 2 {
		t.Fatalf("ParseConfig() = %+v", p)
	}
	api := p.Services["api"]
	if api.Build == nil || api.Build.Context != "/w/api" {
		t.Errorf("build = %+v", api.Build)
	}
	wantPorts := []Port{
		{Target: 80, Published: "8080", Protocol: "tcp"},
		{HostIP: "127.0.0.1", Target: 9229, Published: "9229", Protocol: "tcp"},
		{Target: 3000, Protocol: "tcp"},
	}
	if !reflect.DeepEqual(api.Ports, wantPorts) {
		t.Errorf("ports = %+v", api.Ports)
	}
	wantVolumes := []Volume{
		{Type: "bind", Source: "/w/src", Target: "/app/src", ReadOnly: true},
		{Type: "volume", Source: "data", Target: "/data"},
	}
	if !reflect.DeepEqual(api.Volumes, wantVolumes) {
		t.Errorf("volumes = %+v", api.Volumes)
	}
	if got := SortedKeys(api.DependsOn); !reflect.DeepEqual(got, []string{"db"}) {
		t.Errorf("depends_on = %v", got)
	}
	if got := SortedKeys(api.Networks); !reflect.DeepEqual(got, []string{"back", "default"}) {
		t.Errorf("networks = %v", got)
	}
	db := p.Services["db"]
	if db.Image != "postgres:16" || !reflect.DeepEqual(db.Profiles, []string{"dev"}) {
		t.Errorf("db = %+v", db)
	}
}

func TestParseConfigRejectsInvalidJSON(t *testing.T) {
	if _, err := ParseConfig([]byte("services: {}")); err == nil {
		t.Error("expected an error")
	}
}

func TestConfigAgainstRealCompose(t *testing.T) {
	testutil.RequireCompose(t)
	dir := testutil.TempDir(t)
	testutil.WriteFile(t, dir, "compose.yaml", `services:
  web:
    image: nginx:alpine
    ports: ["8080:80"]
    volumes: ["./html:/usr/share/nginx/html:ro"]
  worker:
    image: busybox
    profiles: [jobs]
`)

	p, err := Compose{}.Config(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := SortedKeys(p.Services); !reflect.DeepEqual(got, []string{"web", "worker"}) {
		t.Fatalf("services = %v, want web and worker (profiles must be included)", got)
	}
	web := p.Services["web"]
	if len(web.Ports) != 1 || web.Ports[0].Published != "8080" || web.Ports[0].Target != 80 {
		t.Errorf("ports = %+v", web.Ports)
	}
	if len(web.Volumes) != 1 || web.Volumes[0].Source != filepath.Join(dir, "html") || !web.Volumes[0].ReadOnly {
		t.Errorf("volumes = %+v", web.Volumes)
	}

	if _, err := (Compose{}).Config(context.Background(), testutil.TempDir(t)); err == nil {
		t.Error("Config() in a directory without Compose files should fail")
	}
}
