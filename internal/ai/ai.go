// Package ai lets Theo's tools ask an AI command line (Claude Code, GitHub
// Copilot) for a bounded piece of work and read its answer. Theo does not
// install those tools nor keep credentials: it uses the session the person
// already has in them. A "stub" provider answers with saved replies, for tests.
//
// The flags the adapters pass to each command line were written from their
// documentation; `theo tool ai doctor` runs a real ping, and the 0.10 notes say
// which provider has been tried against the real command line.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Model is one model a provider offers.
type Model struct {
	ID string `json:"id"`
}

// Found is what Detect learns about a provider.
type Found struct {
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Installed bool   `json:"installed"`
	SignedIn  bool   `json:"signed_in"`
	// Note says why a provider is not ready, in a sentence for the person.
	Note string `json:"note,omitempty"`
}

// Ready is true when the provider can be asked for work.
func (f Found) Ready() bool { return f.Installed && f.SignedIn }

// Job is one bounded request.
type Job struct {
	Model    string
	System   string // the role's instructions
	Prompt   string
	WorkDir  string          // folder the provider may read (never write)
	Schema   json.RawMessage // JSON schema the answer must satisfy, when the provider supports it
	Timeout  time.Duration
	MaxTurns int
}

// Result is the answer to a job.
type Result struct {
	// Text is the raw answer.
	Text string `json:"text"`
	// JSON is the answer's JSON object when the job had a schema and the
	// answer holds one (taken out of the text if the provider wrapped it).
	JSON json.RawMessage `json:"json,omitempty"`
}

// Provider is an adapter for one AI command line.
type Provider interface {
	ID() string
	Detect(ctx context.Context) (Found, error)
	Models(ctx context.Context) ([]Model, error)
	Run(ctx context.Context, job Job) (Result, error)
}

// Errors the callers tell apart.
var (
	// ErrLimit means the provider says the usage limit is reached.
	ErrLimit = errors.New("usage limit reached")
	// ErrTimeout means a job ran out of time.
	ErrTimeout = errors.New("timed out")
	// ErrBadAnswer means the answer is not what the job asked for; a retry may fix it.
	ErrBadAnswer = errors.New("answer does not match what was asked")
)

// Ref names a provider and a model: "claude:<model>" or just "claude".
type Ref struct{ Provider, Model string }

func (r Ref) String() string {
	if r.Model == "" {
		return r.Provider
	}
	return r.Provider + ":" + r.Model
}

// ParseRef reads "provider" or "provider:model".
func ParseRef(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, errors.New("empty provider")
	}
	p, m, _ := strings.Cut(s, ":")
	if p == "" {
		return Ref{}, fmt.Errorf("%q: no provider before the colon", s)
	}
	return Ref{Provider: p, Model: m}, nil
}

// Roles are the provider and model of each role.
type Roles struct{ Coordinator, Worker Ref }

// RolesFrom reads the settings: ai.roles.coordinator and ai.roles.worker, each
// falling back to ai.provider (with ai.model). A role left unset stays empty.
func RolesFrom(get func(string) string) (Roles, error) {
	base := Ref{}
	if p := get("ai.provider"); p != "" {
		base = Ref{Provider: p, Model: get("ai.model")}
	}
	read := func(key string) (Ref, error) {
		v := get(key)
		if v == "" {
			return base, nil
		}
		r, err := ParseRef(v)
		if err != nil {
			return Ref{}, fmt.Errorf("%s: %w", key, err)
		}
		return r, nil
	}
	c, err := read("ai.roles.coordinator")
	if err != nil {
		return Roles{}, err
	}
	w, err := read("ai.roles.worker")
	if err != nil {
		return Roles{}, err
	}
	return Roles{Coordinator: c, Worker: w}, nil
}

var registry = map[string]func() Provider{}

// Register adds a provider constructor (called from init functions).
func Register(id string, mk func() Provider) { registry[id] = mk }

// Get returns a provider by ID.
func Get(id string) (Provider, error) {
	mk, ok := registry[id]
	if !ok {
		return nil, fmt.Errorf("unknown AI provider %q (%s)", id, strings.Join(IDs(), ", "))
	}
	return mk(), nil
}

// IDs lists the providers a person can choose (the stub is for tests).
func IDs() []string {
	var out []string
	for id := range registry {
		if id != "stub" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// ExtractJSON finds the JSON object in an answer that may wrap it in text or a
// code fence. It returns ErrBadAnswer when there is none.
func ExtractJSON(text string) (json.RawMessage, error) {
	t := strings.TrimSpace(text)
	if json.Valid([]byte(t)) && strings.HasPrefix(t, "{") {
		return json.RawMessage(t), nil
	}
	start := strings.IndexByte(t, '{')
	for start >= 0 {
		dec := json.NewDecoder(strings.NewReader(t[start:]))
		var raw json.RawMessage
		if err := dec.Decode(&raw); err == nil {
			return raw, nil
		}
		next := strings.IndexByte(t[start+1:], '{')
		if next < 0 {
			break
		}
		start += 1 + next
	}
	return nil, fmt.Errorf("%w: no JSON object in the answer", ErrBadAnswer)
}
