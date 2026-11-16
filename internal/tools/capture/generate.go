package capture

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ElMaxter99/Theo/internal/ai"
)

//go:embed prompts/explore.md
var promptExplore string

//go:embed prompts/screen.md
var promptScreen string

//go:embed prompts/data.md
var promptData string

// promptVersion changes when a prompt or a schema does, so saved answers of an
// older version are not reused.
const promptVersion = "1"

// GenConfig is a generation of an inventory with AI.
type GenConfig struct {
	// Root is the worktree to read (read only). Nothing is written there.
	Root string
	// App names the application in the inventory.
	App string
	// RunDir keeps the run: state, answers and the draft.
	RunDir string
	Roles  ai.Roles
	// Providers finds a provider by ID.
	Providers func(id string) (ai.Provider, error)
	// Parallel is how many jobs run at once (default 3).
	Parallel int
	// Retries is how many times a bad answer is sent back (default 2).
	Retries int
	// JobTimeout bounds one job (default 5 minutes).
	JobTimeout time.Duration
	// Confirm is asked once the exploration is done, with the number of
	// screens and of jobs; false stops the run (what was done is kept).
	Confirm func(routes, jobs int, framework string) bool
	// Emit reports progress; calls are serialized.
	Emit func(GenEvent)
}

// GenEvent tells how a generation goes.
type GenEvent struct {
	Kind  string // plan, phase, job, retry, failed, done
	Phase string // explore, screens, data, merge
	Job   string
	Done  int
	Total int
	Text  string
}

// GenFailure is something that could not be generated.
type GenFailure struct {
	Screen string `json:"screen"`
	Phase  string `json:"phase"`
	Error  string `json:"error"`
}

// GenResult is the outcome of a generation.
type GenResult struct {
	RunID        string       `json:"run"`
	Dir          string       `json:"dir"`
	Screens      int          `json:"screens"`
	States       int          `json:"states"`
	Flows        int          `json:"flows"`
	NoData       []string     `json:"no_data,omitempty"`
	NotGenerated []GenFailure `json:"not_generated,omitempty"`
	Framework    string       `json:"framework,omitempty"`
	Jobs         int          `json:"jobs"`
	Retries      int          `json:"retries"`
	// Verify is set when the draft was captured with the real application.
	Verify *GenVerify `json:"verify,omitempty"`
}

// GenVerify is what the capture of the draft with the real application found.
type GenVerify struct {
	Rounds   int      `json:"rounds"`
	Repaired []string `json:"repaired,omitempty"` // screens that failed and work after a repair
	Failing  []string `json:"failing,omitempty"`  // screens that still fail
}

// RouteMap is what the exploration returns.
type RouteMap struct {
	Framework string     `json:"framework"`
	Entrada   string     `json:"entrada"`
	Rutas     []MapRoute `json:"rutas"`
	Flujos    []Flow     `json:"flujos"`
}

// MapRoute is one screen found by the exploration.
type MapRoute struct {
	ID        string   `json:"id"`
	Ruta      string   `json:"ruta"`
	Titulo    string   `json:"titulo"`
	Origen    []string `json:"origen"`
	Prioridad string   `json:"prioridad"`
}

type genCall struct {
	Metodo string `json:"metodo"`
	Ruta   string `json:"ruta"`
}

type screenAnswer struct {
	Pantalla Screen               `json:"pantalla"`
	Llamadas map[string][]genCall `json:"llamadas"`
}

type genReply struct {
	Metodo string          `json:"metodo"`
	Ruta   string          `json:"ruta"`
	Estado int             `json:"estado"`
	JSON   json.RawMessage `json:"json"`
}

type dataAnswer struct {
	Estados map[string][]genReply `json:"estados"`
}

type genJob struct {
	Phase       string `json:"phase"`
	Key         string `json:"key"`
	Status      string `json:"status"` // done or failed
	Attempts    int    `json:"attempts"`
	Fingerprint string `json:"fingerprint"`
	Error       string `json:"error,omitempty"`
}

type genState struct {
	ID      string             `json:"id"`
	Started time.Time          `json:"started"`
	Jobs    map[string]*genJob `json:"jobs"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// DefaultViewports are the windows of a generated inventory.
func DefaultViewports() []Viewport {
	return []Viewport{{ID: "desktop", Width: 1280, Height: 720}, {ID: "movil", Width: 390, Height: 800, Mobile: true}}
}

// Generator runs the generation. Create it with NewGenerator.
type Generator struct {
	cfg   GenConfig
	mu    sync.Mutex
	state *genState
	retry int
	hints map[string]string // screen id -> what a real capture said (see SetHints)
}

// NewGenerator checks the configuration.
func NewGenerator(cfg GenConfig) (*Generator, error) {
	if cfg.Root == "" || cfg.RunDir == "" {
		return nil, errors.New("a generation needs the worktree and a run folder")
	}
	if cfg.Roles.Coordinator.Provider == "" || cfg.Roles.Worker.Provider == "" {
		return nil, errors.New("choose a provider and model for both roles first (theo config set ai.roles.coordinator claude:<model>, and ai.roles.worker)")
	}
	if cfg.Providers == nil {
		cfg.Providers = ai.Get
	}
	if cfg.Parallel <= 0 {
		cfg.Parallel = 3
	}
	if cfg.Retries < 0 {
		cfg.Retries = 0
	} else if cfg.Retries == 0 {
		cfg.Retries = 2
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = 5 * time.Minute
	}
	emit := cfg.Emit
	var emitMu sync.Mutex
	cfg.Emit = func(e GenEvent) { // one at a time, so callers need no lock
		if emit == nil {
			return
		}
		emitMu.Lock()
		defer emitMu.Unlock()
		emit(e)
	}
	if cfg.App == "" {
		cfg.App = filepath.Base(cfg.Root)
	}
	return &Generator{cfg: cfg}, nil
}

func (g *Generator) provider(r ai.Ref) (ai.Provider, error) { return g.cfg.Providers(r.Provider) }

func (g *Generator) load() error {
	g.state = &genState{Jobs: map[string]*genJob{}}
	b, err := os.ReadFile(filepath.Join(g.cfg.RunDir, "run.json"))
	if err == nil {
		_ = json.Unmarshal(b, g.state)
		if g.state.Jobs == nil {
			g.state.Jobs = map[string]*genJob{}
		}
	}
	if g.state.ID == "" {
		g.state.ID = filepath.Base(g.cfg.RunDir)
		g.state.Started = time.Now()
	}
	return os.MkdirAll(filepath.Join(g.cfg.RunDir, "jobs"), 0o755)
}

func (g *Generator) save() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	b, err := json.MarshalIndent(g.state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(g.cfg.RunDir, "run.json"), b, 0o644)
}

func jobFile(phase, key string) string { return phase + "-" + safeName(key) + ".json" }

// fingerprint says whether an answer is still good: the files it was made from,
// the model and the version of the prompts.
func (g *Generator) fingerprint(phase, key, model string, files []string, extra string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s\n", promptVersion, phase, key, model, extra)
	sort.Strings(files)
	for _, f := range files {
		p, err := Within(g.cfg.Root, f)
		if err != nil {
			continue
		}
		if b, err := os.ReadFile(p); err == nil {
			fmt.Fprintf(h, "%s=%x\n", f, sha256.Sum256(b))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// cached returns a saved answer when the job is done with the same fingerprint.
func (g *Generator) cached(phase, key, fp string) ([]byte, bool) {
	g.mu.Lock()
	j := g.state.Jobs[phase+"/"+key]
	g.mu.Unlock()
	if j == nil || j.Status != "done" || j.Fingerprint != fp {
		return nil, false
	}
	b, err := os.ReadFile(filepath.Join(g.cfg.RunDir, "jobs", jobFile(phase, key)))
	return b, err == nil
}

func (g *Generator) record(phase, key, fp string, attempts int, ans []byte, jerr error) {
	j := &genJob{Phase: phase, Key: key, Attempts: attempts, Fingerprint: fp}
	if jerr != nil {
		j.Status, j.Error = "failed", jerr.Error()
	} else {
		j.Status = "done"
		_ = os.WriteFile(filepath.Join(g.cfg.RunDir, "jobs", jobFile(phase, key)), ans, 0o644)
	}
	g.mu.Lock()
	g.state.Jobs[phase+"/"+key] = j
	if attempts > 1 {
		g.retry += attempts - 1
	}
	g.mu.Unlock()
	_ = g.save()
}

// ask runs one job with its retries. validate checks the answer and returns a
// sentence about what is wrong, which is sent back to the provider.
func (g *Generator) ask(ctx context.Context, ref ai.Ref, system, prompt string, schema json.RawMessage, validate func(json.RawMessage) error, label string) (json.RawMessage, int, error) {
	p, err := g.provider(ref)
	if err != nil {
		return nil, 0, err
	}
	var lastErr error
	for attempt := 1; attempt <= g.cfg.Retries+1; attempt++ {
		q := prompt
		if lastErr != nil {
			q += "\n\nYour previous answer was rejected: " + lastErr.Error() + "\nAnswer again with a corrected JSON object."
		}
		res, err := p.Run(ctx, ai.Job{Model: ref.Model, System: system, Prompt: q, WorkDir: g.cfg.Root, Schema: schema, Timeout: g.cfg.JobTimeout, MaxTurns: 12})
		if err == nil {
			if verr := validate(res.JSON); verr == nil {
				return res.JSON, attempt, nil
			} else {
				err = fmt.Errorf("%w: %v", ai.ErrBadAnswer, verr)
			}
		}
		if errors.Is(err, ai.ErrLimit) || ctx.Err() != nil {
			return nil, attempt, err
		}
		if errors.Is(err, ai.ErrBadAnswer) || errors.Is(err, ai.ErrTimeout) {
			lastErr = err
			if attempt <= g.cfg.Retries {
				g.cfg.Emit(GenEvent{Kind: "retry", Job: label, Text: strings.TrimPrefix(err.Error(), ai.ErrBadAnswer.Error()+": ") + fmt.Sprintf(" (retry %d of %d)", attempt, g.cfg.Retries)})
			}
			continue
		}
		return nil, attempt, err
	}
	return nil, g.cfg.Retries + 1, lastErr
}

// existingFiles reports the files that do not exist under the worktree.
func (g *Generator) missingFiles(files []string) []string {
	var miss []string
	for _, f := range files {
		p, err := Within(g.cfg.Root, f)
		if err != nil {
			miss = append(miss, f)
			continue
		}
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			miss = append(miss, f)
		}
	}
	return miss
}

var schemaMap = json.RawMessage(`{"type":"object","required":["rutas"],"properties":{"framework":{"type":"string"},"entrada":{"type":"string"},"rutas":{"type":"array","items":{"type":"object","required":["id","ruta","origen"]}},"flujos":{"type":"array"}}}`)
var schemaScreen = json.RawMessage(`{"type":"object","required":["pantalla","llamadas"],"properties":{"pantalla":{"type":"object","required":["id","ruta","estados"]},"llamadas":{"type":"object"}}}`)
var schemaData = json.RawMessage(`{"type":"object","required":["estados"],"properties":{"estados":{"type":"object"}}}`)

func (g *Generator) validateMap(raw json.RawMessage) error {
	var m RouteMap
	if err := strictJSON(raw, &m); err != nil {
		return err
	}
	if len(m.Rutas) == 0 {
		return errors.New(`"rutas" is empty: list at least one screen`)
	}
	seen := map[string]bool{}
	for _, r := range m.Rutas {
		switch {
		case !idPattern.MatchString(r.ID):
			return fmt.Errorf(`screen id %q: use lowercase letters, digits and dashes`, r.ID)
		case seen[r.ID]:
			return fmt.Errorf(`screen id %q is repeated`, r.ID)
		case !strings.HasPrefix(r.Ruta, "/") && !strings.HasPrefix(r.Ruta, "#"):
			return fmt.Errorf(`screen %q: "ruta" %q must start with / or #`, r.ID, r.Ruta)
		}
		seen[r.ID] = true
		if miss := g.missingFiles(r.Origen); len(miss) > 0 {
			return fmt.Errorf(`screen %q: these "origen" files do not exist: %s`, r.ID, strings.Join(miss, ", "))
		}
	}
	return nil
}

func (g *Generator) validateScreen(route MapRoute, raw json.RawMessage) error {
	var a screenAnswer
	if err := strictJSON(raw, &a); err != nil {
		return err
	}
	a.Pantalla.ID, a.Pantalla.Ruta, a.Pantalla.Origen = route.ID, route.Ruta, route.Origen
	for _, st := range a.Pantalla.Estados {
		if !idPattern.MatchString(st.ID) {
			return fmt.Errorf(`state id %q: use lowercase letters, digits and dashes`, st.ID)
		}
	}
	inv := Inventory{Version: 1, App: "x", Viewports: DefaultViewports(), Pantallas: []Screen{a.Pantalla}}
	if errs := inv.Validate(); len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	for st := range a.Llamadas {
		found := false
		for _, s := range a.Pantalla.Estados {
			found = found || s.ID == st
		}
		if !found {
			return fmt.Errorf(`"llamadas" names the state %q, which is not in "estados"`, st)
		}
	}
	for st, calls := range a.Llamadas {
		for _, c := range calls {
			if c.Metodo == "" || !strings.HasPrefix(c.Ruta, "/") {
				return fmt.Errorf(`state %q: a call needs "metodo" and a "ruta" that starts with /`, st)
			}
		}
	}
	return nil
}

func (g *Generator) validateData(sc screenAnswer, raw json.RawMessage) error {
	var d dataAnswer
	if err := strictJSON(raw, &d); err != nil {
		return err
	}
	for st, calls := range sc.Llamadas {
		for _, c := range calls {
			ok := false
			for _, r := range d.Estados[st] {
				if strings.EqualFold(r.Metodo, c.Metodo) && r.Ruta == c.Ruta && len(r.JSON) > 0 {
					ok = true
				}
			}
			if !ok {
				return fmt.Errorf(`state %q: no answer for %s %s`, st, c.Metodo, c.Ruta)
			}
		}
	}
	return nil
}

func strictJSON(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return errors.New("the answer holds no JSON object")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("the JSON does not have the shape asked for: %v", err)
	}
	return nil
}

// ErrGenCancelled means the person did not confirm the plan.
var ErrGenCancelled = errors.New("generation cancelled before it started")

// SetHints tells the next Run which screens failed a real capture and why:
// those screens (and their data) are asked again with the problem, and the
// rest keep their saved answers.
func (g *Generator) SetHints(h map[string]string) {
	g.mu.Lock()
	g.hints = h
	g.cfg.Confirm = nil // the plan was confirmed once: a repair does not ask again
	g.mu.Unlock()
}

func (g *Generator) hint(screen string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hints[screen]
}

// Estimate says how many jobs a generation of n screens takes.
func Estimate(screens int) int { return 1 + 2*screens }

// Explore runs phase 0 (or reuses its saved answer) and returns the route map.
func (g *Generator) explore(ctx context.Context) (*RouteMap, error) {
	if err := g.load(); err != nil {
		return nil, err
	}
	fp := g.fingerprint("explore", "map", g.cfg.Roles.Coordinator.String(), nil, "")
	g.cfg.Emit(GenEvent{Kind: "phase", Phase: "explore", Text: "exploring the project"})
	raw, hit := g.cached("explore", "map", fp)
	if !hit {
		ans, n, err := g.ask(ctx, g.cfg.Roles.Coordinator, promptExplore, "List the screens of this project.", schemaMap, g.validateMap, "explore")
		if err != nil {
			g.record("explore", "map", fp, n, nil, err)
			return nil, fmt.Errorf("exploration failed: %w", err)
		}
		raw = ans
		g.record("explore", "map", fp, n, raw, nil)
	}
	var m RouteMap
	if err := strictJSON(raw, &m); err != nil {
		return nil, err
	}
	_ = os.WriteFile(filepath.Join(g.cfg.RunDir, "mapa.json"), raw, 0o644)
	return &m, nil
}

// Run generates the inventory draft in RunDir/draft and returns what it holds.
func (g *Generator) Run(ctx context.Context) (*GenResult, error) {
	m, err := g.explore(ctx)
	if err != nil {
		return nil, err
	}
	total := Estimate(len(m.Rutas))
	if g.cfg.Confirm != nil && !g.cfg.Confirm(len(m.Rutas), total, m.Framework) {
		return nil, ErrGenCancelled
	}
	g.cfg.Emit(GenEvent{Kind: "plan", Total: total, Text: fmt.Sprintf("%d screens found (%s)", len(m.Rutas), m.Framework)})
	done := 1
	var doneMu sync.Mutex
	tick := func(phase, job string) {
		doneMu.Lock()
		done++
		d := done
		doneMu.Unlock()
		g.cfg.Emit(GenEvent{Kind: "job", Phase: phase, Job: job, Done: d, Total: total})
	}

	screens := make([]*screenAnswer, len(m.Rutas))
	var fails []GenFailure
	var failMu sync.Mutex
	fail := func(screen, phase string, err error) {
		failMu.Lock()
		fails = append(fails, GenFailure{Screen: screen, Phase: phase, Error: err.Error()})
		failMu.Unlock()
		g.cfg.Emit(GenEvent{Kind: "failed", Phase: phase, Job: screen, Text: err.Error()})
	}
	var limitErr error
	var limitMu sync.Mutex

	pool := func(phase string, n int, fn func(i int)) {
		g.cfg.Emit(GenEvent{Kind: "phase", Phase: phase, Total: n})
		sem := make(chan struct{}, g.cfg.Parallel)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			limitMu.Lock()
			stop := limitErr != nil
			limitMu.Unlock()
			if stop || ctx.Err() != nil {
				break
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(i int) {
				defer func() { <-sem; wg.Done() }()
				fn(i)
			}(i)
		}
		wg.Wait()
	}
	onErr := func(screen, phase string, err error) {
		if errors.Is(err, ai.ErrLimit) {
			limitMu.Lock()
			limitErr = err
			limitMu.Unlock()
			return
		}
		if ctx.Err() == nil {
			fail(screen, phase, err)
		}
	}

	pool("screens", len(m.Rutas), func(i int) {
		r := m.Rutas[i]
		hint := g.hint(r.ID)
		fp := g.fingerprint("screen", r.ID, g.cfg.Roles.Worker.String(), r.Origen, r.Ruta+"|"+hint)
		raw, hit := g.cached("screen", r.ID, fp)
		if !hit {
			prompt := fmt.Sprintf("%s\n\nScreen id: %s\nTitle: %s\nRoute: %s\nPriority: %s\nFiles: %s\nThe application is %s (%s).", "Describe this screen.", r.ID, r.Titulo, r.Ruta, r.Prioridad, strings.Join(r.Origen, ", "), g.cfg.App, m.Framework)
			if hint != "" {
				prompt += "\n\nA real capture of this screen with the data you gave failed: " + hint + "\nCorrect the states, actions or calls so that it works."
			}
			ans, n, err := g.ask(ctx, g.cfg.Roles.Worker, promptScreen, prompt, schemaScreen, func(raw json.RawMessage) error { return g.validateScreen(r, raw) }, "screen "+r.ID)
			if err != nil {
				g.record("screen", r.ID, fp, n, nil, err)
				onErr(r.ID, "screens", err)
				return
			}
			raw = ans
			g.record("screen", r.ID, fp, n, raw, nil)
		}
		var a screenAnswer
		if strictJSON(raw, &a) != nil {
			return
		}
		a.Pantalla.ID, a.Pantalla.Ruta, a.Pantalla.Origen = r.ID, r.Ruta, r.Origen
		if a.Pantalla.Titulo == "" {
			a.Pantalla.Titulo = r.Titulo
		}
		if a.Pantalla.Prioridad == "" {
			a.Pantalla.Prioridad = r.Prioridad
		}
		screens[i] = &a
		tick("screens", r.ID)
	})
	if limitErr != nil {
		return nil, fmt.Errorf("%w (progress saved: --resume)", limitErr)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	datos := make([]map[string][]genReply, len(m.Rutas))
	pool("data", len(m.Rutas), func(i int) {
		a := screens[i]
		if a == nil {
			return
		}
		id := a.Pantalla.ID
		if !hasCalls(a) {
			tick("data", id)
			return
		}
		callsJSON, _ := json.Marshal(a.Llamadas)
		hint := g.hint(id)
		fp := g.fingerprint("data", id, g.cfg.Roles.Worker.String(), a.Pantalla.Origen, string(callsJSON)+"|"+hint)
		raw, hit := g.cached("data", id, fp)
		if !hit {
			prompt := fmt.Sprintf("Write the answers for the calls of the screen %q (files: %s).\nCalls by state: %s", id, strings.Join(a.Pantalla.Origen, ", "), callsJSON)
			if hint != "" {
				prompt += "\nA real capture failed: " + hint + "\nMake sure every call the screen makes has an answer."
			}
			ans, n, err := g.ask(ctx, g.cfg.Roles.Worker, promptData, prompt, schemaData, func(raw json.RawMessage) error { return g.validateData(*a, raw) }, "data "+id)
			if err != nil {
				g.record("data", id, fp, n, nil, err)
				onErr(id, "data", err)
				return
			}
			raw = ans
			g.record("data", id, fp, n, raw, nil)
		}
		var d dataAnswer
		if strictJSON(raw, &d) == nil {
			datos[i] = d.Estados
		}
		tick("data", id)
	})
	if limitErr != nil {
		return nil, fmt.Errorf("%w (progress saved: --resume)", limitErr)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return g.merge(m, screens, datos, fails)
}

func hasCalls(a *screenAnswer) bool {
	for _, c := range a.Llamadas {
		if len(c) > 0 {
			return true
		}
	}
	return false
}

// merge joins the answers into a draft inventory and mocks, validates it and
// writes it under RunDir/draft. Nothing in the repository changes.
func (g *Generator) merge(m *RouteMap, screens []*screenAnswer, datos []map[string][]genReply, fails []GenFailure) (*GenResult, error) {
	g.cfg.Emit(GenEvent{Kind: "phase", Phase: "merge", Text: "joining and checking"})
	draft := filepath.Join(g.cfg.RunDir, "draft")
	_ = os.RemoveAll(draft)
	mockDir := filepath.Join(draft, filepath.FromSlash(MocksDir))
	if err := os.MkdirAll(mockDir, 0o755); err != nil {
		return nil, err
	}
	inv := Inventory{Version: 1, App: g.cfg.App, Viewports: DefaultViewports(), Mocks: Mocks{Modo: "mock", Carpeta: MocksDir}}
	res := &GenResult{RunID: g.state.ID, Dir: g.cfg.RunDir, Framework: m.Framework}
	for i, a := range screens {
		if a == nil {
			continue
		}
		sc := a.Pantalla
		for j := range sc.Estados {
			st := &sc.Estados[j]
			calls := a.Llamadas[st.ID]
			if len(calls) == 0 {
				continue
			}
			st.Datos = map[string]json.RawMessage{}
			for _, c := range calls {
				var reply *genReply
				if datos[i] != nil {
					for k := range datos[i][st.ID] {
						r := datos[i][st.ID][k]
						if strings.EqualFold(r.Metodo, c.Metodo) && r.Ruta == c.Ruta {
							reply = &r
							break
						}
					}
				}
				key := strings.ToUpper(c.Metodo) + " " + c.Ruta
				if reply == nil {
					res.NoData = append(res.NoData, StateKey(sc.ID, st.ID))
					continue
				}
				if reply.Estado == 0 || reply.Estado == 200 {
					name := fmt.Sprintf("%s-%s-%d.json", sc.ID, st.ID, len(st.Datos)+1)
					if err := os.WriteFile(filepath.Join(mockDir, name), reply.JSON, 0o644); err != nil {
						return nil, err
					}
					st.Datos[key], _ = json.Marshal(MocksDir + "/" + name)
				} else {
					st.Datos[key], _ = json.Marshal(map[string]any{"status": reply.Estado, "json": reply.JSON})
				}
			}
			if len(st.Datos) == 0 {
				st.Datos = nil
			}
		}
		inv.Pantallas = append(inv.Pantallas, sc)
	}
	res.NoData = dedupe(res.NoData)
	if len(inv.Pantallas) == 0 {
		return nil, errors.New("no screen could be generated (see the failures of the run)")
	}
	ids := map[string]bool{}
	states := map[string]bool{}
	for _, p := range inv.Pantallas {
		ids[p.ID] = true
		for _, s := range p.Estados {
			states[StateKey(p.ID, s.ID)] = true
		}
	}
	if m.Entrada != "" && ids[strings.SplitN(m.Entrada, "#", 2)[0]] && (!strings.Contains(m.Entrada, "#") || states[m.Entrada]) {
		inv.Entrada = m.Entrada
	}
	for _, f := range m.Flujos {
		ok := f.ID != "" && len(f.Pasos) > 0
		for _, s := range f.Pasos {
			ok = ok && states[s]
		}
		if ok {
			inv.Flujos = append(inv.Flujos, f)
		}
	}
	if errs := inv.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("the draft is not valid: %s", strings.Join(errs, "; "))
	}
	b, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(draft, filepath.FromSlash(InventoryPath)), append(b, '\n'), 0o644); err != nil {
		return nil, err
	}
	sum := inv.Summarize()
	res.Screens, res.States, res.Flows = sum.Screens, sum.States, sum.Flows
	res.NotGenerated = fails
	g.mu.Lock()
	res.Jobs, res.Retries = len(g.state.Jobs), g.retry
	g.mu.Unlock()
	rb, _ := json.MarshalIndent(res, "", "  ")
	_ = os.WriteFile(filepath.Join(g.cfg.RunDir, "resultado.json"), rb, 0o644)
	g.cfg.Emit(GenEvent{Kind: "done", Phase: "merge"})
	return res, nil
}

// MocksDir is where the data of a generated inventory lives, in the repository.
const MocksDir = ".theo/capturas/mocks"

func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
