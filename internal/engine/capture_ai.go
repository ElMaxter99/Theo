package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/ai"
	"github.com/ElMaxter99/Theo/internal/filelock"
	"github.com/ElMaxter99/Theo/internal/tools/capture"
)

// ErrAINotChosen means the roles have no provider yet.
var ErrAINotChosen = errors.New("no AI provider is chosen: run `theo tool ai doctor`, then `theo config set ai.roles.coordinator claude:<model>` and `ai.roles.worker`")

// AIProviderFor finds a provider. The tests replace it with a stub.
var AIProviderFor = ai.Get

// GenerateRequest asks for the inventory of a worktree to be generated with AI.
type GenerateRequest struct {
	CaptureTarget
	// Provider ("claude" or "claude:model") overrides both roles.
	Provider string
	// Parallel is how many jobs run at once (0: 3).
	Parallel int
	// Resume continues the last unfinished run of this worktree.
	Resume bool
	// Confirm is asked after the exploration (see capture.GenConfig).
	Confirm func(routes, jobs int, framework string) bool
}

// AIRoles reads the roles from the settings, or from an override for both.
func (e *Engine) AIRoles(override string) (ai.Roles, error) {
	if override != "" {
		r, err := ai.ParseRef(override)
		if err != nil {
			return ai.Roles{}, err
		}
		return ai.Roles{Coordinator: r, Worker: r}, nil
	}
	roles, err := ai.RolesFrom(e.Prefs().Get)
	if err != nil {
		return ai.Roles{}, err
	}
	if roles.Coordinator.Provider == "" || roles.Worker.Provider == "" {
		return ai.Roles{}, ErrAINotChosen
	}
	return roles, nil
}

func (e *Engine) consentPath() string {
	return filepath.Join(e.dataDir, "tools", "capture", "ai-consent.json")
}

func consentKey(roles ai.Roles) string {
	set := map[string]bool{roles.Coordinator.Provider: true, roles.Worker.Provider: true}
	var ids []string
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return strings.Join(ids, "+")
}

// GenerateConsent says whether the person already accepted that the code of
// the project is sent to these providers.
func (e *Engine) GenerateConsent(project string, roles ai.Roles) bool {
	b, err := os.ReadFile(e.consentPath())
	if err != nil {
		return false
	}
	var m map[string][]string
	if json.Unmarshal(b, &m) != nil {
		return false
	}
	for _, k := range m[project] {
		if k == consentKey(roles) {
			return true
		}
	}
	return false
}

// GiveGenerateConsent remembers the acceptance, per project and provider.
func (e *Engine) GiveGenerateConsent(project string, roles ai.Roles) error {
	if err := os.MkdirAll(filepath.Dir(e.consentPath()), 0o755); err != nil {
		return err
	}
	unlock, err := filelock.Lock(e.consentPath() + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	m := map[string][]string{}
	if b, err := os.ReadFile(e.consentPath()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	k := consentKey(roles)
	for _, have := range m[project] {
		if have == k {
			return nil
		}
	}
	m[project] = append(m[project], k)
	b, _ := json.MarshalIndent(m, "", "  ")
	return filelock.WriteFile(e.consentPath(), append(b, '\n'), 0o600)
}

func (e *Engine) generateRunsDir(project string) string {
	return filepath.Join(e.dataDir, "tools", "capture", "runs", safeProjectDir(project))
}

func safeProjectDir(s string) string {
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
	if s == "" || strings.Trim(s, "_") == "" {
		return "_"
	}
	return s
}

type genTarget struct {
	Worktree string `json:"worktree"`
	Branch   string `json:"branch"`
}

// resolveGenerateWorktree finds the worktree of a request.
func (e *Engine) resolveGenerateWorktree(ctx context.Context, t CaptureTarget) (path, branch string, err error) {
	p, err := e.Project(t.Project)
	if err != nil {
		return "", "", err
	}
	list, err := e.worktreeList(ctx, p)
	if err != nil {
		return "", "", err
	}
	wt, err := selectWorktree(list, t.Selector)
	if err != nil {
		return "", "", fmt.Errorf("project %q: %w", p.Name, err)
	}
	return wt.Path, wt.Branch, nil
}

// GenerateRunDir returns the run to continue (Resume and one exists) or a new one.
func (e *Engine) generateRunDir(project, worktree string, resume bool) (string, bool, error) {
	base := e.generateRunsDir(project)
	if resume {
		entries, _ := os.ReadDir(base)
		for i := len(entries) - 1; i >= 0; i-- { // names sort by date
			dir := filepath.Join(base, entries[i].Name())
			var tg genTarget
			if b, err := os.ReadFile(filepath.Join(dir, "target.json")); err == nil && json.Unmarshal(b, &tg) == nil && tg.Worktree == worktree {
				if _, err := os.Stat(filepath.Join(dir, "resultado.json")); err != nil { // not finished
					return dir, true, nil
				}
			}
		}
		return "", false, errors.New("there is no unfinished run of this worktree to resume")
	}
	var rnd [3]byte
	_, _ = rand.Read(rnd[:])
	id := time.Now().Format("20060102-1504") + "-" + hex.EncodeToString(rnd[:])
	return filepath.Join(base, id), false, os.MkdirAll(filepath.Join(base, id), 0o755)
}

// GenerateEstimate says what a generation would do before it starts: the
// roles, the worktree and the folder of the run.
type GenerateEstimate struct {
	Roles    ai.Roles
	Worktree string
	Branch   string
	Consent  bool
}

// GeneratePlan resolves the roles, the worktree and the consent of a request.
func (e *Engine) GeneratePlan(ctx context.Context, req GenerateRequest) (*GenerateEstimate, error) {
	roles, err := e.AIRoles(req.Provider)
	if err != nil {
		return nil, err
	}
	wt, branch, err := e.resolveGenerateWorktree(ctx, req.CaptureTarget)
	if err != nil {
		return nil, err
	}
	return &GenerateEstimate{Roles: roles, Worktree: wt, Branch: branch, Consent: e.GenerateConsent(req.Project, roles)}, nil
}

// CaptureGenerate generates the inventory of a worktree with AI. The draft
// stays in the run folder: only CaptureAccept writes in the repository.
func (e *Engine) CaptureGenerate(ctx context.Context, req GenerateRequest, emit func(capture.GenEvent)) (*capture.GenResult, error) {
	plan, err := e.GeneratePlan(ctx, req)
	if err != nil {
		return nil, err
	}
	if !plan.Consent {
		return nil, ErrAIConsent
	}
	dir, _, err := e.generateRunDir(req.Project, plan.Worktree, req.Resume)
	if err != nil {
		return nil, err
	}
	tb, _ := json.Marshal(genTarget{Worktree: plan.Worktree, Branch: plan.Branch})
	if err := os.WriteFile(filepath.Join(dir, "target.json"), tb, 0o644); err != nil {
		return nil, err
	}
	app := req.Project
	if inv, err := capture.LoadInventory(plan.Worktree); err == nil && inv.App != "" {
		app = inv.App
	}
	g, err := capture.NewGenerator(capture.GenConfig{Root: plan.Worktree, App: app, RunDir: dir, Roles: plan.Roles, Providers: AIProviderFor, Parallel: req.Parallel, Confirm: req.Confirm, Emit: emit})
	if err != nil {
		return nil, err
	}
	return g.Run(ctx)
}

// ErrAIConsent means the person has not yet accepted that code is sent to the provider.
var ErrAIConsent = errors.New("the project's code would be sent to the AI provider and you have not accepted that yet")

// AcceptRequest picks a draft and what to do with an existing inventory.
type AcceptRequest struct {
	CaptureTarget
	// Run is the run to accept (empty: the latest finished run of the worktree).
	Run string
	// Mode is "", "replace" or "merge".
	Mode string
}

// CaptureAccept writes a finished draft into the worktree.
func (e *Engine) CaptureAccept(ctx context.Context, req AcceptRequest) (*capture.AcceptResult, string, error) {
	wt, _, err := e.resolveGenerateWorktree(ctx, req.CaptureTarget)
	if err != nil {
		return nil, "", err
	}
	base := e.generateRunsDir(req.Project)
	run := req.Run
	if run == "" {
		entries, _ := os.ReadDir(base)
		for i := len(entries) - 1; i >= 0; i-- {
			dir := filepath.Join(base, entries[i].Name())
			var tg genTarget
			if b, err := os.ReadFile(filepath.Join(dir, "target.json")); err == nil && json.Unmarshal(b, &tg) == nil && tg.Worktree == wt {
				if _, err := os.Stat(filepath.Join(dir, "draft")); err == nil {
					run = entries[i].Name()
					break
				}
			}
		}
		if run == "" {
			return nil, "", errors.New("no finished generation for this worktree: run `theo tool capture generate` first")
		}
	}
	dir, err := capture.Within(base, run)
	if err != nil || strings.ContainsAny(run, `/\`) {
		return nil, "", fmt.Errorf("run %q is not a run id", run)
	}
	res, err := capture.Accept(dir, wt, req.Mode)
	return res, run, err
}

// pairWithAI asks the coordinator which screens only one set has match a
// screen of the other, and saves the answer in emparejamientos.json (where a
// person can correct it: what is written by hand wins). It returns the pairs
// to apply. Without roles, consent or a provider that answers it only says so:
// a comparison never fails because of the AI.
func (e *Engine) pairWithAI(ctx context.Context, req ComparisonRequest, root, key string, pf capture.PairFile, removed, added []capture.ScreenInfo, emit func(CaptureEvent)) []capture.Pair {
	known := map[string]bool{}
	for _, p := range pf[key] {
		known[p.AID] = true
	}
	var rest []capture.ScreenInfo
	for _, s := range removed {
		if !known[s.ID] {
			rest = append(rest, s)
		}
	}
	var out []capture.Pair
	for _, p := range pf.Applicable(key) {
		if p.Method != capture.ManualPair {
			out = append(out, p)
		}
	}
	if len(rest) == 0 || len(added) == 0 || req.NoAI {
		return out
	}
	roles, err := e.AIRoles("")
	if err != nil {
		emit(CaptureEvent{Kind: "log", Text: fmt.Sprintf("%d screens could not be matched; no AI provider chosen (theo tool ai doctor)", len(rest))})
		return out
	}
	if !e.GenerateConsent(req.Project, roles) {
		emit(CaptureEvent{Kind: "log", Text: fmt.Sprintf("%d screens could not be matched; AI matching is off until you accept sending the project's names to %s (run theo tool capture generate once)", len(rest), roles.Coordinator.Provider)})
		return out
	}
	prov, err := AIProviderFor(roles.Coordinator.Provider)
	if err != nil {
		return out
	}
	emit(CaptureEvent{Kind: "log", Text: fmt.Sprintf("matching %d screens with AI (%s)", len(rest), roles.Coordinator)})
	got, err := capture.ProposePairs(ctx, prov, roles.Coordinator, rest, added, 2)
	if err != nil {
		emit(CaptureEvent{Kind: "log", Text: fmt.Sprintf("AI matching failed (%v): the screens stay unmatched", err)})
		return out
	}
	pf[key] = append(pf[key], got...)
	if err := pf.Save(root); err != nil {
		emit(CaptureEvent{Kind: "log", Text: fmt.Sprintf("could not save %s: %v", capture.PairsPath, err)})
	}
	for _, p := range got {
		line := fmt.Sprintf("%s  ->  %s", p.AID, p.BID)
		if p.BID == "" {
			line = p.AID + "  ->  (no match)"
		}
		emit(CaptureEvent{Kind: "ai-pair", Text: fmt.Sprintf("%-26s confidence %.2f  by AI", line, p.Confidence)})
	}
	for _, p := range got {
		if p.BID != "" && p.Confidence >= capture.AIMinConfidence {
			out = append(out, p)
		}
	}
	return out
}
