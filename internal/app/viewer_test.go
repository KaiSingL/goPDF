// viewer_test.go drives the page area (module B) in ui.NewTester: the
// empty state, nil-doc safety, real renders through the cgo MuPDF
// pipeline (gen_test.go), fit-zoom math, Ctrl+wheel and pinch zooming,
// the 150ms settled re-render, page tracking from the scroll offset, and
// external page jumps. All deviceScale-dependent numbers assume the
// Tester's default scale of 1.
package app

import (
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"gopdf/internal/pdf"
)

// resetViewer isolates the viewer's package-level state between tests.
func resetViewer() {
	viewerMu.Lock()
	for _, st := range viewerStates {
		if st.timer != nil {
			st.timer.Stop()
		}
	}
	viewerStates = map[string]*tabViewState{}
	bmpCache = map[bmpKey]*bmpEntry{}
	bmpOrder = nil
	inflight = map[inflightKey]bool{}
	viewerGen = map[string]int{}
	viewerMu.Unlock()
	viewerScroll = ui.ScrollState{}
	activeViewerTab = ""
	lastTabCount = 0
	deviceScale = 1
}

// viewerDoc opens a generated n-page PDF in a fresh state and closes the
// document when the test ends. The sidebar is off to keep thumbnail
// renders out of the page cache.
func viewerDoc(t *testing.T, n int) (*AppState, *Tab) {
	t.Helper()
	s := NewAppState()
	s.ShowSidebar = false
	tab, err := s.OpenTab(genTestPDF(t, n))
	if err != nil {
		t.Fatalf("open generated pdf: %v", err)
	}
	t.Cleanup(func() { s.CloseTab(tab.ID) }) // closes the Doc too
	return s, tab
}

// pumpFrames builds frames until cond holds or the deadline passes. Page
// renders arrive on the doc's worker goroutine; each Frame picks up what
// has landed, so the loop yields between builds.
func pumpFrames(tt *ui.Tester, cond func() bool, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if cond() {
			return true
		}
		tt.Frame()
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestViewerEmptyStateHint(t *testing.T) {
	resetViewer()
	s := NewAppState()
	tt := ui.NewTester(s.View, 900, 640)
	if !tt.HasText("Drop a PDF anywhere") {
		t.Fatalf("empty state texts = %q", tt.Texts())
	}
	if err := tt.Click("Open PDF…"); err != nil { // stub no-op; must not panic
		t.Fatal(err)
	}
	tt.Frame() // extra frames while the elements stay
	tt.Frame()
}

func TestViewerNilDocPlaceholder(t *testing.T) {
	resetViewer()
	s := NewAppState()
	s.Tabs = append(s.Tabs, &Tab{ID: "t1", Title: "New Tab", NumPages: 3})
	s.ActiveTab = 0
	tt := ui.NewTester(s.View, 900, 640)
	// Doc-less tabs are launcher tabs now (a failed open never creates a
	// tab): they show the drop hint, like the no-tab empty state.
	if !tt.HasText("Drop a PDF anywhere") {
		t.Fatalf("placeholder texts = %q", tt.Texts())
	}
	tt.Frame()
	tt.Frame()

	// A tab without pages keeps a placeholder (no panic on zero pages).
	// Doc-less stays a launcher tab; the drop hint persists.
	s.Tabs[0].NumPages = 0
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Drop a PDF anywhere") {
		t.Fatalf("texts with zero pages = %q", tt.Texts())
	}
}

func TestViewerRendersGeneratedPages(t *testing.T) {
	resetViewer()
	s, tab := viewerDoc(t, 3)
	tt := ui.NewTester(s.View, 900, 640)
	if !pumpFrames(tt, func() bool { return tab.Cache.Len() > 0 }, 5*time.Second) {
		t.Fatalf("no page renders arrived; cache = %d entries", tab.Cache.Len())
	}
	if tab.CurPage != 0 {
		t.Errorf("CurPage = %d, want 0", tab.CurPage)
	}
	viewerMu.Lock()
	_, ok := bmpCache[bmpKey{tabID: tab.ID, page: 0, rotate: 0}]
	viewerMu.Unlock()
	if !ok {
		t.Error("no bitmap for page 0")
	}
	// Closing the tab returns the empty state without a panic.
	s.CloseTab(tab.ID)
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Drop a PDF anywhere") {
		t.Errorf("texts after closing the last tab = %q", tt.Texts())
	}
}

func TestFitZoomMath(t *testing.T) {
	resetViewer()
	_, tab := viewerDoc(t, 1)
	// US Letter: 612×792 pt = 816×1056 DIP at 100% (96/72 DIPs per point).
	tab.ZoomMode = ZoomFitWidth
	if z := tab.FitZoom(816, 2000); math.Abs(z-1) > 1e-9 {
		t.Errorf("FitWidth(816) = %v, want 1", z)
	}
	if z := tab.FitZoom(408, 2000); math.Abs(z-0.5) > 1e-9 {
		t.Errorf("FitWidth(408) = %v, want 0.5", z)
	}
	tab.ZoomMode = ZoomFitPage
	if z := tab.FitZoom(816, 1056); math.Abs(z-1) > 1e-9 {
		t.Errorf("FitPage(816, 1056) = %v, want 1", z)
	}
	if z := tab.FitZoom(408, 528); math.Abs(z-0.5) > 1e-9 {
		t.Errorf("FitPage(408, 528) = %v, want 0.5", z)
	}

	tab.SetZoom(2.5)
	if tab.ZoomMode != ZoomCustom {
		t.Errorf("SetZoom mode = %v, want ZoomCustom", tab.ZoomMode)
	}
	if z := tab.FitZoom(816, 1056); z != 2.5 {
		t.Errorf("custom FitZoom = %v, want 2.5", z)
	}

	// 90° rotation swaps the page size (Tab.PageSizePt), so fitting the
	// width now needs the height's extent.
	tab.ZoomMode = ZoomFitWidth
	tab.Rotate = 90
	if z := tab.FitZoom(1056, 2000); math.Abs(z-1) > 1e-9 {
		t.Errorf("rotated FitWidth(1056) = %v, want 1", z)
	}
}

func TestViewerZoomGestures(t *testing.T) {
	resetViewer()
	s, tab := viewerDoc(t, 1)
	tt := ui.NewTester(s.View, 900, 640)
	tt.Frame()
	z0 := tab.Zoom // fit-width zoom of the first frame with a real viewport
	if z0 <= 0 {
		t.Fatalf("initial zoom = %v", z0)
	}

	// A pinch over the page area zooms by its scale, around the pointer,
	// switching to ZoomCustom.
	tt.Move(450, 320)
	tt.Pinch(450, 320, 1.25)
	tt.Frame()
	if tab.ZoomMode != ZoomCustom {
		t.Fatalf("after pinch mode = %v, want ZoomCustom", tab.ZoomMode)
	}
	want := z0 * 1.25
	if math.Abs(tab.Zoom-want) > want*1e-6 {
		t.Errorf("after pinch zoom = %v, want %v", tab.Zoom, want)
	}

	// Without Ctrl, a wheel notch scrolls and leaves the zoom alone.
	y0 := viewerScroll.Y
	tt.Wheel(450, 320, 0, 40)          // a notch down scrolls down
	tt.Advance(300 * time.Millisecond) // the notch's glide
	if math.Abs(tab.Zoom-want) > want*1e-6 {
		t.Errorf("plain wheel changed zoom to %v, want %v", tab.Zoom, want)
	}
	if viewerScroll.Y <= y0 {
		t.Errorf("plain wheel did not scroll: Y = %v, was %v", viewerScroll.Y, y0)
	}

	// A mouse wheel's notch with Ctrl (InputScroll with Ctrl in Mods —
	// what Windows reports) steps the zoom by ZoomStep. The Tester's
	// Wheel sends no modifiers, so this drives viewerInput directly.
	g := computeGeom(tab, tab.Zoom)
	ok := viewerInput(s, g, ui.InputEvent{Kind: ui.InputScroll, DY: -40, Mods: ui.Ctrl})
	if !ok {
		t.Fatal("viewerInput did not take a Ctrl wheel notch")
	}
	tt.Frame()
	want *= ZoomStep
	if math.Abs(tab.Zoom-want) > want*1e-6 {
		t.Errorf("after Ctrl notch zoom = %v, want %v", tab.Zoom, want)
	}

	// Plain scrolling is left to the scroll container.
	if viewerInput(s, g, ui.InputEvent{Kind: ui.InputScroll, DY: -40}) {
		t.Error("viewerInput took a plain scroll")
	}
}

func TestViewerZoomSettlesRerender(t *testing.T) {
	resetViewer()
	s, tab := viewerDoc(t, 1)
	tab.ZoomMode = ZoomCustom
	tab.SetZoom(1.0) // 96 DPI at the Tester's scale of 1: deterministic keys
	tt := ui.NewTester(s.View, 900, 640)
	if !pumpFrames(tt, func() bool { return tab.Cache.Len() > 0 }, 5*time.Second) {
		t.Fatal("initial render did not arrive")
	}
	n0 := tab.Cache.Len()

	// Zooming in resizes pages at once and re-renders at the settled DPI
	// after the 150ms debounce.
	tab.ZoomIn() // 1.2 → target DPI round(96*1.2) = 115
	if !pumpFrames(tt, func() bool { return tab.Cache.Len() > n0 }, 5*time.Second) {
		t.Fatalf("no settled re-render; cache = %d entries, was %d", tab.Cache.Len(), n0)
	}
	viewerMu.Lock()
	e := bmpCache[bmpKey{tabID: tab.ID, page: 0, rotate: 0}]
	viewerMu.Unlock()
	if e == nil {
		t.Fatal("no bitmap after the settled re-render")
	}
	// At 115 DPI page 0 is 612/72*115 ≈ 977 px wide (fitz rounds; allow a
	// pixel or two).
	if w, _ := e.bm.Size(); math.Abs(float64(w-977)) > 4 {
		t.Errorf("settled bitmap width = %d, want ≈977 (115 DPI)", w)
	}
}

func TestViewerTracksMostVisiblePage(t *testing.T) {
	resetViewer()
	s, tab := viewerDoc(t, 3)
	tab.ZoomMode = ZoomCustom
	tab.SetZoom(1.0) // pages are 816×1056 DIP; tops 24, 1096, 2168
	tt := ui.NewTester(s.View, 900, 640)
	tt.Frame()
	tt.Advance(time.Second) // settle the initial align glide

	// Page 1's top at the viewport top puts the viewport center inside it.
	viewerScroll.Y = 1096 - pagePadY
	tt.Frame()
	if tab.CurPage != 1 {
		t.Errorf("CurPage = %d, want 1", tab.CurPage)
	}
	viewerScroll.Y = 2168 - pagePadY
	tt.Frame()
	if tab.CurPage != 2 {
		t.Errorf("CurPage = %d, want 2", tab.CurPage)
	}
}

func TestViewerFollowsExternalJump(t *testing.T) {
	resetViewer()
	s, tab := viewerDoc(t, 3)
	tab.ZoomMode = ZoomCustom
	tab.SetZoom(1.0)
	tt := ui.NewTester(s.View, 900, 640)
	tt.Frame()
	tt.Advance(time.Second) // settle the initial align glide

	// A jump through state (toolbar, outline, palette, search) glides the
	// page's top to the viewport top.
	tab.Navigate(2, 0)
	tt.Frame() // the build that sees the jump and starts the glide
	tt.Advance(time.Second)
	if y := viewerScroll.Y; math.Abs(float64(y-2144)) > 5 { // 2168 - 24
		t.Errorf("scroll Y = %v, want ≈2144", y)
	}
	if tab.CurPage != 2 {
		t.Errorf("CurPage = %d, want 2", tab.CurPage)
	}

	// Back is an external jump too.
	tab.Back()
	tt.Frame()
	tt.Advance(time.Second)
	if tab.CurPage != 0 {
		t.Errorf("CurPage after Back = %d, want 0", tab.CurPage)
	}
	if y := viewerScroll.Y; math.Abs(float64(y)) > 5 {
		t.Errorf("scroll Y after Back = %v, want ≈0", y)
	}
}

func TestViewerSearchHighlightsRender(t *testing.T) {
	resetViewer()
	s, tab := viewerDoc(t, 2)
	tt := ui.NewTester(s.View, 900, 640)
	tt.Frame()
	tt.Advance(time.Second)

	tab.Search.Hits = []pdf.SearchHit{
		{Page: 0, Spans: []pdf.TextSpan{{Page: 0, X: 72, Y: 700, W: 220, H: 28, Text: "page-1"}}},
		{Page: 1, Spans: []pdf.TextSpan{{Page: 1, X: 72, Y: 700, W: 220, H: 28, Text: "page-2"}}},
	}
	tab.Search.Current = 0
	tt.Frame()
	tt.Frame()
	if tt.Image() == nil {
		t.Error("no frame painted with search highlights")
	}
}

func TestSpanDipRectRotation(t *testing.T) {
	resetViewer()
	_, tab := viewerDoc(t, 1)
	sp := pdf.TextSpan{Page: 0, X: 100, Y: 50, W: 200, H: 20}
	k := dipPerPt // zoom 1

	tab.Rotate = 0
	if r := spanDipRect(tab, 0, sp, k); r != [4]float32{f32(100 * k), f32(50 * k), f32(200 * k), f32(20 * k)} {
		t.Errorf("rot 0 = %v", r)
	}
	// 90° clockwise: (x, y) -> (h0 - y - h, x); the axes swap.
	tab.Rotate = 90
	if r := spanDipRect(tab, 0, sp, k); r != [4]float32{f32(722 * k), f32(100 * k), f32(20 * k), f32(200 * k)} {
		t.Errorf("rot 90 = %v", r)
	}
	// 180°: (x, y) -> (w0 - x - w, h0 - y - h).
	tab.Rotate = 180
	if r := spanDipRect(tab, 0, sp, k); r != [4]float32{f32(312 * k), f32(722 * k), f32(200 * k), f32(20 * k)} {
		t.Errorf("rot 180 = %v", r)
	}
	// 270°: (x, y) -> (y, w0 - x - w); the axes swap the other way.
	tab.Rotate = 270
	if r := spanDipRect(tab, 0, sp, k); r != [4]float32{f32(50 * k), f32(312 * k), f32(20 * k), f32(200 * k)} {
		t.Errorf("rot 270 = %v", r)
	}
}

func f32(f float64) float32 { return float32(f) }
