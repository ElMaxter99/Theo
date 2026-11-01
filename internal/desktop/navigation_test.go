package desktop

import (
	"strings"
	"testing"
)

const app = "http://127.0.0.1:41873"

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want Decision
	}{
		// Stays in the window: the Theo server, exactly.
		{"launch url", app + "/#n=abc", Stay},
		{"root", app + "/", Stay},
		{"api path", app + "/api/v1/projects?x=1", Stay},
		{"uppercase scheme and host", "HTTP://127.0.0.1:41873/x", Stay},

		// Containers and any other web address go to the system browser.
		{"container on localhost", "http://localhost:8080", External},
		{"container on loopback, other port", "http://127.0.0.1:8080/health", External},
		{"same port, other host name", "http://localhost:41873/", External},
		{"loopback v6", "http://[::1]:3000/", External},
		{"https site", "https://example.com/docs", External},
		{"https on the app port", "https://127.0.0.1:41873/", External},
		{"no port is not the app port", "http://127.0.0.1/", External},
		{"lookalike suffix", "http://127.0.0.1:418730/", External},
		{"lookalike host (invalid port)", "http://127.0.0.1:41873.evil.example/", Refuse},
		{"lookalike host", "http://127.0.0.1.evil.example:41873/", External},

		// Refused: not a plain http(s) address.
		{"empty", "", Refuse},
		{"javascript", "javascript:alert(1)", Refuse},
		{"file", "file:///etc/passwd", Refuse},
		{"data", "data:text/html,<script>1</script>", Refuse},
		{"about", "about:blank", Refuse},
		{"blob", "blob:http://127.0.0.1:41873/uuid", Refuse},
		{"mailto", "mailto:a@b.c", Refuse},
		{"custom scheme", "ms-msdt:/id", Refuse},
		{"ftp", "ftp://example.com/", Refuse},
		{"relative", "/api/v1/projects", Refuse},
		{"no host", "http:///x", Refuse},
		{"opaque", "http:example.com", Refuse},
		{"credentials disguise the host", "http://127.0.0.1:41873@evil.example/", Refuse},
		{"credentials", "http://user:pw@localhost:8080/", Refuse},
		{"newline", "http://localhost:8080/\nx", Refuse},
		{"nul", "http://localhost:8080/\x00", Refuse},
		{"space in host", "http://local host/", Refuse},
		{"too long", "http://localhost/" + strings.Repeat("a", maxURLLen), Refuse},
	}
	for _, c := range cases {
		if got := Classify(c.raw, app); got != c.want {
			t.Errorf("%s: Classify(%q) = %v, want %v", c.name, c.raw, got, c.want)
		}
	}
}

func TestClassifyBadAppOrigin(t *testing.T) {
	// Without a usable origin nothing may stay in the window.
	for _, o := range []string{"", "127.0.0.1:1", "http://"} {
		if got := Classify(app+"/", o); got == Stay {
			t.Errorf("origin %q: Classify stayed", o)
		}
	}
}

func TestOpenExternalRefusesWithoutRunningAnything(t *testing.T) {
	for _, raw := range []string{app + "/#n=x", "javascript:1", "file:///etc/passwd", ""} {
		if err := OpenExternal(raw, app); err == nil {
			t.Errorf("OpenExternal(%q) = nil, want an error", raw)
		}
	}
}
