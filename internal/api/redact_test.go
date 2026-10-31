package api

import (
	"strings"
	"testing"
)

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
