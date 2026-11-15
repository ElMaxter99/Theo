package cli

import (
	"bytes"
	"encoding/json"
	"github.com/ElMaxter99/Theo/internal/testutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolListAndDetect(t *testing.T) {
	var out, errb bytes.Buffer
	if err := runTool(nil, []string{"list", "--json"}, &out, &errb); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"id": "capture"`) {
		t.Errorf("tool list must include capture: %s", out.String())
	}
	out.Reset()
	if err := runTool(nil, []string{"nope"}, &out, &errb); err == nil {
		t.Error("an unknown tool must be an error")
	}

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"dev":"vite"},"devDependencies":{"vite":"6"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, ".theo.json"), []byte(`{"copy":[".env"]}`), 0o644)

	out.Reset()
	if err := runTool(nil, []string{"capture", "detect", dir}, &out, &errb); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--port ${PORT}") || !strings.Contains(out.String(), "Nothing was written") {
		t.Errorf("detect output: %s", out.String())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".theo.json")); strings.Contains(string(b), "run") {
		t.Error("detect without --write must not touch .theo.json")
	}

	out.Reset()
	if err := runTool(nil, []string{"capture", "detect", dir, "--write", "--json"}, &out, &errb); err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	b, _ := os.ReadFile(filepath.Join(dir, ".theo.json"))
	if err := json.Unmarshal(b, &doc); err != nil || doc["run"] == nil || doc["copy"] == nil {
		t.Fatalf("--write must add run and keep copy: %s %v", b, err)
	}
	// A second --write leaves the section alone.
	before := string(b)
	if err := runTool(nil, []string{"capture", "detect", dir, "--write"}, &out, &errb); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, ".theo.json")); string(after) != before {
		t.Error("an existing run section must not be replaced")
	}
}

// TestHelperServe is not a test: the test binary runs it as the application of
// a run section (an HTTP server on $PORT) when HELPER_SERVE is set.
func TestHelperServe(t *testing.T) {
	if os.Getenv("HELPER_SERVE") != "1" {
		return
	}
	http.ListenAndServe("127.0.0.1:"+os.Getenv("PORT"), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
}

func TestToolCaptureServeCheck(t *testing.T) {
	main := hookProject(t, nil)
	exe := strings.ReplaceAll(os.Args[0], `\`, `\\`)
	testutil.WriteFile(t, main, ".theo.json", `{"run":{"start":"\"`+exe+`\" -test.run=TestHelperServe","env":{"HELPER_SERVE":"1"},"ready":{"timeout_s":30}}}`)

	// Without a terminal and without --yes the commands are not run.
	withStdin(t, "")
	_, stderr, code := run("tool", "capture", "serve", "app", "--check")
	if code == ExitOK || !strings.Contains(stderr, "--yes") {
		t.Fatalf("unapproved commands must not run: %d %q", code, stderr)
	}

	stdout, stderr, code := run("tool", "capture", "serve", "app", "--check", "--yes", "--json")
	if code != ExitOK {
		t.Fatalf("serve --check: %d %s", code, stderr)
	}
	var got struct {
		URL  string `json:"url"`
		Port int    `json:"port"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || got.Port == 0 || !strings.HasPrefix(got.URL, "http://127.0.0.1:") {
		t.Fatalf("serve --check --json: %q %v", stdout, err)
	}
	// It was stopped: nothing answers any more.
	if resp, err := http.Get(got.URL); err == nil {
		resp.Body.Close()
		t.Error("--check must stop the application")
	}

	// The approval was remembered, so a second run needs no --yes.
	if _, stderr, code := run("tool", "capture", "serve", "app", "--check"); code != ExitOK {
		t.Errorf("approved commands must run without --yes: %s", stderr)
	}
	// A project without a run section says what to do.
	testutil.WriteFile(t, main, ".theo.json", `{}`)
	if _, stderr, code := run("tool", "capture", "serve", "app", "--check"); code == ExitOK || !strings.Contains(stderr, "detect") {
		t.Errorf("no run section: %d %q", code, stderr)
	}
}
