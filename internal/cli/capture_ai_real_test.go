package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeClaude is a program called "claude" that answers like `claude -p ...
// --output-format json`, so the real adapter, the real process and the real
// parsing run end to end. It stands in for Claude Code: it is not Claude Code.
const fakeClaude = `#!/bin/sh
if [ "$1" = "--version" ]; then echo "9.9.9 (fake Claude Code)"; exit 0; fi
# the arguments the adapter must pass
case " $* " in *" --allowedTools Read,Grep,Glob "*) ;; *) echo "missing read-only tools" >&2; exit 3;; esac
case " $* " in *" --output-format json "*) ;; *) echo "missing output format" >&2; exit 3;; esac
prompt="$2"
case "$prompt" in
  *"List the screens"*)
    reply='{"framework":"angularjs","entrada":"inicio","rutas":[{"id":"inicio","ruta":"/","titulo":"Inicio","origen":["src/inicio.html"],"prioridad":"alta"}],"flujos":[{"id":"abrir","pasos":["inicio#base"]}]}' ;;
  *"Screen id: inicio"*)
    reply='{"pantalla":{"id":"inicio","ruta":"/","estados":[{"id":"base"}]},"llamadas":{"base":[{"metodo":"GET","ruta":"/api/resumen"}]}}' ;;
  *"Write the answers"*)
    reply='{"estados":{"base":[{"metodo":"GET","ruta":"/api/resumen","estado":200,"json":{"total":3}}]}}' ;;
  *"Answer with the single word"*)
    reply='ok' ;;
  *) echo "unexpected prompt" >&2; exit 4;;
esac
printf '{"type":"result","is_error":false,"result":"Here you go:\n%s"}\n' "$reply"
`

func TestRealProcessThroughTheClaudeAdapter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in is a shell script")
	}
	main := hookProject(t, map[string]string{"src/inicio.html": "<h1>Inicio</h1>"})
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, stderr, code := run("tool", "ai", "doctor")
	if code != ExitOK || !strings.Contains(out, "9.9.9") || !strings.Contains(out, "ping") {
		t.Fatalf("doctor: %d %s %s", code, out, stderr)
	}
	run("config", "set", "ai.roles.coordinator", "claude:opus")
	run("config", "set", "ai.roles.worker", "claude:sonnet")
	out, stderr, code = run("tool", "capture", "generate", "app", "--yes")
	if code != ExitOK || !strings.Contains(out, "1 screens, 1 states, 1 flows, 0 without data") {
		t.Fatalf("generate: %d %s %s", code, out, stderr)
	}
	if out, stderr, code = run("tool", "capture", "accept", "app", "--yes"); code != ExitOK {
		t.Fatalf("accept: %d %s %s", code, out, stderr)
	}
	if _, stderr, code := run("tool", "capture", "validate", main); code != ExitOK {
		t.Fatalf("validate: %s", stderr)
	}
	mock, err := os.ReadFile(filepath.Join(main, ".theo", "capturas", "mocks", "inicio-base-1.json"))
	if err != nil || !strings.Contains(string(mock), `"total":3`) {
		t.Errorf("mock: %s %v", mock, err)
	}
}
