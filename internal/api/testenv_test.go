package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/api"
	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/git"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// fakeCompose is an in-memory Compose adapter: no Docker is needed.
type fakeCompose struct {
	mu         sync.Mutex
	containers map[string][]compose.Container // by Compose project
	volumes    map[string][]compose.DockerVolume
	users      map[string][]string
	psErr      error
	files      map[string]string // config file of a stack, by Compose project
	// upHook runs inside Up; it can block until ctx is cancelled.
	upHook func(ctx context.Context, t compose.Target, stdout, stderr io.Writer) error
	// logsHook runs inside Logs.
	logsHook func(ctx context.Context, o compose.LogOptions, stdout, stderr io.Writer) error
}

func (f *fakeCompose) Config(_ context.Context, dir string, _ []string) (*compose.Project, error) {
	return &compose.Project{Name: filepath.Base(dir), Services: map[string]compose.Service{
		"web": {Image: "scratch-ub", Ports: []compose.Port{{Target: 80, Published: "18080", Protocol: "tcp"}}},
		"db":  {Image: "scratch-ub-db"},
	}}, nil
}

func (f *fakeCompose) ConfigFor(ctx context.Context, t compose.Target) (*compose.Project, error) {
	return f.Config(ctx, t.Dir, nil)
}
func (f *fakeCompose) ConfigText(_ context.Context, t compose.Target) ([]byte, error) {
	return []byte("name: " + t.Name + "\n"), nil
}

func (f *fakeCompose) Up(ctx context.Context, t compose.Target, _ compose.UpOptions, so, se io.Writer) error {
	if f.upHook != nil {
		if err := f.upHook(ctx, t, so, se); err != nil {
			return err
		}
	}
	f.set(t.Name, running("web", t.Name), running("db", t.Name))
	return nil
}

func running(svc, project string) compose.Container {
	return compose.Container{Name: project + "-" + svc + "-1", Service: svc, State: "running", Status: "Up"}
}

func (f *fakeCompose) set(project string, cs ...compose.Container) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.containers == nil {
		f.containers = map[string][]compose.Container{}
	}
	f.containers[project] = cs
}

func (f *fakeCompose) Down(_ context.Context, t compose.Target, _, _ io.Writer) error {
	f.set(t.Name)
	return nil
}
func (f *fakeCompose) Restart(context.Context, compose.Target, io.Writer, io.Writer) error {
	return nil
}
func (f *fakeCompose) Logs(ctx context.Context, t compose.Target, o compose.LogOptions, so, se io.Writer) error {
	if f.logsHook != nil {
		return f.logsHook(ctx, o, so, se)
	}
	io.WriteString(so, t.Name+"-web-1  | hello\n")
	return nil
}
func (f *fakeCompose) PS(_ context.Context, t compose.Target) ([]compose.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.psErr != nil {
		return nil, f.psErr
	}
	return append([]compose.Container(nil), f.containers[t.Name]...), nil
}
func (f *fakeCompose) Stacks(context.Context) ([]compose.Stack, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []compose.Stack
	for name, cs := range f.containers {
		if len(cs) > 0 {
			out = append(out, compose.Stack{Name: name, Status: "running(1)", ConfigFiles: f.files[name]})
		}
	}
	return out, f.psErr
}
func (f *fakeCompose) Volumes(_ context.Context, p string) ([]compose.DockerVolume, error) {
	return f.volumes[p], nil
}
func (f *fakeCompose) VolumeUsers(_ context.Context, v string) ([]string, error) {
	return f.users[v], nil
}
func (f *fakeCompose) RemoveVolume(context.Context, string) error          { return nil }
func (f *fakeCompose) EnsureNetwork(context.Context, string) (bool, error) { return true, nil }
func (f *fakeCompose) RemoveNetwork(context.Context, string) error         { return nil }

// env is a running server with a fake engine, a repository registered as
// project "ubapp" and a logged-in client.
type env struct {
	t       *testing.T
	srv     *api.Server
	fc      *fakeCompose
	eng     *engine.Engine
	main    string // main worktree
	linked  string // linked worktree on feature/x
	c       *client
	logs    *bytes.Buffer
	dataDir string
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func newEnv(t *testing.T, opts ...func(*api.Config)) *env {
	t.Helper()
	main, linked := testutil.NewRepo(t, map[string]string{"compose.yaml": "services:\n  web:\n    image: scratch-ub\n"})
	dir := testutil.TempDir(t)
	fc := &fakeCompose{}
	eng := engine.New(registry.Open(dir), git.Git{}, fc).WithDataDir(dir)
	if _, err := eng.AddProject(context.Background(), main, "ubapp", ""); err != nil {
		t.Fatal(err)
	}
	return startEnv(t, eng, fc, main, linked, dir, opts...)
}

func startEnv(t *testing.T, eng *engine.Engine, fc *fakeCompose, main, linked, dir string, opts ...func(*api.Config)) *env {
	t.Helper()
	cfg := api.Config{Engine: eng, DataDir: dir, IdleTimeout: -1, PollInterval: 100 * time.Millisecond}
	sb := &syncBuf{}
	cfg.Log = log.New(sb, "", 0)
	for _, o := range opts {
		o(&cfg)
	}
	srv, err := api.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { srv.Serve(context.Background()); close(done) }()
	t.Cleanup(func() { srv.Close(); <-done })
	e := &env{t: t, srv: srv, fc: fc, eng: eng, main: main, linked: linked, dataDir: dir}
	e.c = e.login()
	e.logs = &bytes.Buffer{}
	t.Cleanup(func() {
		// Nothing secret may reach the access log.
		if strings.Contains(sb.String(), e.c.csrf) && e.c.csrf != "" {
			t.Errorf("the CSRF token was logged")
		}
	})
	return e
}

// client talks to the server like a browser: one cookie, one CSRF token.
type client struct {
	t      *testing.T
	base   string
	port   int
	http   *http.Client
	csrf   string
	cookie string
}

func (e *env) login() *client {
	e.t.Helper()
	return loginURL(e.t, e.srv, e.srv.LaunchURL())
}

func loginURL(t *testing.T, srv *api.Server, launch string) *client {
	t.Helper()
	u, err := url.Parse(launch)
	if err != nil {
		t.Fatal(err)
	}
	nonce := strings.TrimPrefix(u.Fragment, "n=")
	c := newBareClient(t, srv.Port())
	status, body, hdr := c.raw("POST", "/api/v1/session", map[string]string{"nonce": nonce}, nil)
	if status != 200 {
		t.Fatalf("session exchange: %d %s", status, body)
	}
	var out struct {
		Csrf string `json:"csrf_token"`
	}
	json.Unmarshal(body, &out)
	c.csrf = out.Csrf
	for _, ck := range (&http.Response{Header: hdr}).Cookies() {
		if ck.Name == "theo_session" {
			c.cookie = ck.Value
		}
	}
	return c
}

func newBareClient(t *testing.T, port int) *client {
	return &client{t: t, port: port, base: "http://127.0.0.1:" + strconv.Itoa(port), http: &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// raw sends a request with the session cookie and CSRF header (when this
// client has them) and returns status, body and headers.
func (c *client) raw(method, path string, body any, hdr map[string]string) (int, []byte, http.Header) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		c.t.Fatal(err)
	}
	if body != nil || method != "GET" {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: "theo_session", Value: c.cookie})
	}
	if c.csrf != "" && method != "GET" {
		req.Header.Set("X-Theo-Csrf", c.csrf)
	}
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
			if strings.EqualFold(k, "Host") {
				req.Host = ""
			}
			continue
		}
		if strings.EqualFold(k, "Host") {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data, resp.Header
}

// call is raw with a decoded JSON answer.
func (c *client) call(method, path string, body, out any) int {
	c.t.Helper()
	status, data, _ := c.raw(method, path, body, nil)
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			c.t.Fatalf("%s %s: bad JSON (%d): %v\n%s", method, path, status, err, data)
		}
	}
	return status
}

// apiErr is the error body.
type apiErr struct {
	Error struct {
		Code      string                                `json:"code"`
		Message   string                                `json:"message"`
		Level     int                                   `json:"level"`
		Confirm   *struct{ Expected, Prompt string }    `json:"confirm"`
		Affected  []struct{ Kind, Name, Detail string } `json:"affected"`
		Preserved []string                              `json:"preserved"`
		Operation *struct{ ID, Kind string }            `json:"operation"`
	} `json:"error"`
}

func (e *env) wtID(path string) string {
	e.t.Helper()
	var list []struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if st := e.c.call("GET", "/api/v1/projects/ubapp/worktrees", nil, &list); st != 200 {
		e.t.Fatalf("list worktrees: %d", st)
	}
	for _, w := range list {
		if sameDir(w.Path, path) {
			return w.ID
		}
	}
	e.t.Fatalf("no worktree %s in %v", path, list)
	return ""
}

func sameDir(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 == nil && err2 == nil {
		return ra == rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
