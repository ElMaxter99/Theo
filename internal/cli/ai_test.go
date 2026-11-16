package cli

import (
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/ai"
)

func withProviders(t *testing.T, m map[string]*ai.Stub) {
	old := providerFor
	providerFor = func(id string) (ai.Provider, error) {
		if s, ok := m[id]; ok {
			return s, nil
		}
		return old(id)
	}
	t.Cleanup(func() { providerFor = old })
}

func TestToolAIDoctor(t *testing.T) {
	hookProject(t, nil)
	withProviders(t, map[string]*ai.Stub{
		"claude":  {Rules: []ai.StubRule{{Contains: "ok", Replies: []string{"ok"}}}},
		"copilot": {NotReady: true, Rules: []ai.StubRule{{Contains: "ok", Err: ai.ErrLimit}}},
	})
	out, stderr, code := run("tool", "ai", "doctor")
	if code != ExitOK {
		t.Fatalf("doctor: %d %s", code, stderr)
	}
	for _, want := range []string{"PROVIDER", "claude", "yes", "ping", "copilot", "no", "stores no credentials", "roles: not chosen yet"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output lacks %q:\n%s", want, out)
		}
	}
	if _, _, code := run("config", "set", "ai.roles.coordinator", "claude:opus"); code != ExitOK {
		t.Fatal("config set")
	}
	out, _, _ = run("tool", "ai", "doctor", "--no-ping")
	if !strings.Contains(out, "coordinator: claude:opus") {
		t.Errorf("roles line: %s", out)
	}
	if _, _, code := run("config", "set", "ai.roles.worker", "gemini:x"); code == ExitOK {
		t.Error("an unknown provider must be refused")
	}
}

func TestToolAIDoctorNoProviderReady(t *testing.T) {
	hookProject(t, nil)
	withProviders(t, map[string]*ai.Stub{"claude": {NotReady: true}, "copilot": {NotReady: true}})
	_, stderr, code := run("tool", "ai", "doctor", "--no-ping")
	if code == ExitOK || !strings.Contains(stderr, "no AI provider is ready") {
		t.Errorf("exit %d %q", code, stderr)
	}
}

func TestToolAIModels(t *testing.T) {
	hookProject(t, nil)
	withProviders(t, map[string]*ai.Stub{"claude": {}})
	out, _, code := run("tool", "ai", "models", "claude")
	if code != ExitOK || !strings.Contains(out, "stub-large") {
		t.Errorf("%d %s", code, out)
	}
	if _, _, code := run("tool", "ai", "models", "nope"); code == ExitOK {
		t.Error("unknown provider")
	}
	out, _, _ = run("tool", "list")
	if !strings.Contains(out, "ai ") {
		t.Errorf("tool list must show ai: %s", out)
	}
}
