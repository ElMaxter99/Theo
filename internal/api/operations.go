package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Operation states.
const (
	OpQueued     = "queued"
	OpRunning    = "running"
	OpSucceeded  = "succeeded"
	OpFailed     = "failed"
	OpCancelling = "cancelling"
	OpCancelled  = "cancelled"
)

const (
	ringLines    = 5000             // events kept per operation for reconnections
	maxLineBytes = 64 << 10         // longer lines are cut and marked truncated
	idemTTL      = 10 * time.Minute // how long an Idempotency-Key is remembered
	keepFinished = 50               // finished operations kept in memory
)

type opEvent struct {
	ID   int
	Name string
	Data json.RawMessage
}

// Operation is a long-running action (up, down, restart...) that runs in the
// server, whether or not a browser is watching.
type Operation struct {
	ID        string
	Kind      string
	Project   string
	Worktree  string
	StartedAt time.Time

	key     string // the instance it works on: one mutating operation at a time
	session string // the session that started it (scopes the Idempotency-Key)

	mu         sync.Mutex
	state      string
	finishedAt time.Time
	result     *opResult
	cancel     context.CancelFunc
	events     []opEvent
	firstID    int // id of events[0]
	nextID     int
	wake       chan struct{}
	errTail    []string // the last lines of output, kept to explain a failure
}

// tailedError adds the last lines the tool printed to its error: Docker says
// why it failed on stderr, and the exit status alone explains nothing.
type tailedError struct {
	err  error
	tail []string
}

func (t *tailedError) Error() string { return t.err.Error() + "\n\n" + strings.Join(t.tail, "\n") }
func (t *tailedError) Unwrap() error { return t.err }

type opResult struct {
	OK      bool   `json:"ok"`
	Summary string `json:"summary"`
	Error   *Error `json:"error,omitempty"`
}

// opView is the JSON form of an operation.
type opView struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Project     string     `json:"project"`
	Worktree    string     `json:"worktree,omitempty"`
	State       string     `json:"state"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Result      *opResult  `json:"result,omitempty"`
	Error       *Error     `json:"error,omitempty"`
	Cancellable bool       `json:"cancellable"`
}

func (op *Operation) view() opView {
	op.mu.Lock()
	defer op.mu.Unlock()
	v := opView{ID: op.ID, Kind: op.Kind, Project: op.Project, Worktree: op.Worktree, State: op.state, StartedAt: op.StartedAt,
		Result: op.result, Cancellable: op.state == OpQueued || op.state == OpRunning}
	if !op.finishedAt.IsZero() {
		t := op.finishedAt
		v.FinishedAt = &t
	}
	if op.result != nil && !op.result.OK {
		v.Error = op.result.Error
	}
	return v
}

func (op *Operation) active() bool {
	op.mu.Lock()
	defer op.mu.Unlock()
	return op.stateActive()
}

func (op *Operation) stateActive() bool {
	return op.state == OpQueued || op.state == OpRunning || op.state == OpCancelling
}

// append adds an event to the ring and wakes the streams that wait for it.
func (op *Operation) append(name string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	op.mu.Lock()
	op.nextID++
	op.events = append(op.events, opEvent{ID: op.nextID, Name: name, Data: raw})
	if len(op.events) > ringLines {
		drop := len(op.events) - ringLines
		op.events = append([]opEvent(nil), op.events[drop:]...)
	}
	op.firstID = op.events[0].ID
	old := op.wake
	op.wake = make(chan struct{})
	op.mu.Unlock()
	close(old)
}

func (op *Operation) setState(state string) {
	op.mu.Lock()
	op.state = state
	op.mu.Unlock()
	op.append("state", map[string]any{"state": state, "at": time.Now().UTC()})
}

// moveTo changes the state only if it is still from.
func (op *Operation) moveTo(from, to string) bool {
	op.mu.Lock()
	if op.state != from {
		op.mu.Unlock()
		return false
	}
	op.state = to
	op.mu.Unlock()
	op.append("state", map[string]any{"state": to, "at": time.Now().UTC()})
	return true
}

// since returns the events after lastID, how many were lost from the ring
// before them, a channel that closes when something new is appended, and
// whether the operation is over.
func (op *Operation) since(lastID int) (evs []opEvent, dropped int, wake <-chan struct{}, done bool) {
	op.mu.Lock()
	defer op.mu.Unlock()
	if len(op.events) > 0 && lastID+1 < op.firstID {
		dropped = op.firstID - lastID - 1
		lastID = op.firstID - 1
	}
	for _, e := range op.events {
		if e.ID > lastID {
			evs = append(evs, e)
		}
	}
	return evs, dropped, op.wake, !op.stateActive()
}

// lineWriter turns the output of a process into one "log" event per line.
type lineWriter struct {
	op     *Operation
	stream string
	buf    []byte
	cut    bool
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := indexByte(p, '\n')
		if i < 0 {
			w.keep(p)
			break
		}
		w.keep(p[:i])
		w.Flush()
		p = p[i+1:]
	}
	return n, nil
}

func indexByte(p []byte, b byte) int {
	for i, c := range p {
		if c == b {
			return i
		}
	}
	return -1
}

func (w *lineWriter) keep(p []byte) {
	room := maxLineBytes - len(w.buf)
	if len(p) > room {
		p, w.cut = p[:max(room, 0)], true
	}
	w.buf = append(w.buf, p...)
}

// Flush emits the pending partial line, if any.
func (w *lineWriter) Flush() {
	if len(w.buf) == 0 && !w.cut {
		return
	}
	line := strings.TrimRight(string(w.buf), "\r")
	data := map[string]any{"stream": w.stream, "line": line, "at": time.Now().UTC()}
	if w.cut {
		data["truncated"] = true
	}
	w.op.append("log", data)
	if strings.TrimSpace(line) != "" { // docker compose reports some errors on stdout
		w.op.mu.Lock()
		w.op.errTail = append(w.op.errTail, line)
		if len(w.op.errTail) > 20 {
			w.op.errTail = w.op.errTail[1:]
		}
		w.op.mu.Unlock()
	}
	w.buf, w.cut = w.buf[:0], false
}

// operations is the registry of operations of the process.
type operations struct {
	s *Server

	mu     sync.Mutex
	byID   map[string]*Operation
	order  []string
	active map[string]*Operation // by instance key
	idem   map[string]idemEntry
	slots  chan struct{}
	seq    int
}

type idemEntry struct {
	id string
	at time.Time
}

func newOperations(s *Server) *operations {
	return &operations{s: s, byID: map[string]*Operation{}, active: map[string]*Operation{}, idem: map[string]idemEntry{},
		slots: make(chan struct{}, s.cfg.MaxOperations)}
}

func (o *operations) running() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, op := range o.active {
		if op.active() {
			n++
		}
	}
	return n
}

func (o *operations) cancelAll() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, op := range o.active {
		op.mu.Lock()
		if op.cancel != nil {
			op.cancel()
		}
		op.mu.Unlock()
	}
}

// busy returns the active operation on an instance, if any.
func (o *operations) busy(key string) *Operation {
	o.mu.Lock()
	defer o.mu.Unlock()
	if op := o.active[key]; op != nil && op.active() {
		return op
	}
	return nil
}

// startRequest describes an operation to start.
type startRequest struct {
	kind, project, worktree, key, session, idemKey string
	run                                            func(ctx context.Context, stdout, stderr io.Writer) (summary string, err error)
}

// start creates the operation and runs it in the background. When the
// Idempotency-Key was seen before it returns the same operation.
func (o *operations) start(req startRequest) (op *Operation, replay bool, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now()
	for k, e := range o.idem {
		if now.Sub(e.at) > idemTTL {
			delete(o.idem, k)
		}
	}
	idemKey := ""
	if req.idemKey != "" {
		idemKey = req.session + "|" + req.idemKey
		if e, ok := o.idem[idemKey]; ok {
			if prev := o.byID[e.id]; prev != nil {
				return prev, true, nil
			}
		}
	}
	if cur := o.active[req.key]; cur != nil && cur.active() {
		e := errf(http.StatusConflict, "busy", "another operation is running on this instance")
		e.Operation = &OperationRef{ID: cur.ID, Kind: cur.Kind}
		e.Hint = "wait for it or cancel it"
		return nil, false, e
	}
	o.seq++
	op = &Operation{ID: "op_" + randomToken()[:16], Kind: req.kind, Project: req.project, Worktree: req.worktree, StartedAt: now.UTC(),
		key: req.key, session: req.session, state: OpQueued, wake: make(chan struct{}), nextID: 0, firstID: 1}
	ctx, cancel := context.WithCancel(context.Background())
	op.cancel = cancel
	o.byID[op.ID] = op
	o.order = append(o.order, op.ID)
	o.active[req.key] = op
	if idemKey != "" {
		o.idem[idemKey] = idemEntry{id: op.ID, at: now}
	}
	o.trim()
	o.s.touch()
	go o.run(ctx, op, req)
	return op, false, nil
}

// trim forgets the oldest finished operations beyond keepFinished.
func (o *operations) trim() {
	finished := 0
	for _, id := range o.order {
		if !o.byID[id].active() {
			finished++
		}
	}
	for finished > keepFinished {
		for i, id := range o.order {
			if !o.byID[id].active() {
				delete(o.byID, id)
				o.order = append(o.order[:i:i], o.order[i+1:]...)
				finished--
				break
			}
		}
	}
}

func (o *operations) run(ctx context.Context, op *Operation, req startRequest) {
	hub := o.s.hub
	hub.publish("operation.created", op.view())
	var summary string
	var runErr error
	defer func() {
		if rec := recover(); rec != nil {
			runErr = fmt.Errorf("internal error: %v", rec)
		}
		o.finish(op, summary, runErr, ctx.Err() != nil)
	}()

	// Wait for a slot: at most MaxOperations run at once (NFR-013 asks for
	// parallelism between instances; the limit only protects Docker).
	select {
	case o.slots <- struct{}{}:
		defer func() { <-o.slots }()
	case <-ctx.Done():
		runErr = ctx.Err()
		return
	}
	if !op.moveTo(OpQueued, OpRunning) { // cancelled while it waited
		runErr = context.Canceled
		return
	}
	hub.publish("operation.updated", op.view())
	out, errw := &lineWriter{op: op, stream: "stdout"}, &lineWriter{op: op, stream: "stderr"}
	defer func() { out.Flush(); errw.Flush() }()
	summary, runErr = req.run(ctx, out, errw)
}

func (o *operations) finish(op *Operation, summary string, err error, wasCancelled bool) {
	op.mu.Lock()
	cancelRequested := op.state == OpCancelling
	if err != nil && len(op.errTail) > 0 {
		err = &tailedError{err: err, tail: append([]string(nil), op.errTail...)}
	}
	op.mu.Unlock()
	res := &opResult{OK: err == nil, Summary: summary}
	state := OpSucceeded
	switch {
	case err == nil:
	case cancelRequested || wasCancelled:
		state = OpCancelled
		res.Summary = "cancelled; nothing is rolled back, the instance may be partially up"
		res.Error = &Error{Code: "cancelled", Message: "the operation was cancelled", Status: http.StatusConflict}
	default:
		state = OpFailed
		ref := &ResourceRef{Kind: "instance", Project: op.Project, Worktree: op.Worktree}
		if strings.HasPrefix(op.Kind, "shared.") {
			ref = &ResourceRef{Kind: "shared", Project: op.Project}
		}
		res.Error = fromEngine(err, ref)
		if res.Summary == "" {
			res.Summary = op.Kind + " failed"
		}
	}
	op.mu.Lock()
	op.finishedAt = time.Now().UTC()
	op.result = res
	op.state = state
	op.mu.Unlock()
	op.append("result", res)
	op.append("state", map[string]any{"state": state, "at": time.Now().UTC()})
	o.mu.Lock()
	if o.active[op.key] == op {
		delete(o.active, op.key)
	}
	o.trim()
	o.mu.Unlock()
	o.s.touch()
	o.s.cache.invalidate(op.Project)
	o.s.hub.publish("operation.finished", op.view())
	o.s.hub.publish("state.changed", map[string]string{"project": op.Project, "worktree": op.Worktree})
}

func (o *operations) get(id string) *Operation {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.byID[id]
}

// list returns the operations, the live ones first, then the most recent
// finished ones.
func (o *operations) list(state string) []opView {
	o.mu.Lock()
	ops := make([]*Operation, 0, len(o.order))
	for _, id := range o.order {
		ops = append(ops, o.byID[id])
	}
	o.mu.Unlock()
	out := []opView{}
	for _, op := range ops {
		v := op.view()
		if state == "" || v.State == state {
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// --- HTTP ---

func (s *Server) handleOperations(w http.ResponseWriter, r *http.Request, _ *session) error {
	state := r.URL.Query().Get("state")
	return writeJSON(w, http.StatusOK, s.ops.list(state))
}

func (s *Server) handleOperation(w http.ResponseWriter, r *http.Request, _ *session) error {
	op := s.ops.get(r.PathValue("id"))
	if op == nil {
		return operationNotFound(r.PathValue("id"))
	}
	return writeJSON(w, http.StatusOK, op.view())
}

func operationNotFound(id string) *Error {
	return &Error{Status: http.StatusNotFound, Code: "operation_not_found", Message: "no operation " + quote(id) + " (the server keeps the last " + strconv.Itoa(keepFinished) + ")",
		Resource: &ResourceRef{Kind: "operation"}}
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request, _ *session) error {
	op := s.ops.get(r.PathValue("id"))
	if op == nil {
		return operationNotFound(r.PathValue("id"))
	}
	op.mu.Lock()
	cancellable := op.stateActive() && op.state != OpCancelling
	cancel := op.cancel
	op.mu.Unlock()
	if cancellable {
		op.setState(OpCancelling)
		cancel()
	}
	return writeJSON(w, http.StatusAccepted, op.view())
}

// handleOperationEvents serves GET /operations/{id}/events.
func (s *Server) handleOperationEvents(w http.ResponseWriter, r *http.Request, _ *session) error {
	op := s.ops.get(r.PathValue("id"))
	if op == nil {
		return operationNotFound(r.PathValue("id"))
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		return errf(http.StatusInternalServerError, "internal", "streaming is not supported")
	}
	last := 0
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			last = n
		}
	}
	s.sse.Add(1)
	defer func() { s.sse.Add(-1); s.touch() }()
	startSSE(w)
	fl.Flush()
	keep := time.NewTicker(20 * time.Second)
	defer keep.Stop()
	for {
		evs, dropped, wake, done := op.since(last)
		if dropped > 0 {
			fmt.Fprintf(w, "event: gap\ndata: {\"dropped\":%d}\n\n", dropped)
		}
		for _, e := range evs {
			writeSSE(w, event{ID: e.ID, Name: e.Name, Data: e.Data})
			last = e.ID
		}
		fl.Flush()
		if done && len(evs) == 0 {
			return nil
		}
		if len(evs) > 0 {
			continue // look again before waiting: more may have arrived
		}
		select {
		case <-r.Context().Done():
			return nil
		case <-wake:
		case <-keep.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			fl.Flush()
		}
	}
}
