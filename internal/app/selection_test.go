// selection_test.go covers text selection (selection.go): the DIP↔page
// mapping round-trips through all four rotations, a drag around a known
// quad selects its text with EXACT MuPDF geometry, the pointer wiring
// claims presses with capture, Esc/click clear, and the painted quad
// sits on the glyphs of the rendered bitmap.
package app

import (
	"image"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"gopdf/internal/pdf"
)

// TestDipPagePtRoundTrip: dipToPagePt inverts spanDipRect's point
// mapping at every rotation.
func TestDipPagePtRoundTrip(t *testing.T) {
	s := reproState(t, 1)
	tab := s.Active()
	t.Cleanup(func() { s.CloseTab(tab.ID) })

	k := 1.47 // an arbitrary drawn scale
	for _, rot := range []int{0, 90, 180, 270} {
		tab.Rotate = rot
		for _, p := range [][2]float64{{10, 10}, {300, 200}, {600, 780}, {61, 42}} {
			fwd := spanDipRect(tab, 0, pdf.TextSpan{X: p[0], Y: p[1]}, k)
			gx, gy := dipToPagePt(tab, 0, k, fwd[0], fwd[1])
			if abs64(gx-p[0]) > 0.01 || abs64(gy-p[1]) > 0.01 {
				t.Fatalf("rotate %d: (%.1f,%.1f) → dip (%.1f,%.1f) → (%.1f,%.1f)",
					rot, p[0], p[1], fwd[0], fwd[1], gx, gy)
			}
		}
	}
}

// TestSelectionDragSelectsText: dragging around a known hit quad (its
// position learned from search) selects with fz_highlight_selection
// quads that cover the search quad, and fz_copy_selection text that
// carries the hit's word.
func TestSelectionDragSelectsText(t *testing.T) {
	s := reproState(t, 3)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	t.Cleanup(func() { s.CloseTab(tab.ID) })

	buildIndex(t, tab)
	tab.StartSearch("page-1")
	s.RunSearch(tab)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(tab.Search.Hits) == 0 {
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}
	if len(tab.Search.Hits) == 0 || len(tab.Search.Hits[0].Rects) == 0 {
		t.Fatal("no exact hit to anchor the drag")
	}
	r := tab.Search.Hits[0].Rects[0]

	k := pageDipScale(tab, 0, tab.Zoom)
	ptDip := func(pt float64) float32 { return float32(pt * k) }
	s.StartSelection(tab, 0, ptDip(r.X0-40), ptDip(r.Y0-6))
	s.ExtendSelection(tab, 0, ptDip(r.X1+40), ptDip(r.Y1+6))
	s.FinishSelection(tab)

	// The worker reply lands through the frame-drained queue.
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !tab.Sel.resolved {
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}

	if !tab.Sel.Active || tab.Sel.Dragging {
		t.Fatalf("selection active %v dragging %v, want active and finished", tab.Sel.Active, tab.Sel.Dragging)
	}
	if !tab.Sel.resolved || len(tab.Sel.Quads) == 0 {
		t.Fatalf("resolved %v quads %d — the exact geometry never landed", tab.Sel.resolved, len(tab.Sel.Quads))
	}
	if text := tab.SelectionText(); !strings.Contains(text, "page-1") {
		t.Fatalf("selection text %q lacks the hit's word", text)
	}

	// The selection must cover what search found: every search-quad
	// corner sits inside some selection quad (±2pt tolerance).
	covered := func(x, y float64) bool {
		for _, q := range tab.Sel.Quads {
			if x >= q.X0-2 && x <= q.X1+2 && y >= q.Y0-2 && y <= q.Y1+2 {
				return true
			}
		}
		return false
	}
	for _, c := range [][2]float64{{r.X0, r.Y0}, {r.X1, r.Y1}, {(r.X0 + r.X1) / 2, (r.Y0 + r.Y1) / 2}} {
		if !covered(c[0], c[1]) {
			t.Fatalf("search quad corner (%.0f,%.0f) not covered by any selection quad", c[0], c[1])
		}
	}

	rects := selPaintRects(tab, 0, k)
	if len(rects) == 0 {
		t.Fatal("nothing to paint for a live selection")
	}
	for _, rc := range rects {
		if rc[2] <= 0 || rc[3] <= 0 {
			t.Fatalf("paint rect %v degenerate", rc)
		}
	}
}

// pumpToPage0 waits out the glide to page 0: viewerScroll is
// package-global and carries whatever offset the previous test left,
// and one Frame lands mid-glide — a press then hits the wrong page.
func pumpToPage0(t *testing.T, tt *ui.Tester) {
	t.Helper()
	for i := 0; i < 80; i++ {
		tt.Frame()
		// Scrolling() is no part of the condition: a palette-wheel test
		// earlier in the suite leaves the shared idle flag set; the
		// POSITION is the contract.
		if viewerScroll.Y <= pagePadY+2 {
			return
		}
		tt.Advance(16 * time.Millisecond)
	}
	t.Fatalf("the glide to page 0 never settled (Y=%.1f scrolling=%v)", viewerScroll.Y, viewerScroll.Scrolling())
}

// TestSelectionPointerWiring: a press on the page claims the pointer and
// starts a drag, moves keep extending (press capture), the release
// finishes, and Esc clears.
func TestSelectionPointerWiring(t *testing.T) {
	s := reproState(t, 3)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	t.Cleanup(func() { s.CloseTab(tab.ID) })
	pumpToPage0(t, tt)

	tt.Press(600, 400) // over page 0's middle
	if !tab.Sel.Active || !tab.Sel.Dragging || tab.Sel.Page != 0 {
		t.Fatalf("press did not start a selection: %+v %+v page %d", tab.Sel.Active, tab.Sel.Dragging, tab.Sel.Page)
	}
	tt.Move(650, 440)
	if tab.Sel.Cur == tab.Sel.Anchor {
		t.Fatal("move did not extend the selection")
	}
	tt.Release(660, 450)
	if tab.Sel.Dragging || !tab.Sel.Active {
		t.Fatalf("release: dragging %v active %v, want finished and kept", tab.Sel.Dragging, tab.Sel.Active)
	}

	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tab.Sel.Active {
		t.Fatal("Esc did not clear the selection")
	}
}

// TestSelectionClickClears: a press/release without movement is a click,
// not a selection.
func TestSelectionClickClears(t *testing.T) {
	s := reproState(t, 3)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	t.Cleanup(func() { s.CloseTab(tab.ID) })
	pumpToPage0(t, tt)

	tt.Press(600, 400)
	tt.Release(600, 400)
	if tab.Sel.Active {
		t.Fatal("a click left a selection behind")
	}
}

// TestSelectionQuadCoversGlyphs is the pixel-level guard against
// selection misalignment on real documents: it renders page 0, drags a
// selection around a known word, and asserts the exact quad lands on
// the text pixels of the bitmap actually drawn — the same guard that
// caught the search-highlight drift.
func TestSelectionQuadCoversGlyphs(t *testing.T) {
	s := reproState(t, 3)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	t.Cleanup(func() { s.CloseTab(tab.ID) })

	tab.SetZoom(1) // custom 100%: the render DPI is deterministic
	dpi := dpiFor(1)

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

	buildIndex(t, tab)
	tab.StartSearch("page-1")
	s.RunSearch(tab)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(tab.Search.Hits) == 0 {
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}
	if len(tab.Search.Hits) == 0 || len(tab.Search.Hits[0].Rects) == 0 {
		t.Fatal("no exact hit to anchor the drag")
	}
	r := tab.Search.Hits[0].Rects[0]

	k := pageDipScale(tab, 0, tab.Zoom)
	s.StartSelection(tab, 0, float32((r.X0-40)*k), float32((r.Y0-6)*k))
	s.ExtendSelection(tab, 0, float32((r.X1+40)*k), float32((r.Y1+6)*k))
	s.FinishSelection(tab)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !tab.Sel.resolved {
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}
	if !tab.Sel.resolved || len(tab.Sel.Quads) == 0 {
		t.Fatalf("resolved %v quads %d — no exact geometry", tab.Sel.resolved, len(tab.Sel.Quads))
	}

	// Quad → bitmap pixels (the bitmap is the page at px = pt/72·DPI).
	pw, ph := tab.Doc.PageSize(0)
	ox, oy := tab.Doc.PageOrigin(0)
	q := tab.Sel.Quads[0]
	b := img.Bounds()
	x0 := int((q.X0 - ox) * float64(b.Dx()) / pw)
	x1 := int((q.X1 - ox) * float64(b.Dx()) / pw)
	y0 := int((q.Y0 - oy) * float64(b.Dy()) / ph)
	y1 := int((q.Y1 - oy) * float64(b.Dy()) / ph)
	x0, x1 = max(x0, b.Min.X), min(x1, b.Max.X)
	y0, y1 = max(y0, b.Min.Y), min(y1, b.Max.Y)
	if x1-x0 < 2 || y1-y0 < 2 {
		t.Fatalf("selection quad maps to a %dx%d pixel rect — off-page or degenerate", x1-x0, y1-y0)
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
		t.Fatalf("dark pixels inside the selection quad = %.1f%% — the quad misses the text", inFrac*100)
	}
	if inFrac < 3*pageFrac {
		t.Fatalf("quad density %.3f not clearly above the page baseline %.3f — misplaced", inFrac, pageFrac)
	}
	t.Logf("selection quad %dx%dpx, in %.1f%% vs page %.2f%% — on the glyphs",
		x1-x0, y1-y0, inFrac*100, pageFrac*100)
}
