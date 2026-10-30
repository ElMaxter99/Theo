package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestDumpExamplesV5 captures the answers of the shared services, reconcile,
// health, diagnostics and paginated history resources into files, to paste
// into docs/api.md. It runs only when UB_DOCS_DIR is set (see
// TestDumpExamples).
func TestDumpExamplesV5(t *testing.T) {
	out := os.Getenv("UB_DOCS_DIR")
	if out == "" {
		t.Skip("set UB_DOCS_DIR to capture examples")
	}
	os.MkdirAll(out, 0o755)
	d := newSharedDockerEnv(t)
	save := func(name string, status int, body []byte, hdr http.Header, headers ...string) {
		var pretty bytes.Buffer
		if json.Indent(&pretty, bytes.TrimSpace(body), "", "  ") != nil {
			pretty.Reset()
			pretty.Write(body)
		}
		head := "HTTP " + itoa3(status) + "\n"
		for _, h := range headers {
			if v := hdr.Get(h); v != "" {
				head += h + ": " + v + "\n"
			}
		}
		os.WriteFile(filepath.Join(out, name+".txt"), append([]byte(head), pretty.Bytes()...), 0o644)
	}
	send := func(name, method, path string, body any, headers ...string) []byte {
		st, b, h := d.c.raw(method, path, body, nil)
		save(name, st, b, h, headers...)
		return b
	}
	long := &dockerEnv{env: d.env}
	p := "/api/v1/projects/" + d.project
	mainWT, linkedWT := d.wt(d.main), d.wt(d.linked)
	send("shared-not-configured", "GET", p+"/shared", nil)
	send("shared-add", "POST", p+"/shared", map[string]any{"services": []string{"db"}})
	var op opState
	json.Unmarshal(send("shared-up", "POST", p+"/shared/up", nil, "Location"), &op)
	long.waitOpLong(op.ID)
	send("shared-use", "PUT", linkedWT+"/shared", map[string]any{"use": []string{"db"}})
	d.c.call("PUT", mainWT+"/shared", map[string]any{"use": []string{"db"}}, nil)
	for _, w := range []string{mainWT, linkedWT} {
		d.op("POST", w+"/up", map[string]any{"auto_ports": true})
	}
	send("shared-status", "GET", p+"/shared", nil)
	send("shared-down-consumers", "POST", p+"/shared/down", nil)
	send("shared-purge-in-use", "POST", p+"/shared/purge", map[string]any{"confirm": d.shared})
	send("shared-remove-in-use", "DELETE", p+"/shared/services/db", nil)
	json.Unmarshal(send("shared-down", "POST", p+"/shared/down", map[string]any{"confirm": d.shared}), &op)
	long.waitOpLong(op.ID)
	for _, w := range []string{mainWT, linkedWT} {
		d.op("POST", w+"/down", nil)
		d.c.call("DELETE", w+"/shared", nil, nil)
	}
	send("history-page", "GET", linkedWT+"/history?limit=1", nil, "Theo-History-Total", "Theo-History-Next")
	send("history-project", "GET", p+"/history?limit=2", nil, "Theo-History-Total", "Theo-History-Next")
	send("shared-purge-confirm", "POST", p+"/shared/purge", map[string]any{})
	send("shared-purge", "POST", p+"/shared/purge", map[string]any{"confirm": d.shared})
	send("shared-remove", "DELETE", p+"/shared/services/db", nil)
	// Reconcile: delete a worktree behind Theo's back.
	d.c.call("PUT", linkedWT+"/env/UB5_X", map[string]any{"value": "1"}, nil)
	if o, err := exec.Command("git", "-C", d.main, "worktree", "remove", "--force", d.linked).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, o)
	}
	send("reconcile", "GET", "/api/v1/reconcile?project="+d.project, nil)
	send("health", "GET", "/api/v1/health", nil)
	var rep reconcileOut
	d.c.call("GET", "/api/v1/reconcile?project="+d.project, nil, &rep)
	var ids []string
	for _, f := range rep.Findings {
		if f.Forgettable {
			ids = append(ids, f.ID)
		}
	}
	send("reconcile-apply", "POST", "/api/v1/reconcile/apply", map[string]any{"project": d.project, "ids": ids})
	send("meta", "GET", "/api/v1/meta", nil)
	send("diagnostics", "GET", "/api/v1/diagnostics", nil)
}
