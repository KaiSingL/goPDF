// teardown_test.go verifies the close-tab teardown (A4): closing a tab
// drops its per-tab UI caches — the viewer's page bitmaps and view state
// and the sidebar's thumbnail bitmaps, list state and outline open-state —
// instead of leaving their pixel buffers reachable after the doc is
// released. Driven against a REAL document so the async render pipeline
// fills the caches with actual renders first.
package app

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"gopdf/internal/pdf"
)

// countBmpCache returns how many cached page bitmaps belong to tabID.
func countBmpCache(tabID string) int {
	viewerMu.Lock()
	defer viewerMu.Unlock()
	n := 0
	for k := range bmpCache {
		if k.tabID == tabID {
			n++
		}
	}
	return n
}

// countThumbBitmaps returns how many cached sidebar thumbnails belong to
// tabID (UI-thread-only map; tests count it on the test goroutine, which
// update() runs builds on when no window exists).
func countThumbBitmaps(tabID string) int {
	n := 0
	for k := range thumbBitmaps {
		if k.tabID == tabID {
			n++
		}
	}
	return n
}

func TestTeardownTabStateOnClose(t *testing.T) {
	resetViewer()
	s := NewAppState()
	tab, err := s.OpenTab(genTestPDF(t, 8))
	if err != nil {
		t.Fatalf("OpenTab: %v", err)
	}
	t.Cleanup(func() { s.CloseTab(tab.ID) }) // closes the Doc; no-op after the test's own CloseTab

	// outlineOpen fills only from a real outline tree; the generated PDF
	// has none, so seed one entry for this tab and one for a foreign tab
	// to check the "tabID|" prefix scoping of the removal.
	outlineOpen[tab.ID+"|o/0"] = new(bool)
	outlineOpen["tab-teardown-other|o/0"] = new(bool)
	// viewerGen fills only on reload; seed it so teardown has an entry to
	// drop (any consistent generation works for the render callbacks).
	viewerMu.Lock()
	viewerGen[tab.ID] = 0
	viewerMu.Unlock()
	// thumbLastPage is only written when CurPage changes (a missing entry
	// reads as 0), so leave page 0 to populate it like scrolling would.
	tab.Navigate(1, 0)

	// Default view: sidebar thumbnails + page strip. Pump frames until
	// real renders have landed in every cache the teardown must clear.
	tt := ui.NewTester(s.View, 1200, 800)
	if !pumpFrames(tt, func() bool {
		return tab.Cache.Len() > 0 && countThumbBitmaps(tab.ID) > 0
	}, 5*time.Second) {
		t.Fatalf("renders did not land: cache=%d thumbs=%d",
			tab.Cache.Len(), countThumbBitmaps(tab.ID))
	}
	if countBmpCache(tab.ID) == 0 {
		t.Fatal("bmpCache has no entries for the tab before CloseTab")
	}
	if _, ok := viewerStates[tab.ID]; !ok {
		t.Fatal("viewerStates has no entry for the tab before CloseTab")
	}
	if _, ok := thumbLists[tab.ID]; !ok {
		t.Fatal("thumbLists has no entry for the tab before CloseTab")
	}
	if _, ok := thumbLastPage[tab.ID]; !ok {
		t.Fatal("thumbLastPage has no entry for the tab before CloseTab")
	}

	// A render stuck in flight is torn down too (normally cleared on
	// completion; seed one so the inflightClearTab path is exercised).
	viewerMu.Lock()
	inflight[inflightKey{tabID: tab.ID, key: pdf.CacheKey{Page: 3, DPI: 999, Rotate: 0}}] = true
	viewerMu.Unlock()

	// Close. Everything keyed by the tab must be gone; no frames are
	// pumped after this, so the asserts below see teardown's work alone.
	s.CloseTab(tab.ID)

	viewerMu.Lock()
	for k := range bmpCache {
		if k.tabID == tab.ID {
			t.Errorf("bmpCache still has %v", k)
		}
	}
	for k := range inflight {
		if k.tabID == tab.ID {
			t.Errorf("inflight still has %v", k)
		}
	}
	if _, ok := viewerStates[tab.ID]; ok {
		t.Error("viewerStates still has the tab")
	}
	if _, ok := viewerGen[tab.ID]; ok {
		t.Error("viewerGen still has the tab")
	}
	viewerMu.Unlock()

	if n := countThumbBitmaps(tab.ID); n != 0 {
		t.Errorf("thumbBitmaps still has %d entries for the tab", n)
	}
	for _, k := range thumbOrder {
		if k.tabID == tab.ID {
			t.Errorf("thumbOrder still has %v", k)
		}
	}
	if _, ok := thumbLists[tab.ID]; ok {
		t.Error("thumbLists still has the tab")
	}
	if _, ok := thumbLastPage[tab.ID]; ok {
		t.Error("thumbLastPage still has the tab")
	}
	thumbMu.Lock()
	_, okReq := thumbsRequested[tab.ID]
	_, okGen := thumbGens[tab.ID]
	thumbMu.Unlock()
	if okReq {
		t.Error("thumbsRequested still has the tab")
	}
	if okGen {
		t.Error("thumbGens still has the tab")
	}
	prefix := tab.ID + "|"
	for k := range outlineOpen {
		if strings.HasPrefix(k, prefix) {
			t.Errorf("outlineOpen still has %q", k)
		}
	}
	if _, ok := outlineOpen["tab-teardown-other|o/0"]; !ok {
		t.Error("outlineOpen lost a foreign tab's entry (over-broad removal)")
	}

	// Loose memory sanity: with the caches dropped and the doc closed,
	// two GCs must bring the heap back under the bound. The structural
	// asserts above are the real check.
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if ms.HeapAlloc > 256<<20 {
		t.Errorf("HeapAlloc after closing the last tab = %d MB, want < 256 MB",
			ms.HeapAlloc>>20)
	}
}
