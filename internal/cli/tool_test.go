package cli

import (
	"bytes"
	"encoding/json"
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
