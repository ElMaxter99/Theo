package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/ai"
	"github.com/ElMaxter99/Theo/internal/tools/capture"
)

func pairingEngine(t *testing.T, stub *ai.Stub) (*Engine, string) {
	t.Helper()
	e, _, _, _ := newEngine()
	e.WithDataDir(t.TempDir())
	if err := e.Prefs().Set("ai.roles.coordinator", "claude:big"); err != nil {
		t.Fatal(err)
	}
	if err := e.Prefs().Set("ai.roles.worker", "claude:small"); err != nil {
		t.Fatal(err)
	}
	old := AIProviderFor
	AIProviderFor = func(string) (ai.Provider, error) { return stub, nil }
	t.Cleanup(func() { AIProviderFor = old })
	return e, t.TempDir()
}

func TestPairWithAISavesAndReuses(t *testing.T) {
	stub := &ai.Stub{Rules: []ai.StubRule{{Contains: "FIRST version", Replies: []string{
		`{"parejas":[{"a":"factura-detalle","b":"detalle-factura","confianza":0.93,"motivo":"same purpose"},{"a":"ayuda","b":"","confianza":0.41,"motivo":"nothing alike"}]}`}}}}
	e, root := pairingEngine(t, stub)
	removed := []capture.ScreenInfo{{ID: "factura-detalle", Route: "/facturas/1"}, {ID: "ayuda", Route: "/ayuda"}}
	added := []capture.ScreenInfo{{ID: "detalle-factura", Route: "/factura/1"}}
	key := capture.PairKey("develop", "rebranding")
	var logs []string
	emit := func(ev CaptureEvent) { logs = append(logs, ev.Kind+": "+ev.Text) }
	req := ComparisonRequest{Project: "app"}

	// Without consent the AI is not asked.
	pairs := e.pairWithAI(context.Background(), req, root, key, capture.PairFile{}, removed, added, emit)
	if len(pairs) != 0 || len(stub.Jobs) != 0 || !strings.Contains(strings.Join(logs, "\n"), "AI matching is off") {
		t.Fatalf("no consent: %v %d %v", pairs, len(stub.Jobs), logs)
	}
	roles, _ := e.AIRoles("")
	if err := e.GiveGenerateConsent("app", roles); err != nil {
		t.Fatal(err)
	}

	logs = nil
	pairs = e.pairWithAI(context.Background(), req, root, key, capture.PairFile{}, removed, added, emit)
	if len(pairs) != 1 || pairs[0].AID != "factura-detalle" || pairs[0].BID != "detalle-factura" || pairs[0].Method != capture.ByAI || pairs[0].Confidence != 0.93 {
		t.Fatalf("pairs: %+v", pairs)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "ayuda  ->  (no match)") {
		t.Errorf("a low-confidence answer is shown as no match: %v", logs)
	}
	// It is saved where a person can correct it.
	saved, err := capture.LoadPairs(root)
	if err != nil || len(saved[key]) != 2 {
		t.Fatalf("saved: %+v %v", saved, err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(capture.PairsPath))); err != nil {
		t.Error(err)
	}
	// The next run does not ask again: the file already answers both screens.
	jobs := len(stub.Jobs)
	pairs = e.pairWithAI(context.Background(), req, root, key, saved, removed, added, emit)
	if len(stub.Jobs) != jobs || len(pairs) != 1 {
		t.Errorf("asked again (%d jobs) or lost the pair: %+v", len(stub.Jobs), pairs)
	}
	// --no-ai asks nothing.
	if p := e.pairWithAI(context.Background(), ComparisonRequest{Project: "app", NoAI: true}, root, key, capture.PairFile{}, removed, added, emit); len(p) != 0 || len(stub.Jobs) != jobs {
		t.Errorf("--no-ai: %+v", p)
	}
}

func TestPairWithAIFailureNeverFailsTheComparison(t *testing.T) {
	stub := &ai.Stub{Rules: []ai.StubRule{{Contains: "FIRST version", Err: ai.ErrLimit}}}
	e, root := pairingEngine(t, stub)
	roles, _ := e.AIRoles("")
	_ = e.GiveGenerateConsent("app", roles)
	var logs []string
	pairs := e.pairWithAI(context.Background(), ComparisonRequest{Project: "app"}, root, "a..b", capture.PairFile{},
		[]capture.ScreenInfo{{ID: "x"}}, []capture.ScreenInfo{{ID: "y"}}, func(ev CaptureEvent) { logs = append(logs, ev.Text) })
	if len(pairs) != 0 || !strings.Contains(strings.Join(logs, "\n"), "AI matching failed") {
		t.Errorf("%v %v", pairs, logs)
	}
}
