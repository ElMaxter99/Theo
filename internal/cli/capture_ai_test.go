package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/ai"
	"github.com/ElMaxter99/Theo/internal/engine"
)

const cliMap = `{"framework":"angularjs","entrada":"inicio","rutas":[{"id":"inicio","ruta":"/","titulo":"Inicio","origen":["src/inicio.html"],"prioridad":"alta"}],"flujos":[]}`
const cliScreen = `{"pantalla":{"id":"inicio","ruta":"/","estados":[{"id":"base"}]},"llamadas":{"base":[{"metodo":"GET","ruta":"/api/resumen"}]}}`
const cliData = `{"estados":{"base":[{"metodo":"GET","ruta":"/api/resumen","estado":200,"json":{"total":3}}]}}`

func withStubAI(t *testing.T) *ai.Stub {
	t.Helper()
	stub := &ai.Stub{Rules: []ai.StubRule{
		{Contains: "List the screens", Replies: []string{cliMap}},
		{Contains: "Screen id: inicio", Replies: []string{cliScreen}},
		{Contains: "Write the answers", Replies: []string{cliData}},
	}}
	old := engine.AIProviderFor
	engine.AIProviderFor = func(string) (ai.Provider, error) { return stub, nil }
	t.Cleanup(func() { engine.AIProviderFor = old })
	return stub
}

func TestCaptureGenerateAndAccept(t *testing.T) {
	main := hookProject(t, map[string]string{"src/inicio.html": "<h1>Inicio</h1>"})
	stub := withStubAI(t)

	// No provider chosen: it says what to do and sends nothing.
	if _, stderr, code := run("tool", "capture", "generate", "app", "--yes"); code == ExitOK || !strings.Contains(stderr, "theo config set ai.roles.coordinator") {
		t.Fatalf("without roles: %d %q", code, stderr)
	}
	run("config", "set", "ai.roles.coordinator", "claude:big")
	run("config", "set", "ai.roles.worker", "claude:small")

	// Without a terminal and without --yes the code is not sent.
	if _, stderr, code := run("tool", "capture", "generate", "app"); code == ExitOK || !strings.Contains(stderr, "would be sent to claude") || len(stub.Jobs) != 0 {
		t.Fatalf("consent: %d %q jobs=%d", code, stderr, len(stub.Jobs))
	}

	stdout, stderr, code := run("tool", "capture", "generate", "app", "--yes")
	if code != ExitOK {
		t.Fatalf("generate: %d %s", code, stderr)
	}
	for _, want := range []string{"First time for this project", "Route map (angularjs): 1 routes", "Plan: 1 exploration + 1 screens + 1 data = 3 jobs", "1 screens, 1 states, 0 flows, 0 without data", "nothing in the repository yet", "theo tool capture accept app --run"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("generate output lacks %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(main, ".theo")); err == nil {
		t.Error("generate must not write in the repository")
	}
	// The second time no notice is needed.
	if out, _, code := run("tool", "capture", "generate", "app", "--yes"); code != ExitOK || strings.Contains(out, "First time") {
		t.Errorf("the consent must be remembered: %d %s", code, out)
	}

	// Accept: without a terminal it needs --yes.
	if _, stderr, code := run("tool", "capture", "accept", "app"); code == ExitOK || !strings.Contains(stderr, "--yes") {
		t.Errorf("accept without --yes: %d %q", code, stderr)
	}
	out, stderr, code := run("tool", "capture", "accept", "app", "--yes")
	if code != ExitOK || !strings.Contains(out, "Wrote .theo/capturas/pantallas.json (1 screens) and 1 mock files") {
		t.Fatalf("accept: %d %s %s", code, out, stderr)
	}
	if _, stderr, code := run("tool", "capture", "validate", main); code != ExitOK {
		t.Errorf("the accepted inventory must validate: %s", stderr)
	}

	// An existing inventory is not overwritten: it says so and offers the options.
	before, _ := os.ReadFile(filepath.Join(main, ".theo", "capturas", "pantallas.json"))
	out, stderr, code = run("tool", "capture", "accept", "app", "--yes")
	if code == ExitOK || !strings.Contains(out, "already exists. Not overwritten") || !strings.Contains(out, "--merge") || !strings.Contains(out, "--replace") {
		t.Fatalf("existing: %d %q %q", code, out, stderr)
	}
	after, _ := os.ReadFile(filepath.Join(main, ".theo", "capturas", "pantallas.json"))
	if string(before) != string(after) {
		t.Error("the inventory must be untouched")
	}
	out, _, code = run("tool", "capture", "accept", "app", "--merge")
	if code != ExitOK || !strings.Contains(out, "0 screens added") || !strings.Contains(out, "1 kept as they were") {
		t.Errorf("merge: %d %s", code, out)
	}
	if out, _, code = run("tool", "capture", "accept", "app", "--replace"); code != ExitOK || !strings.Contains(out, "Replaced") {
		t.Errorf("replace: %d %s", code, out)
	}
	var inv map[string]any
	b, _ := os.ReadFile(filepath.Join(main, ".theo", "capturas", "pantallas.json"))
	if json.Unmarshal(b, &inv) != nil || inv["app"] != "app" {
		t.Errorf("inventory: %s", b)
	}
}

func TestDetectWithAIProposesAndValidates(t *testing.T) {
	main := hookProject(t, map[string]string{"README.md": "# a project nobody can detect"})
	stub := &ai.Stub{Rules: []ai.StubRule{{Contains: "Propose the", Replies: []string{
		`{"run":{"kind":"script"},"motivo":"README says so"}`, // no start command: rejected
		`{"run":{"kind":"script","start":"node server.js --port ${PORT}","install":["npm ci"]},"motivo":"server.js listens on a port"}`,
	}}}}
	old := engine.AIProviderFor
	engine.AIProviderFor = func(string) (ai.Provider, error) { return stub, nil }
	t.Cleanup(func() { engine.AIProviderFor = old })

	if _, stderr, code := run("tool", "capture", "detect", main, "--ai", "--yes"); code == ExitOK || !strings.Contains(stderr, "theo config set ai.roles.coordinator") {
		t.Fatalf("without roles: %d %q", code, stderr)
	}
	run("config", "set", "ai.roles.coordinator", "claude:big")
	run("config", "set", "ai.roles.worker", "claude:small")
	// Without a terminal and without --yes the code is not sent.
	if _, stderr, code := run("tool", "capture", "detect", main, "--ai"); code == ExitOK || !strings.Contains(stderr, "would be sent") || len(stub.Jobs) != 0 {
		t.Fatalf("consent: %d %q", code, stderr)
	}
	out, stderr, code := run("tool", "capture", "detect", main, "--ai", "--yes")
	if code != ExitOK {
		t.Fatalf("detect --ai: %d %s", code, stderr)
	}
	for _, want := range []string{"Rules found nothing. Asking claude:big", "low confidence", "node server.js --port ${PORT}", "server.js listens on a port", "Nothing was written", "serve <project> --check"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(stub.Jobs) != 2 || !strings.Contains(stub.Jobs[1].Prompt, "previous answer was rejected") {
		t.Errorf("an invalid proposal must be sent back: %d jobs", len(stub.Jobs))
	}
	if _, err := os.Stat(filepath.Join(main, ".theo.json")); err == nil {
		t.Error("a proposal is not saved without --write")
	}
	// With --write it is saved, and the commands still need approval to run.
	stub.Jobs = nil
	stub.Rules[0].Replies = stub.Rules[0].Replies[1:]
	out, _, code = run("tool", "capture", "detect", main, "--ai", "--write")
	if code != ExitOK || !strings.Contains(out, "Written to .theo.json") {
		t.Errorf("--write: %d %s", code, out)
	}
	b, _ := os.ReadFile(filepath.Join(main, ".theo.json"))
	if !strings.Contains(string(b), "server.js") {
		t.Errorf(".theo.json: %s", b)
	}
}
