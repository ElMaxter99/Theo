package mcp

import (
	"bytes"
	"sync"
)

// Limits on what a tool returns, so that one call cannot flood the model's
// context (or the server's memory).
const (
	// outputLimit bounds the Docker output kept from an operation (up,
	// down...). The end is kept: that is where errors are.
	outputLimit = 16 << 10
	// logsLimit bounds the logs returned by logs, keeping the most recent.
	logsLimit = 64 << 10
	// configLimit bounds the configuration returned by instance_config.
	configLimit = 256 << 10
	// defaultLogTail and maxLogTail bound the lines per container of logs.
	defaultLogTail = 200
	maxLogTail     = 2000
	// defaultHistory and maxHistory bound the entries of history.
	defaultHistory = 20
	maxHistory     = 500
)

// tailBuffer keeps the last limit bytes written to it, from the start of a
// line, and remembers whether it dropped anything. It is safe for
// concurrent writes (stdout and stderr of one command share it).
type tailBuffer struct {
	mu      sync.Mutex
	limit   int
	buf     []byte
	dropped bool
}

func newTailBuffer(limit int) *tailBuffer { return &tailBuffer{limit: limit} }

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > 2*b.limit { // trim now and then, not on every write
		b.trim()
	}
	return len(p), nil
}

func (b *tailBuffer) trim() {
	if len(b.buf) <= b.limit {
		return
	}
	cut := b.buf[len(b.buf)-b.limit:]
	if i := bytes.IndexByte(cut, '\n'); i >= 0 && i < len(cut)-1 {
		cut = cut[i+1:] // start at a whole line
	}
	b.buf = append([]byte(nil), cut...)
	b.dropped = true
}

// Text returns what was kept, and whether the beginning was dropped.
func (b *tailBuffer) Text() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.trim()
	return string(b.buf), b.dropped
}

// headLimit cuts s to limit bytes, at a line end when there is one, and
// says whether it cut.
func headLimit(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	cut := s[:limit]
	if i := bytes.LastIndexByte([]byte(cut), '\n'); i > 0 {
		cut = cut[:i+1]
	}
	return cut, true
}
