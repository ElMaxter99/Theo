package capture

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrInventoryExists means the worktree already has a pantallas.json and the
// person did not say what to do with it.
var ErrInventoryExists = errors.New(InventoryPath + " already exists")

// AcceptResult says what accepting a draft wrote.
type AcceptResult struct {
	Mode    string   `json:"mode"` // new, replace or merge
	Screens int      `json:"screens"`
	Added   []string `json:"added,omitempty"`   // screens added by a merge
	Skipped []string `json:"skipped,omitempty"` // screens a merge kept as they were
	Mocks   int      `json:"mocks"`
	Files   []string `json:"files"`
}

// Accept writes the draft of a generation into the worktree. With an existing
// inventory it refuses (ErrInventoryExists) unless mode is "replace" (the draft
// takes its place) or "merge" (only the screens it does not have are added).
func Accept(runDir, root, mode string) (*AcceptResult, error) {
	draft := filepath.Join(runDir, "draft")
	b, err := os.ReadFile(filepath.Join(draft, filepath.FromSlash(InventoryPath)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("run %s has no draft: generate it first", filepath.Base(runDir))
	}
	if err != nil {
		return nil, err
	}
	inv, err := ParseInventory(b)
	if err != nil {
		return nil, err
	}
	if errs := inv.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("the draft is not valid: %v", errs)
	}
	existing, lerr := LoadInventory(root)
	switch {
	case errors.Is(lerr, ErrNoInventory):
		mode = "new"
	case lerr != nil:
		return nil, fmt.Errorf("%s exists and cannot be read (%v): fix it or use --replace", InventoryPath, lerr)
	case mode == "":
		return nil, ErrInventoryExists
	case mode != "replace" && mode != "merge":
		return nil, fmt.Errorf("unknown mode %q", mode)
	}
	res := &AcceptResult{Mode: mode}
	out := inv
	if mode == "merge" {
		out, res.Added, res.Skipped = mergeInventories(existing, inv)
	}
	res.Screens = len(out.Pantallas)

	// Mocks: only those a written screen uses (a merge skips the others).
	want := map[string]bool{}
	keep := map[string]bool{}
	if mode == "merge" {
		for _, n := range res.Added {
			keep[n] = true
		}
	}
	for _, p := range inv.Pantallas {
		if mode == "merge" && !keep[p.ID] {
			continue
		}
		for _, st := range p.Estados {
			for _, v := range st.Datos {
				var rel string
				if json.Unmarshal(v, &rel) == nil && rel != "" {
					want[rel] = true
				}
			}
		}
	}
	for rel := range want {
		src, err := Within(draft, rel)
		if err != nil {
			return nil, err
		}
		dst, err := Within(root, rel)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return nil, err
		}
		res.Mocks++
		res.Files = append(res.Files, rel)
	}
	ob, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	dst, err := Within(root, InventoryPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(dst, append(ob, '\n'), 0o644); err != nil {
		return nil, err
	}
	res.Files = append([]string{InventoryPath}, res.Files...)
	return res, nil
}

// mergeInventories keeps everything of base and adds the screens (and flows)
// of add whose ids base does not have.
func mergeInventories(base, add *Inventory) (*Inventory, []string, []string) {
	out := *base
	have := map[string]bool{}
	for _, p := range base.Pantallas {
		have[p.ID] = true
	}
	var added, skipped []string
	for _, p := range add.Pantallas {
		if have[p.ID] {
			skipped = append(skipped, p.ID)
			continue
		}
		out.Pantallas = append(out.Pantallas, p)
		added = append(added, p.ID)
	}
	flows := map[string]bool{}
	for _, f := range base.Flujos {
		flows[f.ID] = true
	}
	states := map[string]bool{}
	for _, p := range out.Pantallas {
		for _, s := range p.Estados {
			states[StateKey(p.ID, s.ID)] = true
		}
	}
	for _, f := range add.Flujos {
		ok := !flows[f.ID]
		for _, s := range f.Pasos {
			ok = ok && states[s]
		}
		if ok {
			out.Flujos = append(out.Flujos, f)
		}
	}
	if out.Entrada == "" {
		out.Entrada = add.Entrada
	}
	return &out, added, skipped
}
