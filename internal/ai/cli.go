package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/execx"
)

// runner runs a command and returns its standard output. It is a field so the
// tests can stand in for the real command lines.
type runner func(ctx context.Context, dir, name string, args ...string) ([]byte, error)

func realRunner(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	return execx.RunChange(ctx, dir, name, args...)
}

func lookup(name string) (string, bool) {
	p, err := exec.LookPath(name)
	return p, err == nil
}

// cliProvider is what Claude Code and Copilot share: a binary run once per job.
type cliProvider struct {
	id, bin string
	models  []string
	run     runner
	look    func(string) (string, bool)
	// args builds the command line of a job.
	args func(Job, string) []string
	// parse reads the command's output into an answer.
	parse func(out []byte) (Result, error)
}

func (p *cliProvider) ID() string { return p.id }

func (p *cliProvider) Detect(ctx context.Context) (Found, error) {
	path, ok := p.look(p.bin)
	if !ok {
		return Found{Note: "not installed (no \"" + p.bin + "\" on the path)"}, nil
	}
	f := Found{Path: path, Installed: true, SignedIn: true}
	if out, err := p.run(ctx, "", p.bin, "--version"); err == nil {
		f.Version = firstLine(string(out))
	} else {
		f.SignedIn = false
		f.Note = "\"" + p.bin + " --version\" failed: " + short(err.Error())
	}
	return f, nil
}

// Models returns the model names the command line is known to accept. Neither
// command line lists them in a way Theo can rely on, so these are the aliases
// each documents; any other name still works with --model.
func (p *cliProvider) Models(context.Context) ([]Model, error) {
	var out []Model
	for _, m := range p.models {
		out = append(out, Model{ID: m})
	}
	return out, nil
}

func (p *cliProvider) Run(ctx context.Context, job Job) (Result, error) {
	if job.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, job.Timeout)
		defer cancel()
	}
	prompt := job.Prompt
	if len(job.Schema) > 0 {
		prompt += "\n\nAnswer with one JSON object and nothing else. It must satisfy this JSON schema:\n" + string(job.Schema)
	}
	out, err := p.run(ctx, job.WorkDir, p.bin, p.args(job, prompt)...)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Result{}, ErrTimeout
		}
		msg := err.Error()
		if limitText(msg) {
			return Result{}, fmt.Errorf("%s: %w", p.id, ErrLimit)
		}
		return Result{}, fmt.Errorf("%s: %s", p.id, short(msg))
	}
	res, err := p.parse(out)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", p.id, err)
	}
	if limitText(res.Text) && len(res.Text) < 400 {
		return Result{}, fmt.Errorf("%s: %w", p.id, ErrLimit)
	}
	if len(job.Schema) > 0 {
		j, err := ExtractJSON(res.Text)
		if err != nil {
			return Result{}, err
		}
		res.JSON = j
	}
	return res, nil
}

func limitText(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "usage limit") || strings.Contains(l, "rate limit") || strings.Contains(l, "quota")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func short(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

func init() {
	Register("claude", func() Provider { return newClaude(realRunner, lookup) })
	Register("copilot", func() Provider { return newCopilot(realRunner, lookup) })
}

// newClaude: `claude -p` is Claude Code's non-interactive mode. Reading is
// allowed (Read, Grep, Glob) and nothing else, so it cannot write.
func newClaude(run runner, look func(string) (string, bool)) *cliProvider {
	return &cliProvider{
		id: "claude", bin: "claude", run: run, look: look,
		models: []string{"opus", "sonnet", "haiku"},
		args: func(j Job, prompt string) []string {
			a := []string{"-p", prompt, "--output-format", "json", "--allowedTools", "Read,Grep,Glob"}
			if j.Model != "" {
				a = append(a, "--model", j.Model)
			}
			if j.System != "" {
				a = append(a, "--append-system-prompt", j.System)
			}
			if j.MaxTurns > 0 {
				a = append(a, "--max-turns", fmt.Sprint(j.MaxTurns))
			}
			return a
		},
		parse: func(out []byte) (Result, error) {
			var r struct {
				Result  string `json:"result"`
				IsError bool   `json:"is_error"`
			}
			if err := json.Unmarshal(out, &r); err != nil {
				// not JSON: take the text as it is
				return Result{Text: strings.TrimSpace(string(out))}, nil
			}
			if r.IsError {
				return Result{}, errors.New(short(r.Result))
			}
			return Result{Text: r.Result}, nil
		},
	}
}

// newCopilot: `copilot -p` is the non-interactive mode of GitHub Copilot CLI.
// It has no schema option, so the answer is validated and retried by the caller.
func newCopilot(run runner, look func(string) (string, bool)) *cliProvider {
	return &cliProvider{
		id: "copilot", bin: "copilot", run: run, look: look,
		models: nil,
		args: func(j Job, prompt string) []string {
			if j.System != "" {
				prompt = j.System + "\n\n" + prompt
			}
			a := []string{"-p", prompt}
			if j.Model != "" {
				a = append(a, "--model", j.Model)
			}
			return a
		},
		parse: func(out []byte) (Result, error) { return Result{Text: strings.TrimSpace(string(out))}, nil },
	}
}

// Ping asks a provider for a one-word answer and returns how long it took.
func Ping(ctx context.Context, p Provider, model string) (time.Duration, error) {
	start := time.Now()
	res, err := p.Run(ctx, Job{Model: model, Prompt: "Answer with the single word: ok", Timeout: 90 * time.Second, MaxTurns: 1})
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(res.Text) == "" {
		return 0, errors.New("empty answer")
	}
	return time.Since(start), nil
}
