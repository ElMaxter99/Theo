// Package mcp serves Theo's operations to AI agents over the Model Context
// Protocol (MCP): JSON-RPC 2.0 messages, one per line, on stdin and stdout.
//
// It is hand-rolled on purpose (Theo keeps its dependencies to a minimum) and
// implements the part of the protocol a tool server needs: initialize, ping,
// tools/list and tools/call, plus the initialized and cancelled
// notifications. The tools call the same engine functions as the CLI, never
// the theo binary, and return the same JSON shapes as its --json output.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/ElMaxter99/Theo/internal/engine"
)

// ProtocolVersion is the MCP revision the server implements.
const ProtocolVersion = "2025-06-18"

// supportedVersions are the revisions a client may ask for. The server
// answers with the client's when it is one of them, and with ProtocolVersion
// otherwise (the client then decides whether to go on).
var supportedVersions = []string{ProtocolVersion, "2025-03-26", "2024-11-05"}

// maxMessage is the largest message the server reads. Longer lines are
// answered with a parse error and skipped.
const maxMessage = 4 << 20

// JSON-RPC error codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// Options configure a Server.
type Options struct {
	// Engine runs the tools. It is shared by concurrent calls.
	Engine *engine.Engine
	// Open returns a new engine. explain uses it for an engine whose writes
	// are discarded. Nil means engine.Open.
	Open func() (*engine.Engine, error)
	// ReadOnly lists and runs only the tools that change nothing.
	ReadOnly bool
	// Owner is the owner given to worktrees that add_worktree creates
	// without one.
	Owner string
	// Version is reported as the server's version. Empty means engine.Version.
	Version string
}

// Server is an MCP server. Create it with New and run it with Serve.
type Server struct {
	opts   Options
	tools  []*tool
	byName map[string]*tool

	writeMu sync.Mutex
	out     io.Writer

	// explainMu makes explain exclusive: explain mode is process-wide, so
	// while it is on, a command run by another call would only be listed
	// instead of run. Every other call holds it for reading.
	explainMu sync.RWMutex

	busyMu sync.Mutex
	busy   map[string]string // key (instance or project) -> operation running on it

	callsMu sync.Mutex
	calls   map[string]context.CancelFunc // in-flight tools/call by request id
}

// New returns a server with the given options.
func New(opts Options) *Server {
	if opts.Open == nil {
		opts.Open = engine.Open
	}
	if opts.Version == "" {
		opts.Version = engine.Version
	}
	s := &Server{opts: opts, busy: map[string]string{}, calls: map[string]context.CancelFunc{}, byName: map[string]*tool{}}
	for _, t := range allTools() {
		if opts.ReadOnly {
			if t.readOnlyVariant != nil {
				t = t.readOnlyVariant()
			}
			if !t.readOnly {
				continue
			}
		}
		s.tools = append(s.tools, t)
		s.byName[t.name] = t
	}
	return s
}

// request is a JSON-RPC request or notification.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

var nullID = json.RawMessage("null")

// Serve reads messages from in until it ends or ctx is done, and writes the
// answers to out. Tool calls run concurrently; at the end of the input Serve
// waits for the ones still running, so that an operation is never cut in the
// middle by a client that closed the pipe.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.out = out
	r := bufio.NewReaderSize(in, 64<<10)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line, tooLong, err := readLine(r)
		if tooLong {
			s.send(response{ID: nullID, Error: &rpcError{Code: codeParseError, Message: fmt.Sprintf("message longer than %d bytes", maxMessage)}})
		} else if len(bytes.TrimSpace(line)) > 0 {
			s.handle(ctx, line, &wg)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// readLine reads one line without its end of line. A line longer than
// maxMessage is skipped and reported with tooLong.
func readLine(r *bufio.Reader) (line []byte, tooLong bool, err error) {
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLong {
			line = append(line, chunk...)
			if len(line) > maxMessage {
				tooLong, line = true, nil
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimRight(line, "\r\n"), tooLong, err
	}
}

func (s *Server) handle(ctx context.Context, line []byte, wg *sync.WaitGroup) {
	trimmed := bytes.TrimSpace(line)
	if trimmed[0] == '[' {
		s.send(response{ID: nullID, Error: &rpcError{Code: codeInvalidRequest, Message: "batches are not supported"}})
		return
	}
	var req request
	if err := json.Unmarshal(trimmed, &req); err != nil {
		s.send(response{ID: nullID, Error: &rpcError{Code: codeParseError, Message: "parse error: " + err.Error()}})
		return
	}
	isNotification := len(req.ID) == 0
	if req.JSONRPC != "2.0" || req.Method == "" || bytes.Equal(req.ID, nullID) || !validID(req.ID) {
		if isNotification && req.JSONRPC == "2.0" && req.Method != "" {
			return // a malformed notification gets no answer
		}
		id := req.ID
		if !validID(id) {
			id = nullID
		}
		s.send(response{ID: id, Error: &rpcError{Code: codeInvalidRequest, Message: `invalid request: it needs "jsonrpc": "2.0", a method, and a string or number id`}})
		return
	}
	if isNotification {
		s.notification(req)
		return
	}

	switch req.Method {
	case "initialize":
		s.reply(req.ID, s.initialize(req.Params))
	case "ping":
		s.send(response{ID: req.ID, Result: struct{}{}})
	case "tools/list":
		s.send(response{ID: req.ID, Result: map[string]any{"tools": s.toolList()}})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := strictObject(req.Params, &p); err != nil || p.Name == "" {
			s.send(response{ID: req.ID, Error: &rpcError{Code: codeInvalidParams, Message: `invalid params: tools/call needs {"name": <tool>, "arguments": {...}}`}})
			return
		}
		t, ok := s.byName[p.Name]
		if !ok {
			msg := fmt.Sprintf("unknown tool %q", p.Name)
			if s.opts.ReadOnly {
				msg += " (the server runs with --read-only: only tools that change nothing are available)"
			}
			s.send(response{ID: req.ID, Error: &rpcError{Code: codeInvalidParams, Message: msg}})
			return
		}
		callCtx, cancel := context.WithCancel(ctx)
		key := string(req.ID)
		s.callsMu.Lock()
		s.calls[key] = cancel
		s.callsMu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := s.call(callCtx, t, p.Arguments)
			s.callsMu.Lock()
			_, live := s.calls[key]
			delete(s.calls, key)
			s.callsMu.Unlock()
			cancelled := !live
			cancel()
			if cancelled {
				return // the client cancelled it: no answer is expected
			}
			s.send(response{ID: req.ID, Result: res})
		}()
	default:
		s.send(response{ID: req.ID, Error: &rpcError{Code: codeMethodNotFound, Message: fmt.Sprintf("method %q not found", req.Method)}})
	}
}

// validID reports whether id is absent, a string or a number.
func validID(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	var v any
	if json.Unmarshal(id, &v) != nil {
		return false
	}
	switch v.(type) {
	case string, float64, nil:
		return true
	}
	return false
}

// strictObject decodes params, which must be a JSON object.
func strictObject(params json.RawMessage, v any) error {
	t := bytes.TrimSpace(params)
	if len(t) == 0 || t[0] != '{' {
		return errors.New("params must be an object")
	}
	return json.Unmarshal(t, v)
}

func (s *Server) notification(req request) {
	switch req.Method {
	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(req.Params, &p) != nil {
			return
		}
		s.callsMu.Lock()
		cancel, ok := s.calls[string(bytes.TrimSpace(p.RequestID))]
		delete(s.calls, string(bytes.TrimSpace(p.RequestID)))
		s.callsMu.Unlock()
		if ok {
			cancel()
		}
	}
	// notifications/initialized and any other notification need nothing.
}

// rpcResult is a method's result or its error.
type rpcResult struct {
	result any
	err    *rpcError
}

func (s *Server) reply(id json.RawMessage, r rpcResult) {
	if r.err != nil {
		s.send(response{ID: id, Error: r.err})
		return
	}
	s.send(response{ID: id, Result: r.result})
}

func (s *Server) initialize(params json.RawMessage) rpcResult {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := strictObject(params, &p); err != nil || p.ProtocolVersion == "" {
		return rpcResult{err: &rpcError{Code: codeInvalidParams, Message: `invalid params: initialize needs {"protocolVersion": ..., "capabilities": {...}, "clientInfo": {...}}`}}
	}
	version := ProtocolVersion
	for _, v := range supportedVersions {
		if v == p.ProtocolVersion {
			version = v
		}
	}
	return rpcResult{result: map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      map[string]any{"name": "theo", "title": "Theo", "version": s.opts.Version},
		"instructions":    s.instructions(),
	}}
}

func (s *Server) instructions() string {
	text := "Theo runs an isolated Docker Compose environment per Git worktree of a registered project. " +
		"Start with list_projects, then list_worktrees; select a worktree by branch, directory name or path (empty means the main worktree). " +
		"Typical flow: add_worktree, up, ports (URLs), logs, down, remove_worktree. explain shows the Git and Docker commands of up or down without running them. " +
		"Volumes are always kept; deleting data (volume purge, restore, clone) and removing projects are left to the human."
	if s.opts.ReadOnly {
		text += " This server is read-only: only tools that change nothing are available."
	}
	if s.opts.Owner != "" {
		text += fmt.Sprintf(" Worktrees created here are owned by %q unless another owner is given.", s.opts.Owner)
	}
	return text
}

// send writes one message. Messages never contain a newline: encoding/json
// escapes the ones in strings.
func (s *Server) send(r response) {
	r.JSONRPC = "2.0"
	data, err := json.Marshal(r)
	if err != nil {
		data, _ = json.Marshal(response{JSONRPC: "2.0", ID: r.ID, Error: &rpcError{Code: codeInternalError, Message: "cannot encode the result: " + err.Error()}})
	}
	data = append(data, '\n')
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.out.Write(data)
}
