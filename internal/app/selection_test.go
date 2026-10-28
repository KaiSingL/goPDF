// selection_test.go covers text selection (selection.go): the DIP↔page
// mapping round-trips through all four rotations, a drag around a known
// quad selects its text, the pointer wiring claims presses with capture,
// and Esc/click clear.
package app

import (
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
// position learned from search) selects the covering spans and yields
// copyable text. With the index prebuilt the resolution is synchronous.
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

	if !tab.Sel.Active || tab.Sel.Dragging {
		t.Fatalf("selection active %v dragging %v, want active and finished", tab.Sel.Active, tab.Sel.Dragging)
	}
	if len(tab.Sel.Spans) == 0 {
		t.Fatal("no spans selected around the hit")
	}
	if text := tab.SelectionText(); !strings.Contains(text, "page-1") {
		t.Fatalf("selection text %q lacks the hit's text", text)
	}
	if rects := selPaintRects(tab, 0, k); len(rects) == 0 {
		t.Fatal("nothing to paint for a live selection")
	} else {
		for _, rc := range rects {
			if rc[2] <= 0 || rc[3] <= 0 {
				t.Fatalf("paint rect %v degenerate", rc)
			}
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
