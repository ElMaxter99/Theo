package api

import (
	"strings"
	"testing"
)

func TestNetworkPath(t *testing.T) {
	for _, c := range []struct {
		p, goos string
		want    bool
	}{
		{`\\evil\share`, "windows", true},
		{`\\evil\share\dir`, "windows", true},
		{`//evil/share`, "windows", true},
		{`\\.\pipe\docker_engine`, "windows", true},
		{`\\.\C:`, "windows", true},
		{`\\?\UNC\evil\share`, "windows", true},
		{`\\?\GLOBALROOT\Device\x`, "windows", true},
		{`\\?\C:\Users\ana`, "windows", false},
		{`\\?\C:`, "windows", false},
		{`\\wsl$\Ubuntu\home\ana`, "windows", false},
		{`\\WSL.localhost\Ubuntu`, "windows", false},
		{`C:\Users\ana`, "windows", false},
		{`C:/Users/ana`, "windows", false},
		{`//wsl$/Ubuntu/home`, "windows", false},
		{`//tmp/x`, "linux", false}, // legal on POSIX
		{`/home/ana`, "linux", false},
		{`\\evil\share`, "linux", true},
		{``, "windows", false},
	} {
		if got := networkPath(c.p, c.goos); got != c.want {
			t.Errorf("networkPath(%q, %s) = %v, want %v", c.p, c.goos, got, c.want)
		}
	}
}

func TestRedactPaths(t *testing.T) {
	for in, want := range map[string]string{
		"/home/ana/.local/share/theo":                                    "<path>",
		"Theo data directory: /home/ana/.local/share/theo: not writable": "Theo data directory: <path>: not writable",
		`C:\Users\Ana\AppData\Roaming\theo`:                              "<path>",
		`D:/work/proj`:                                                   "<path>",
		`\\server\share\proj`:                                            "<path>",
		`the data directory path is long (C:\Users\Ana\x)`:               "the data directory path is long (<path>)",
		"projects outside the folders: /opt/a, /srv/b":                   "projects outside the folders: <path>, <path>",
		"install Docker Engine: https://docs.docker.com/engine/install/": "install Docker Engine: https://docs.docker.com/engine/install/",
		"engine 27.3.1":                                                  "engine 27.3.1",
		"docker.sock=/var/run/docker.sock":                               "docker.sock=<path>",
	} {
		if got := redactPaths(in); got != want {
			t.Errorf("redactPaths(%q) = %q, want %q", in, got, want)
		}
	}
	if got := redactPaths("/Users/ana/p and C:\\Users\\ana\\q"); strings.Contains(got, "ana") {
		t.Errorf("a user name survives: %q", got)
	}
}
