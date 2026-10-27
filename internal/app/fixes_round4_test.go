// fixes_round4_test.go: launcher tabs, the thumbnail placeholder on far
// jumps, the priority render lanes, and center-out page ordering.
package app

import (
	"image"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"gopdf/internal/pdf"
)

// TestLauncherTab: "+" creates an empty launcher tab with the drop hint;
// opening a document afterwards replaces it.
func TestLauncherTab(t *testing.T) {
	s := NewAppState()
	tt := ui.NewTester(s.View, 1200, 800)
	tt.Frame()

	if err := tt.Click("New tab"); err != nil {
		t.Fatalf("click New tab: %v", err)
	}
	tt.Frame()
	if len(s.Tabs) != 1 || !s.Tabs[0].IsEmpty() {
		t.Fatalf("after +: %d tabs, IsEmpty=%v", len(s.Tabs), s.Active().IsEmpty())
	}
	if !tt.HasText("Drop a PDF anywhere") {
		t.Fatalf("launcher tab does not show the drop hint: %q", tt.Texts())
	}

	path := genTestPDF(t, 2)
	if _, err := s.OpenTab(path); err != nil {
		t.Fatal(err)
	}
	// fitz holds the file open until Close; TempDir cleanup would fail.
	t.Cleanup(func() {
		for _, tab := range s.Tabs {
			if tab.Doc != nil {
				tab.Doc.Close()
			}
		}
	})
	tt.Frame()
	if len(s.Tabs) != 1 || s.Tabs[0].IsEmpty() {
		t.Fatalf("opening a doc should replace the launcher: %d tabs, first empty=%v",
			len(s.Tabs), s.Tabs[0].IsEmpty())
	}
}

// TestThumbPlaceholderOnFarJump: with a rendered thumbnail in the cache,
// pageBitmap returns it upscaled as an instant placeholder while the full
// render is still missing.
func TestThumbPlaceholderOnFarJump(t *testing.T) {
	s := reproState(t, 4)
	tab := s.Active()
	t.Cleanup(func() { s.CloseTab(tab.ID) })

	// A rendered thumbnail, as the sidebar would have produced.
	thumb := image.NewRGBA(image.Rect(0, 0, 100, 130))
	tab.Cache.Put(pdf.CacheKey{Page: 2, DPI: pdf.ThumbDPI, Rotate: 0}, thumb)

	tt := ui.NewTester(s.View, 1200, 800) // pumps build state (deviceScale etc.)
	tt.Frame()

	bm := pageBitmap(tab, 2, 1.0, 1.0) // settled zoom, no full render cached
	if bm == nil {
		t.Fatal("no instant placeholder from the cached thumbnail")
	}
}

// TestCenterOut: page order radiates from the current page.
func TestCenterOut(t *testing.T) {
	got := centerOut(3, 9, 5)
	want := []int{5, 6, 4, 7, 3, 8, 9}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("centerOut(3,9,5) = %v, want %v", got, want)
		}
	}
	if single := centerOut(4, 4, 9); len(single) != 1 || single[0] != 4 {
		t.Fatalf("centerOut(4,4,9) = %v, want [4]", single)
	}
}

// TestRenderPriorityLanes: a page render queued behind thumbnails still
// completes first — the worker prefers the page lane.
func TestRenderPriorityLanes(t *testing.T) {
	s := reproState(t, 2)
	tab := s.Active()
	t.Cleanup(func() { s.CloseTab(tab.ID) })

	var mu sync.Mutex
	var order []string
	done := func(kind string) func(*image.RGBA, error) {
		return func(*image.RGBA, error) {
			mu.Lock()
			order = append(order, kind)
			mu.Unlock()
		}
	}
	// Queue a wall of thumbnail renders, then one page render.
	for i := 0; i < 6; i++ {
		tab.Doc.RenderAsync(pdf.RenderReq{Page: 0, DPI: pdf.ThumbDPI, Kind: pdf.KindThumb}, done("thumb"))
	}
	tab.Doc.RenderAsync(pdf.RenderReq{Page: 1, DPI: 150, Kind: pdf.KindPage}, done("page"))

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(order)
		mu.Unlock()
		if n == 7 {
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			t.Fatalf("only %d renders completed: %v", len(order), order)
			mu.Unlock()
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	pageAt := -1
	lastThumbAt := -1
	for i, k := range order {
		if k == "page" {
			pageAt = i
		}
		if k == "thumb" {
			lastThumbAt = i
		}
	}
	if pageAt > lastThumbAt-3 { // the page must beat the tail of the wall
		t.Fatalf("page render completed at %d of %d (order %v): priority lane not effective",
			pageAt, len(order)-1, order)
	}
}
