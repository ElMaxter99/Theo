package proxy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/engine"
)

func TestHostLabel(t *testing.T) {
	for in, want := range map[string]string{
		"Shop":                  "shop",
		"feature/Login_Form":    "feature-login-form",
		"my.app":                "my-app",
		"--x--":                 "x",
		"":                      "wt",
		"ünïcode":               "n-code",
		strings.Repeat("a", 80): strings.Repeat("a", 63),
	} {
		if got := HostLabel(in); got != want {
			t.Errorf("HostLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeAndShape(t *testing.T) {
	for in, want := range map[string]string{
		"Web.Main.Shop.LOCALHOST:7080": "web.main.shop.localhost",
		"main.shop.localhost.":         "main.shop.localhost",
		"main.shop.localhost.:80":      "main.shop.localhost",
		"[::1]:7080":                   "::1",
		"localhost":                    "localhost",
	} {
		if got := NormalizeHost(in); got != want {
			t.Errorf("NormalizeHost(%q) = %q, want %q", in, got, want)
		}
	}
	for host, want := range map[string]bool{
		"main.shop.localhost":          true,
		"web.main.shop.localhost:7080": true,
		"shop.localhost":               false,
		"a.b.c.d.localhost":            false,
		"main.shop.example.com":        false,
		"localhost":                    false,
	} {
		if got := IsRouteShaped(host); got != want {
			t.Errorf("IsRouteShaped(%q) = %v", host, got)
		}
	}
}

func shopTargets() []Target {
	return []Target{
		{Project: "Shop", Worktree: "/src/shop", Branch: "main", Main: true, ComposeProject: "shop",
			Service: "db", ContainerPort: 5432, HostPort: 5432, Running: true},
		{Project: "Shop", Worktree: "/src/shop", Branch: "main", Main: true, ComposeProject: "shop",
			Service: "web", ContainerPort: 8080, HostPort: 8080, Running: true},
		{Project: "Shop", Worktree: "/src/shop", Branch: "main", Main: true, ComposeProject: "shop",
			Service: "web", ContainerPort: 9229, HostPort: 9229, Running: true},
		{Project: "Shop", Worktree: "/wt/shop/feature-x", Branch: "feature/X", ComposeProject: "shop-feature-x",
			Service: "web", ContainerPort: 8080, HostPort: 8081, HostIP: "0.0.0.0", Running: false},
		{Project: "Shop", Worktree: "/wt/shop/feature-x", Branch: "feature/X", ComposeProject: "shop-feature-x",
			Service: "cache", ContainerPort: 6379, HostPort: 0}, // random host port: no route
	}
}

func TestBuildTable(t *testing.T) {
	tbl := BuildTable(shopTargets(), 7080)
	var got []string
	for _, r := range tbl.Routes {
		got = append(got, r.URL+" "+r.Service+" "+r.Target)
	}
	want := []string{
		"http://main.shop.localhost:7080 web 127.0.0.1:8080",
		"http://db.main.shop.localhost:7080 db 127.0.0.1:5432",
		"http://web.main.shop.localhost:7080 web 127.0.0.1:8080", // the lowest container port
		"http://feature-x.shop.localhost:7080 web 127.0.0.1:8081",
		"http://web.feature-x.shop.localhost:7080 web 127.0.0.1:8081",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	for host, want := range map[string]string{
		"main.shop.localhost":               "web",
		"MAIN.Shop.localhost:7080":          "web",
		"db.main.shop.localhost":            "db",
		"web.feature-x.shop.localhost:7080": "web",
		"feature-x.shop.localhost":          "web",
	} {
		r, ok := tbl.Lookup(host)
		if !ok || r.Service != want {
			t.Errorf("Lookup(%q) = %+v, %v; want service %s", host, r, ok, want)
		}
	}
	for _, host := range []string{"cache.feature-x.shop.localhost", "web.other.shop.localhost", "shop.localhost", "localhost", "web.main.shop.example"} {
		if r, ok := tbl.Lookup(host); ok {
			t.Errorf("Lookup(%q) found %+v", host, r)
		}
	}
	if r, _ := tbl.Lookup("feature-x.shop.localhost"); r.Running || r.Instance != "feature-x" || r.Branch != "feature/X" {
		t.Errorf("feature-x route: %+v", r)
	}
	// The branch slug is an alias of the instance; main's alias is "main" itself.
	if r, ok := tbl.Lookup("web.feature-x.shop.localhost"); !ok || !reflect.DeepEqual(r.Aliases, []string(nil)) {
		t.Errorf("feature-x aliases: %+v", r)
	}
}

func TestBranchAlias(t *testing.T) {
	tbl := BuildTable([]Target{
		{Project: "app", Worktree: "/wt/app-login", Branch: "feature/login", ComposeProject: "app-app-login",
			Service: "api", ContainerPort: 3000, HostPort: 3001, Running: true},
	}, 80)
	r, ok := tbl.Lookup("feature-login.app.localhost")
	if !ok || r.Instance != "app-login" || r.URL != "http://app-login.app.localhost" || !reflect.DeepEqual(r.Aliases, []string{"feature-login"}) {
		t.Fatalf("alias lookup: %+v %v", r, ok)
	}
	if _, ok := tbl.Lookup("api.feature-login.app.localhost"); !ok {
		t.Error("service form of the alias not routed")
	}
	if got := tbl.Hosts(); !reflect.DeepEqual(got, []string{
		"api.app-login.app.localhost", "api.feature-login.app.localhost", "app-login.app.localhost", "feature-login.app.localhost",
	}) {
		t.Errorf("hosts: %v", got)
	}
}

func TestDefaultService(t *testing.T) {
	for _, c := range []struct {
		names []string
		want  string
	}{
		{[]string{"admin", "api", "app", "web"}, "web"},
		{[]string{"admin", "api", "app"}, "app"},
		{[]string{"admin", "api", "frontend"}, "api"},
		{[]string{"admin", "frontend", "zeta"}, "frontend"},
		{[]string{"admin", "mailhog"}, "admin"},
		{nil, ""},
	} {
		if got := DefaultService(c.names); got != c.want {
			t.Errorf("DefaultService(%v) = %q, want %q", c.names, got, c.want)
		}
	}
}

func TestInstanceNames(t *testing.T) {
	tbl := BuildTable([]Target{
		// A linked worktree whose directory is "main" must not take main's name.
		{Project: "My App", Worktree: "/wt/main", Branch: "hotfix", ComposeProject: "my-app-main", Service: "web", ContainerPort: 80, HostPort: 8082},
		{Project: "My App", Worktree: "/src/myapp", Branch: "trunk", Main: true, ComposeProject: "myapp", Service: "web", ContainerPort: 80, HostPort: 8080},
		// Two worktrees with the same directory name: Theo adds a hash to the Compose name.
		{Project: "My App", Worktree: "/a/x", ComposeProject: "my-app-x-1a2b3c", Service: "web", ContainerPort: 80, HostPort: 8083},
		// Configuration unreadable: the directory name.
		{Project: "My App", Worktree: "/b/Feature_Y", Service: "web", ContainerPort: 80, HostPort: 8084},
		// Specific host address.
		{Project: "other", Worktree: "/o", Main: true, Service: "ui", ContainerPort: 80, HostPort: 9000, HostIP: "127.0.0.2"},
	}, 7080)
	var got []string
	for _, r := range tbl.Routes {
		if r.Default {
			got = append(got, r.Host+" "+r.Target)
		}
	}
	want := []string{
		"main.my-app.localhost 127.0.0.1:8080",
		"x-1a2b3c.my-app.localhost 127.0.0.1:8083",
		"feature-y.my-app.localhost 127.0.0.1:8084",
	}
	if len(got) != 5 || !reflect.DeepEqual(got[:3], want) || got[4] != "main.other.localhost 127.0.0.2:9000" ||
		!strings.HasPrefix(got[3], "main-") || !strings.HasSuffix(got[3], ".my-app.localhost 127.0.0.1:8082") {
		t.Fatalf("instances:\n%s", strings.Join(got, "\n"))
	}
	if r, _ := tbl.Lookup("hotfix.my-app.localhost"); r == nil || r.HostPort != 8082 {
		t.Errorf("branch alias of the clashing worktree: %+v", r)
	}
	if r, _ := tbl.Lookup("trunk.my-app.localhost"); r == nil || r.HostPort != 8080 {
		t.Errorf("branch alias of main: %+v", r)
	}
}

func TestFromStatusAndAddURLs(t *testing.T) {
	r := &engine.StatusReport{Projects: []engine.ProjectStatus{{
		Name: "shop", Worktrees: []engine.WorktreeStatus{
			{Path: "/src/shop", Branch: "main", Kind: engine.KindMain, Instance: &engine.InstanceStatus{
				ComposeProject: "shop", Ports: []engine.PortStatus{
					{Service: "web", ContainerPort: 80, Protocol: "tcp", HostPort: 8080, URL: "http://localhost:8080", Status: engine.PortRunning},
					{Service: "dns", ContainerPort: 53, Protocol: "udp", HostPort: 5353, Status: engine.PortRunning},
					{Service: "adm", ContainerPort: 80, Protocol: "tcp", HostPort: 9000, URL: "http://192.168.1.5:9000", Status: "free"},
				}}},
			{Path: "/src/bare", Kind: engine.KindBare},
		}}}}
	ts := FromStatus(r)
	if len(ts) != 2 || !ts[0].Main || !ts[0].Running || ts[0].HostIP != "" || ts[1].HostIP != "192.168.1.5" || ts[1].Running {
		t.Fatalf("targets: %+v", ts)
	}
	AddURLs(r, 7080)
	got := r.Projects[0].Worktrees[0].Instance.ProxyURLs
	want := []string{"http://main.shop.localhost:7080", "http://adm.main.shop.localhost:7080", "http://web.main.shop.localhost:7080"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("proxy urls: %v", got)
	}
}

func TestCheckAddr(t *testing.T) {
	for _, c := range []struct {
		addr   string
		remote bool
		want   string
		err    bool
	}{
		{"", false, "127.0.0.1", false},
		{"localhost", false, "127.0.0.1", false},
		{"127.0.0.1", false, "127.0.0.1", false},
		{"::1", false, "::1", false},
		{"[::1]", false, "::1", false},
		{"0.0.0.0", false, "", true},
		{"192.168.1.10", false, "", true},
		{"0.0.0.0", true, "0.0.0.0", false},
		{"example.com", true, "", true},
	} {
		got, err := CheckAddr(c.addr, c.remote)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("CheckAddr(%q, %v) = %q, %v", c.addr, c.remote, got, err)
		}
	}
}
