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

const vMap = `{"framework":"x","entrada":"inicio","rutas":[{"id":"inicio","ruta":"/","titulo":"Inicio","origen":["src/inicio.html"],"prioridad":"alta"}],"flujos":[]}`
const vScreenBad = `{"pantalla":{"id":"inicio","ruta":"/","estados":[{"id":"base"}]},"llamadas":{"base":[{"metodo":"GET","ruta":"/api/uno"}]}}`
const vScreenFixed = `{"pantalla":{"id":"inicio","ruta":"/","estados":[{"id":"base"}]},"llamadas":{"base":[{"metodo":"GET","ruta":"/api/uno"},{"metodo":"GET","ruta":"/api/dos"}]}}`
const vDataBad = `{"estados":{"base":[{"metodo":"GET","ruta":"/api/uno","estado":200,"json":{}}]}}`
const vDataFixed = `{"estados":{"base":[{"metodo":"GET","ruta":"/api/uno","estado":200,"json":{}},{"metodo":"GET","ruta":"/api/dos","estado":200,"json":{}}]}}`

// A screen whose capture fails because a call has no mock is repaired once
// the AI is told what the real capture said.
func TestVerifyRepairsWhatFailsToLoad(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "src", "inicio.html"), []byte("<h1>x</h1>"), 0o644)
	stub := &ai.Stub{Rules: []ai.StubRule{
		{Contains: "List the screens", Replies: []string{vMap}},
		{Contains: "A real capture of this screen", Replies: []string{vScreenFixed}},
		{Contains: "Screen id: inicio", Replies: []string{vScreenBad}},
		{Contains: "A real capture failed", Replies: []string{vDataFixed}},
		{Contains: "Write the answers", Replies: []string{vDataBad}},
	}}
	e, _ := pairingEngine(t, stub)
	dir := t.TempDir()
	g, err := capture.NewGenerator(capture.GenConfig{Root: repo, App: "App", RunDir: dir, Roles: ai.Roles{Coordinator: ai.Ref{Provider: "stub"}, Worker: ai.Ref{Provider: "stub"}}, Providers: func(string) (ai.Provider, error) { return stub, nil }})
	if err != nil {
		t.Fatal(err)
	}
	res, err := g.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	oldS, oldC := verifySession, verifyCapture
	t.Cleanup(func() { verifySession, verifyCapture = oldS, oldC })
	stopped := false
	verifySession = func(context.Context, *Engine, GenerateRequest, string, func(CaptureEvent)) (string, func(), error) {
		return "http://127.0.0.1:1/", func() { stopped = true }, nil
	}
	var rounds int
	verifyCapture = func(_ context.Context, _ *Engine, draft string, inv *capture.Inventory, _ string, _ string) ([]capture.Shot, error) {
		rounds++
		// The app calls /api/dos: it works only when the draft has a mock for it.
		has := false
		for _, p := range inv.Pantallas {
			for _, st := range p.Estados {
				_, ok := st.Datos["GET /api/dos"]
				has = has || ok
			}
		}
		if !has {
			return []capture.Shot{{Screen: "inicio", State: "base", Viewport: "desktop", Status: capture.ShotFailed, Reason: "the app asked GET /api/dos and no mock answers it"}}, nil
		}
		return []capture.Shot{{Screen: "inicio", State: "base", Viewport: "desktop", Status: capture.ShotOK}}, nil
	}
	var events []string
	out, err := e.verifyGenerated(context.Background(), GenerateRequest{CaptureTarget: CaptureTarget{Project: "app"}, Verify: true}, &GenerateEstimate{Worktree: repo}, dir, g, res, func(ev capture.GenEvent) { events = append(events, ev.Kind+":"+ev.Text) })
	if err != nil {
		t.Fatal(err)
	}
	if rounds != 2 || out.Verify == nil || out.Verify.Rounds != 2 || len(out.Verify.Repaired) != 1 || out.Verify.Repaired[0] != "inicio" || len(out.Verify.Failing) != 0 {
		t.Fatalf("rounds %d, verify %+v", rounds, out.Verify)
	}
	if !stopped {
		t.Error("the application must be stopped")
	}
	if !strings.Contains(strings.Join(events, "\n"), "GET /api/dos and no mock") {
		t.Errorf("the failure must be reported: %v", events)
	}
	// The repair prompt carried what the capture said.
	found := false
	for _, j := range stub.Jobs {
		found = found || (strings.Contains(j.Prompt, "A real capture of this screen") && strings.Contains(j.Prompt, "GET /api/dos"))
	}
	if !found {
		t.Error("the AI must be told what failed")
	}
}

func TestVerifyGivesUpAfterTwoRepairs(t *testing.T) {
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, "src"), 0o755)
	os.WriteFile(filepath.Join(repo, "src", "inicio.html"), []byte("x"), 0o644)
	stub := &ai.Stub{Rules: []ai.StubRule{
		{Contains: "List the screens", Replies: []string{vMap}},
		{Contains: "Screen id: inicio", Replies: []string{vScreenFixed}},
		{Contains: "Write the answers", Replies: []string{vDataFixed}},
	}}
	e, _ := pairingEngine(t, stub)
	dir := t.TempDir()
	g, _ := capture.NewGenerator(capture.GenConfig{Root: repo, App: "App", RunDir: dir, Roles: ai.Roles{Coordinator: ai.Ref{Provider: "stub"}, Worker: ai.Ref{Provider: "stub"}}, Providers: func(string) (ai.Provider, error) { return stub, nil }})
	res, err := g.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	oldS, oldC := verifySession, verifyCapture
	t.Cleanup(func() { verifySession, verifyCapture = oldS, oldC })
	verifySession = func(context.Context, *Engine, GenerateRequest, string, func(CaptureEvent)) (string, func(), error) {
		return "http://127.0.0.1:1/", func() {}, nil
	}
	rounds := 0
	verifyCapture = func(context.Context, *Engine, string, *capture.Inventory, string, string) ([]capture.Shot, error) {
		rounds++
		return []capture.Shot{{Screen: "inicio", State: "base", Status: capture.ShotFailed, Reason: "always broken"}}, nil
	}
	out, err := e.verifyGenerated(context.Background(), GenerateRequest{}, &GenerateEstimate{Worktree: repo}, dir, g, res, func(capture.GenEvent) {})
	if err != nil {
		t.Fatal(err)
	}
	if rounds != 3 || len(out.Verify.Failing) != 1 || out.Verify.Failing[0] != "inicio" || len(out.Verify.Repaired) != 0 {
		t.Errorf("rounds %d, %+v", rounds, out.Verify)
	}
}
