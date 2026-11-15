package capture

import (
	"image"
	"image/color"
	"math"
)

// Rect is a region of a screenshot, in pixels.
type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// DiffOptions tunes the pixel comparison.
type DiffOptions struct {
	// Tolerance is how far apart two colors may be (0 to 255, in the largest
	// difference of any channel) before the pixel counts as changed. Default 12.
	Tolerance int
	// Masks are areas ignored in both images (dynamic zones).
	Masks []Rect
}

// DiffResult is the comparison of two screenshots of the same state.
type DiffResult struct {
	Width, Height int // size of the compared canvas (the larger of both)
	HeightA       int
	HeightB       int
	Compared      int // pixels compared (not masked)
	Changed       int // pixels that differ
	Percent       float64
	// Boxes are the regions where the changes are, merged on a coarse grid.
	Boxes []Rect
	// Image shows the second screenshot dimmed with the changes in red.
	Image *image.NRGBA
}

const gridCell = 24

// Diff compares two screenshots. If their sizes differ, they are aligned at
// the top left and what one has and the other lacks counts as changed: a
// longer new view is a change, not something to hide. Pixels that only differ
// by anti-aliasing (the soft edge of a letter) are not counted.
func Diff(a, b image.Image, opt DiffOptions) DiffResult {
	if opt.Tolerance <= 0 {
		opt.Tolerance = 12
	}
	ab, bb := a.Bounds(), b.Bounds()
	w, h := max(ab.Dx(), bb.Dx()), max(ab.Dy(), bb.Dy())
	res := DiffResult{Width: w, Height: h, HeightA: ab.Dy(), HeightB: bb.Dy()}
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	masked := func(x, y int) bool {
		for _, m := range opt.Masks {
			if x >= m.X && x < m.X+m.W && y >= m.Y && y < m.Y+m.H {
				return true
			}
		}
		return false
	}
	cellsW, cellsH := (w+gridCell-1)/gridCell, (h+gridCell-1)/gridCell
	cells := make([]bool, cellsW*cellsH)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			inA := x < ab.Dx() && y < ab.Dy()
			inB := x < bb.Dx() && y < bb.Dy()
			var pa, pb color.NRGBA
			if inA {
				pa = color.NRGBAModel.Convert(a.At(ab.Min.X+x, ab.Min.Y+y)).(color.NRGBA)
			}
			if inB {
				pb = color.NRGBAModel.Convert(b.At(bb.Min.X+x, bb.Min.Y+y)).(color.NRGBA)
				g := uint8((int(pb.R)*30 + int(pb.G)*59 + int(pb.B)*11) / 100)
				out.SetNRGBA(x, y, color.NRGBA{g/3 + 170, g/3 + 170, g/3 + 170, 255})
			} else {
				out.SetNRGBA(x, y, color.NRGBA{240, 240, 240, 255})
			}
			if masked(x, y) {
				continue
			}
			res.Compared++
			changed := inA != inB
			if !changed && distance(pa, pb) > opt.Tolerance {
				changed = !(antialiased(a, ab, x, y) || antialiased(b, bb, x, y))
			}
			if changed {
				res.Changed++
				out.SetNRGBA(x, y, color.NRGBA{255, 40, 40, 255})
				cells[(y/gridCell)*cellsW+x/gridCell] = true
			}
		}
	}
	if res.Compared > 0 {
		res.Percent = math.Round(float64(res.Changed)/float64(res.Compared)*100000) / 1000
	}
	res.Boxes = boxes(cells, cellsW, cellsH, w, h)
	res.Image = out
	return res
}

func distance(a, b color.NRGBA) int {
	d := 0
	for _, v := range [4]int{int(a.R) - int(b.R), int(a.G) - int(b.G), int(a.B) - int(b.B), int(a.A) - int(b.A)} {
		if v < 0 {
			v = -v
		}
		d = max(d, v)
	}
	return d
}

func brightness(c color.Color) float64 {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	return 0.299*float64(n.R) + 0.587*float64(n.G) + 0.114*float64(n.B)
}

// antialiased says whether the pixel sits on a soft edge: it has neighbors
// both darker and lighter than itself and few identical ones, the way the
// outline of a glyph does. Such pixels move by a shade between two renders.
func antialiased(img image.Image, bnd image.Rectangle, x, y int) bool {
	if x >= bnd.Dx() || y >= bnd.Dy() {
		return false
	}
	c := brightness(img.At(bnd.Min.X+x, bnd.Min.Y+y))
	same, darker, lighter := 0, 0, 0
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			if dx == 0 && dy == 0 {
				continue
			}
			nx, ny := x+dx, y+dy
			if nx < 0 || ny < 0 || nx >= bnd.Dx() || ny >= bnd.Dy() {
				continue
			}
			d := brightness(img.At(bnd.Min.X+nx, bnd.Min.Y+ny)) - c
			switch {
			case math.Abs(d) < 1:
				same++
			case d < 0:
				darker++
			default:
				lighter++
			}
		}
	}
	return same <= 2 && darker > 0 && lighter > 0
}

// boxes merges the changed grid cells into bounding boxes of connected groups.
func boxes(cells []bool, cw, ch, w, h int) []Rect {
	seen := make([]bool, len(cells))
	var out []Rect
	for i := range cells {
		if !cells[i] || seen[i] {
			continue
		}
		minX, minY, maxX, maxY := cw, ch, 0, 0
		stack := []int{i}
		seen[i] = true
		for len(stack) > 0 {
			c := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			cx, cy := c%cw, c/cw
			minX, minY, maxX, maxY = min(minX, cx), min(minY, cy), max(maxX, cx), max(maxY, cy)
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {-1, -1}, {1, -1}, {-1, 1}} {
				nx, ny := cx+d[0], cy+d[1]
				if nx < 0 || ny < 0 || nx >= cw || ny >= ch {
					continue
				}
				if n := ny*cw + nx; cells[n] && !seen[n] {
					seen[n] = true
					stack = append(stack, n)
				}
			}
		}
		out = append(out, Rect{X: minX * gridCell, Y: minY * gridCell, W: min((maxX+1)*gridCell, w) - minX*gridCell, H: min((maxY+1)*gridCell, h) - minY*gridCell})
	}
	return out
}
