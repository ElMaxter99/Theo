package capture

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ElMaxter99/Theo/internal/ai"
)

func genRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for f, c := range map[string]string{"src/login.html": "<form></form>", "src/facturas.html": "<table></table>", "src/facturas.js": "$http.get('/api/facturas')"} {
		p := filepath.Join(root, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(c), 0o644)
	}
	return root
}

const mapReply = `{"framework":"angularjs","entrada":"login","rutas":[
 {"id":"login","ruta":"/login","titulo":"Login","origen":["src/login.html"],"prioridad":"alta"},
 {"id":"facturas","ruta":"/facturas","titulo":"Facturas","origen":["src/facturas.html","src/facturas.js"],"prioridad":"alta"}],
 "flujos":[{"id":"entrar","pasos":["login#base","facturas#base"]},{"id":"roto","pasos":["nada#base"]}]}`

const loginReply = `{"pantalla":{"id":"login","titulo":"Login","ruta":"/login","estados":[{"id":"base"}]},"llamadas":{}}`
const facturasBad = `{"pantalla":{"id":"facturas","ruta":"/facturas","estados":[{"id":"Base!"}]},"llamadas":{}}`
const facturasOK = `{"pantalla":{"id":"facturas","ruta":"/facturas","estados":[{"id":"base"},{"id":"vacio"}]},"llamadas":{"base":[{"metodo":"GET","ruta":"/api/facturas"}],"vacio":[{"metodo":"GET","ruta":"/api/facturas"}]}}`
const dataOK = `{"estados":{"base":[{"metodo":"GET","ruta":"/api/facturas","estado":200,"json":[{"id":1,"cliente":"Acme"}]}],"vacio":[{"metodo":"GET","ruta":"/api/facturas","estado":200,"json":[]}]}}`

func newGen(t *testing.T, root string, stub *ai.Stub, events *[]GenEvent) *Generator {
	t.Helper()
	g, err := NewGenerator(GenConfig{
		Root: root, App: "Portal", RunDir: filepath.Join(t.TempDir(), "run1"),
		Roles:     ai.Roles{Coordinator: ai.Ref{Provider: "stub", Model: "big"}, Worker: ai.Ref{Provider: "stub", Model: "small"}},
		Providers: func(string) (ai.Provider, error) { return stub, nil },
		Parallel:  2,
		Emit: func(e GenEvent) {
			if events != nil {
				*events = append(*events, e)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGenerateDraft(t *testing.T) {
	root := genRepo(t)
	stub := &ai.Stub{Rules: []ai.StubRule{
		{Contains: "List the screens", Replies: []string{mapReply}},
		{Contains: "Screen id: login", Replies: []string{loginReply}},
		// the first answer for facturas is invalid and is sent back
		{Contains: "Screen id: facturas", Replies: []string{facturasBad, facturasOK}},
		{Contains: "Write the answers", Replies: []string{dataOK}},
	}}
	var events []GenEvent
	g := newGen(t, root, stub, &events)
	res, err := g.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Screens != 2 || res.States != 3 || res.Flows != 1 || res.Retries != 1 || len(res.NotGenerated) != 0 {
		t.Fatalf("result: %+v", res)
	}
	// The repository was not written.
	if _, err := os.Stat(filepath.Join(root, ".theo")); err == nil {
		t.Error("the generation must not write in the worktree")
	}
	b, err := os.ReadFile(filepath.Join(res.Dir, "draft", filepath.FromSlash(InventoryPath)))
	if err != nil {
		t.Fatal(err)
	}
	inv, err := ParseInventory(b)
	if err != nil || len(inv.Validate()) > 0 {
		t.Fatalf("the draft must be a valid inventory: %v %v", err, inv.Validate())
	}
	if inv.Entrada != "login" || inv.Mocks.Modo != "mock" {
		t.Errorf("entry/mocks: %+v", inv)
	}
	var datos string
	for _, p := range inv.Pantallas {
		if p.ID == "facturas" {
			datos = string(p.Estados[0].Datos["GET /api/facturas"])
		}
	}
	if !strings.Contains(datos, MocksDir+"/facturas-base-1.json") {
		t.Errorf("datos: %s", datos)
	}
	mock, err := os.ReadFile(filepath.Join(res.Dir, "draft", filepath.FromSlash(MocksDir), "facturas-base-1.json"))
	if err != nil || !strings.Contains(string(mock), "Acme") {
		t.Errorf("mock file: %s %v", mock, err)
	}
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	k := strings.Join(kinds, ",")
	for _, want := range []string{"plan", "retry", "job", "done"} {
		if !strings.Contains(k, want) {
			t.Errorf("events lack %q: %s", want, k)
		}
	}
}

func TestGenerateResumeReusesAnswers(t *testing.T) {
	root := genRepo(t)
	rules := func(fail bool) []ai.StubRule {
		r := []ai.StubRule{
			{Contains: "List the screens", Replies: []string{mapReply}},
			{Contains: "Screen id: login", Replies: []string{loginReply}},
			{Contains: "Screen id: facturas", Replies: []string{facturasOK}},
			{Contains: "Write the answers", Replies: []string{dataOK}},
		}
		if fail {
			r[3] = ai.StubRule{Contains: "Write the answers", Err: ai.ErrLimit}
		}
		return r
	}
	stub := &ai.Stub{Rules: rules(true)}
	g := newGen(t, root, stub, nil)
	if _, err := g.Run(context.Background()); !errors.Is(err, ai.ErrLimit) {
		t.Fatalf("want the limit error, got %v", err)
	}
	first := len(stub.Jobs)
	// Resume in the same run folder with a provider that works.
	stub2 := &ai.Stub{Rules: rules(false)}
	g2, _ := NewGenerator(GenConfig{Root: root, App: "Portal", RunDir: g.cfg.RunDir, Roles: g.cfg.Roles, Providers: func(string) (ai.Provider, error) { return stub2, nil }})
	res, err := g2.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Screens != 2 {
		t.Fatalf("%+v", res)
	}
	for _, j := range stub2.Jobs {
		if strings.Contains(j.Prompt, "List the screens") || strings.Contains(j.Prompt, "Screen id:") {
			t.Errorf("a saved answer was asked again: %.60s", j.Prompt)
		}
	}
	if first < 3 {
		t.Errorf("first run did too little: %d", first)
	}
}

func TestGenerateReportsWhatFailed(t *testing.T) {
	root := genRepo(t)
	var n atomic.Int32
	stub := &ai.Stub{Rules: []ai.StubRule{
		{Contains: "List the screens", Replies: []string{mapReply}},
		{Contains: "Screen id: login", Replies: []string{loginReply}},
		{Contains: "Screen id: facturas", Replies: []string{"not json at all"}},
	}}
	_ = n
	g := newGen(t, root, stub, nil)
	res, err := g.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Screens != 1 || len(res.NotGenerated) != 1 || res.NotGenerated[0].Screen != "facturas" {
		t.Fatalf("a failed screen must be listed, never dropped in silence: %+v", res)
	}
	// 1 attempt + 2 retries for facturas
	count := 0
	for _, j := range stub.Jobs {
		if strings.Contains(j.Prompt, "Screen id: facturas") {
			count++
		}
	}
	if count != 3 {
		t.Errorf("facturas was asked %d times, want 3", count)
	}
}

func TestExploreRejectsFilesThatDoNotExist(t *testing.T) {
	root := genRepo(t)
	bad := `{"framework":"x","rutas":[{"id":"a","ruta":"/a","origen":["../../etc/passwd"]}]}`
	stub := &ai.Stub{Rules: []ai.StubRule{{Contains: "List the screens", Replies: []string{bad}}}}
	g := newGen(t, root, stub, nil)
	if _, err := g.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "origen") {
		t.Fatalf("a file outside the worktree must be rejected: %v", err)
	}
}

func TestNewGeneratorNeedsRoles(t *testing.T) {
	if _, err := NewGenerator(GenConfig{Root: "x", RunDir: "y"}); err == nil || !strings.Contains(err.Error(), "both roles") {
		t.Errorf("%v", err)
	}
	var _ = json.RawMessage{}
}
