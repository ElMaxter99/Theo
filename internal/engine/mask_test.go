package engine

import (
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/instances"
)

// `docker compose config` does not print a value the way it was typed: a
// literal $ comes out as $$, and a multi-line value becomes an indented block
// whose lines are separate. A mask that looks for the typed value only lets
// those secrets through (GET .../config, `theo config`).
func TestMaskSecretsCoversHowComposeRendersAValue(t *testing.T) {
	cfg := func(vals ...string) instances.Config {
		c := instances.Config{Env: map[string]instances.Var{}}
		for i, v := range vals {
			c.Env["S"+string(rune('A'+i))] = instances.Var{Value: v, Secret: true}
		}
		return c
	}
	cases := []struct {
		name, secret, rendered string
		leak                   []string // fragments that must not survive
	}{
		{"dollar", "pa$word", "      DOLLAR: pa$$word\n", []string{"pa$$word", "pa$word"}},
		{"multiline", "line1-secret\nline2-secret\nline3", "      MULTI: |-\n        line1-secret\n        line2-secret\n        line3\n", []string{"line1-secret", "line2-secret"}},
		{"json", "say \"hi\"\\now", `{"environment":{"Q":"say \"hi\"\\now"}}`, []string{`say \"hi\"`}},
		{"single quote", "it's-a-secret", "      Q: 'it''s-a-secret'\n", []string{"it''s-a-secret"}},
		{"plain", "hunter2", "      DB: hunter2\n", []string{"hunter2"}},
	}
	for _, c := range cases {
		out := MaskSecrets(c.rendered, cfg(c.secret))
		for _, leak := range c.leak {
			if strings.Contains(out, leak) {
				t.Errorf("%s: %q survives the mask:\n%s", c.name, leak, out)
			}
		}
		if !strings.Contains(out, Mask) {
			t.Errorf("%s: nothing was masked:\n%s", c.name, out)
		}
	}
	// What is not secret stays readable.
	out := MaskSecrets("      LEVEL: debug\n      DOLLAR: pa$$word\n", instances.Config{Env: map[string]instances.Var{
		"LEVEL": {Value: "debug"}, "DOLLAR": {Value: "pa$word", Secret: true}}})
	if !strings.Contains(out, "LEVEL: debug") || strings.Contains(out, "pa$$word") {
		t.Errorf("mask = %q", out)
	}
}
