// view_anim_test.go locks in the slide-in/out transitions of the two
// side panels (left: thumbnails/outline; right: search results): a
// toggle glides the clipped container's width over ~200ms with the
// content riding the slide, while a document or tab coming or going
// snaps the panel at once. Reduced motion is respected implicitly —
// Animate goes straight to its target when the desktop asks for less
// motion — so there is nothing to assert for it here.
package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// closeDocs closes every open document at test end, so fitz lets the
// TempDir cleanup remove the generated PDFs.
func closeDocs(t *testing.T, s *AppState) {
	t.Helper()
	t.Cleanup(func() {
		for _, tab := range s.Tabs {
			if tab.Doc != nil {
				tab.Doc.Close()
			}
		}
	})
}

// TestViewAnimSidebarSlide: toggling the sidebar glides it in and out
// — the content is there as the slide starts, rides the transition,
// and is gone once the panel settles shut. A document opening or
// closing (no tabs, a launcher tab) snaps the panel instead, so the
// sidebar never appears for a launcher and never lingers past its
// document.
func TestViewAnimSidebarSlide(t *testing.T) {
	s := NewAppState() // ShowSidebar defaults true
	tab, err := s.OpenTab(genTestPDF(t, 3))
	if err != nil {
		t.Fatal(err)
	}
	closeDocs(t, s)
	tt := ui.NewTester(s.View, 1200, 800)
	tt.Frame()
	if !tt.HasText("Thumbnails") {
		t.Fatalf("sidebar missing on the first frame with a document: %q", tt.Texts())
	}

	// Toggle off: the first frame of the close sits at the full width
	// (the Tester's clock stands still, so the slide has not moved
	// yet) and the content steps down at once. On a live display the
	// first frame is already mid-slide, and the next assertions see
	// the content riding the panel out.
	s.ToggleSidebar()
	tt.Frame()
	if tt.HasText("Thumbnails") {
		t.Fatalf("sidebar content still built after the toggle frame: %q", tt.Texts())
	}
	tt.Advance(50 * time.Millisecond) // a quarter of the 200ms slide
	tt.Frame()
	if !tt.HasText("Thumbnails") {
		t.Fatalf("sidebar content missing mid-slide: the close is not gliding: %q", tt.Texts())
	}
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if tt.HasText("Thumbnails") {
		t.Fatalf("sidebar content still visible after the slide settled shut: %q", tt.Texts())
	}

	// Toggle back on: the slide starts from shut with the content
	// already built in the (zero-width, clipped) container, so nothing
	// pops in late.
	s.ToggleSidebar()
	tt.Frame()
	if !tt.HasText("Thumbnails") {
		t.Fatalf("sidebar content missing as the slide opens: %q", tt.Texts())
	}
	tt.Advance(50 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Thumbnails") {
		t.Fatalf("sidebar content missing mid-slide while opening: %q", tt.Texts())
	}
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Thumbnails") {
		t.Fatalf("sidebar content missing after the slide settled open: %q", tt.Texts())
	}

	// Closing the last tab snaps the panel shut: no content the frame
	// the document goes away, and none after the clock moves on.
	s.CloseTab(tab.ID)
	tt.Frame()
	if tt.HasText("Thumbnails") {
		t.Fatalf("sidebar visible without a document: %q", tt.Texts())
	}
	tt.Advance(500 * time.Millisecond)
	tt.Frame()
	if tt.HasText("Thumbnails") {
		t.Fatalf("sidebar content came back after the document closed: %q", tt.Texts())
	}

	// A launcher tab is not a document: the sidebar stays hidden, and
	// building those frames panics nowhere.
	s.NewEmptyTab()
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	tt.Frame()
	if tt.HasText("Thumbnails") {
		t.Fatalf("sidebar visible on a launcher tab: %q", tt.Texts())
	}

	// Opening a document in the launcher's place snaps the sidebar in
	// for it at once.
	if _, err := s.OpenTab(genTestPDF(t, 1)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Thumbnails") {
		t.Fatalf("sidebar missing right after a document opened: %q", tt.Texts())
	}
}

// TestViewAnimSearchPanelSlide: the right-hand search results panel
// slides on the same terms — the content builds as the slide starts,
// rides the transition both ways, and a tab closing snaps it away.
func TestViewAnimSearchPanelSlide(t *testing.T) {
	s := NewAppState()
	s.ShowSidebar = false // keep the sidebar's texts out of the assertions
	tab, err := s.OpenTab(genTestPDF(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	closeDocs(t, s)
	tt := ui.NewTester(s.View, 1000, 700)
	tt.Frame()
	if tt.HasText("Type and press Enter") {
		t.Fatalf("search panel visible before it was toggled on: %q", tt.Texts())
	}

	// Toggle on: the empty-panel hint is there the frame the slide
	// starts (the container builds its content at any width).
	s.ShowSearchPanel = true
	tt.Frame()
	if !tt.HasText("Type and press Enter") {
		t.Fatalf("search panel content missing as the slide opens: %q", tt.Texts())
	}
	tt.Advance(50 * time.Millisecond) // a quarter of the 200ms slide
	tt.Frame()
	if !tt.HasText("Type and press Enter") {
		t.Fatalf("search panel content missing mid-slide while opening: %q", tt.Texts())
	}
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Type and press Enter") {
		t.Fatalf("search panel content missing after the slide settled open: %q", tt.Texts())
	}

	// Toggle off: the content steps down on the frozen first frame and
	// rides the glide on the moved ones, then stays gone.
	s.ShowSearchPanel = false
	tt.Frame()
	if tt.HasText("Type and press Enter") {
		t.Fatalf("search panel content still built after the toggle frame: %q", tt.Texts())
	}
	tt.Advance(50 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Type and press Enter") {
		t.Fatalf("search panel content missing mid-slide: the close is not gliding: %q", tt.Texts())
	}
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if tt.HasText("Type and press Enter") {
		t.Fatalf("search panel content still visible after the slide settled shut: %q", tt.Texts())
	}

	// Closing the tab snaps the panel away for good.
	s.CloseTab(tab.ID)
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	tt.Frame()
	if tt.HasText("Type and press Enter") {
		t.Fatalf("search panel visible without a tab: %q", tt.Texts())
	}
}
