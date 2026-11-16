package capture

import (
	"context"
	"errors"
	"testing"

	"github.com/ElMaxter99/Theo/internal/ai"
)

func manifest(screens ...ScreenInfo) *Manifest { return &Manifest{Screens: screens} }

func TestManualPairWinsOverTheRules(t *testing.T) {
	a := manifest(ScreenInfo{ID: "inicio", Route: "/"}, ScreenInfo{ID: "panel", Route: "/panel"})
	b := manifest(ScreenInfo{ID: "inicio", Route: "/"}, ScreenInfo{ID: "panel-nuevo", Route: "/p"})
	// By hand: "inicio" of A is "panel-nuevo" of B, although the ids match.
	pairs, newIDs, removed := MatchScreens(a, b, []Pair{{AID: "inicio", BID: "panel-nuevo", Method: ManualPair}})
	got := map[string]string{}
	for _, p := range pairs {
		got[p.AID] = p.BID + "/" + p.Method
	}
	if got["inicio"] != "panel-nuevo/manual" {
		t.Errorf("the pair by hand must win: %v", got)
	}
	if len(newIDs) != 1 || newIDs[0] != "inicio" || len(removed) != 1 || removed[0] != "panel" {
		t.Errorf("leftovers: new %v removed %v", newIDs, removed)
	}
}

func TestAIPairsApplyOnlyToWhatTheRulesLeftOver(t *testing.T) {
	a := manifest(ScreenInfo{ID: "a1", Route: "/a"}, ScreenInfo{ID: "ayuda", Route: "/ayuda"})
	b := manifest(ScreenInfo{ID: "a1", Route: "/a"}, ScreenInfo{ID: "soporte", Route: "/soporte"})
	pairs, _, _ := MatchScreens(a, b, []Pair{{AID: "a1", BID: "soporte", Method: ByAI, Confidence: 0.9}, {AID: "ayuda", BID: "soporte", Method: ByAI, Confidence: 0.8}})
	if len(pairs) != 2 || pairs[0].Method != ByID || pairs[1].Method != ByAI || pairs[1].AID != "ayuda" {
		t.Errorf("%+v", pairs)
	}
}

func TestApplicableKeepsHandAndConfidentPairs(t *testing.T) {
	f := PairFile{"x..y": {
		{AID: "a", BID: "b", Method: ByAI, Confidence: 0.9},
		{AID: "c", BID: "d", Method: ByAI, Confidence: 0.3},
		{AID: "e", BID: "", Method: ByAI, Confidence: 0.9},
		{AID: "g", BID: "h", Method: ManualPair},
	}}
	got := f.Applicable("x..y")
	if len(got) != 2 || got[0].AID != "a" || got[1].AID != "g" {
		t.Errorf("%+v", got)
	}
}

func TestProposePairsValidatesAndRetries(t *testing.T) {
	removed := []ScreenInfo{{ID: "ayuda"}, {ID: "detalle"}}
	added := []ScreenInfo{{ID: "soporte"}}
	stub := &ai.Stub{Rules: []ai.StubRule{{Contains: "FIRST version", Replies: []string{
		`{"parejas":[{"a":"inventada","b":"soporte","confianza":0.9}]}`,
		`{"parejas":[{"a":"ayuda","b":"soporte","confianza":0.8,"motivo":"help"},{"a":"detalle","b":"soporte","confianza":0.7}]}`,
		`{"parejas":[{"a":"ayuda","b":"soporte","confianza":0.8,"motivo":"help"},{"a":"detalle","b":"","confianza":0.2}]}`,
	}}}}
	got, err := ProposePairs(context.Background(), stub, ai.Ref{Provider: "stub"}, removed, added, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Method != ByAI || got[0].BID != "soporte" || got[1].BID != "" || len(stub.Jobs) != 3 {
		t.Fatalf("%+v after %d jobs", got, len(stub.Jobs))
	}
	if q := stub.Jobs[1].Prompt; !contains([]string{q}, q) || len(q) < 20 {
		t.Error("the error is sent back")
	}
	// Nothing to match: nothing is asked.
	if p, err := ProposePairs(context.Background(), stub, ai.Ref{}, nil, added, 2); p != nil || err != nil || len(stub.Jobs) != 3 {
		t.Errorf("%v %v", p, err)
	}
	// A provider error is returned as it is (the limit must stop the caller).
	lim := &ai.Stub{Rules: []ai.StubRule{{Contains: "FIRST version", Err: ai.ErrLimit}}}
	if _, err := ProposePairs(context.Background(), lim, ai.Ref{}, removed, added, 2); !errors.Is(err, ai.ErrLimit) {
		t.Errorf("%v", err)
	}
}
