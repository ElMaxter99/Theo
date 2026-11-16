package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/ai"
)

// PairsPath is where the pairings between two sets are kept in the main
// folder of a project: AI proposals and the person's own corrections.
const PairsPath = ".theo/capturas/emparejamientos.json"

// ManualPair is the method of a pair written by a person: it always wins.
const ManualPair = "manual"

// AIMinConfidence is the confidence under which a pair an AI proposes is kept
// in the file but not applied.
const AIMinConfidence = 0.5

// PairKey names the comparison of two sets in the pairs file.
func PairKey(a, b string) string { return a + ".." + b }

// PairFile is emparejamientos.json: the pairs of each comparison, by key.
type PairFile map[string][]Pair

// LoadPairs reads the pairs file of a project (none: empty).
func LoadPairs(root string) (PairFile, error) {
	p, err := Within(root, PairsPath)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return PairFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	var f PairFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", PairsPath, err)
	}
	if f == nil {
		f = PairFile{}
	}
	return f, nil
}

// Save writes the pairs file.
func (f PairFile) Save(root string) error {
	p, err := Within(root, PairsPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o644)
}

// Applicable returns the pairs of a comparison that may be applied: the
// person's own, and the AI's with enough confidence.
func (f PairFile) Applicable(key string) []Pair {
	var out []Pair
	for _, p := range f[key] {
		if p.AID == "" || p.BID == "" {
			continue
		}
		if p.Method == ManualPair || p.Confidence >= AIMinConfidence {
			out = append(out, p)
		}
	}
	return out
}

type pairAnswer struct {
	Parejas []struct {
		A         string  `json:"a"`
		B         string  `json:"b"`
		Confianza float64 `json:"confianza"`
		Motivo    string  `json:"motivo"`
	} `json:"parejas"`
}

var schemaPairs = json.RawMessage(`{"type":"object","required":["parejas"],"properties":{"parejas":{"type":"array","items":{"type":"object","required":["a","confianza"]}}}}`)

const pairSystem = `You match the screens of two versions of the same web application for a before/after comparison. A screen of the first version may have been renamed, moved or rewritten in the second; the same purpose means the same screen. Answer only with JSON.`

// UnmatchedScreens lists what the deterministic matching left over.
func UnmatchedScreens(a, b *Manifest, manual []Pair) (removed, added []ScreenInfo) {
	_, newIDs, removedIDs := MatchScreens(a, b, manual)
	for _, id := range removedIDs {
		if s := a.Screen(id); s != nil {
			removed = append(removed, *s)
		}
	}
	for _, id := range newIDs {
		if s := b.Screen(id); s != nil {
			added = append(added, *s)
		}
	}
	return
}

// ProposePairs asks an AI which of the screens only in A match which of the
// screens only in B. It sends titles, routes and file names, never code. The
// answer is validated (known ids, one use each, confidence 0 to 1) and sent
// back with the error up to retries times.
func ProposePairs(ctx context.Context, p ai.Provider, ref ai.Ref, removed, added []ScreenInfo, retries int) ([]Pair, error) {
	if len(removed) == 0 || len(added) == 0 {
		return nil, nil
	}
	describe := func(l []ScreenInfo) string {
		var sb strings.Builder
		for _, s := range l {
			fmt.Fprintf(&sb, "- id %q, title %q, route %q, files %v\n", s.ID, s.Title, s.Route, s.Origin)
		}
		return sb.String()
	}
	prompt := "Screens only in the FIRST version:\n" + describe(removed) + "\nScreens only in the SECOND version:\n" + describe(added) +
		"\nFor each screen of the first version say which screen of the second it became, as {\"parejas\":[{\"a\":\"<first id>\",\"b\":\"<second id or empty when there is none>\",\"confianza\":0.0 to 1.0,\"motivo\":\"one short sentence\"}]}. Use each id once. Judge from the title, route and file names."
	inA, inB := map[string]bool{}, map[string]bool{}
	for _, s := range removed {
		inA[s.ID] = true
	}
	for _, s := range added {
		inB[s.ID] = true
	}
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		q := prompt
		if lastErr != nil {
			q += "\n\nYour previous answer was rejected: " + lastErr.Error() + "\nAnswer again."
		}
		res, err := p.Run(ctx, ai.Job{Model: ref.Model, System: pairSystem, Prompt: q, Schema: schemaPairs, Timeout: 3 * time.Minute, MaxTurns: 1})
		if err == nil {
			var ans pairAnswer
			if err = strictJSON(res.JSON, &ans); err == nil {
				var out []Pair
				usedA, usedB := map[string]bool{}, map[string]bool{}
				for _, x := range ans.Parejas {
					switch {
					case !inA[x.A]:
						err = fmt.Errorf("%q is not a screen of the first version", x.A)
					case usedA[x.A]:
						err = fmt.Errorf("%q is used twice", x.A)
					case x.B != "" && !inB[x.B]:
						err = fmt.Errorf("%q is not a screen of the second version", x.B)
					case x.B != "" && usedB[x.B]:
						err = fmt.Errorf("%q is used twice", x.B)
					case x.Confianza < 0 || x.Confianza > 1:
						err = fmt.Errorf("confianza %v is not between 0 and 1", x.Confianza)
					}
					if err != nil {
						break
					}
					usedA[x.A], usedB[x.B] = true, x.B != ""
					out = append(out, Pair{AID: x.A, BID: x.B, Method: ByAI, Confidence: x.Confianza, Reason: x.Motivo})
				}
				if err == nil {
					return out, nil
				}
			}
			err = fmt.Errorf("%w: %v", ai.ErrBadAnswer, err)
		}
		if !errors.Is(err, ai.ErrBadAnswer) {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}
