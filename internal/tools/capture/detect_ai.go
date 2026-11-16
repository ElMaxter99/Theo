package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/ai"
	"github.com/ElMaxter99/Theo/internal/repoconfig"
)

var schemaRun = json.RawMessage(`{"type":"object","required":["run"],"properties":{"run":{"type":"object"},"motivo":{"type":"string"}}}`)

const detectSystem = `You work out how to start a web application for a screen-capture tool. Read the project in the current folder (read only; never read secrets or .env files).`

const detectPrompt = `Propose the "run" section of .theo.json for this project, as {"run": {...}, "motivo": "one sentence"}.

Two kinds:
- "script": {"kind":"script","install":["npm ci"],"start":"npm start -- --port ${PORT}","ready":{"path":"/"}}. start keeps running and must listen on ${PORT}.
- "static" (the build only leaves a folder of files): {"kind":"static","install":["npm ci"],"build":["npm run build"],"dir":"dist","base":"/","spa":false}. "base" is the path the application lives under ("/portal", or "/").
Use commands the project really has (package.json scripts, Makefile targets, README). Do not invent scripts. No commands that write outside the project, and no network calls other than installing dependencies.`

// ProposeRun asks an AI for the run section of a project the rules could not
// work out. The answer is validated like a section written by hand and sent
// back with the error up to retries times. It is only a proposal: nothing is
// written here, and its commands run only after the person approves them.
func ProposeRun(ctx context.Context, p ai.Provider, ref ai.Ref, root string, retries int) (*repoconfig.RunSpec, string, error) {
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		q := detectPrompt
		if lastErr != nil {
			q += "\n\nYour previous answer was rejected: " + lastErr.Error() + "\nAnswer again."
		}
		res, err := p.Run(ctx, ai.Job{Model: ref.Model, System: detectSystem, Prompt: q, WorkDir: root, Schema: schemaRun, Timeout: 5 * time.Minute, MaxTurns: 10})
		if err == nil {
			var ans struct {
				Run    repoconfig.RunSpec `json:"run"`
				Motivo string             `json:"motivo"`
			}
			if err = strictJSON(res.JSON, &ans); err == nil {
				if verr := ans.Run.Validate(); verr != nil {
					err = verr
				} else if ans.Run.EffectiveKind() == repoconfig.RunCompose {
					err = errors.New(`use "script" or "static": Compose projects are started with theo up`)
				} else {
					return &ans.Run, strings.TrimSpace(ans.Motivo), nil
				}
			}
			err = fmt.Errorf("%w: %v", ai.ErrBadAnswer, err)
		}
		if !errors.Is(err, ai.ErrBadAnswer) {
			return nil, "", err
		}
		lastErr = err
	}
	return nil, "", lastErr
}
