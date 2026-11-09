package api_test

import (
	"testing"
	"time"
)

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

// Parking runs as an operation: 202 at once, then the progress can be asked for until it finishes.
func TestParkInactiveRunsAsAnOperationWithProgress(t *testing.T) {
	e := newEnv(t)
	var op struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		State string `json:"state"`
	}
	if st := e.c.call("POST", "/api/v1/projects/ubapp/park-inactive", map[string]any{"confirm": "ubapp"}, &op); st != 202 || op.ID == "" || op.Kind != "park-inactive" {
		t.Fatalf("start = %d %+v", st, op)
	}
	var run struct {
		Operation struct {
			State string `json:"state"`
		} `json:"operation"`
		Total   int              `json:"total"`
		Done    int              `json:"done"`
		Entries []map[string]any `json:"entries"`
	}
	for i := 0; i < 100; i++ {
		if st := e.c.call("GET", "/api/v1/projects/ubapp/park-inactive/"+op.ID, nil, &run); st != 200 {
			t.Fatalf("progress = %d", st)
		}
		if run.Operation.State != "running" && run.Operation.State != "queued" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if run.Operation.State != "succeeded" || run.Done != run.Total || len(run.Entries) != run.Done {
		t.Errorf("finished run = %+v", run)
	}
	var nf apiErr
	if st := e.c.call("GET", "/api/v1/projects/ubapp/park-inactive/op_nope", nil, &nf); st != 404 {
		t.Errorf("unknown run = %d, want 404", st)
	}
}
