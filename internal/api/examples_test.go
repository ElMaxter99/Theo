package api_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDumpExamples captures real answers of a running server into files, to
// paste into docs/api.md. It runs only when UB_DOCS_DIR is set:
//
//	UB_DOCS_DIR=/tmp/examples go test -run TestDumpExamples ./internal/api
func TestDumpExamples(t *testing.T) {
	out := os.Getenv("UB_DOCS_DIR")
	if out == "" {
		t.Skip("set UB_DOCS_DIR to capture examples")
	}
	os.MkdirAll(out, 0o755)
	d := newDockerEnv(t)
	save := func(name string, status int, body []byte) {
		var pretty bytes.Buffer
		if json.Indent(&pretty, bytes.TrimSpace(body), "", "  ") != nil {
			pretty.Reset()
			pretty.Write(body)
		}
		os.WriteFile(filepath.Join(out, name+".txt"), append([]byte("HTTP "+itoa3(status)+"\n"), pretty.Bytes()...), 0o644)
	}
	get := func(name, path string) {
		st, b, _ := d.c.raw("GET", path, nil, nil)
		save(name, st, b)
	}
	send := func(name, method, path string, body any) []byte {
		st, b, _ := d.c.raw(method, path, body, nil)
		save(name, st, b)
		return b
	}
	p := "/api/v1/projects/" + d.project
	// Before anything exists.
	anon := newBareClient(t, d.srv.Port())
	st, b, _ := anon.raw("GET", "/api/v1/projects", nil, nil)
	save("error-unauthorized", st, b)
	get("meta", "/api/v1/meta")
	get("session", "/api/v1/session")
	get("projects", "/api/v1/projects")
	get("branches", p+"/branches")
	get("browse", "/api/v1/fs/browse?path="+filepath.Dir(d.main))
	send("project-not-found", "GET", "/api/v1/projects/nope/overview", nil)
	get("overview-absent", p+"/overview")
	base := d.base(d.linked)
	b = send("worktree-up", "POST", base+"/up", map[string]any{"auto_ports": true})
	var op opState
	json.Unmarshal(b, &op)
	send("worktree-up-busy", "POST", base+"/up", nil)
	d.waitOpLong(op.ID)
	// A few events of the operation.
	s := d.c.sse("/api/v1/operations/"+op.ID+"/events", "")
	var ev strings.Builder
	for i := 0; i < 400; i++ {
		e, ok := s.next(t, time.Second)
		if !ok {
			break
		}
		if e.Name == "log" && i > 6 && !strings.Contains(e.Data, "Created") && !strings.Contains(e.Data, "Started") {
			continue
		}
		ev.WriteString("id: " + e.ID + "\nevent: " + e.Name + "\ndata: " + e.Data + "\n\n")
	}
	os.WriteFile(filepath.Join(out, "operation-events.txt"), []byte(ev.String()), 0o644)
	get("operation", "/api/v1/operations/"+op.ID)
	get("operations", "/api/v1/operations")
	get("overview", p+"/overview?refresh=1")
	get("worktrees", p+"/worktrees")
	get("worktree", base)
	get("services", base+"/services")
	get("ports", base+"/ports")
	get("ports-auto", base+"/ports?auto_ports=1")
	send("env-put", "PUT", base+"/env/DB_URL", map[string]any{"value": "postgres://user:hunter2@db/app", "secret": true})
	send("env-put-plain", "PUT", base+"/env/LOG_LEVEL", map[string]any{"value": "debug", "services": []string{"web"}})
	get("env", base+"/env")
	send("env-reveal", "POST", base+"/env/DB_URL/reveal", nil)
	get("settings", base+"/settings")
	send("settings-patch", "PATCH", base+"/settings", map[string]any{"neutralize_container_names": true})
	get("config", base+"/config")
	get("history", base+"/history")
	// A log line.
	ls := d.c.sse(base+"/logs?follow=1&tail=5", "")
	var lb strings.Builder
	for i := 0; i < 3; i++ {
		e, ok := ls.next(t, 4*time.Second)
		if !ok {
			break
		}
		lb.WriteString("id: " + e.ID + "\nevent: " + e.Name + "\ndata: " + e.Data + "\n\n")
	}
	ls.close()
	os.WriteFile(filepath.Join(out, "logs-events.txt"), []byte(lb.String()), 0o644)
	// Down, volumes and the purge confirmation.
	b = send("worktree-down", "POST", base+"/down", nil)
	json.Unmarshal(b, &op)
	d.waitOpLong(op.ID)
	get("volumes", base+"/volumes")
	send("volumes-purge-confirm", "POST", base+"/volumes/purge", map[string]any{})
	send("volumes-purge-wrong", "POST", base+"/volumes/purge", map[string]any{"confirm": "nope"})
	// Create and remove a worktree.
	path := filepath.Join(t.TempDir(), "ub-demo")
	b = send("worktree-create", "POST", p+"/worktrees", map[string]any{"branch": "ub-demo", "new": true, "path": path})
	var created struct{ ID string }
	json.Unmarshal(b, &created)
	send("worktree-create-conflict", "POST", p+"/worktrees", map[string]any{"branch": "ub-demo", "path": filepath.Join(t.TempDir(), "other")})
	os.WriteFile(filepath.Join(path, "notes.txt"), []byte("x"), 0o644)
	send("worktree-remove-confirm", "POST", p+"/worktrees/"+created.ID+"/remove", map[string]any{})
	send("worktree-remove", "POST", p+"/worktrees/"+created.ID+"/remove", map[string]any{"confirm": "ub-demo"})
	send("project-patch", "PATCH", p, map[string]any{"worktree_dir": nil})
	send("project-delete-body", "DELETE", p, nil)
	// launch
	st, b, _ = anon.raw("POST", "/api/v1/launch", nil, map[string]string{"X-Theo-Control": d.srv.ControlSecret()})
	save("launch", st, b)
}

func itoa3(n int) string { b, _ := json.Marshal(n); return string(b) }
