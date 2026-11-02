package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// exchange feeds input to a server and returns its answers by id (the
// string form of the id; "null" for answers without one).
func exchange(t *testing.T, s *Server, input string) (map[string]map[string]any, []map[string]any) {
	t.Helper()
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	byID := map[string]map[string]any{}
	var all []map[string]any
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("answer is not one JSON object per line: %q: %v", line, err)
		}
		if msg["jsonrpc"] != "2.0" {
			t.Errorf("answer without jsonrpc 2.0: %s", line)
		}
		id, _ := json.Marshal(msg["id"])
		byID[string(id)] = msg
		all = append(all, msg)
	}
	return byID, all
}

func errorCode(t *testing.T, msg map[string]any) int {
	t.Helper()
	if msg == nil {
		t.Fatal("no answer")
	}
	e, ok := msg["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected an error, got %v", msg)
	}
	return int(e["code"].(float64))
}

func TestFraming(t *testing.T) {
	s := New(Options{})
	// Two messages, a blank line, CRLF, and a last line without a newline.
	input := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n\n" +
		`{"jsonrpc":"2.0","id":"two","method":"ping"}` + "\r\n" +
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`
	byID, all := exchange(t, s, input)
	if len(all) != 3 {
		t.Fatalf("got %d answers, want 3: %v", len(all), all)
	}
	for _, id := range []string{`1`, `"two"`, `3`} {
		if r, ok := byID[id]["result"].(map[string]any); !ok || len(r) != 0 {
			t.Errorf("ping %s: %v", id, byID[id])
		}
	}
}

func TestProtocolErrors(t *testing.T) {
	s := New(Options{})
	input := strings.Join([]string{
		`{not json`,
		`{"jsonrpc":"2.0","id":1,"method":"nope"}`,
		`{"jsonrpc":"1.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3}`,
		`[{"jsonrpc":"2.0","id":4,"method":"ping"}]`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":[1]}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"arguments":{}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"no_such_tool"}}`,
		`{"jsonrpc":"2.0","id":8,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":{"x":1},"method":"ping"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"some/unknown/notification"}`,
	}, "\n") + "\n"
	byID, all := exchange(t, s, input)
	want := map[string]int{
		"2": codeInvalidRequest,
		"3": codeInvalidRequest,
		"1": codeMethodNotFound,
		"5": codeInvalidParams,
		"6": codeInvalidParams,
		"7": codeInvalidParams,
		"8": codeInvalidParams,
	}
	for id, code := range want {
		if got := errorCode(t, byID[id]); got != code {
			t.Errorf("id %s: code %d, want %d (%v)", id, got, code, byID[id])
		}
	}
	// Parse error, batch and the object id are answered with a null id.
	nulls := 0
	for _, m := range all {
		if m["id"] == nil {
			nulls++
		}
	}
	if nulls != 3 {
		t.Errorf("%d answers with a null id, want 3 (parse error, batch, bad id): %v", nulls, all)
	}
	// Notifications get no answer: 7 errors + 3 null-id ones.
	if len(all) != 10 {
		t.Errorf("got %d answers, want 10: %v", len(all), all)
	}
}

func TestInitializeNegotiation(t *testing.T) {
	for _, tc := range []struct{ asked, want string }{
		{"2025-06-18", "2025-06-18"},
		{"2025-03-26", "2025-03-26"},
		{"2024-11-05", "2024-11-05"},
		{"1999-01-01", ProtocolVersion},
		{"2099-12-31", ProtocolVersion},
	} {
		s := New(Options{Version: "1.2.3", Owner: "agent-7"})
		byID, _ := exchange(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+tc.asked+`","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`+"\n")
		r, ok := byID["1"]["result"].(map[string]any)
		if !ok {
			t.Fatalf("initialize %s: %v", tc.asked, byID["1"])
		}
		if r["protocolVersion"] != tc.want {
			t.Errorf("asked %s, got %v, want %s", tc.asked, r["protocolVersion"], tc.want)
		}
		info := r["serverInfo"].(map[string]any)
		if info["name"] != "theo" || info["version"] != "1.2.3" {
			t.Errorf("serverInfo: %v", info)
		}
		if _, ok := r["capabilities"].(map[string]any)["tools"]; !ok {
			t.Errorf("no tools capability: %v", r["capabilities"])
		}
		if !strings.Contains(r["instructions"].(string), "agent-7") {
			t.Errorf("instructions do not mention the default owner: %v", r["instructions"])
		}
	}
}

func toolsByName(t *testing.T, s *Server) map[string]map[string]any {
	t.Helper()
	byID, _ := exchange(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`+"\n")
	list := byID["1"]["result"].(map[string]any)["tools"].([]any)
	out := map[string]map[string]any{}
	for _, x := range list {
		tl := x.(map[string]any)
		out[tl["name"].(string)] = tl
	}
	return out
}

func TestToolsListAndAnnotations(t *testing.T) {
	tools := toolsByName(t, New(Options{}))
	for _, name := range []string{"list_projects", "project_show", "list_worktrees", "list_branches", "add_worktree", "remove_worktree",
		"services", "ports", "up", "down", "restart", "stop", "start", "ps", "logs", "status", "env_list", "env_set", "env_unset",
		"instance_show", "instance_config", "history", "reconcile", "volumes_list", "explain"} {
		tl, ok := tools[name]
		if !ok {
			t.Errorf("missing tool %s", name)
			continue
		}
		schema := tl["inputSchema"].(map[string]any)
		if schema["type"] != "object" {
			t.Errorf("%s: schema type %v", name, schema["type"])
		}
		if _, ok := tl["annotations"].(map[string]any)["readOnlyHint"]; !ok {
			t.Errorf("%s: no readOnlyHint", name)
		}
	}
	// Destructive data operations are never offered.
	for name := range tools {
		for _, banned := range []string{"purge", "restore", "clone_volume", "project_remove", "remove_project", "exec", "shell"} {
			if strings.Contains(name, banned) {
				t.Errorf("tool %s must not be exposed", name)
			}
		}
	}
	ann := func(name string) map[string]any { return tools[name]["annotations"].(map[string]any) }
	if ann("down")["destructiveHint"] != true || ann("remove_worktree")["destructiveHint"] != true {
		t.Error("down and remove_worktree must be marked destructive")
	}
	if ann("up")["destructiveHint"] != false || ann("up")["idempotentHint"] != true {
		t.Errorf("up annotations: %v", ann("up"))
	}
	if ann("logs")["readOnlyHint"] != true || ann("explain")["readOnlyHint"] != true {
		t.Error("logs and explain are read-only")
	}
	if ann("add_worktree")["readOnlyHint"] != false {
		t.Error("add_worktree changes things")
	}
	required := tools["remove_worktree"]["inputSchema"].(map[string]any)["required"].([]any)
	if len(required) != 2 {
		t.Errorf("remove_worktree requires %v", required)
	}
}

func TestReadOnlyServer(t *testing.T) {
	s := New(Options{ReadOnly: true})
	tools := toolsByName(t, s)
	for name, tl := range tools {
		if tl["annotations"].(map[string]any)["readOnlyHint"] != true {
			t.Errorf("--read-only lists %s, which changes things", name)
		}
	}
	for _, name := range []string{"up", "down", "add_worktree", "remove_worktree", "env_set"} {
		if _, ok := tools[name]; ok {
			t.Errorf("--read-only lists %s", name)
		}
	}
	rec, ok := tools["reconcile"]
	if !ok {
		t.Fatal("--read-only should keep reconcile as a report")
	}
	if _, hasApply := rec["inputSchema"].(map[string]any)["properties"].(map[string]any)["apply"]; hasApply {
		t.Error("read-only reconcile must not take apply")
	}
	byID, _ := exchange(t, s, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"up","arguments":{"project":"x"}}}`+"\n")
	if code := errorCode(t, byID["9"]); code != codeInvalidParams {
		t.Errorf("calling up on a read-only server: code %d", code)
	}
	if msg := byID["9"]["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "read-only") {
		t.Errorf("the refusal should say why: %q", msg)
	}
}

func TestBadArgumentsAreToolErrors(t *testing.T) {
	s := New(Options{})
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"project_show","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"project_show","arguments":{"project":3}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"project_show","arguments":{"project":"x","bogus":true}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"explain","arguments":{"project":"x","operation":"purge"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"history","arguments":{"project":"x","limit":1.5}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"project_show","arguments":"x"}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"project_show","arguments":{"project":"  "}}}`,
	}, "\n") + "\n"
	byID, _ := exchange(t, s, input)
	for id, want := range map[string]string{
		"1": `missing required argument "project"`,
		"2": `"project" must be a string`,
		"3": `unknown argument "bogus"`,
		"4": "must be one of up, down",
		"5": `"limit" must be an integer`,
		"6": "must be a JSON object",
		"7": "must not be empty",
	} {
		r, ok := byID[id]["result"].(map[string]any)
		if !ok {
			t.Errorf("id %s: want a tool result, got %v", id, byID[id])
			continue
		}
		if r["isError"] != true {
			t.Errorf("id %s: isError not set: %v", id, r)
		}
		text := r["content"].([]any)[0].(map[string]any)["text"].(string)
		if !strings.Contains(text, want) {
			t.Errorf("id %s: %q does not contain %q", id, text, want)
		}
		if code := r["structuredContent"].(map[string]any)["error"].(map[string]any)["code"]; code != "invalid_arguments" {
			t.Errorf("id %s: code %v", id, code)
		}
	}
}

func TestLongLineIsRejected(t *testing.T) {
	s := New(Options{})
	input := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":"` + strings.Repeat("a", maxMessage) + `"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"ping"}` + "\n"
	byID, all := exchange(t, s, input)
	if len(all) != 2 {
		t.Fatalf("got %d answers: want a parse error and the ping", len(all))
	}
	if errorCode(t, byID["null"]) != codeParseError {
		t.Errorf("long line: %v", byID["null"])
	}
	if _, ok := byID["2"]["result"]; !ok {
		t.Errorf("the server should go on after a long line: %v", byID["2"])
	}
}

func TestCancelledCallGetsNoAnswer(t *testing.T) {
	s := New(Options{})
	block := make(chan struct{})
	started := make(chan struct{})
	slow := &tool{name: "slow", readOnly: true, run: func(ctx context.Context, _ *Server, _ args) (*result, error) {
		close(started)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-block:
			return ok("done", nil), nil
		}
	}}
	s.tools = append(s.tools, slow)
	s.byName["slow"] = slow

	pr, pw := io.Pipe()
	var out bytes.Buffer
	done := make(chan error)
	go func() { done <- s.Serve(context.Background(), pr, &syncWriter{w: &out}) }()
	io.WriteString(pw, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"slow"}}`+"\n")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the call did not start")
	}
	io.WriteString(pw, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1,"reason":"test"}}`+"\n")
	io.WriteString(pw, `{"jsonrpc":"2.0","id":2,"method":"ping"}`+"\n")
	pw.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Contains(text, `"id":1`) {
		t.Errorf("a cancelled call was answered: %s", text)
	}
	if !strings.Contains(text, `"id":2`) {
		t.Errorf("ping not answered: %s", text)
	}
}

type syncWriter struct{ w io.Writer }

func (s *syncWriter) Write(p []byte) (int, error) { return s.w.Write(p) }

func TestTailBuffer(t *testing.T) {
	b := newTailBuffer(10)
	for i := 0; i < 10; i++ {
		b.Write([]byte("line " + string(rune('a'+i)) + "\n"))
	}
	text, dropped := b.Text()
	if !dropped || text != "line j\n" {
		t.Errorf("got %q, %v", text, dropped)
	}
	small := newTailBuffer(100)
	small.Write([]byte("hello\n"))
	if text, dropped := small.Text(); dropped || text != "hello\n" {
		t.Errorf("got %q, %v", text, dropped)
	}
	if s, cut := headLimit("aaa\nbbb\nccc\n", 9); !cut || s != "aaa\nbbb\n" {
		t.Errorf("headLimit: %q %v", s, cut)
	}
}
