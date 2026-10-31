package api

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"
	"sync"
	"time"
)

// event is one server-sent event.
type event struct {
	ID   int             `json:"-"`
	Name string          `json:"-"`
	Data json.RawMessage `json:"-"`
}

// hub is the global channel (GET /events): operation changes and state
// changes. It has no history: a client that reconnects reads the current
// state again (the first event says so).
type hub struct {
	mu      sync.Mutex
	subs    map[chan event]struct{}
	nextID  int
	closed  bool
	lastSig map[string]string // project -> signature of its overview, for the poller
}

func newHub() *hub { return &hub{subs: map[chan event]struct{}{}, lastSig: map[string]string{}} }

func (h *hub) publish(name string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	ev := event{ID: h.nextID, Name: name, Data: raw}
	for ch := range h.subs {
		select {
		case ch <- ev:
		default: // a client that does not read loses events; it will resync on reconnect
		}
	}
}

func (h *hub) subscribe() (chan event, func()) {
	ch := make(chan event, 256)
	h.mu.Lock()
	if h.closed {
		close(ch)
		h.mu.Unlock()
		return ch, func() {}
	}
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
		}
		h.mu.Unlock()
	}
}

func (h *hub) clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

func (h *hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for ch := range h.subs {
		close(ch)
		delete(h.subs, ch)
	}
}

// pollLoop looks for changes made outside Theo (another terminal) every
// interval, but only while some client is connected and some tab is visible.
// With no client there is no polling at all.
func (h *hub) pollLoop(s *Server) {
	t := time.NewTicker(s.cfg.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stopped:
			return
		case <-t.C:
		}
		if h.clients() == 0 || !s.auth.anyVisible() {
			continue
		}
		ctx, cancel := context.WithTimeout(s.workCtx, pollCycleTimeout)
		s.pollOnce(ctx)
		cancel()
	}
}

// pollCycleTimeout bounds one look at all projects.
const pollCycleTimeout = 20 * time.Second

// pollParallelism is how many projects one cycle looks at at once.
const pollParallelism = 4

// pollOnce looks at every project and publishes state.changed for the ones
// that differ from the last look. Docker is read once for the whole cycle. A
// project is recomputed in full (its worktrees from Git, their changes, the
// port plan) when the containers of its instances changed, when an operation
// or a forget happened, or every pollFullFactor poll intervals; otherwise the
// cycle costs two Docker commands for all projects together.
func (s *Server) pollOnce(ctx context.Context) {
	projects, err := s.eng.Projects()
	if err != nil {
		return
	}
	gen := s.cache.generation()
	snap := s.dockerSnapshot(ctx, true)
	fullEvery := pollFullFactor * s.cfg.PollInterval
	var wg sync.WaitGroup
	sem := make(chan struct{}, pollParallelism)
	for _, p := range projects {
		name := p.Name
		if s.pollSkips(name, snap, gen) {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			ov, err := s.overviewWith(ctx, name, overviewOpts{fresh: true, snap: snap})
			if err != nil {
				return
			}
			s.pollRemember(name, ov, snap, gen, fullEvery)
			sig := ov.signature()
			s.hub.mu.Lock()
			prev, known := s.hub.lastSig[name]
			s.hub.lastSig[name] = sig
			s.hub.mu.Unlock()
			if known && prev != sig {
				s.hub.publish("state.changed", map[string]string{"project": name})
			}
		}()
	}
	wg.Wait()
}

// pollFullFactor: a project that looks unchanged from Docker's side is still
// recomputed in full every pollFullFactor poll intervals (changes made in the
// repository or in the ports of the host do not show in Docker).
const pollFullFactor = 4

type pollEntry struct {
	composeNames []string
	sig          string // containers seen at the last full look; "" means: always look in full
	gen          uint64
	nextFull     time.Time
}

type pollMemo struct {
	mu sync.Mutex
	m  map[string]*pollEntry
}

func snapSignature(snap *snapResult, names []string) string {
	switch {
	case snap == nil:
		return ""
	case snap.err != nil:
		return "docker-down"
	case snap.snap == nil:
		return "" // the adapter cannot read all containers at once: no cheap comparison
	}
	return snap.snap.Signature(names)
}

// pollSkips says whether the full look at a project can wait for this cycle.
func (s *Server) pollSkips(name string, snap *snapResult, gen uint64) bool {
	s.polled.mu.Lock()
	ent := s.polled.m[strings.ToLower(name)]
	s.polled.mu.Unlock()
	if ent == nil || ent.sig == "" || ent.gen != gen || !time.Now().Before(ent.nextFull) {
		return false
	}
	return snapSignature(snap, ent.composeNames) == ent.sig
}

func (s *Server) pollRemember(name string, ov *Overview, snap *snapResult, gen uint64, fullEvery time.Duration) {
	var names []string
	for _, w := range ov.Worktrees {
		if w.ComposeProject != "" {
			names = append(names, w.ComposeProject)
		}
	}
	// Spread the full looks of different projects over time.
	h := fnv.New32a()
	h.Write([]byte(name))
	jitter := time.Duration(h.Sum32()%1000) * fullEvery / 4000
	ent := &pollEntry{composeNames: names, sig: snapSignature(snap, names), gen: gen, nextFull: time.Now().Add(fullEvery + jitter)}
	s.polled.mu.Lock()
	if s.polled.m == nil {
		s.polled.m = map[string]*pollEntry{}
	}
	s.polled.m[strings.ToLower(name)] = ent
	s.polled.mu.Unlock()
}

// handleEvents serves GET /events.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, sess *session) error {
	fl, ok := w.(http.Flusher)
	if !ok {
		return errf(http.StatusInternalServerError, "internal", "streaming is not supported")
	}
	ch, unsub := s.hub.subscribe()
	defer unsub()
	s.sse.Add(1)
	defer func() { s.sse.Add(-1); s.touch() }()
	defer s.auth.setVisible(sess, false)

	startSSE(w)
	writeSSE(w, event{Name: "hello", Data: mustJSON(map[string]any{"resync": true, "api_version": APIVersion})})
	fl.Flush()
	keep := time.NewTicker(20 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			writeSSE(w, ev)
			fl.Flush()
		case <-keep.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			fl.Flush()
		}
	}
}

func startSSE(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

func writeSSE(w http.ResponseWriter, ev event) {
	if ev.ID > 0 {
		fmt.Fprintf(w, "id: %d\n", ev.ID)
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Name, ev.Data)
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return raw
}
