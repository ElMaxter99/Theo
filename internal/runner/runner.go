// Package runner starts an application that is not a container: one command
// in a folder, on a port Theo chooses, until it answers over HTTP. It is what
// the "script" kind of the run section of .theo.json asks for. Stopping it
// takes down the whole process tree, so a dev server never outlives Theo's
// request (a `npm run dev` leaves several children behind otherwise).
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Spec says what to run.
type Spec struct {
	Dir     string // working folder
	Command string // shell command; ${PORT} (or ${<PortEnv>}) is replaced by the port
	Env     map[string]string
	// Port is the port the application listens on; 0 asks for a free one.
	Port    int
	PortEnv string // variable that carries the port; default PORT
	// Ready: the application is up when GET http://127.0.0.1:Port<ReadyPath>
	// answers one of ReadyStatus.
	ReadyPath    string
	ReadyStatus  []int
	ReadyTimeout time.Duration
	// Log receives what the command prints (stdout and stderr); may be nil.
	Log io.Writer
}

// Process is a running application.
type Process struct {
	Port int
	cmd  *exec.Cmd
	done chan struct{}
	err  error // how it ended, valid once done is closed
	tail *tailWriter
	once sync.Once
}

// URL is where the application answers.
func (p *Process) URL() string { return "http://127.0.0.1:" + strconv.Itoa(p.Port) }

// Done is closed when the process ended.
func (p *Process) Done() <-chan struct{} { return p.done }

// Err is how the process ended; call it after Done.
func (p *Process) Err() error { return p.err }

// ErrNotReady means the application did not answer in time.
var ErrNotReady = errors.New("the application did not become ready")

// FreePort asks the system for a port nothing listens on.
func FreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Start runs the command and waits until it is ready. If it exits first or
// does not answer in time, the process tree is stopped and the error carries
// the last lines the command printed.
func Start(ctx context.Context, s Spec) (*Process, error) {
	if strings.TrimSpace(s.Command) == "" {
		return nil, errors.New("runner: no command")
	}
	port := s.Port
	if port == 0 {
		var err error
		if port, err = FreePort(); err != nil {
			return nil, fmt.Errorf("runner: no free port: %w", err)
		}
	}
	portEnv := s.PortEnv
	if portEnv == "" {
		portEnv = "PORT"
	}
	p := strconv.Itoa(port)
	expand := func(v string) string {
		return strings.NewReplacer("${PORT}", p, "${"+portEnv+"}", p).Replace(v)
	}

	cmd := buildCommand(expand(s.Command))
	cmd.Dir = s.Dir
	cmd.Env = append(os.Environ(), portEnv+"="+p)
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+expand(v))
	}
	proc := &Process{Port: port, cmd: cmd, done: make(chan struct{}), tail: &tailWriter{max: 20}}
	var out io.Writer = proc.tail
	if s.Log != nil {
		out = io.MultiWriter(proc.tail, s.Log)
	}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("runner: could not start %q: %w", s.Command, err)
	}
	go func() {
		proc.err = cmd.Wait()
		close(proc.done)
	}()

	timeout := s.ReadyTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	if err := proc.waitReady(ctx, s, timeout); err != nil {
		proc.Stop()
		if tail := proc.tail.String(); tail != "" {
			return nil, fmt.Errorf("%w\n\n%s", err, tail)
		}
		return nil, err
	}
	return proc, nil
}

func (p *Process) waitReady(ctx context.Context, s Spec, timeout time.Duration) error {
	path := s.ReadyPath
	if path == "" {
		path = "/"
	}
	ok := s.ReadyStatus
	if len(ok) == 0 {
		ok = []int{200, 304}
	}
	url := p.URL() + path
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-p.done:
			return fmt.Errorf("the command ended before it was ready: %v", p.err)
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("%w: %s gave no accepted answer in %s", ErrNotReady, url, timeout)
		case <-tick.C:
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			resp, err := client.Do(req)
			if err != nil {
				continue
			}
			resp.Body.Close()
			for _, c := range ok {
				if resp.StatusCode == c {
					return nil
				}
			}
		}
	}
}

// Stop ends the process and everything it started, and waits for it.
// Calling it again does nothing.
func (p *Process) Stop() {
	p.once.Do(func() {
		select {
		case <-p.done:
			return
		default:
		}
		terminate(p.cmd, p.done)
		<-p.done
	})
}

// tailWriter keeps the last lines written to it.
type tailWriter struct {
	mu    sync.Mutex
	max   int
	lines []string
	part  string
}

func (t *tailWriter) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.part += string(b)
	for {
		i := strings.IndexByte(t.part, '\n')
		if i < 0 {
			break
		}
		t.lines = append(t.lines, strings.TrimRight(t.part[:i], "\r"))
		t.part = t.part[i+1:]
		if len(t.lines) > t.max {
			t.lines = t.lines[1:]
		}
	}
	if len(t.part) > 4096 {
		t.part = t.part[len(t.part)-4096:]
	}
	return len(b), nil
}

func (t *tailWriter) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	l := t.lines
	if t.part != "" {
		l = append(append([]string{}, l...), t.part)
	}
	return strings.Join(l, "\n")
}
