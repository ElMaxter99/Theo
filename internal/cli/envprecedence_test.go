package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/testutil"
)

// envLine reports whether the resolved configuration has KEY: value in the
// environment of service s (six spaces of indentation).
func hasEnv(cfg, key, value string) bool {
	return strings.Contains(cfg, "\n      "+key+": "+value+"\n")
}

// TestEnvPrecedence verifies, with the real docker compose, how the project's
// .env, --env-file (several), the process environment, a service's env_file
// and environment, and the variables set with `theo env` combine
// (B-004; docs/cli/env-precedence.md has the table).
func TestEnvPrecedence(t *testing.T) {
	testutil.RequireCompose(t)
	compatProject(t, "t3-env", map[string]string{
		"compose.yaml": `name: t3-env
services:
  s:
    image: alpine:3.20
    env_file: [svc.env]
    environment:
      T: fromcompose
      IV: "${V:-unset}"
      IW: "${W:-unset}"
      IX: "${X:-unset}"
      IY: "${Y:-unset}"
      IZ: "${Z:-unset}"
`,
		".env":    "V=dotenv\nW=dotenv\nX=dotenv\nY=dotenv\n",
		"svc.env": "S=svcfile\nT=svcfile\nV=svcfile\n",
		"e1.env":  "W=e1\nX=e1\nZ=e1\n",
		"e2.env":  "X=e2\nZ=e2\n",
	})
	// Variables of the real environment must not leak into the checks.
	for _, k := range []string{"V", "W", "X", "Y", "Z", "S", "T"} {
		unsetenv(t, k)
	}
	cfgOf := func(args ...string) string {
		t.Helper()
		stdout, stderr, code := run(append([]string{"instance", "config", "t3-env", "-w", "feature/x"}, args...)...)
		if code != ExitOK {
			t.Fatalf("instance config %v: %s", args, stderr)
		}
		return stdout
	}
	wt := func(file string) string { // the file in the linked worktree
		_, linked := worktreePaths(t, "t3-env")
		return linked + "/" + file
	}
	check := func(what, cfg string, want map[string]string) {
		t.Helper()
		for k, v := range want {
			if !hasEnv(cfg, k, v) {
				t.Errorf("%s: want %s: %s in\n%s", what, k, v, cfg)
			}
		}
	}

	// The worktree's own .env feeds interpolation; env_file feeds the container;
	// environment: wins over env_file.
	check("plain", cfgOf(), map[string]string{"IV": "dotenv", "IW": "dotenv", "IX": "dotenv", "IZ": "unset", "S": "svcfile", "T": "fromcompose", "V": "svcfile"})

	// --env-file replaces the .env; the last file wins for a variable.
	check("one env file", cfgOf("--env-file", wt("e1.env")), map[string]string{"IV": "unset", "IW": "e1", "IX": "e1", "IY": "unset", "IZ": "e1"})
	check("two env files", cfgOf("--env-file", wt("e1.env"), "--env-file", wt("e2.env")), map[string]string{"IW": "e1", "IX": "e2", "IZ": "e2"})
	check("two env files, reversed", cfgOf("--env-file", wt("e2.env"), "--env-file", wt("e1.env")), map[string]string{"IW": "e1", "IX": "e1", "IZ": "e1"})

	// The process environment beats env files for interpolation, not for the service's env_file.
	t.Setenv("W", "process")
	t.Setenv("V", "process")
	check("process", cfgOf(), map[string]string{"IV": "process", "IW": "process", "IX": "dotenv", "V": "svcfile"})
	unsetenv(t, "W")
	unsetenv(t, "V")

	// theo env set: the value reaches the container (overlay environment, over
	// env_file and environment:) and interpolation (generated env file, read
	// last), and the worktree's own .env keeps working next to it.
	for _, a := range [][]string{
		{"env", "set", "t3-env", "-w", "feature/x", "X=theo"},
		{"env", "set", "t3-env", "-w", "feature/x", "T=theo"},
		{"env", "set", "t3-env", "-w", "feature/x", "S=theo"},
	} {
		if _, stderr, code := run(a...); code != ExitOK {
			t.Fatalf("%v: %s", a, stderr)
		}
	}
	check("theo env", cfgOf(), map[string]string{"X": "theo", "T": "theo", "S": "theo", "IX": "theo", "IV": "dotenv", "IW": "dotenv", "IY": "dotenv"})
	check("theo env with env files", cfgOf("--env-file", wt("e1.env"), "--env-file", wt("e2.env")), map[string]string{"X": "theo", "IX": "theo", "IW": "e1", "IZ": "e2", "IV": "unset"})

	// A process variable still wins for interpolation, but the container gets
	// the value set with theo: this is the one place where they differ.
	t.Setenv("X", "process")
	check("theo env and process", cfgOf(), map[string]string{"X": "theo", "IX": "process"})
}

// unsetenv removes a variable for the rest of the test; an empty one would
// still shadow the env files.
func unsetenv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "x") // registers the restore of the original value
	os.Unsetenv(key)
}

// worktreePaths returns the paths of the main and the linked worktree of a
// registered project.
func worktreePaths(t *testing.T, project string) (main, linked string) {
	t.Helper()
	stdout, stderr, code := run("worktrees", project, "--json")
	if code != ExitOK {
		t.Fatal(stderr)
	}
	var paths []string
	for _, line := range strings.Split(stdout, "\n") {
		if i := strings.Index(line, `"path": "`); i >= 0 {
			rest := line[i+len(`"path": "`):]
			paths = append(paths, rest[:strings.Index(rest, `"`)])
		}
	}
	if len(paths) < 2 {
		t.Fatalf("worktrees --json: %s", stdout)
	}
	return paths[0], paths[1]
}
