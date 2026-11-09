package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/ElMaxter99/Theo/internal/engine"
)

const keepParkRuns = 10

// parkRun is what a bulk park has done so far, kept so a browser can ask how
// far it is (GET /projects/{p}/park-inactive/{op}) while the operation runs.
type parkRun struct {
	mu       sync.Mutex
	project  string
	total    int
	done     int
	current  string
	branch   string
	finished []engine.ParkInactiveEntry
	report   *engine.ParkInactiveReport
}

func (r *parkRun) update(pp engine.ParkProgress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.total, r.done, r.current, r.branch = pp.Total, pp.Done, pp.Current, pp.Branch
	if pp.Entry != nil {
		r.finished = append(r.finished, *pp.Entry)
	}
}

// parkRunView is the JSON form of a parkRun together with its operation.
type parkRunView struct {
	Operation opView                     `json:"operation"`
	Total     int                        `json:"total"`
	Done      int                        `json:"done"`
	Current   string                     `json:"current,omitempty"`
	Branch    string                     `json:"branch,omitempty"`
	Entries   []engine.ParkInactiveEntry `json:"entries"`
	Report    *engine.ParkInactiveReport `json:"report,omitempty"`
}

type parkRuns struct {
	mu    sync.Mutex
	byID  map[string]*parkRun
	order []string
}

func (p *parkRuns) add(id string, r *parkRun) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byID == nil {
		p.byID = map[string]*parkRun{}
	}
	p.byID[id] = r
	p.order = append(p.order, id)
	for len(p.order) > keepParkRuns {
		delete(p.byID, p.order[0])
		p.order = p.order[1:]
	}
}

func (p *parkRuns) get(id string) *parkRun {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.byID[id]
}

// startParkInactive runs the bulk park as an operation, reporting progress.
func (s *Server) startParkInactive(sess *session, idemKey, project string, opts engine.ParkInactiveOptions) (*Operation, error) {
	pr := &parkRun{project: project, finished: []engine.ParkInactiveEntry{}}
	opts.Progress = pr.update
	run := func(ctx context.Context, _, _ io.Writer) (string, error) {
		report, err := s.eng.ParkInactive(ctx, project, opts)
		s.cache.invalidate(project)
		s.hub.publish("state.changed", map[string]string{"project": project})
		if err != nil {
			return "", err
		}
		pr.mu.Lock()
		pr.report = report
		pr.mu.Unlock()
		sm := report.Summary
		return fmt.Sprintf("Parked %d of %d inactive worktrees; %d stayed.", countParked(report), sm.Total, sm.Total-countParked(report)), nil
	}
	op, replay, err := s.ops.start(startRequest{kind: "park-inactive", project: project, key: "park-inactive|" + project,
		session: sess.id, idemKey: idemKey, run: run})
	if err != nil {
		return nil, err
	}
	if !replay {
		s.parkRuns.add(op.ID, pr)
	}
	return op, nil
}

func countParked(r *engine.ParkInactiveReport) int {
	n := 0
	for _, e := range r.Entries {
		if e.Parked {
			n++
		}
	}
	return n
}

// handleParkInactiveRun answers how far a bulk park has got.
func (s *Server) handleParkInactiveRun(w http.ResponseWriter, r *http.Request, _ *session) error {
	id := r.PathValue("op")
	op, pr := s.ops.get(id), s.parkRuns.get(id)
	if op == nil || pr == nil || pr.project != r.PathValue("p") {
		return &Error{Status: http.StatusNotFound, Code: "operation_not_found", Message: "no bulk park " + quote(id)}
	}
	pr.mu.Lock()
	v := parkRunView{Operation: op.view(), Total: pr.total, Done: pr.done, Current: pr.current, Branch: pr.branch,
		Entries: append([]engine.ParkInactiveEntry{}, pr.finished...), Report: pr.report}
	pr.mu.Unlock()
	return writeJSON(w, http.StatusOK, v)
}
