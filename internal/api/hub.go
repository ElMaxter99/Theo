package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		s.pollOnce(ctx)
		cancel()
	}
}

// pollOnce recomputes the overview of every project and publishes
// state.changed for the ones that differ from the last look.
func (s *Server) pollOnce(ctx context.Context) {
	projects, err := s.eng.Projects()
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, p := range projects {
		seen[p.Name] = true
		ov, err := s.overview(ctx, p.Name, true)
		if err != nil {
			continue
		}
		sig := ov.signature()
		s.hub.mu.Lock()
		prev, known := s.hub.lastSig[p.Name]
		s.hub.lastSig[p.Name] = sig
		s.hub.mu.Unlock()
		if known && prev != sig {
			s.hub.publish("state.changed", map[string]string{"project": p.Name})
		}
	}
}

// handleEvents serves GET /events.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, sess *session) error {
	fl, ok := w.(http.Flusher)
	if !ok {
		return errf(http.StatusInternalServerError, "internal", "streaming is not supported")
	}
	closeStream, err := s.openStream()
	if err != nil {
		return err
	}
	defer closeStream()
	ch, unsub := s.hub.subscribe()
	defer unsub()
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
