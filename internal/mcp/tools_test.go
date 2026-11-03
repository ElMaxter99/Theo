package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// setup registers a real Git repository as a project in a temporary
// THEO_HOME and returns a server on the engine, the project name and the
// paths of the main and the linked worktree ("feature/x").
func setup(t *testing.T, files map[string]string, opts Options) (s *Server, project, main, linked string) {
	t.Helper()
	main, linked = testutil.NewRepo(t, files)
	t.Setenv(registry.EnvHome, testutil.TempDir(t))
	e, err := engine.Open()
	if err != nil {
		t.Fatal(err)
	}
	project = fmt.Sprintf("mcpt-%d", time.Now().UnixNano()%1_000_000_000)
	if _, err := e.AddProject(context.Background(), main, project, ""); err != nil {
		t.Fatal(err)
	}
	opts.Engine = e
	return New(opts), project, main, linked
}

// callTool calls a tool the way tools/call does and returns the result,
// decoded from JSON as a client sees it.
func callTool(t *testing.T, s *Server, name string, arguments map[string]any) (text string, structured map[string]any, isError bool) {
	t.Helper()
	tl, ok := s.byName[name]
	if !ok {
		t.Fatalf("no tool %s", name)
	}
	raw, _ := json.Marshal(arguments)
	res := s.call(context.Background(), tl, raw)
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("%s: result does not encode: %v", name, err)
	}
	var decoded struct {
		Content []struct {
			Type, Text string
		} `json:"content"`
		StructuredContent map[string]any `json:"structuredContent"`
		IsError           bool           `json:"isError"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("%s: %v (%s)", name, err, data)
	}
	if len(decoded.Content) == 0 || decoded.Content[0].Type != "text" {
		t.Fatalf("%s: no text content: %s", name, data)
	}
	return decoded.Content[0].Text, decoded.StructuredContent, decoded.IsError
}

func mustCall(t *testing.T, s *Server, name string, arguments map[string]any) (string, map[string]any) {
	t.Helper()
	text, sc, isErr := callTool(t, s, name, arguments)
	if isErr {
		t.Fatalf("%s %v failed: %s", name, arguments, text)
	}
	return text, sc
}

func errorOf(t *testing.T, s *Server, name string, arguments map[string]any) (string, map[string]any) {
	t.Helper()
	text, sc, isErr := callTool(t, s, name, arguments)
	if !isErr {
		t.Fatalf("%s %v should fail, got: %s", name, arguments, text)
	}
	return text, sc["error"].(map[string]any)
}

func TestProjectAndWorktreeTools(t *testing.T) {
	s, project, main, _ := setup(t, nil, Options{Owner: "agent-1"})

	text, sc := mustCall(t, s, "list_projects", nil)
	if !strings.Contains(text, project) || len(sc["projects"].([]any)) != 1 {
		t.Errorf("list_projects: %s %v", text, sc)
	}
	_, sc = mustCall(t, s, "project_show", map[string]any{"project": project})
	if sc["name"] != project || sc["path"] != main {
		t.Errorf("project_show: %v", sc)
	}
	if _, e := errorOf(t, s, "project_show", map[string]any{"project": "nope"}); e["code"] != "not_found" {
		t.Errorf("unknown project: %v", e)
	}

	_, sc = mustCall(t, s, "list_worktrees", map[string]any{"project": project})
	if n := len(sc["worktrees"].([]any)); n != 2 {
		t.Errorf("list_worktrees: %d worktrees", n)
	}

	// add_worktree without owner takes the server's --owner.
	_, sc = mustCall(t, s, "add_worktree", map[string]any{"project": project, "branch": "mcp/one", "new": true})
	if sc["owner"] != "agent-1" || sc["kind"] != engine.KindWorktree {
		t.Errorf("add_worktree: %v", sc)
	}
	added := sc["path"].(string)
	_, sc = mustCall(t, s, "add_worktree", map[string]any{"project": project, "branch": "mcp/two", "new": true, "from": "main", "owner": "someone"})
	if sc["owner"] != "someone" {
		t.Errorf("explicit owner: %v", sc)
	}
	if _, e := errorOf(t, s, "add_worktree", map[string]any{"project": project, "branch": "x", "from": "main"}); e["code"] != "invalid_arguments" {
		t.Errorf("from without new: %v", e)
	}
	if text, _ := errorOf(t, s, "add_worktree", map[string]any{"project": project, "branch": "mcp/one"}); text == "" {
		t.Error("a branch checked out elsewhere must be refused")
	}

	_, sc = mustCall(t, s, "list_worktrees", map[string]any{"project": project, "owner": "agent-1"})
	if list := sc["worktrees"].([]any); len(list) != 1 || list[0].(map[string]any)["path"] != added {
		t.Errorf("owner filter: %v", list)
	}
	_, sc = mustCall(t, s, "list_worktrees", map[string]any{"project": project, "owner": "nobody"})
	if list := sc["worktrees"].([]any); len(list) != 0 {
		t.Errorf("owner filter nobody: %v", list)
	}

	_, sc = mustCall(t, s, "list_branches", map[string]any{"project": project})
	found := false
	for _, b := range sc["branches"].([]any) {
		if b.(map[string]any)["name"] == "mcp/one" && b.(map[string]any)["worktree"] == added {
			found = true
		}
	}
	if !found {
		t.Errorf("list_branches: %v", sc)
	}

	_, sc = mustCall(t, s, "history", map[string]any{"project": project})
	if _, ok := sc["events"].([]any); !ok {
		t.Errorf("history: %v", sc)
	}
	if _, e := errorOf(t, s, "history", map[string]any{"project": project, "limit": 100000}); e["code"] != "invalid_arguments" {
		t.Errorf("history limit: %v", e)
	}
	text, sc = mustCall(t, s, "reconcile", map[string]any{"project": project})
	if _, ok := sc["findings"]; !ok || sc["forgotten"] != float64(0) {
		t.Errorf("reconcile: %s %v", text, sc)
	}
}

const composeFile = `services:
  web:
    image: mcpt-never-pulled:latest
    environment:
      TOKEN: ${TOKEN:-none}
    ports: ["8080"]
`

func TestInstanceSettingsTools(t *testing.T) {
	testutil.RequireCompose(t)
	s, project, _, _ := setup(t, map[string]string{"compose.yaml": composeFile}, Options{})
	wt := map[string]any{"project": project, "worktree": "feature/x"}
	with := func(more map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range wt {
			out[k] = v
		}
		for k, v := range more {
			out[k] = v
		}
		return out
	}

	text, sc := mustCall(t, s, "services", wt)
	if !strings.Contains(text, "web") || sc["compose_project"] != project+"-app-feature" {
		t.Errorf("services: %s %v", text, sc)
	}

	const secret = "s3cr3t-value-for-mcp"
	mustCall(t, s, "env_set", with(map[string]any{"name": "TOKEN", "value": secret, "secret": true}))
	text, sc = mustCall(t, s, "env_set", with(map[string]any{"name": "PLAIN", "value": "visible", "services": []string{"web"}}))
	if strings.Contains(text, "visible") {
		t.Errorf("env_set echoes the value: %s", text)
	}
	text, sc = mustCall(t, s, "env_list", wt)
	raw, _ := json.Marshal(sc)
	if strings.Contains(text, secret) || strings.Contains(string(raw), secret) {
		t.Fatalf("env_list leaks the secret: %s %s", text, raw)
	}
	if sc["TOKEN"].(map[string]any)["value"] != engine.Mask || sc["PLAIN"].(map[string]any)["value"] != "visible" {
		t.Errorf("env_list: %v", sc)
	}
	if _, ok := s.byName["env_list"].schema()["properties"].(map[string]any)["show_secrets"]; ok {
		t.Error("env_list must not offer to show secrets")
	}

	text, sc = mustCall(t, s, "instance_config", wt)
	raw, _ = json.Marshal(sc)
	if strings.Contains(text, secret) || strings.Contains(string(raw), secret) {
		t.Fatalf("instance_config leaks the secret:\n%s", text)
	}
	if !strings.Contains(sc["config"].(string), engine.Mask) {
		t.Errorf("instance_config should show the masked secret:\n%s", sc["config"])
	}

	_, sc = mustCall(t, s, "instance_show", wt)
	if vars := sc["variables"].([]any); len(vars) != 2 {
		t.Errorf("instance_show variables: %v", vars)
	}
	raw, _ = json.Marshal(sc)
	if strings.Contains(string(raw), secret) {
		t.Error("instance_show leaks the secret")
	}

	mustCall(t, s, "env_unset", with(map[string]any{"name": "PLAIN"}))
	if text, _ := errorOf(t, s, "env_unset", with(map[string]any{"name": "PLAIN"})); !strings.Contains(text, "no variable PLAIN") {
		t.Errorf("unset twice: %s", text)
	}

	// Docker may be down: status still answers.
	text, sc = mustCall(t, s, "status", map[string]any{"project": project})
	if len(sc["projects"].([]any)) != 1 {
		t.Errorf("status: %s %v", text, sc)
	}

	// The logs tail is bounded.
	if _, e := errorOf(t, s, "logs", with(map[string]any{"tail": maxLogTail + 1})); e["code"] != "invalid_arguments" {
		t.Errorf("logs tail: %v", e)
	}
}

// TestRemoveWorktreeTool needs Docker: the engine refuses to remove a
// worktree when it cannot check that it has no containers.
func TestRemoveWorktreeTool(t *testing.T) {
	testutil.RequireDaemon(t)
	s, project, main, _ := setup(t, nil, Options{})
	_, sc := mustCall(t, s, "add_worktree", map[string]any{"project": project, "branch": "mcp/one", "new": true})
	added := sc["path"].(string)
	// remove_worktree is refused while the worktree has untracked files,
	// exactly like the CLI, and the refusal says how to confirm.
	testutil.WriteFile(t, added, "notes.txt", "work in progress\n")
	text, e := errorOf(t, s, "remove_worktree", map[string]any{"project": project, "worktree": "mcp/one"})
	dir := filepath.Base(added)
	if e["code"] != "confirmation_required" || e["confirm"] != dir || !strings.Contains(text, "notes.txt") || !strings.Contains(text, "confirm=") {
		t.Errorf("dirty remove: %s %v", text, e)
	}
	if _, err := os.Stat(added); err != nil {
		t.Fatal("the worktree was removed although it is dirty")
	}
	if _, e := errorOf(t, s, "remove_worktree", map[string]any{"project": project, "worktree": "mcp/one", "discard_changes": true, "confirm": "wrong"}); e["code"] != "confirmation_required" {
		t.Errorf("wrong confirm: %v", e)
	}
	if _, e := errorOf(t, s, "remove_worktree", map[string]any{"project": project, "worktree": "mcp/one", "confirm": dir}); e["code"] != "invalid_arguments" {
		t.Errorf("confirm without discard_changes: %v", e)
	}
	_, sc = mustCall(t, s, "remove_worktree", map[string]any{"project": project, "worktree": "mcp/one", "discard_changes": true, "confirm": dir})
	if sc["path"] != added || sc["branch"] != "mcp/one" {
		t.Errorf("remove: %v", sc)
	}
	if _, err := os.Stat(added); !os.IsNotExist(err) {
		t.Error("the worktree is still there")
	}
	// The branch is kept.
	if out, err := exec.Command("git", "-C", main, "rev-parse", "--verify", "mcp/one").CombinedOutput(); err != nil {
		t.Errorf("branch was deleted: %s", out)
	}

}

func TestReadOnlyReconcileTakesNoApply(t *testing.T) {
	s, project, _, _ := setup(t, nil, Options{ReadOnly: true})
	if _, e := errorOf(t, s, "reconcile", map[string]any{"project": project, "apply": true}); e["code"] != "invalid_arguments" {
		t.Errorf("read-only reconcile with apply: %v", e)
	}
	mustCall(t, s, "reconcile", map[string]any{"project": project})
	if _, ok := s.byName["add_worktree"]; ok {
		t.Error("read-only server has add_worktree")
	}
}

func TestClaimRefusesConcurrentOperations(t *testing.T) {
	s := New(Options{})
	release, err := s.claim("app-x", "up")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.claim("app-x", "down"); err == nil || !strings.Contains(err.Error(), "up is already running") {
		t.Errorf("second claim: %v", err)
	}
	if res := toolError(fmt.Errorf("wrapped: %w", errBusy)); res.StructuredContent.(map[string]any)["error"].(map[string]any)["code"] != "busy" {
		t.Errorf("busy code: %v", res.StructuredContent)
	}
	release()
	release() // twice is harmless
	if r, err := s.claim("app-x", "down"); err != nil {
		t.Errorf("after release: %v", err)
	} else {
		r()
	}
}

// TestDockerTools runs up, ps, ports, logs, explain, volumes_list, stop,
// start, restart and down on a real instance.
func TestDockerTools(t *testing.T) {
	testutil.RequireDaemon(t)
	files := map[string]string{
		".gitignore": "server\nDockerfile\n",
		"compose.yaml": `services:
  web:
    build: .
    environment:
      TOKEN: ${TOKEN:-none}
    ports: ["8080"]
    volumes: [data:/data]
volumes:
  data: {}
`,
	}
	s, project, _, linked := setup(t, files, Options{})
	testutil.WriteServerImage(t, linked) // ignored files: written in the worktree that runs
	wt := map[string]any{"project": project, "worktree": "feature/x"}
	composeName := project + "-app-feature"
	t.Cleanup(func() {
		exec.Command("docker", "compose", "-p", composeName, "down", "--volumes", "--rmi", "local").Run()
		exec.Command("docker", "volume", "rm", "-f", composeName+"_data").Run()
	})
	with := func(more map[string]any) map[string]any {
		out := map[string]any{"project": project, "worktree": "feature/x"}
		for k, v := range more {
			out[k] = v
		}
		return out
	}

	// explain lists the changes and runs none of them.
	text, sc := mustCall(t, s, "explain", with(map[string]any{"operation": "up", "build": true}))
	if sc["changes"].(float64) < 1 || !strings.Contains(text, "compose") || !strings.Contains(text, " up ") {
		t.Errorf("explain up: %s", text)
	}
	if got := testutil.Docker(t, "ps", "-a", "--filter", "label=com.docker.compose.project="+composeName, "--format", "{{.Names}}"); got != "" {
		t.Fatalf("explain started containers: %s", got)
	}
	_, sc = mustCall(t, s, "history", map[string]any{"project": project})
	if n := len(sc["events"].([]any)); n != 0 {
		t.Errorf("explain recorded %d event(s)", n)
	}

	mustCall(t, s, "env_set", with(map[string]any{"name": "TOKEN", "value": "mcp-secret-in-logs", "secret": true}))
	text, sc = mustCall(t, s, "up", with(map[string]any{"build": true}))
	if sc["compose_project"] != composeName || !strings.Contains(text, "http://localhost:") {
		t.Errorf("up: %s %v", text, sc)
	}
	if _, ok := sc["output"].(string); !ok {
		t.Errorf("up: no output: %v", sc)
	}

	_, sc = mustCall(t, s, "ps", wt)
	if sc["state"] != "running" || len(sc["containers"].([]any)) != 1 {
		t.Errorf("ps -w: %v", sc)
	}
	text, _ = mustCall(t, s, "ps", map[string]any{"project": project})
	if !strings.Contains(text, composeName) {
		t.Errorf("ps overview: %s", text)
	}
	_, sc = mustCall(t, s, "ports", wt)
	if ports := sc["ports"].([]any); len(ports) != 1 || ports[0].(map[string]any)["url"] == "" {
		t.Errorf("ports: %v", sc)
	}
	// With a proxy running, ports, status and proxy_routes give the local domains.
	s.opts.ProxyPort = func() (int, bool) { return 7080, true }
	host := "app-feature." + project + ".localhost:7080"
	text, sc = mustCall(t, s, "ports", wt)
	if links, _ := sc["proxy_urls"].([]any); len(links) != 2 || links[0].(map[string]any)["url"] != "http://"+host || !strings.Contains(text, "http://web."+host) {
		t.Errorf("ports with a proxy: %s %v", text, sc["proxy_urls"])
	}
	text, _ = mustCall(t, s, "status", map[string]any{"project": project})
	if !strings.Contains(text, "http://"+host) {
		t.Errorf("status with a proxy: %s", text)
	}
	_, sc = mustCall(t, s, "proxy_routes", map[string]any{"project": project})
	if sc["proxy_running"] != true || len(sc["routes"].([]any)) != 2 {
		t.Errorf("proxy_routes: %v", sc)
	}
	s.opts.ProxyPort = func() (int, bool) { return 0, false }
	if _, sc = mustCall(t, s, "ports", wt); sc["proxy_urls"] != nil {
		t.Errorf("ports without a proxy: %v", sc["proxy_urls"])
	}

	text, sc = mustCall(t, s, "logs", with(map[string]any{"tail": 10}))
	if !strings.Contains(sc["logs"].(string), "server ready") {
		t.Errorf("logs: %s", text)
	}
	_, sc = mustCall(t, s, "volumes_list", wt)
	if len(sc["volumes"].([]any)) != 1 {
		t.Errorf("volumes_list: %v", sc)
	}

	// remove_worktree is refused while the instance has containers.
	if _, e := errorOf(t, s, "remove_worktree", map[string]any{"project": project, "worktree": "feature/x"}); e["code"] != "instance_has_containers" {
		t.Errorf("remove with containers: %v", e)
	}

	mustCall(t, s, "stop", with(map[string]any{"services": []string{"web"}}))
	mustCall(t, s, "start", wt)
	mustCall(t, s, "restart", wt)
	if _, e := errorOf(t, s, "restart", with(map[string]any{"services": []string{"nope"}})); e == nil {
		t.Error("restart of an unknown service should fail")
	}

	text, sc = mustCall(t, s, "down", wt)
	if sc["volumes_kept"] != true {
		t.Errorf("down: %s %v", text, sc)
	}
	_, sc = mustCall(t, s, "volumes_list", wt)
	if v := sc["volumes"].([]any); len(v) != 1 || v[0].(map[string]any)["status"] != engine.VolumeKept {
		t.Errorf("volume after down: %v", v)
	}
	_, sc = mustCall(t, s, "history", map[string]any{"project": project, "worktree": "feature/x"})
	if n := len(sc["events"].([]any)); n < 2 {
		t.Errorf("history after up and down: %d event(s)", n)
	}
}

func TestProxyRoutesWithoutInstances(t *testing.T) {
	s, project, _, _ := setup(t, nil, Options{ProxyPort: func() (int, bool) { return 0, false }})
	text, sc := mustCall(t, s, "proxy_routes", map[string]any{"project": project})
	if sc["proxy_running"] != false || sc["port"].(float64) != 7080 || len(sc["routes"].([]any)) != 0 {
		t.Errorf("proxy_routes: %v", sc)
	}
	if !strings.Contains(text, "No proxy is running") {
		t.Errorf("proxy_routes text: %s", text)
	}
}
