package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// The --json output of the commands that interfaces build on is part of
// Theo's contract: the HTTP API and the web interface read these shapes.
// The golden files in testdata/golden hold the output of a fixed scenario
// with the volatile parts (temporary paths, commit ids, times, host ports,
// Docker's human status text) replaced by placeholders. A change in a field
// name, type or presence fails here; regenerate on purpose with
// UPDATE_GOLDEN=1 go test -run TestGoldenJSON ./internal/cli

var (
	reSHA  = regexp.MustCompile(`\b[0-9a-f]{40}\b`)
	reTime = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z`)
	rePort = regexp.MustCompile(`\d{4,5}`)
	reHex6 = regexp.MustCompile(`-[0-9a-f]{6}\.yaml`)
)

var goldenHome string

func normalizeJSON(v any, tmp string, key string) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = normalizeJSON(e, tmp, k)
		}
		return x
	case []any:
		if key == "Publishers" {
			// Docker lists a port twice (0.0.0.0 and ::) when IPv6 is enabled,
			// which depends on the machine, so only the IPv4 entry is compared.
			kept := x[:0]
			for _, e := range x {
				if m, ok := e.(map[string]any); ok && m["URL"] == "::" {
					continue
				}
				kept = append(kept, e)
			}
			x = kept
		}
		for i, e := range x {
			x[i] = normalizeJSON(e, tmp, key)
		}
		return x
	case string:
		if goldenHome != "" {
			x = strings.ReplaceAll(x, goldenHome, "<HOME>")
		}
		x = strings.ReplaceAll(x, tmp, "<TMP>")
		if real, err := filepath.EvalSymlinks(tmp); err == nil {
			x = strings.ReplaceAll(x, real, "<TMP>")
		}
		x = reSHA.ReplaceAllString(x, "<SHA>")
		x = reTime.ReplaceAllString(x, "<TIME>")
		x = reHex6.ReplaceAllString(x, "-<HASH>.yaml")
		switch key {
		case "Status", "status_text":
			if strings.HasPrefix(x, "Up ") || strings.HasPrefix(x, "Exited") {
				return "<STATUS>"
			}
		case "url", "URL", "published", "configured", "effective":
			x = rePort.ReplaceAllString(x, "<PORT>")
		}
		return x
	case float64:
		switch key {
		case "host_port", "PublishedPort", "effective", "running":
			if x > 1000 {
				return "<PORT>"
			}
		}
		return x
	}
	return v
}

func goldenCheck(t *testing.T, name, tmp string, args ...string) {
	t.Helper()
	stdout, stderr, code := run(args...)
	if code != ExitOK {
		t.Fatalf("theo %s: exit %d\n%s", strings.Join(args, " "), code, stderr)
	}
	var v any
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatalf("theo %s printed no JSON: %v\n%s", strings.Join(args, " "), err, stdout)
	}
	v = normalizeJSON(v, tmp, "")
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	enc.Encode(v) // maps are written with sorted keys
	path := filepath.Join("testdata", "golden", name+".json")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden file %s (run with UPDATE_GOLDEN=1): %v", path, err)
	}
	if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(buf.Bytes())) {
		t.Errorf("theo %s: the JSON changed.\n--- golden %s\n%s\n--- now\n%s", strings.Join(args, " "), path, want, buf.String())
	}
}

func TestGoldenJSON(t *testing.T) {
	testutil.RequireDaemon(t)
	const name = "ubgold"
	main, linked := testutil.NewRepo(t, map[string]string{
		".gitignore": "server\n",
		"compose.yaml": `name: ubgold
services:
  web:
    build: .
    ports: ["8080"]
    volumes: [data:/data]
volumes:
  data: {}
`,
	})
	testutil.WriteServerImage(t, main)
	testutil.WriteServerImage(t, linked)
	home := t.TempDir()
	t.Setenv(registry.EnvHome, home)
	goldenHome = home
	t.Cleanup(func() { goldenHome = "" })
	t.Setenv("USER", "ubuser")
	t.Setenv("USERNAME", "ubuser")
	if _, stderr, code := run("project", "add", main, "--name", name); code != ExitOK {
		t.Fatalf("project add: %s", stderr)
	}
	linkedName := name + "-app-feature"
	t.Cleanup(func() {
		run("down", name, "-w", "feature/x")
		run("down", name)
		for _, n := range []string{"ubgold", linkedName} {
			testutil.Docker(t, "volume", "rm", "--force", n+"_data")
		}
	})
	tmp := filepath.Dir(main) // NewRepo puts both worktrees in one temporary directory
	tmpAll := filepath.Dir(tmp)
	_ = tmpAll

	if _, stderr, code := run("env", "set", name, "-w", "feature/x", "UB_PLAIN=1"); code != ExitOK {
		t.Fatalf("env set: %s", stderr)
	}
	if _, stderr, code := run("env", "set", name, "-w", "feature/x", "--secret", "UB_SECRET=hunter2"); code != ExitOK {
		t.Fatalf("env set secret: %s", stderr)
	}
	if _, stderr, code := run("up", name, "-w", "feature/x", "--auto-ports"); code != ExitOK {
		t.Fatalf("up: %s", stderr)
	}

	goldenCheck(t, "project-list", tmp, "project", "list", "--json")
	goldenCheck(t, "worktrees", tmp, "worktrees", name, "--json")
	goldenCheck(t, "branches", tmp, "branches", name, "--json")
	goldenCheck(t, "ps-overview", tmp, "ps", name, "--json")
	goldenCheck(t, "ps-instance", tmp, "ps", name, "-w", "feature/x", "--json")
	goldenCheck(t, "volumes", tmp, "volumes", name, "-w", "feature/x", "--json")
	goldenCheck(t, "env-list", tmp, "env", "list", name, "-w", "feature/x", "--json")
	goldenCheck(t, "history", tmp, "history", name, "-w", "feature/x", "--json")
	goldenCheck(t, "ports", tmp, "ports", name, "-w", "feature/x", "--json")
	goldenCheck(t, "services", tmp, "services", name, "-w", "feature/x", "--json")
	goldenCheck(t, "status", tmp, "status", name, "--json")
}
