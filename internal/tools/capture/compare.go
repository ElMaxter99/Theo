package capture

import (
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Classes of a screen or state in a comparison.
const (
	ClassSame       = "same"
	ClassMinor      = "minor"
	ClassChanged    = "changed"
	ClassNew        = "new"
	ClassRemoved    = "removed"
	ClassMoved      = "moved"
	ClassUnstable   = "unstable"
	ClassNotCapture = "not_captured"
)

// Pairing methods, strongest first.
const (
	ByID    = "id"
	ByRoute = "route"
	ByFile  = "file"
	ByAI    = "ai"
)

// Pair is a screen of the first set matched to a screen of the second.
type Pair struct {
	AID        string  `json:"a"`
	BID        string  `json:"b"`
	Method     string  `json:"method"`
	Confidence float64 `json:"confidence,omitempty"` // for AI pairs
	Reason     string  `json:"reason,omitempty"`
}

// StateResult is the comparison of one state at one viewport.
type StateResult struct {
	Screen   string  `json:"screen"` // id in the second set (or the first for removed)
	State    string  `json:"state"`
	Viewport string  `json:"viewport"`
	Class    string  `json:"class"`
	Percent  float64 `json:"percent"`
	Boxes    []Rect  `json:"boxes,omitempty"`
	Before   string  `json:"before,omitempty"` // file in the first set
	After    string  `json:"after,omitempty"`  // file in the second set
	DiffFile string  `json:"diff,omitempty"`   // relative to the report folder
	HeightA  int     `json:"height_a,omitempty"`
	HeightB  int     `json:"height_b,omitempty"`
	Reason   string  `json:"reason,omitempty"`
}

// ScreenResult is the classification of one screen.
type ScreenResult struct {
	ID      string  `json:"id"` // id in the second set; the first's for removed screens
	FromID  string  `json:"from_id,omitempty"`
	Title   string  `json:"title,omitempty"`
	Class   string  `json:"class"`
	Moved   bool    `json:"moved,omitempty"`
	Method  string  `json:"method,omitempty"`
	RouteA  string  `json:"route_a,omitempty"`
	RouteB  string  `json:"route_b,omitempty"`
	Percent float64 `json:"percent"` // the largest of its states
	Reason  string  `json:"reason,omitempty"`
}

// FlowOp is one change in the sequence of a flow.
type FlowOp struct {
	Kind   string `json:"kind"` // add or remove
	Screen string `json:"screen"`
	After  string `json:"after,omitempty"` // empty: at the start
	AtEnd  bool   `json:"at_end,omitempty"`
}

// FlowResult compares a flow (or the entry) between the two sets.
type FlowResult struct {
	ID    string   `json:"id"`
	Entry bool     `json:"entry,omitempty"`
	Class string   `json:"class"` // same, changed, new or removed
	A     []string `json:"a,omitempty"`
	B     []string `json:"b,omitempty"`
	Ops   []FlowOp `json:"ops,omitempty"`
}

// NotCaptured is something the comparison could not cover.
type NotCaptured struct {
	Kind   string `json:"kind"`   // failed, no_mock, not_in_inventory, no_screen, no_data
	Item   string `json:"item"`   // screen#state, or a file
	Reason string `json:"reason"` // what the engine said
}

// Counts totals a comparison.
type Counts struct {
	Changed, Same, New, Removed, Moved, FlowChanged, NotCaptured, Unstable, NeedLook int
}

// Comparison is comparacion.json: everything the three reports are made from.
type Comparison struct {
	Version     int            `json:"version"`
	A           Manifest       `json:"a"`
	B           Manifest       `json:"b"`
	Pairs       []Pair         `json:"pairs"`
	Screens     []ScreenResult `json:"screens"`
	States      []StateResult  `json:"states"`
	Flows       []FlowResult   `json:"flows"`
	NotCaptured []NotCaptured  `json:"not_captured"`
	Counts      Counts         `json:"counts"`
	Largest     *StateResult   `json:"largest,omitempty"`
}

// CompareOptions tunes a comparison.
type CompareOptions struct {
	// SameBelow and MinorBelow are the percentages under which a state is
	// "same" and "minor". Defaults 0.1 and 1.
	SameBelow, MinorBelow float64
	Tolerance             int
	// ChangedFiles are the files the branch changed (git diff): the ones that
	// belong to no screen are reported as not captured.
	ChangedFiles []string
	// OutDir is the report folder: difference images are written to its diff/ folder.
	OutDir string
	// AIPairs are pairs proposed by an AI (0.10), applied but marked.
	AIPairs []Pair
}

// MatchScreens pairs the screens of two sets without any AI: by id, then by
// route, then by a source file they share. Screens left over are new (only in
// B) or removed (only in A).
func MatchScreens(a, b *Manifest, extra []Pair) (pairs []Pair, newIDs, removedIDs []string) {
	usedA, usedB := map[string]bool{}, map[string]bool{}
	add := func(p Pair) {
		pairs = append(pairs, p)
		usedA[p.AID], usedB[p.BID] = true, true
	}
	// What a person wrote by hand comes first and wins over every other method.
	for _, p := range extra {
		if p.Method == ManualPair && !usedA[p.AID] && !usedB[p.BID] && a.Screen(p.AID) != nil && b.Screen(p.BID) != nil {
			add(p)
		}
	}
	for _, sa := range a.Screens {
		if !usedA[sa.ID] && !usedB[sa.ID] && b.Screen(sa.ID) != nil {
			add(Pair{AID: sa.ID, BID: sa.ID, Method: ByID})
		}
	}
	for _, sa := range a.Screens {
		if usedA[sa.ID] {
			continue
		}
		for _, sb := range b.Screens {
			if !usedB[sb.ID] && sa.Route == sb.Route {
				add(Pair{AID: sa.ID, BID: sb.ID, Method: ByRoute})
				break
			}
		}
	}
	for _, sa := range a.Screens {
		if usedA[sa.ID] || len(sa.Origin) == 0 {
			continue
		}
		for _, sb := range b.Screens {
			if !usedB[sb.ID] && shares(sa.Origin, sb.Origin) {
				add(Pair{AID: sa.ID, BID: sb.ID, Method: ByFile})
				break
			}
		}
	}
	for _, p := range extra {
		if p.Method != ManualPair && !usedA[p.AID] && !usedB[p.BID] && a.Screen(p.AID) != nil && b.Screen(p.BID) != nil {
			p.Method = ByAI
			add(p)
		}
	}
	for _, sb := range b.Screens {
		if !usedB[sb.ID] {
			newIDs = append(newIDs, sb.ID)
		}
	}
	for _, sa := range a.Screens {
		if !usedA[sa.ID] {
			removedIDs = append(removedIDs, sa.ID)
		}
	}
	return
}

func shares(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// MinorPixels is how many differing pixels make a state at least "minor"
// even when they are a tiny share of a large screenshot.
const MinorPixels = 150

// Compare compares two sets: it pairs their screens, compares every state
// at every viewport by pixel, classifies screens and flows, and lists what
// could not be covered. Difference images go to opt.OutDir/diff.
func Compare(a, b *Set, opt CompareOptions) (*Comparison, error) {
	if opt.SameBelow == 0 {
		opt.SameBelow = 0.1
	}
	if opt.MinorBelow == 0 {
		opt.MinorBelow = 1
	}
	c := &Comparison{Version: 1, A: a.Manifest, B: b.Manifest}
	pairs, newIDs, removedIDs := MatchScreens(&a.Manifest, &b.Manifest, opt.AIPairs)
	c.Pairs = pairs
	toB := map[string]string{}
	for _, p := range pairs {
		toB[p.AID] = p.BID
	}

	// A few pixels can be a whole word on a sparse page: under the "same"
	// threshold a state still counts as minor when at least MinorPixels differ.
	classify := func(p float64, changed int) string {
		switch {
		case p < opt.SameBelow && changed < MinorPixels:
			return ClassSame
		case p < opt.MinorBelow:
			return ClassMinor
		}
		return ClassChanged
	}
	viewports := map[string]bool{}
	for _, v := range b.Viewports {
		viewports[v.ID] = true
	}
	for _, v := range a.Viewports {
		viewports[v.ID] = true
	}
	vids := make([]string, 0, len(viewports))
	for v := range viewports {
		vids = append(vids, v)
	}
	sort.Strings(vids)

	// pairs: compare their states
	for _, p := range pairs {
		sa, sb := a.Screen(p.AID), b.Screen(p.BID)
		res := ScreenResult{ID: sb.ID, FromID: sa.ID, Title: sb.Title, Method: p.Method, RouteA: sa.Route, RouteB: sb.Route}
		stateIDs := map[string]bool{}
		for _, s := range sa.States {
			stateIDs[s] = true
		}
		for _, s := range sb.States {
			stateIDs[s] = true
		}
		ordered := append([]string{}, sb.States...)
		for _, s := range sa.States {
			if !contains(sb.States, s) {
				ordered = append(ordered, s)
			}
		}
		anyChange, anyMinor, anyUnstable := false, false, false
		for _, st := range ordered {
			for _, vp := range vids {
				r, err := compareState(c, a, b, sa.ID, sb.ID, st, vp, opt, classify)
				if err != nil {
					return nil, err
				}
				if r == nil {
					continue
				}
				c.States = append(c.States, *r)
				switch r.Class {
				case ClassChanged, ClassMinor, ClassNew, ClassRemoved:
					if r.Class == ClassMinor {
						anyMinor = true
					} else {
						anyChange = true
					}
				case ClassUnstable:
					anyUnstable = true
				}
				res.Percent = max(res.Percent, r.Percent)
			}
		}
		res.Moved = p.Method != ByID || sa.Route != sb.Route
		switch {
		case anyChange:
			res.Class = ClassChanged
		case anyMinor || res.Percent >= opt.SameBelow:
			res.Class = ClassMinor
		case anyUnstable && res.Percent == 0:
			res.Class = ClassUnstable
		default:
			res.Class = ClassSame
		}
		c.Screens = append(c.Screens, res)
	}
	for _, id := range newIDs {
		s := b.Screen(id)
		c.Screens = append(c.Screens, ScreenResult{ID: id, Title: s.Title, Class: ClassNew, RouteB: s.Route})
		for _, st := range s.States {
			for _, vp := range vids {
				if sh := b.Shot(id, st, vp); sh != nil && sh.Status != ShotFailed {
					c.States = append(c.States, StateResult{Screen: id, State: st, Viewport: vp, Class: ClassNew, After: sh.File})
				}
			}
		}
	}
	for _, id := range removedIDs {
		s := a.Screen(id)
		c.Screens = append(c.Screens, ScreenResult{ID: id, Title: s.Title, Class: ClassRemoved, RouteA: s.Route})
		for _, st := range s.States {
			for _, vp := range vids {
				if sh := a.Shot(id, st, vp); sh != nil && sh.Status != ShotFailed {
					c.States = append(c.States, StateResult{Screen: id, State: st, Viewport: vp, Class: ClassRemoved, Before: sh.File})
				}
			}
		}
	}

	// not captured: failed shots of either set, and changed files without a screen
	for _, set := range []*Set{a, b} {
		for _, sh := range set.Shots {
			if sh.Status == ShotFailed {
				kind := "failed"
				if strings.Contains(sh.Reason, "no mock") || strings.Contains(sh.Reason, "not intercepted") {
					kind = "no_mock"
				}
				c.NotCaptured = append(c.NotCaptured, NotCaptured{Kind: kind, Item: StateKey(sh.Screen, sh.State) + " (" + set.Label + ", " + sh.Viewport + ")", Reason: sh.Reason})
			}
		}
	}
	for _, f := range opt.ChangedFiles {
		known := false
		for _, s := range b.Screens {
			for _, o := range s.Origin {
				known = known || o == f
			}
		}
		if !known && isSourceFile(f) {
			c.NotCaptured = append(c.NotCaptured, NotCaptured{Kind: "no_screen", Item: f})
		}
	}

	c.Flows = compareFlows(a, b, toB)
	c.count()
	return c, nil
}

func isSourceFile(f string) bool {
	switch strings.ToLower(filepath.Ext(f)) {
	case ".ts", ".tsx", ".js", ".jsx", ".vue", ".svelte", ".html", ".css", ".scss", ".less", ".sass":
		return !strings.Contains(f, ".test.") && !strings.Contains(f, ".spec.")
	}
	return false
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func compareState(c *Comparison, a, b *Set, aid, bid, state, vp string, opt CompareOptions, classify func(float64, int) string) (*StateResult, error) {
	sa, sb := a.Shot(aid, state, vp), b.Shot(bid, state, vp)
	r := &StateResult{Screen: bid, State: state, Viewport: vp}
	switch {
	case sa == nil && sb == nil:
		return nil, nil
	case sa == nil:
		if sb.Status == ShotFailed {
			return nil, nil
		}
		r.Class, r.After = ClassNew, sb.File
		return r, nil
	case sb == nil:
		if sa.Status == ShotFailed {
			return nil, nil
		}
		r.Class, r.Before, r.Screen = ClassRemoved, sa.File, aid
		return r, nil
	case sa.Status == ShotFailed || sb.Status == ShotFailed:
		r.Class, r.Reason = ClassNotCapture, firstNonEmpty(sa.Reason, sb.Reason)
		return r, nil
	case sa.Status == ShotUnstable || sb.Status == ShotUnstable:
		r.Class, r.Reason = ClassUnstable, firstNonEmpty(sa.Reason, sb.Reason)
		r.Before, r.After = sa.File, sb.File
		return r, nil
	}
	ia, err := a.Image(sa.File)
	if err != nil {
		return nil, err
	}
	ib, err := b.Image(sb.File)
	if err != nil {
		return nil, err
	}
	masks := append(append([]Rect{}, sa.Masks...), sb.Masks...)
	d := Diff(ia, ib, DiffOptions{Tolerance: opt.Tolerance, Masks: masks})
	r.Percent, r.Boxes, r.Before, r.After = d.Percent, d.Boxes, sa.File, sb.File
	r.HeightA, r.HeightB = d.HeightA, d.HeightB
	r.Class = classify(d.Percent, d.Changed)
	if r.Class != ClassSame && opt.OutDir != "" {
		name := "diff/" + safeName(bid) + "--" + safeName(state) + "--" + safeName(vp) + ".png"
		out, err := Within(opt.OutDir, name)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return nil, err
		}
		f, err := os.Create(out)
		if err != nil {
			return nil, err
		}
		err = png.Encode(f, d.Image)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
		r.DiffFile = name
	}
	return r, nil
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// compareFlows compares the entry screen and every flow by id, with the
// screens of the first set named as their pair in the second.
func compareFlows(a, b *Set, toB map[string]string) []FlowResult {
	var out []FlowResult
	mapSeq := func(f Flow) []string {
		var seq []string
		for _, step := range f.Pasos {
			id := strings.SplitN(step, "#", 2)[0]
			if m, ok := toB[id]; ok {
				id = m
			}
			if len(seq) == 0 || seq[len(seq)-1] != id {
				seq = append(seq, id)
			}
		}
		return seq
	}
	ea, eb := strings.SplitN(a.Entry, "#", 2)[0], strings.SplitN(b.Entry, "#", 2)[0]
	if ea != "" || eb != "" {
		if m, ok := toB[ea]; ok {
			ea = m
		}
		r := FlowResult{ID: "entry", Entry: true, Class: "same"}
		if ea != "" {
			r.A = []string{ea}
		}
		if eb != "" {
			r.B = []string{eb}
		}
		if ea != eb {
			r.Class = "changed"
			r.Ops = []FlowOp{{Kind: "add", Screen: eb}}
			if ea != "" {
				r.Ops = append(r.Ops, FlowOp{Kind: "remove", Screen: ea})
			}
		}
		out = append(out, r)
	}
	seen := map[string]bool{}
	for _, fb := range b.Flows {
		seen[fb.ID] = true
		var fa *Flow
		for i := range a.Flows {
			if a.Flows[i].ID == fb.ID {
				fa = &a.Flows[i]
			}
		}
		r := FlowResult{ID: fb.ID, B: mapSeqB(fb)}
		if fa == nil {
			r.Class = ClassNew
			out = append(out, r)
			continue
		}
		r.A = mapSeq(*fa)
		r.Ops = seqDiff(r.A, r.B)
		r.Class = "same"
		if len(r.Ops) > 0 {
			r.Class = "changed"
		}
		out = append(out, r)
	}
	for _, fa := range a.Flows {
		if !seen[fa.ID] {
			out = append(out, FlowResult{ID: fa.ID, A: mapSeq(fa), Class: ClassRemoved})
		}
	}
	return out
}

func mapSeqB(f Flow) []string {
	var seq []string
	for _, step := range f.Pasos {
		id := strings.SplitN(step, "#", 2)[0]
		if len(seq) == 0 || seq[len(seq)-1] != id {
			seq = append(seq, id)
		}
	}
	return seq
}

// seqDiff lists what to add and remove to turn a into b (longest common subsequence).
func seqDiff(a, b []string) []FlowOp {
	n, m := len(a), len(b)
	l := make([][]int, n+1)
	for i := range l {
		l[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				l[i][j] = l[i+1][j+1] + 1
			} else {
				l[i][j] = max(l[i+1][j], l[i][j+1])
			}
		}
	}
	var ops []FlowOp
	i, j, last := 0, 0, ""
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			last = a[i]
			i++
			j++
		case j < m && (i == n || l[i][j+1] >= l[i+1][j]):
			ops = append(ops, FlowOp{Kind: "add", Screen: b[j], After: last, AtEnd: i == n && last != ""})
			j++
		default:
			ops = append(ops, FlowOp{Kind: "remove", Screen: a[i]})
			i++
		}
	}
	return ops
}

func (c *Comparison) count() {
	for _, s := range c.Screens {
		switch s.Class {
		case ClassChanged, ClassMinor:
			c.Counts.Changed++
		case ClassSame:
			c.Counts.Same++
		case ClassNew:
			c.Counts.New++
		case ClassRemoved:
			c.Counts.Removed++
		}
		if s.Moved {
			c.Counts.Moved++
		}
	}
	for _, f := range c.Flows {
		if f.Class != "same" {
			c.Counts.FlowChanged++
		}
	}
	c.Counts.NotCaptured = len(c.NotCaptured)
	for _, st := range c.States {
		if st.Class == ClassUnstable {
			c.Counts.Unstable++
		}
	}
	var rm, nw int
	for _, s := range c.Screens {
		if s.Class == ClassRemoved {
			rm++
		}
		if s.Class == ClassNew {
			nw++
		}
	}
	c.Counts.NeedLook = min(rm, nw)
	for i := range c.States {
		if st := &c.States[i]; st.Class == ClassUnstable {
			continue
		} else if c.Largest == nil || st.Percent > c.Largest.Percent {
			c.Largest = st
		}
	}
	sort.SliceStable(c.States, func(i, j int) bool { return c.States[i].Percent > c.States[j].Percent })
}
