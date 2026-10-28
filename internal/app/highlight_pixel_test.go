// highlight_pixel_test.go is the end-to-end guard for highlight
// placement: it renders a page through the real pipeline, maps an exact
// fz_search quad into the RENDERED BITMAP's pixel space, and asserts the
// quad covers the glyphs — dark-pixel density inside the quad far above
// the page's baseline. Any drift between MuPDF's geometry and ours (axis
// flip, origin, rotation, scale) fails here, whatever its cause.
package app

import (
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"gopdf/internal/pdf"
)

// TestHighlightQuadCoversGlyphs renders page 0 at a fixed custom 100%
// zoom (deterministic DPI headless), searches "page-1", and checks the
// hit's exact quad lands on the text pixels of the bitmap actually drawn.
func TestHighlightQuadCoversGlyphs(t *testing.T) {
	s := reproState(t, 3)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	t.Cleanup(func() { s.CloseTab(tab.ID) })

	tab.SetZoom(1) // custom 100%: the render DPI is deterministic
	dpi := dpiFor(1)

	// Pump until the settled render lands in the page cache.
	key := pdf.CacheKey{Page: 0, DPI: dpi, Rotate: 0}
	deadline := time.Now().Add(10 * time.Second)
	var img *image.RGBA
	for time.Now().Before(deadline) {
		tt.Frame()
		if v := tab.Cache.Get(key); v != nil {
			img = v
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if img == nil {
		t.Fatal("page render never landed in the cache")
	}

	// One exact hit on page 0.
	buildIndex(t, tab)
	tab.StartSearch("page-1")
	s.RunSearch(tab)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(tab.Search.Hits) == 0 {
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}
	if len(tab.Search.Hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(tab.Search.Hits))
	}
	h := tab.Search.Hits[0]
	if h.Page != 0 || len(h.Rects) != 1 {
		t.Fatalf("hit page %d rects %d, want page 0 / 1 rect", h.Page, len(h.Rects))
	}

	// Quad → bitmap pixels: the bitmap is the page at px = pt/72*DPI,
	// 0,0-anchored (origin subtracted, none here).
	pw, ph := tab.Doc.PageSize(0)
	ox, oy := tab.Doc.PageOrigin(0)
	r := h.Rects[0]
	b := img.Bounds()
	x0 := int((r.X0 - ox) * float64(b.Dx()) / pw)
	x1 := int((r.X1 - ox) * float64(b.Dx()) / pw)
	y0 := int((r.Y0 - oy) * float64(b.Dy()) / ph)
	y1 := int((r.Y1 - oy) * float64(b.Dy()) / ph)
	x0, x1 = max(x0, b.Min.X), min(x1, b.Max.X)
	y0, y1 = max(y0, b.Min.Y), min(y1, b.Max.Y)
	if x1-x0 < 2 || y1-y0 < 2 {
		t.Fatalf("quad maps to a %dx%d pixel rect — off-page or degenerate", x1-x0, y1-y0)
	}

	darkIn, areaIn := 0, 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			o := img.PixOffset(x, y)
			if img.Pix[o] < 128 && img.Pix[o+1] < 128 && img.Pix[o+2] < 128 {
				darkIn++
			}
			areaIn++
		}
	}
	darkAll, areaAll := 0, 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			o := img.PixOffset(x, y)
			if img.Pix[o] < 128 && img.Pix[o+1] < 128 && img.Pix[o+2] < 128 {
				darkAll++
			}
			areaAll++
		}
	}
	inFrac := float64(darkIn) / float64(max(areaIn, 1))
	pageFrac := float64(darkAll) / float64(max(areaAll, 1))
	if inFrac < 0.03 {
		t.Fatalf("dark pixels inside the quad = %.1f%% — the quad misses the text", inFrac*100)
	}
	if inFrac < 3*pageFrac {
		t.Fatalf("quad density %.3f not clearly above the page baseline %.3f — misplaced", inFrac, pageFrac)
	}
	t.Logf("quad %dx%dpx, in %.1f%% vs page %.2f%% — on the glyphs",
		x1-x0, y1-y0, inFrac*100, pageFrac*100)
}
