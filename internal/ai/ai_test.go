package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExtractJSON(t *testing.T) {
	for _, in := range []string{`{"a":1}`, "Here:\n```json\n{\"a\":1}\n```", `text { not json } then {"a":1} end`} {
		j, err := ExtractJSON(in)
		if err != nil || string(j) != `{"a":1}` {
			t.Errorf("%q: got %s, %v", in, j, err)
		}
	}
	if _, err := ExtractJSON("no object"); !errors.Is(err, ErrBadAnswer) {
		t.Errorf("want ErrBadAnswer, got %v", err)
	}
}

func TestParseRefAndRoles(t *testing.T) {
	r, err := ParseRef("claude:opus")
	if err != nil || r.Provider != "claude" || r.Model != "opus" || r.String() != "claude:opus" {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := ParseRef(":x"); err == nil {
		t.Error("no provider must fail")
	}
	vals := map[string]string{"ai.provider": "copilot", "ai.model": "m", "ai.roles.coordinator": "claude:opus"}
	roles, err := RolesFrom(func(k string) string { return vals[k] })
	if err != nil {
		t.Fatal(err)
	}
	if roles.Coordinator.String() != "claude:opus" || roles.Worker.String() != "copilot:m" {
		t.Errorf("%+v", roles)
	}
	vals["ai.roles.worker"] = ":bad"
	if _, err := RolesFrom(func(k string) string { return vals[k] }); err == nil {
		t.Error("a bad role must fail")
	}
}

func TestStubRepliesInTurn(t *testing.T) {
	s := &Stub{Rules: []StubRule{{Contains: "route", Replies: []string{"not json", `{"ok":true}`}}}}
	schema := []byte(`{"type":"object"}`)
	r1, err := s.Run(context.Background(), Job{Prompt: "the route", Schema: schema})
	if err != nil || r1.JSON != nil {
		t.Fatalf("first reply is malformed: %+v %v", r1, err)
	}
	r2, _ := s.Run(context.Background(), Job{Prompt: "the route", Schema: schema})
	if string(r2.JSON) != `{"ok":true}` {
		t.Errorf("second: %+v", r2)
	}
	if _, err := s.Run(context.Background(), Job{Prompt: "other"}); err == nil {
		t.Error("no rule must fail")
	}
	if len(s.Jobs) != 3 {
		t.Errorf("jobs recorded: %d", len(s.Jobs))
	}
}

func fakeLook(string) (string, bool) { return "/bin/x", true }

func TestClaudeCommandLineAndAnswer(t *testing.T) {
	var got []string
	var dir string
	run := func(_ context.Context, d, name string, args ...string) ([]byte, error) {
		got, dir = append([]string{name}, args...), d
		return []byte(`{"type":"result","is_error":false,"result":"Here {\"rutas\":[]}"}`), nil
	}
	p := newClaude(run, fakeLook)
	res, err := p.Run(context.Background(), Job{Model: "sonnet", System: "Be brief", Prompt: "list", WorkDir: "/repo", Schema: []byte(`{"type":"object"}`), MaxTurns: 3})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.JSON) != `{"rutas":[]}` {
		t.Errorf("json: %s", res.JSON)
	}
	line := strings.Join(got, " ")
	for _, want := range []string{"claude -p", "--output-format json", "--allowedTools Read,Grep,Glob", "--model sonnet", "--append-system-prompt Be brief", "--max-turns 3"} {
		if !strings.Contains(line, want) {
			t.Errorf("command line lacks %q: %s", want, line)
		}
	}
	if strings.Contains(line, "Write") || strings.Contains(line, "Edit") || strings.Contains(line, "Bash") {
		t.Errorf("the job must be read-only: %s", line)
	}
	if dir != "/repo" {
		t.Errorf("dir %q", dir)
	}
}

func TestClaudeErrorsAreClassified(t *testing.T) {
	limit := func(context.Context, string, string, ...string) ([]byte, error) {
		return nil, errors.New("Claude AI usage limit reached")
	}
	if _, err := newClaude(limit, fakeLook).Run(context.Background(), Job{Prompt: "x"}); !errors.Is(err, ErrLimit) {
		t.Errorf("want ErrLimit, got %v", err)
	}
	slow := func(ctx context.Context, _, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if _, err := newClaude(slow, fakeLook).Run(context.Background(), Job{Prompt: "x", Timeout: 20 * time.Millisecond}); !errors.Is(err, ErrTimeout) {
		t.Errorf("want ErrTimeout, got %v", err)
	}
	isErr := func(context.Context, string, string, ...string) ([]byte, error) {
		return []byte(`{"is_error":true,"result":"boom"}`), nil
	}
	if _, err := newClaude(isErr, fakeLook).Run(context.Background(), Job{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("is_error must fail: %v", err)
	}
	noJSON := func(context.Context, string, string, ...string) ([]byte, error) {
		return []byte(`{"result":"no object here"}`), nil
	}
	if _, err := newClaude(noJSON, fakeLook).Run(context.Background(), Job{Prompt: "x", Schema: []byte(`{}`)}); !errors.Is(err, ErrBadAnswer) {
		t.Errorf("want ErrBadAnswer, got %v", err)
	}
}

func TestDetect(t *testing.T) {
	ok := func(context.Context, string, string, ...string) ([]byte, error) {
		return []byte("2.1.4 (Claude Code)\n"), nil
	}
	f, _ := newClaude(ok, fakeLook).Detect(context.Background())
	if !f.Ready() || f.Version != "2.1.4 (Claude Code)" {
		t.Errorf("%+v", f)
	}
	f, _ = newClaude(ok, func(string) (string, bool) { return "", false }).Detect(context.Background())
	if f.Installed || f.Ready() || f.Note == "" {
		t.Errorf("%+v", f)
	}
}

func TestCopilotPutsSystemInThePrompt(t *testing.T) {
	var got []string
	run := func(_ context.Context, _, name string, args ...string) ([]byte, error) {
		got = args
		return []byte("ok\n"), nil
	}
	if _, err := newCopilot(run, fakeLook).Run(context.Background(), Job{System: "ROLE", Prompt: "ask", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got[1], "ROLE\n\nask") || got[2] != "--model" {
		t.Errorf("%q", got)
	}
}

func TestGetUnknownAndIDs(t *testing.T) {
	if _, err := Get("nope"); err == nil {
		t.Error("unknown provider must fail")
	}
	ids := strings.Join(IDs(), ",")
	if ids != "claude,copilot" {
		t.Errorf("ids %s", ids)
	}
}
