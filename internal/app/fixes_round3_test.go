// fixes_round3_test.go locks in the four fixes: Left/Right page turns,
// the palette keyboard flow, the custom-zoom width cap, and thumbnails
// re-requesting after LRU eviction.
package app

import (
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"gopdf/internal/pdf"
)

// TestArrowKeysTurnPages: bare Left/Right navigate one page.
func TestArrowKeysTurnPages(t *testing.T) {
	s := reproState(t, 5)
	tt := ui.NewTester(s.View, 1200, 800)
	tt.Frame()

	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if s.Active().CurPage != 1 {
		t.Fatalf("after Right, CurPage = %d, want 1", s.Active().CurPage)
	}
	tt.Key(0, ui.KeyLeft)
	tt.Frame()
	if s.Active().CurPage != 0 {
		t.Fatalf("after Left, CurPage = %d, want 0", s.Active().CurPage)
	}
}

// TestPaletteFlow drives the palette like a user: Ctrl+K opens it, typing
// filters, Down moves the selection, Enter runs it and closes.
func TestPaletteFlow(t *testing.T) {
	s := reproState(t, 5)
	tt := ui.NewTester(s.View, 1200, 800)
	tt.Frame()

	tt.Key(ui.Ctrl, ui.KeyK)
	tt.Frame()
	if !s.ShowPalette {
		t.Fatal("Ctrl+K did not open the palette")
	}
	if !tt.HasText("COMMANDS") && !tt.HasText("Commands") {
		t.Fatalf("palette has no command section: %q", tt.Texts())
	}

	tt.Type("3") // a number: the "Page 3" jump is the top runnable result
	tt.Frame()
	if !tt.HasText("Go to page") {
		t.Fatalf("no Page 3 jump result for query '3': %q", tt.Texts())
	}

	// The selection must sit on a runnable row right after typing: plain
	// Ctrl+K, type, Enter has to DO something, not close on a header.
	if palSel < 0 || palItems[palSel].run == nil {
		t.Fatalf("palSel=%d is not a runnable row after a query change", palSel)
	}

	// Enter on the fresh selection runs the Page 3 jump.
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if s.ShowPalette {
		t.Fatal("Enter did not close the palette")
	}
	if s.Active().CurPage != 2 {
		t.Fatalf("Enter did not run the Page 3 jump: CurPage %d", s.Active().CurPage)
	}

	// Down from the top also has to land on a runnable row, never a
	// section header.
	s.ShowPalette = true
	tt.Frame()
	tt.Type("z") // "Zoom In" / "Zoom Out" match
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if palSel < 0 || palSel >= len(palItems) || palItems[palSel].run == nil {
		t.Fatalf("after Down, palSel=%d of %d is not runnable", palSel, len(palItems))
	}
}

// TestCustomZoomUncapped: an explicit zoom must scale past the 900-DIP
// reading-column cap, which stays for the fit modes only.
func TestCustomZoomUncapped(t *testing.T) {
	s := reproState(t, 3)
	tab := s.Active()

	// US Letter is 612x792 pt; at 200% the page is 1632 DIPs wide.
	tab.SetZoomMode(ZoomCustom)
	tab.SetZoom(2.0)
	w, h := pageDipSize(tab, 0, 2.0)
	if w < 1600 || h < 2000 {
		t.Fatalf("custom zoom 200%%: page %.0fx%.0f DIPs, want ~1632x2112 (uncapped)", w, h)
	}

	// Fit modes keep the spec's 900-DIP reading column.
	tab.SetZoomMode(ZoomFitWidth)
	w, h = pageDipSize(tab, 0, 2.0)
	if w > 900 {
		t.Fatalf("fit-mode width %.0f exceeds the 900 cap", w)
	}
	if h > 1200 { // height scales by the same ratio: 2112*900/1632 ≈ 1165
		t.Fatalf("fit-mode height %.0f not scaled with the cap", h)
	}
}

// TestThumbnailsRerequestAfterEviction: the shared PageCache is an LRU —
// after full-page renders push thumbnails out, the sidebar must ask for
// them again instead of staying blank forever.
func TestThumbnailsRerequestAfterEviction(t *testing.T) {
	s := reproState(t, 8)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()

	thumbKey := pdf.CacheKey{Page: 0, DPI: pdf.ThumbDPI, Rotate: 0}
	if ok := crashPump(t, tt, 3*time.Second, func() bool {
		return tab.Cache.Get(thumbKey) != nil
	}); !ok {
		t.Fatal("thumbnails never rendered initially")
	}

	// Flood the LRU (256MB) with big page renders until the thumb is gone.
	// No Cache.Get in the loop: Get bumps recency and would protect the
	// thumbnail from eviction.
	big := image.NewRGBA(image.Rect(0, 0, 2200, 2800)) // ~24MB each
	for p := 0; p < 40; p++ {
		tab.Cache.Put(pdf.CacheKey{Page: 100 + p, DPI: 300, Rotate: 0}, big)
	}
	if tab.Cache.Get(thumbKey) != nil {
		t.Skip("could not evict the thumbnail in this run")
	}

	// Back on the sidebar, the thumbnail must come back.
	if ok := crashPump(t, tt, 3*time.Second, func() bool {
		return tab.Cache.Get(thumbKey) != nil
	}); !ok {
		t.Fatal("thumbnail stayed blank after eviction (not re-requested)")
	}
}
