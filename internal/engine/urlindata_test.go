package engine

import (
	"strings"
	"testing"
)

func TestImageApp(t *testing.T) {
	for image, want := range map[string]string{
		"wordpress":                        "WordPress",
		"wordpress:6.6-apache":             "WordPress",
		"docker.io/library/wordpress:6":    "WordPress",
		"bitnami/wordpress@sha256:abc":     "WordPress",
		"registry.local:5000/ghost:5":      "Ghost",
		"nextcloud":                        "Nextcloud",
		"postgres:16":                      "",
		"registry.local:5000/myapp:latest": "",
		"":                                 "",
	} {
		if got := imageApp(image); got != want {
			t.Errorf("imageApp(%q) = %q, want %q", image, got, want)
		}
	}
}

func TestServiceURL(t *testing.T) {
	plan := &PortPlan{Mappings: []PortMapping{
		{Service: "db", Target: 3306, Protocol: "tcp", Effective: 3306},
		{Service: "wordpress", Target: 443, Protocol: "tcp", Effective: 8443},
		{Service: "wordpress", Target: 80, Protocol: "tcp", Effective: 8081, Running: 8082},
		{Service: "wordpress", Target: 53, Protocol: "udp", Effective: 5353},
	}}
	if got := serviceURL(plan, "wordpress"); got != "http://localhost:8082" {
		t.Errorf("serviceURL = %q", got)
	}
	if got := serviceURL(plan, "cache"); got != "" {
		t.Errorf("serviceURL of a service without ports = %q", got)
	}
}

func TestURLInDataWarningText(t *testing.T) {
	inst := &Instance{Project: "blog", Compose: "blog-fix-menu"}
	w := urlInDataWarning(inst, "wordpress", "WordPress", "http://localhost:8081", "http://localhost:8082", "fix-menu")
	if w.Kind != WarnURLInData || w.Previous != "http://localhost:8081" || w.Current != "http://localhost:8082" {
		t.Fatalf("warning: %+v", w)
	}
	if w.Command != "wp search-replace 'http://localhost:8081' 'http://localhost:8082'" {
		t.Errorf("command: %q", w.Command)
	}
	if w.Fix != "theo instance set blog -w fix-menu --data-address-updated" {
		t.Errorf("fix: %q", w.Fix)
	}
	if !strings.HasPrefix(w.Message, "WordPress stores its own address") || !strings.Contains(w.Message, "Theo does not change data") {
		t.Errorf("message: %q", w.Message)
	}
	other := urlInDataWarning(inst, "web", "", "a", "b", "x")
	if other.Command != "" || !strings.HasPrefix(other.Message, "web stores") {
		t.Errorf("unknown app: %+v", other)
	}
}
