package execx

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// THEO_DOCKER replaces the program run for `docker`, in every runner, and the
// environment (DOCKER_HOST, DOCKER_CONTEXT) reaches it unchanged.
func TestTheoDockerAndEnvironment(t *testing.T) {
	name, args := helper(t, "0")
	abs, err := filepath.Abs(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvDocker, abs)
	t.Setenv("DOCKER_HOST", "unix:///run/user/1000/podman/podman.sock")
	t.Setenv("THEO_EXECX_PRINTENV", "DOCKER_HOST")

	out, err := Run(context.Background(), t.TempDir(), "docker", args...)
	if err != nil || !strings.Contains(string(out), "got:") || !strings.Contains(string(out), "DOCKER_HOST=unix:///run/user/1000/podman/podman.sock") {
		t.Fatalf("Run: %q, %v", out, err)
	}
	var buf bytes.Buffer
	if err := Stream(context.Background(), "", &buf, &buf, "docker", args...); err != nil || !strings.Contains(buf.String(), "got:") {
		t.Errorf("Stream: %q, %v", buf.String(), err)
	}
	buf.Reset()
	if err := Pipe(context.Background(), strings.NewReader("x"), &buf, "docker", args...); err != nil || !strings.Contains(buf.String(), "got:x") {
		t.Errorf("Pipe: %q, %v", buf.String(), err)
	}
	if got := DockerOverride(); got != abs {
		t.Errorf("DockerOverride = %q", got)
	}
}

func TestTheoDockerRefusesRelativeAndMissing(t *testing.T) {
	for _, v := range []string{"./bin/docker", "bin/docker", "theo-no-such-docker-cli"} {
		t.Setenv(EnvDocker, v)
		_, err := Run(context.Background(), "", "docker", "version")
		if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), EnvDocker+"="+v) {
			t.Errorf("%s: err = %v", v, err)
		}
	}
	// Other tools are not affected.
	t.Setenv(EnvDocker, "theo-no-such-docker-cli")
	if _, err := lookPath("theo-no-such-tool"); err == nil || strings.Contains(err.Error(), EnvDocker) {
		t.Errorf("other tool: %v", err)
	}
}
