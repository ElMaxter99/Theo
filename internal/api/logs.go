package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ElMaxter99/Theo/internal/compose"
)

// logLine is one line of `docker compose logs`.
type logLine struct {
	service string
	stream  string
	text    string
	at      time.Time
	cut     bool
}

// chanWriter splits the output of a process into lines and offers them to a
// bounded channel without ever blocking the process: when the client does
// not keep up, lines are dropped and counted (back-pressure, reported as
// gap events).
type chanWriter struct {
	ch      chan logLine
	dropped *atomic.Int64
	stream  string
	compose string
	buf     []byte
	cut     bool
}

var replicaSuffix = regexp.MustCompile(`-\d+$`)

// serviceOf extracts the service from a "container | text" line. Compose
// names containers <project>-<service>-<n>.
func serviceOf(prefix, composeProject string) string {
	name := strings.TrimSpace(prefix)
	name = strings.TrimPrefix(name, composeProject+"-")
	return replicaSuffix.ReplaceAllString(name, "")
}

func (w *chanWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := indexByte(p, '\n')
		if i < 0 {
			w.keep(p)
			break
		}
		w.keep(p[:i])
		w.flush()
		p = p[i+1:]
	}
	return n, nil
}

func (w *chanWriter) keep(p []byte) {
	room := maxLineBytes - len(w.buf)
	if len(p) > room {
		p, w.cut = p[:max(room, 0)], true
	}
	w.buf = append(w.buf, p...)
}

func (w *chanWriter) flush() {
	if len(w.buf) == 0 && !w.cut {
		return
	}
	text := strings.TrimRight(string(w.buf), "\r")
	svc := ""
	if prefix, rest, ok := strings.Cut(text, " | "); ok && !strings.ContainsAny(strings.TrimSpace(prefix), " \t") {
		svc, text = serviceOf(prefix, w.compose), rest
	}
	l := logLine{service: svc, stream: w.stream, text: text, at: time.Now().UTC(), cut: w.cut}
	w.buf, w.cut = w.buf[:0], false
	select {
	case w.ch <- l:
	default:
		w.dropped.Add(1)
	}
}

// handleLogs serves GET …/{wt}/logs. With follow=1 it is a server-sent event
// stream with one docker compose logs process, killed when the client
// leaves. Without follow it returns the logs as a text download.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request, _ *session) error {
	inst, _, err := s.instance(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	opts := compose.LogOptions{Services: q["service"], Follow: q.Get("follow") == "1", Tail: 200}
	if t := q.Get("tail"); t == "all" {
		opts.Tail = 0
	} else if t != "" {
		n, err := strconv.Atoi(t)
		if err != nil || n < 0 || n > 1_000_000 {
			return badRequest("tail must be a number or all")
		}
		opts.Tail = n
	}
	if err := inst.CheckServices(opts.Services); err != nil {
		return err
	}

	if !opts.Follow { // download
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+inst.Compose+`-logs.txt"`)
		return s.eng.Logs(r.Context(), inst, opts, w, w)
	}

	fl, ok := w.(http.Flusher)
	if !ok {
		return errf(http.StatusInternalServerError, "internal", "streaming is not supported")
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	var dropped atomic.Int64
	ch := make(chan logLine, 1000)
	var wg sync.WaitGroup
	out := &chanWriter{ch: ch, dropped: &dropped, stream: "stdout", compose: inst.Compose}
	errw := &chanWriter{ch: ch, dropped: &dropped, stream: "stderr", compose: inst.Compose}
	var runErr error
	done := make(chan struct{})
	wg.Add(1)
	go func() { // the only docker compose logs process of this stream
		defer wg.Done()
		defer close(done)
		runErr = s.eng.Logs(ctx, inst, opts, out, errw)
		out.flush()
		errw.flush()
	}()
	// Whatever happens, the process is gone when this handler returns.
	defer func() { cancel(); wg.Wait() }()

	s.sse.Add(1)
	defer func() { s.sse.Add(-1); s.touch() }()
	startSSE(w)
	fl.Flush()
	keep := time.NewTicker(15 * time.Second)
	defer keep.Stop()
	id := 0
	emit := func(l logLine) {
		id++
		data := map[string]any{"service": l.service, "stream": l.stream, "line": l.text, "at": l.at}
		if l.cut {
			data["truncated"] = true
		}
		writeSSE(w, event{ID: id, Name: "log", Data: mustJSON(data)})
	}
	gap := func() {
		if n := dropped.Swap(0); n > 0 {
			writeSSE(w, event{Name: "gap", Data: mustJSON(map[string]int64{"dropped": n})})
		}
	}
	for {
		select {
		case <-r.Context().Done():
			return nil
		case l := <-ch:
			emit(l)
			for more := true; more; { // write a batch, then flush once
				select {
				case l = <-ch:
					emit(l)
				default:
					more = false
				}
			}
			gap()
			fl.Flush()
		case <-keep.C:
			gap()
			fmt.Fprint(w, ": keep-alive\n\n")
			fl.Flush()
		case <-done:
			for { // what is left in the channel
				select {
				case l := <-ch:
					emit(l)
					continue
				default:
				}
				break
			}
			end := map[string]any{"ok": runErr == nil || ctx.Err() != nil}
			if runErr != nil && ctx.Err() == nil {
				end["error"] = fromEngine(runErr, nil)
			}
			gap()
			writeSSE(w, event{Name: "end", Data: mustJSON(end)})
			fl.Flush()
			return nil
		}
	}
}

var _ io.Writer = (*chanWriter)(nil)
