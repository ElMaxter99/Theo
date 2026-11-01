package api

import (
	"context"
	"sync"
	"time"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/execx"
)

// What keeps many projects cheap (docs/api.md, "Performance"): the expensive
// reads (overview, health, reconcile, the Docker snapshot) are computed once
// for all concurrent callers, on a context of their own that carries a
// timeout per command and a bound on how many commands run at once, and
// Docker is read once for all instances instead of once per instance.

// Defaults of the Config fields that bound the work.
const (
	defaultCommandTimeout = 15 * time.Second
	defaultWorkTimeout    = 60 * time.Second
	defaultBreakerPause   = 5 * time.Second
	defaultConfigCacheTTL = 5 * time.Minute

	// snapshotTTL is how long a read of Docker is reused. A request that asks
	// for fresh data accepts one no older than snapshotFreshAge.
	snapshotTTL      = time.Second
	snapshotFreshAge = 250 * time.Millisecond
)

// snapResult is one read of Docker: err is set when Docker could not be
// asked, snap is nil when the adapter reads per instance instead.
type snapResult struct {
	snap *engine.DockerSnapshot
	err  error
}

type snapCache struct {
	mu  sync.Mutex
	at  time.Time
	gen uint64
	res *snapResult
}

// work returns the context that expensive reads run on: it outlives the
// request that started them, ends with the server, and bounds every Git and
// Docker command it starts (timeout, hang breaker, process count).
func (s *Server) work() (context.Context, context.CancelFunc) {
	ctx := engine.WithCachedConfig(engine.WithRequestMemo(execx.WithLimits(s.workCtx, s.limits)))
	return context.WithTimeout(ctx, s.cfg.WorkTimeout)
}

// dockerSnapshot returns a read of all Compose containers no older than
// snapshotTTL (snapshotFreshAge when fresh), shared by concurrent callers.
// Docker being down is a result, not an error: it is cached for the same
// short time, so a stopped or stuck Docker is asked about once per interval,
// not once per project card.
func (s *Server) dockerSnapshot(ctx context.Context, fresh bool) *snapResult {
	gen := s.cache.generation()
	maxAge := snapshotTTL
	if fresh {
		maxAge = snapshotFreshAge
	}
	s.snaps.mu.Lock()
	if r := s.snaps.res; r != nil && s.snaps.gen == gen && time.Since(s.snaps.at) < maxAge {
		s.snaps.mu.Unlock()
		return r
	}
	s.snaps.mu.Unlock()
	res, _, err := s.snapFlight.do(ctx, "snapshot", gen, func() (*snapResult, error) {
		wctx, cancel := s.work()
		defer cancel()
		snap, err := s.eng.DockerSnapshot(wctx)
		r := &snapResult{snap: snap, err: err}
		s.snaps.mu.Lock()
		if s.cache.generation() == gen {
			s.snaps.res, s.snaps.at, s.snaps.gen = r, time.Now(), gen
		}
		s.snaps.mu.Unlock()
		return r, nil
	})
	if err != nil { // the caller gave up waiting
		return &snapResult{err: err}
	}
	return res
}
