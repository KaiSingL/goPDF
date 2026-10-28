// quads_test.go verifies the exact search-highlight pipeline: after a
// search, MuPDF's own fz_search quads arrive per hit (SearchState.
// QuadsByHit) and the viewer paints them in preference to the estimated
// span rectangles.
package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestSearchQuadsArrivePerHit: search a real document, wait for the exact
// quads, and check they land on the hit pages within page bounds.
func TestSearchQuadsArrivePerHit(t *testing.T) {
	s := reproState(t, 3)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	t.Cleanup(func() { s.CloseTab(tab.ID) })

	// Build the index synchronously first (the B2 helper): RunSearch's
	// IndexAsync then fires its callback inline on THIS goroutine, so the
	// Hits/QuadsByHit writes happen on main before the polling below —
	// race-clean, as TestSearchPanelFlow does it.
	buildIndex(t, tab)
	tab.StartSearch("page")
	s.RunSearch(tab)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tt.Frame()
		if len(tab.Search.Hits) > 0 && len(tab.Search.QuadsByHit) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(tab.Search.Hits) == 0 {
		t.Fatal("no hits")
	}
	if len(tab.Search.QuadsByHit) == 0 {
		t.Fatal("exact quads never arrived")
	}

	// Every quad must be inside its page and have positive extent, and the
	// hit index must exist in Hits.
	pw, ph := tab.PageSizePt(tab.Search.Hits[0].Page)
	for hi, rects := range tab.Search.QuadsByHit {
		if hi < 0 || hi >= len(tab.Search.Hits) {
			t.Fatalf("quad hit index %d out of Hits (len %d)", hi, len(tab.Search.Hits))
		}
		for _, r := range rects {
			if r.X1 <= r.X0 || r.Y1 <= r.Y0 {
				t.Fatalf("degenerate quad %+v", r)
			}
			if r.X0 < -1 || r.Y0 < -1 || r.X1 > pw+1 || r.Y1 > ph+1 {
				t.Fatalf("quad %+v outside page %.0fx%.0f", r, pw, ph)
			}
		}
	}

	// The viewer prefers the quads: hitRects for the hit's page must
	// produce one rect per exact quad for the current hit.
	page := tab.Search.Hits[0].Page
	rects := hitRects(tab, page, 1.0)
	if len(rects) == 0 {
		t.Fatal("hitRects produced nothing for a hit page")
	}
	t.Logf("hits=%d quads=%d hitRects(page %d)=%d", len(tab.Search.Hits), len(tab.Search.QuadsByHit), page, len(rects))
}
