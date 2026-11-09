package api_test

import "testing"

// The web client writes boolean query parameters as "1"; the plan must honour them (it once ignored every option,
// so a worktree with changes could never be picked in the interface).
func TestParkInactivePlanReadsTheQueryTheWebClientSends(t *testing.T) {
	e := newEnv(t)
	var plan struct {
		Entries []map[string]any `json:"entries"`
		Summary map[string]any   `json:"summary"`
	}
	for _, q := range []string{"", "?save_changes=1&include_ignored=1&rescue_detached=1&size=1&max_changes=500", "?save_changes=true"} {
		if st := e.c.call("GET", "/api/v1/projects/ubapp/park-inactive"+q, nil, &plan); st != 200 || plan.Summary == nil {
			t.Fatalf("plan%s = %d %+v", q, st, plan)
		}
	}
	var bad apiErr
	if st := e.c.call("POST", "/api/v1/projects/ubapp/park-inactive", map[string]any{"paths": []string{}}, &bad); st != 409 {
		t.Errorf("without confirm = %d, want 409", st)
	}
}
