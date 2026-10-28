// quads_test.go verifies the exact search-highlight pipeline: MuPDF's
// fz_search quads are the hit source (each hit carries its exact Rects),
// and page-box origins are subtracted so highlights sit on the glyphs
// even for pages whose CropBox does not start at 0,0.
package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestSearchHitsCarryExactRects: search a real document; every hit has an
// exact rect within page bounds, and hitRects paints them.
func TestSearchHitsCarryExactRects(t *testing.T) {
	s := reproState(t, 3)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	t.Cleanup(func() { s.CloseTab(tab.ID) })

	buildIndex(t, tab) // synchronous index: RunSearch writes land on main
	tab.StartSearch("page")
	s.RunSearch(tab)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(tab.Search.Hits) == 0 {
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}
	if len(tab.Search.Hits) == 0 {
		t.Fatal("no hits")
	}

	for hi, h := range tab.Search.Hits {
		if len(h.Rects) != 1 {
			t.Fatalf("hit %d: %d exact rects, want 1 (fz_search is the hit source)", hi, len(h.Rects))
		}
		pw, ph := tab.Doc.PageSize(h.Page)
		ox, oy := tab.Doc.PageOrigin(h.Page)
		r := h.Rects[0]
		if r.X0 < ox-1 || r.Y0 < oy-1 || r.X1 > ox+pw+1 || r.Y1 > oy+ph+1 {
			t.Fatalf("hit %d rect (%.1f,%.1f)-(%.1f,%.1f) outside page (origin (%.1f,%.1f) size %.0fx%.0f)",
				hi, r.X0, r.Y0, r.X1, r.Y1, ox, oy, pw, ph)
		}
	}

	page := tab.Search.Hits[0].Page
	if rects := hitRects(tab, page, 1.0); len(rects) == 0 {
		t.Fatal("hitRects produced nothing for a hit page")
	}
	t.Logf("hits=%d (all with exact rects), hitRects(page %d)=%d",
		len(tab.Search.Hits), page, len(hitRects(tab, page, 1.0)))
}

// TestCroppedPageOriginMapping: with a page whose MediaBox starts at
// (50,50), the origin is reported and the DIP mapping subtracts it, so a
// hit at the top-left of the crop maps near (0,0) — not off-page.
func TestCroppedPageOriginMapping(t *testing.T) {
	s := NewAppState()
	tab, err := s.OpenTab(genCroppedTestPDF(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.CloseTab(tab.ID) })

	// MuPDF normalizes a page's box to origin (0,0) in fz_bound_page —
	// verify that (it makes PageOrigin a no-op defense here, which the
	// mapping asserts below rely on).
	ox, oy := tab.Doc.PageOrigin(0)
	if ox != 0 || oy != 0 {
		t.Logf("MuPDF preserved box origin (%.1f,%.1f) — mapping must subtract it", ox, oy)
	}

	buildIndex(t, tab)
	tab.StartSearch("cropmark")
	s.RunSearch(tab)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(tab.Search.Hits) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if len(tab.Search.Hits) == 0 {
		t.Fatal("no hits in the cropped document")
	}

	// The word sits at user-space (72,750) on a [50 50 662 842] box:
	// normalized, that is ~(22, 42) from the page's top-left, give or
	// take the 24pt font's ascent — the quad must sit right there.
	r := tab.Search.Hits[0].Rects[0]
	cx, cy := (r.X0+r.X1)/2, (r.Y0+r.Y1)/2
	pw, ph := tab.Doc.PageSize(0)
	if cx < 5 || cx > 120 || cy < 5 || cy > 120 {
		t.Fatalf("quad center (%.1f,%.1f) not near the text at ~(22,42..66) — misplaced (page %.0fx%.0f)", cx, cy, pw, ph)
	}

	// And the DIP mapping keeps it in-page.
	k := dipPerPt * 1.0
	dip := quadDipRect(tab, 0, r, k)
	if dip[0] < -1 || dip[1] < -1 {
		t.Fatalf("mapped rect %v starts off-page: origin not subtracted (quad (%.1f,%.1f)-(%.1f,%.1f))",
			dip, r.X0, r.Y0, r.X1, r.Y1)
	}
	if float64(dip[0]+dip[2]) > pw*k+2 || float64(dip[1]+dip[3]) > ph*k+2 {
		t.Fatalf("mapped rect %v exceeds page %.0fx%.0f DIPs", dip, pw*k, ph*k)
	}
}
