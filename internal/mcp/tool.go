package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/ElMaxter99/Theo/internal/engine"
)

// tool is one MCP tool.
type tool struct {
	name        string
	title       string
	description string
	params      []param
	// Annotations (hints for the client; see the MCP specification).
	readOnly    bool
	destructive bool
	idempotent  bool
	// readOnlyVariant, if set, returns the tool as --read-only lists it
	// (reconcile without apply).
	readOnlyVariant func() *tool
	run             func(ctx context.Context, s *Server, a args) (*result, error)
}

// param is one argument of a tool.
type param struct {
	name        string
	kind        string // "string", "boolean", "integer" or "array" (of strings)
	description string
	required    bool
	enum        []string
}

func (t *tool) schema() map[string]any {
	props := map[string]any{}
	required := []string{}
	for _, p := range t.params {
		prop := map[string]any{"type": p.kind, "description": p.description}
		if p.kind == "array" {
			prop["items"] = map[string]any{"type": "string"}
		}
		if len(p.enum) > 0 {
			prop["enum"] = p.enum
		}
		props[p.name] = prop
		if p.required {
			required = append(required, p.name)
		}
	}
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func (s *Server) toolList() []map[string]any {
	out := make([]map[string]any, 0, len(s.tools))
	for _, t := range s.tools {
		ann := map[string]any{
			"title":         t.title,
			"readOnlyHint":  t.readOnly,
			"openWorldHint": false, // only this machine's Git and Docker
		}
		if !t.readOnly { // the other hints only mean something for a tool that changes things
			ann["destructiveHint"] = t.destructive
			ann["idempotentHint"] = t.idempotent
		}
		out = append(out, map[string]any{
			"name":        t.name,
			"title":       t.title,
			"description": t.description,
			"inputSchema": t.schema(),
			"annotations": ann,
		})
	}
	return out
}

// args are the arguments of a call, checked against the tool's parameters.
type args map[string]any

func (a args) str(name string) string {
	v, _ := a[name].(string)
	return v
}

func (a args) bool(name string) bool {
	v, _ := a[name].(bool)
	return v
}

func (a args) list(name string) []string {
	v, _ := a[name].([]string)
	return v
}

// int returns an integer argument, or def when it is absent.
func (a args) int(name string, def int) int {
	if v, ok := a[name].(int); ok {
		return v
	}
	return def
}

// parseArgs checks raw against the tool's parameters: an object, no unknown
// argument, the required ones present, and each of its type.
func (t *tool) parseArgs(raw json.RawMessage) (args, error) {
	out := args{}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		raw = []byte("{}")
	}
	var in map[string]json.RawMessage
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, errors.New("arguments must be a JSON object")
	}
	known := map[string]param{}
	for _, p := range t.params {
		known[p.name] = p
	}
	for name, v := range in {
		p, ok := known[name]
		if !ok {
			return nil, fmt.Errorf("unknown argument %q (%s takes: %s)", name, t.name, t.paramNames())
		}
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			continue // null is the same as absent
		}
		var err error
		switch p.kind {
		case "string":
			var s string
			if err = json.Unmarshal(v, &s); err == nil {
				if len(p.enum) > 0 && !contains(p.enum, s) {
					return nil, fmt.Errorf("argument %q must be one of %s", name, strings.Join(p.enum, ", "))
				}
				out[name] = s
			}
		case "boolean":
			var b bool
			if err = json.Unmarshal(v, &b); err == nil {
				out[name] = b
			}
		case "integer":
			var f float64
			if err = json.Unmarshal(v, &f); err == nil {
				if f != float64(int(f)) {
					err = errors.New("not an integer")
				} else {
					out[name] = int(f)
				}
			}
		case "array":
			var l []string
			if err = json.Unmarshal(v, &l); err == nil {
				out[name] = l
			}
		}
		if err != nil {
			return nil, fmt.Errorf("argument %q must be %s", name, kindText(p.kind))
		}
	}
	for _, p := range t.params {
		if _, ok := out[p.name]; p.required && !ok {
			return nil, fmt.Errorf("missing required argument %q", p.name)
		}
		if s, ok := out[p.name].(string); ok && p.required && strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("argument %q must not be empty", p.name)
		}
	}
	return out, nil
}

func kindText(kind string) string {
	switch kind {
	case "array":
		return "a list of strings"
	case "integer":
		return "an integer"
	}
	return "a " + kind
}

func (t *tool) paramNames() string {
	if len(t.params) == 0 {
		return "no arguments"
	}
	names := make([]string, len(t.params))
	for i, p := range t.params {
		names[i] = p.name
	}
	return strings.Join(names, ", ")
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// result is the result of tools/call.
type result struct {
	Content           []content `json:"content"`
	StructuredContent any       `json:"structuredContent,omitempty"`
	IsError           bool      `json:"isError"`
}

type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ok builds a successful result: a short text for the model and the user,
// and the data both as structuredContent and as JSON text, for clients that
// only read the content.
func ok(summary string, data any) *result {
	r := &result{Content: []content{{Type: "text", Text: summary}}}
	if data != nil {
		r.StructuredContent = data
		if text, err := json.Marshal(data); err == nil {
			r.Content = append(r.Content, content{Type: "text", Text: string(text)})
		}
	}
	return r
}

// coder is an engine error with a stable code.
type coder interface{ Code() string }

// toolError turns an error into a result with isError, so that the model
// sees it and can correct the call.
func toolError(err error) *result {
	code := "error"
	var c coder
	if errors.As(err, &c) {
		code = c.Code()
	}
	switch {
	case errors.Is(err, engine.ErrNotFound):
		code = "not_found"
	case errors.Is(err, errBadArgs):
		code = "invalid_arguments"
	case errors.Is(err, errBusy):
		code = "busy"
	case errors.Is(err, context.Canceled):
		code = "cancelled"
	}
	errInfo := map[string]any{"code": code, "message": err.Error()}
	var ce *engine.ChangesError
	if errors.As(err, &ce) {
		errInfo["changes"] = ce.Changes
		errInfo["confirm"] = ce.Name
	}
	var ie *engine.InstanceError
	if errors.As(err, &ie) {
		errInfo["compose_projects"] = ie.Stacks
	}
	return &result{
		Content:           []content{{Type: "text", Text: err.Error()}},
		StructuredContent: map[string]any{"error": errInfo},
		IsError:           true,
	}
}

var (
	errBadArgs = errors.New("invalid arguments")
	errBusy    = errors.New("busy")
)

// call runs a tool and turns what it returns into a result.
func (s *Server) call(ctx context.Context, t *tool, raw json.RawMessage) (res *result) {
	defer func() {
		if r := recover(); r != nil {
			res = toolError(fmt.Errorf("internal error in %s: %v", t.name, r))
		}
	}()
	a, err := t.parseArgs(raw)
	if err != nil {
		return toolError(fmt.Errorf("%w: %s", errBadArgs, err.Error()))
	}
	if t.name != "explain" { // explain takes the lock exclusively itself
		s.explainMu.RLock()
		defer s.explainMu.RUnlock()
	}
	res, err = t.run(ctx, s, a)
	if err != nil {
		return toolError(err)
	}
	return res
}

// claim marks key as busy with op for the duration of a mutating call. A
// second operation on the same instance through this server is refused
// rather than queued, so that the agent knows; operations from other
// processes are serialised by the engine's own locks and Docker.
func (s *Server) claim(key, op string) (release func(), err error) {
	s.busyMu.Lock()
	defer s.busyMu.Unlock()
	if running, ok := s.busy[key]; ok {
		return nil, fmt.Errorf("%w: %s is already running on %s through this server; wait for it to finish", errBusy, running, key)
	}
	s.busy[key] = op
	var once sync.Once
	return func() {
		once.Do(func() {
			s.busyMu.Lock()
			delete(s.busy, key)
			s.busyMu.Unlock()
		})
	}, nil
}
