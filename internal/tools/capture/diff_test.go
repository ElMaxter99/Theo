package capture

import (
	"image"
	"image/color"
	"testing"
)

func solid(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func fill(img *image.NRGBA, r Rect, c color.NRGBA) {
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
}

var white = color.NRGBA{255, 255, 255, 255}

func TestDiffIdenticalIsZero(t *testing.T) {
	a := solid(100, 80, white)
	if r := Diff(a, solid(100, 80, white), DiffOptions{}); r.Changed != 0 || r.Percent != 0 || len(r.Boxes) != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestDiffPercentAndBoxes(t *testing.T) {
	a, b := solid(100, 100, white), solid(100, 100, white)
	fill(b, Rect{10, 10, 20, 50}, color.NRGBA{200, 0, 0, 255}) // 1000 px of 10000
	r := Diff(a, b, DiffOptions{})
	if r.Changed != 1000 || r.Percent != 10 {
		t.Fatalf("%d %v", r.Changed, r.Percent)
	}
	if len(r.Boxes) != 1 || r.Boxes[0].X > 10 || r.Boxes[0].Y > 10 || r.Boxes[0].X+r.Boxes[0].W < 30 || r.Boxes[0].Y+r.Boxes[0].H < 60 {
		t.Fatalf("%+v", r.Boxes)
	}
}

func TestDiffToleranceMaskAndHeight(t *testing.T) {
	a, b := solid(100, 100, white), solid(100, 100, white)
	fill(b, Rect{0, 0, 100, 100}, color.NRGBA{250, 250, 250, 255})
	if r := Diff(a, b, DiffOptions{}); r.Changed != 0 {
		t.Fatalf("a slight shade is within tolerance: %d", r.Changed)
	}
	fill(b, Rect{0, 0, 50, 50}, color.NRGBA{0, 0, 0, 255})
	if r := Diff(a, b, DiffOptions{Masks: []Rect{{0, 0, 50, 50}}}); r.Changed != 0 || r.Compared != 7500 {
		t.Fatalf("masked: %+v", r)
	}
	// a longer second view: the extra height is a change
	long := solid(100, 150, white)
	r := Diff(a, long, DiffOptions{})
	if r.Changed != 5000 || r.HeightA != 100 || r.HeightB != 150 {
		t.Fatalf("height: %+v", r)
	}
}
