package capture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// InventoryPath is where a worktree keeps its list of screens.
const InventoryPath = ".theo/capturas/pantallas.json"

// Inventory is pantallas.json: the screens of an application, the states of
// each one (a screen plus its data plus the actions that lead to it) and the
// flows between them. It is the contract between whoever writes it (a person
// or, later, an AI) and the capture engine. Keys are in Spanish like the
// reports; messages and the CLI are in English.
type Inventory struct {
	Version   int        `json:"version"`
	App       string     `json:"app"`
	Entrada   string     `json:"entrada,omitempty"`
	Viewports []Viewport `json:"viewports"`
	Mocks     Mocks      `json:"mocks"`
	Sesion    *Sesion    `json:"sesion,omitempty"`
	Pantallas []Screen   `json:"pantallas"`
	Flujos    []Flow     `json:"flujos,omitempty"`
}

// Viewport is a window size to capture.
type Viewport struct {
	ID     string  `json:"id"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Mobile bool    `json:"mobile,omitempty"`
	Scale  float64 `json:"scale,omitempty"`
}

// Mocks says where the data of a capture comes from: "mock" (intercepted API
// calls answered from files), "real" (the back the app is connected to) or
// "grabar" (real, recording the traffic).
type Mocks struct {
	Modo    string `json:"modo,omitempty"`
	Carpeta string `json:"carpeta,omitempty"`
}

// Sesion is how the capture is signed in.
type Sesion struct {
	StorageState string `json:"storage_state,omitempty"`
}

// Screen is one screen of the application.
type Screen struct {
	ID        string   `json:"id"`
	Titulo    string   `json:"titulo,omitempty"`
	Ruta      string   `json:"ruta"`
	Origen    []string `json:"origen,omitempty"`
	Prioridad string   `json:"prioridad,omitempty"`
	Estados   []State  `json:"estados"`
	Ignorar   []Ignore `json:"ignorar,omitempty"`
}

// State is what is captured and compared: a screen with some data after some actions.
type State struct {
	ID       string                     `json:"id"`
	Datos    map[string]json.RawMessage `json:"datos,omitempty"`
	Acciones []Action                   `json:"acciones,omitempty"`
}

// Ignore masks a dynamic area in the comparison.
type Ignore struct {
	Selector string `json:"selector"`
}

// Flow is a sequence of states, "screen#state", a person goes through.
type Flow struct {
	ID    string   `json:"id"`
	Pasos []string `json:"pasos"`
}

// Action is one declarative step: an object with exactly one verb key.
type Action map[string]json.RawMessage

// Target locates an element by accessible role and name, or by CSS selector.
type Target struct {
	Rol      string `json:"rol,omitempty"`
	Nombre   string `json:"nombre,omitempty"`
	Selector string `json:"selector,omitempty"`
}

func (t Target) valid() bool { return t.Selector != "" || (t.Rol != "" && t.Nombre != "") }

// Verb returns the action's verb and its raw argument.
func (a Action) Verb() (string, json.RawMessage) {
	for k, v := range a {
		return k, v
	}
	return "", nil
}

// LoadInventory reads and decodes pantallas.json from a worktree (it does not validate).
func LoadInventory(root string) (*Inventory, error) {
	path := filepath.Join(root, filepath.FromSlash(InventoryPath))
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoInventory
	}
	if err != nil {
		return nil, err
	}
	return ParseInventory(data)
}

// ErrNoInventory means the worktree has no pantallas.json.
var ErrNoInventory = errors.New(InventoryPath + " not found")

// ParseInventory decodes pantallas.json, rejecting keys it does not know.
func ParseInventory(data []byte) (*Inventory, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	dec.DisallowUnknownFields()
	var inv Inventory
	if err := dec.Decode(&inv); err != nil {
		return nil, fmt.Errorf("not valid: %w", err)
	}
	return &inv, nil
}

// Summary counts what an inventory holds.
type Summary struct {
	Viewports, Screens, States, Flows, NoOrigin int
	Entry                                       string
}

// Summarize counts the inventory.
func (inv *Inventory) Summarize() Summary {
	s := Summary{Viewports: len(inv.Viewports), Screens: len(inv.Pantallas), Flows: len(inv.Flujos), Entry: inv.Entrada}
	for _, p := range inv.Pantallas {
		s.States += len(p.Estados)
		if len(p.Origen) == 0 {
			s.NoOrigin++
		}
	}
	return s
}

// StateKey is "screen#state".
func StateKey(screen, state string) string { return screen + "#" + state }

// Validate checks the inventory and returns every mistake, with where it is.
func (inv *Inventory) Validate() []string {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	if inv.Version != 1 {
		add(`version: must be 1, got %d`, inv.Version)
	}
	if strings.TrimSpace(inv.App) == "" {
		add(`app: needs a name`)
	}
	if len(inv.Viewports) == 0 {
		add(`viewports: needs at least one`)
	}
	seen := map[string]bool{}
	for i, v := range inv.Viewports {
		switch {
		case v.ID == "":
			add(`viewports[%d]: needs an "id"`, i)
		case seen[v.ID]:
			add(`viewports[%d]: id %q is repeated`, i, v.ID)
		}
		seen[v.ID] = true
		if v.Width <= 0 || v.Height <= 0 {
			add(`viewports[%d]: width and height must be positive`, i)
		}
	}
	switch inv.Mocks.Modo {
	case "", "mock", "real", "grabar":
	default:
		add(`mocks.modo: %q is not known (use "mock", "real" or "grabar")`, inv.Mocks.Modo)
	}
	if len(inv.Pantallas) == 0 {
		add(`pantallas: needs at least one screen`)
	}
	states := map[string]bool{}
	screens := map[string]bool{}
	for i, p := range inv.Pantallas {
		switch {
		case p.ID == "":
			add(`screens[%d]: needs an "id"`, i)
		case strings.Contains(p.ID, "#"):
			add(`screens[%d]: id %q cannot contain #`, i, p.ID)
		case screens[p.ID]:
			add(`screens[%d]: id %q is repeated`, i, p.ID)
		}
		screens[p.ID] = true
		if !strings.HasPrefix(p.Ruta, "/") && !strings.HasPrefix(p.Ruta, "#") {
			add(`screens[%d]: "ruta" %q must start with / or # (relative to the base of the app)`, i, p.Ruta)
		}
		switch p.Prioridad {
		case "", "alta", "media", "baja":
		default:
			add(`screens[%d]: "prioridad" %q is not known (use alta, media or baja)`, i, p.Prioridad)
		}
		if len(p.Estados) == 0 {
			add(`screens[%d]: needs at least one state`, i)
		}
		ss := map[string]bool{}
		for j, st := range p.Estados {
			switch {
			case st.ID == "":
				add(`screens[%d].states[%d]: needs an "id"`, i, j)
			case ss[st.ID]:
				add(`screens[%d].states[%d]: id %q is repeated`, i, j, st.ID)
			}
			ss[st.ID] = true
			states[StateKey(p.ID, st.ID)] = true
			for k, a := range st.Acciones {
				if msg := validateAction(a); msg != "" {
					add(`screens[%d].states[%d].actions[%d]: %s`, i, j, k, msg)
				}
			}
		}
		for j, ig := range p.Ignorar {
			if ig.Selector == "" {
				add(`screens[%d].ignore[%d]: needs a "selector"`, i, j)
			}
		}
	}
	if inv.Entrada != "" {
		if !screens[strings.SplitN(inv.Entrada, "#", 2)[0]] || (strings.Contains(inv.Entrada, "#") && !states[inv.Entrada]) {
			add(`entry: %q is not a screen or state of the inventory`, inv.Entrada)
		}
	}
	fl := map[string]bool{}
	for i, f := range inv.Flujos {
		switch {
		case f.ID == "":
			add(`flows[%d]: needs an "id"`, i)
		case fl[f.ID]:
			add(`flows[%d]: id %q is repeated`, i, f.ID)
		}
		fl[f.ID] = true
		if len(f.Pasos) == 0 {
			add(`flows[%d]: needs at least one step`, i)
		}
		for j, s := range f.Pasos {
			if !states[s] {
				add(`flows[%d].steps[%d]: %q is not a state of any screen`, i, j, s)
			}
		}
	}
	return errs
}

func validateAction(a Action) string {
	if len(a) != 1 {
		return `an action has exactly one verb`
	}
	verb, raw := a.Verb()
	target := func(r json.RawMessage) (Target, bool) {
		var t Target
		d := json.NewDecoder(bytes.NewReader(r))
		d.DisallowUnknownFields()
		return t, d.Decode(&t) == nil
	}
	switch verb {
	case "ir", "esperar", "tecla":
		var s string
		if json.Unmarshal(raw, &s) != nil || s == "" {
			return fmt.Sprintf(`%q needs a text`, verb)
		}
	case "scroll":
		var n float64
		var s string
		if json.Unmarshal(raw, &n) != nil && (json.Unmarshal(raw, &s) != nil || s != "fin") {
			return `"scroll" needs pixels or "fin"`
		}
	case "click", "pasar_raton":
		if t, ok := target(raw); !ok || !t.valid() {
			return fmt.Sprintf(`%q needs "rol" and "nombre", or "selector"`, verb)
		}
	case "escribir", "elegir":
		var in struct {
			En    Target `json:"en"`
			Texto string `json:"texto"`
			Valor string `json:"valor"`
		}
		if json.Unmarshal(raw, &in) != nil || !in.En.valid() {
			return fmt.Sprintf(`%q needs "en" with "rol" and "nombre", or "selector"`, verb)
		}
	default:
		known := []string{"ir", "click", "escribir", "elegir", "esperar", "scroll", "pasar_raton", "tecla"}
		sort.Strings(known)
		return fmt.Sprintf(`%q is not an action (use %s)`, verb, strings.Join(known, ", "))
	}
	return ""
}
