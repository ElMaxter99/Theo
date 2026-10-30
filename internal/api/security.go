package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// NonceTTL is how long a launch nonce can be exchanged for a session.
var NonceTTL = 60 * time.Second

const (
	cookieName   = "theo_session"
	csrfHeader   = "X-Theo-Csrf"
	controlHdr   = "X-Theo-Control"
	nonceLen     = 32 // bytes: 256 bits
	maxSessions  = 64
	sessionIdle  = 12 * time.Hour
	apiBase      = "/api/v1"
	contentJSON  = "application/json"
	csp          = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
	headerNoSnif = "nosniff"
)

func randomToken() string {
	b := make([]byte, nonceLen)
	if _, err := rand.Read(b); err != nil {
		panic("api: no randomness: " + err.Error()) // the process cannot be trusted without it
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func equal(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// session is a browser that exchanged a launch nonce.
type session struct {
	id       string
	csrf     string
	lastSeen time.Time
	visible  bool // the tab says it is visible (POST /events/focus)
}

// auth holds the launch nonces and the sessions. Nothing here is ever logged.
type auth struct {
	mu       sync.Mutex
	now      func() time.Time
	nonces   map[string]time.Time // nonce -> expiry; removed when used
	sessions map[string]*session
	control  string // secret of ui.lock: lets a second `theo ui` ask for a nonce
}

func newAuth() *auth {
	return &auth{now: time.Now, nonces: map[string]time.Time{}, sessions: map[string]*session{}, control: randomToken()}
}

// newNonce creates a single-use launch nonce valid for NonceTTL.
func (a *auth) newNonce() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for n, exp := range a.nonces { // expired nonces are dropped here, no timer needed
		if now.After(exp) {
			delete(a.nonces, n)
		}
	}
	n := randomToken()
	a.nonces[n] = now.Add(NonceTTL)
	return n
}

// exchange consumes a nonce and creates a session. A nonce works once, even
// if it is wrong the second time: it is deleted before it is checked.
func (a *auth) exchange(nonce string) (*session, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var found string
	var exp time.Time
	for n, e := range a.nonces {
		if equal(n, nonce) {
			found, exp = n, e
		}
	}
	if found == "" {
		return nil, false
	}
	delete(a.nonces, found)
	if a.now().After(exp) {
		return nil, false
	}
	if len(a.sessions) >= maxSessions { // drop the least recently used
		var oldest *session
		for _, s := range a.sessions {
			if oldest == nil || s.lastSeen.Before(oldest.lastSeen) {
				oldest = s
			}
		}
		delete(a.sessions, oldest.id)
	}
	s := &session{id: randomToken(), csrf: randomToken(), lastSeen: a.now()}
	a.sessions[s.id] = s
	return s, true
}

func (a *auth) lookup(r *http.Request) *session {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, s := range a.sessions {
		if equal(id, c.Value) {
			if a.now().Sub(s.lastSeen) > sessionIdle {
				delete(a.sessions, id)
				return nil
			}
			s.lastSeen = a.now()
			return s
		}
	}
	return nil
}

func (a *auth) setVisible(s *session, v bool) {
	a.mu.Lock()
	s.visible = v
	a.mu.Unlock()
}

// anyVisible reports whether some connected tab says it is visible.
func (a *auth) anyVisible() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.sessions {
		if s.visible {
			return true
		}
	}
	return false
}

func (a *auth) controlOK(r *http.Request) bool {
	return equal(r.Header.Get(controlHdr), a.control)
}

func sessionCookie(s *session) *http.Cookie {
	return &http.Cookie{Name: cookieName, Value: s.id, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode}
}

func mutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// hostOrigin is the only Host and Origin the server accepts.
func (s *Server) hostPort() string { return "127.0.0.1:" + strconv.Itoa(s.port) }
func (s *Server) origin() string   { return "http://" + s.hostPort() }

// setCommonHeaders sets the headers every response carries.
func setCommonHeaders(h http.Header) {
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", headerNoSnif)
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("X-Frame-Options", "DENY")
}
