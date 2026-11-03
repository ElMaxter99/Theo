package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ElMaxter99/Theo/internal/proxy"
)

// Resources (experimental, `theo mcp --resources`): theo://status and
// theo://status/<project> hold the JSON of `theo status --json`. A client
// may subscribe to them; Theo then reads the state every resourceInterval
// and sends notifications/resources/updated when it changed. Claude Code
// lists resources but, as of 2.1, does not subscribe, so the wait_for tool is
// the way that works everywhere.

const (
	resourcePrefix   = "theo://status"
	resourceInterval = 2 * time.Second
)

type subscriptions struct {
	mu     sync.Mutex
	uris   map[string]string // subscribed URI -> digest of its last content
	wakeup chan struct{}
}

func (s *Server) resourceList() ([]map[string]any, error) {
	out := []map[string]any{{
		"uri": resourcePrefix, "name": "status", "title": "Status of every project",
		"description": "The JSON of `theo status --json`: projects, worktrees, instance states, ports, warnings.", "mimeType": "application/json",
	}}
	projects, err := s.opts.Engine.Projects()
	if err != nil {
		return nil, err
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	for _, p := range projects {
		out = append(out, map[string]any{
			"uri": resourcePrefix + "/" + p.Name, "name": "status-" + p.Name, "title": "Status of " + p.Name,
			"description": "`theo status " + p.Name + " --json`.", "mimeType": "application/json",
		})
	}
	return out, nil
}

// resourceProject returns the project of a status URI ("" for all of them).
func resourceProject(uri string) (string, bool) {
	if uri == resourcePrefix {
		return "", true
	}
	if rest, ok := strings.CutPrefix(uri, resourcePrefix+"/"); ok && rest != "" && !strings.Contains(rest, "/") {
		return rest, true
	}
	return "", false
}

func (s *Server) readResource(ctx context.Context, uri string) ([]byte, error) {
	project, ok := resourceProject(uri)
	if !ok {
		return nil, fmt.Errorf("unknown resource %q (Theo serves %s and %s/<project>)", uri, resourcePrefix, resourcePrefix)
	}
	report, err := s.opts.Engine.Status(ctx, project)
	if err != nil {
		return nil, err
	}
	if port, running := s.proxyPort(); running {
		proxy.AddURLs(report, port)
	}
	return json.Marshal(report)
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum[:8])
}

// handleResource answers the resources/* methods. ok is false for a method
// it does not know.
func (s *Server) handleResource(ctx context.Context, req request) (rpcResult, bool) {
	var p struct {
		URI string `json:"uri"`
	}
	switch req.Method {
	case "resources/list":
		list, err := s.resourceList()
		if err != nil {
			return rpcResult{err: &rpcError{Code: codeInternalError, Message: err.Error()}}, true
		}
		return rpcResult{result: map[string]any{"resources": list}}, true
	case "resources/templates/list":
		return rpcResult{result: map[string]any{"resourceTemplates": []any{}}}, true
	case "resources/read", "resources/subscribe", "resources/unsubscribe":
		if err := strictObject(req.Params, &p); err != nil || p.URI == "" {
			return rpcResult{err: &rpcError{Code: codeInvalidParams, Message: `invalid params: needs {"uri": ...}`}}, true
		}
		if _, ok := resourceProject(p.URI); !ok {
			return rpcResult{err: &rpcError{Code: -32002, Message: fmt.Sprintf("resource %q not found", p.URI)}}, true
		}
	default:
		return rpcResult{}, false
	}
	switch req.Method {
	case "resources/read":
		data, err := s.readResource(ctx, p.URI)
		if err != nil {
			return rpcResult{err: &rpcError{Code: codeInternalError, Message: err.Error()}}, true
		}
		return rpcResult{result: map[string]any{"contents": []any{map[string]any{"uri": p.URI, "mimeType": "application/json", "text": string(data)}}}}, true
	case "resources/subscribe":
		s.subs.mu.Lock()
		if s.subs.uris == nil {
			s.subs.uris = map[string]string{}
		}
		if _, has := s.subs.uris[p.URI]; !has {
			s.subs.uris[p.URI] = ""
		}
		s.subs.mu.Unlock()
		select {
		case s.subs.wakeup <- struct{}{}:
		default:
		}
	case "resources/unsubscribe":
		s.subs.mu.Lock()
		delete(s.subs.uris, p.URI)
		s.subs.mu.Unlock()
	}
	return rpcResult{result: struct{}{}}, true
}

// watch sends notifications/resources/updated for subscribed URIs whose
// content changed. It runs until ctx is done.
func (s *Server) watch(ctx context.Context) {
	t := time.NewTicker(resourceInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.subs.wakeup:
		}
		s.subs.mu.Lock()
		uris := make([]string, 0, len(s.subs.uris))
		for u := range s.subs.uris {
			uris = append(uris, u)
		}
		s.subs.mu.Unlock()
		for _, u := range uris {
			data, err := s.readResource(ctx, u)
			if err != nil {
				continue
			}
			d := digest(data)
			s.subs.mu.Lock()
			prev, still := s.subs.uris[u]
			if still {
				s.subs.uris[u] = d
			}
			s.subs.mu.Unlock()
			if still && prev != "" && prev != d {
				s.notify("notifications/resources/updated", map[string]any{"uri": u})
			}
		}
	}
}
