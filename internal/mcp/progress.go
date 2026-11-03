package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// progressKey carries the progress reporter of a tools/call in its context.
type progressKey struct{}

// progress sends notifications/progress for one tools/call whose request
// carried _meta.progressToken. Docker's output becomes one notification per
// line, at most every minGap, so the client can show what is happening while
// up, down or restart run.
type progress struct {
	s     *Server
	token json.RawMessage
	mask  func(string) string

	mu    sync.Mutex
	n     int
	last  time.Time
	buf   []byte
	clock func() time.Time
}

const progressGap = 200 * time.Millisecond

func withProgress(ctx context.Context, s *Server, token json.RawMessage) context.Context {
	t := bytes.TrimSpace(token)
	if len(t) == 0 || string(t) == "null" {
		return ctx
	}
	return context.WithValue(ctx, progressKey{}, &progress{s: s, token: t, clock: time.Now})
}

func progressOf(ctx context.Context) *progress {
	p, _ := ctx.Value(progressKey{}).(*progress)
	return p
}

// notify sends one progress notification with message; force skips the
// rate limit (for the first and the last message of an operation).
func (p *progress) notify(message string, force bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	now := p.clock()
	if !force && now.Sub(p.last) < progressGap {
		p.mu.Unlock()
		return
	}
	p.n++
	p.last = now
	n := p.n
	if p.mask != nil {
		message = p.mask(message)
	}
	p.mu.Unlock()
	if len(message) > 200 {
		message = message[:200] + "…"
	}
	p.s.notify("notifications/progress", map[string]any{"progressToken": p.token, "progress": n, "message": message})
}

// Write takes Docker's output and reports its complete lines.
func (p *progress) Write(b []byte) (int, error) {
	p.mu.Lock()
	p.buf = append(p.buf, b...)
	var lines []string
	for {
		i := bytes.IndexAny(p.buf, "\r\n")
		if i < 0 {
			break
		}
		if line := strings.TrimSpace(string(p.buf[:i])); line != "" {
			lines = append(lines, line)
		}
		p.buf = p.buf[i+1:]
	}
	if len(p.buf) > 4096 { // a line without end: drop it rather than grow
		p.buf = p.buf[:0]
	}
	p.mu.Unlock()
	if len(lines) > 0 {
		p.notify(lines[len(lines)-1], false)
	}
	return len(b), nil
}

// notify sends a JSON-RPC notification to the client.
func (s *Server) notify(method string, params any) {
	data, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return
	}
	data = append(data, '\n')
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.out.Write(data)
}
