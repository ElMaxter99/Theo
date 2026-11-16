package ai

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Stub answers with saved replies. A reply is chosen by the first rule whose
// Contains text appears in the prompt; each rule can hold several replies,
// handed out in turn (so a test can give a bad answer first and a good one
// after). It records the jobs it received.
type Stub struct {
	mu    sync.Mutex
	Rules []StubRule
	Jobs  []Job
	// Ready is false to look like a provider that is not signed in.
	NotReady bool
	next     map[int]int
}

// StubRule is one saved answer (or a few, in turn).
type StubRule struct {
	Contains string
	Replies  []string
	Err      error // returned instead of a reply
}

func init() { Register("stub", func() Provider { return &Stub{} }) }

func (s *Stub) ID() string { return "stub" }

func (s *Stub) Detect(context.Context) (Found, error) {
	if s.NotReady {
		return Found{Installed: true, Note: "the stub is not signed in"}, nil
	}
	return Found{Path: "(stub)", Version: "0", Installed: true, SignedIn: true}, nil
}

func (s *Stub) Models(context.Context) ([]Model, error) {
	return []Model{{ID: "stub-large"}, {ID: "stub-small"}}, nil
}

func (s *Stub) Run(ctx context.Context, job Job) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	s.Jobs = append(s.Jobs, job)
	if s.next == nil {
		s.next = map[int]int{}
	}
	for i, r := range s.Rules {
		if !strings.Contains(job.Prompt, r.Contains) {
			continue
		}
		if r.Err != nil {
			return Result{}, r.Err
		}
		if len(r.Replies) == 0 {
			break
		}
		n := s.next[i]
		if n >= len(r.Replies) {
			n = len(r.Replies) - 1
		}
		s.next[i]++
		res := Result{Text: r.Replies[n]}
		if len(job.Schema) > 0 {
			if j, err := ExtractJSON(res.Text); err == nil {
				res.JSON = j
			}
		}
		return res, nil
	}
	return Result{}, fmt.Errorf("stub: no saved reply for this prompt")
}
