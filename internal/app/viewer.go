// viewer.go is the scrollable page area (module B): the continuous,
// single-column page strip of the active tab (spec §3–§4).
//
// # Rendering pipeline (spec §3)
//
// Each build lays the whole strip out on paper (computeGeom) and builds
// elements only for the pages around the scroll offset. A page without a
// bitmap at the target DPI — 96 * zoom * deviceScale, so a render's pixels
// match its on-screen size one to one — is requested through
// Tab.Doc.RenderAsync. The worker callback stores the converted bitmap in
// a small package-level cache and the *image.RGBA in the tab's PageCache,
// then asks the window to redraw. It never touches AppState: everything it
// needs is goroutine-safe (pdf.PageCache, Window.Invalidate) or guarded by
// viewerMu, so it is race-free even where update() would run inline (unit
// tests, window == nil).
//
// # Zoom (spec §3)
//
// Zoom changes resize pages immediately, scaling the cached bitmaps; a
// per-tab 150ms time.AfterFunc (coalesced, one per tab) re-renders the
// visible pages at the settled DPI afterwards. Ctrl+wheel notches and
// pinch / touchpad zoom gestures (InputZoom) zoom around the pointer.
//
// # Page tracking
//
// The most visible page becomes Tab.CurPage via SetCurrentPage (silent, no
// history). Jumps from the toolbar, outline, palette or search change
// CurPage behind the viewer's back; the viewer notices and glides that
// page's top to the viewport top. ReloadGen changes drop cached bitmaps so
// pages re-render.
package app

import (
	"fmt"
	"image"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"gopdf/internal/pdf"
)

const (
	pageGap      float32 = 16    // DIPs between pages (spec §4)
	pagePadY     float32 = 24    // strip padding above and below
	pagePadX     float32 = 12    // strip padding at the sides
	maxPageW     float32 = 900.0 // widest a page shows (spec §4)
	pageRadius   float32 = 6
	zoomDebounce         = 150 * time.Millisecond // re-render delay after zooming
	bitmapCap            = 48                     // bitmaps kept across all tabs
	// dipPerPt is DIPs per point at 100% zoom, as Tab.FitZoom computes it.
	dipPerPt = 96.0 / 72.0
)

var (
	pageWhite  = ui.RGBA(255, 255, 255, 1) // pages stay white in dark mode (spec §4)
	pageShadow = ui.RGBA(0, 0, 0, 0.30)
)

// bmpKey identifies a page bitmap: one per tab, page and rotation. The
// entry records the DPI it was rendered at: while zooming, the old
// bitmap keeps showing (scaled) until the settled re-render at the new
// DPI replaces it.
type bmpKey struct {
	tabID        string
	page, rotate int
}

// bmpEntry is a cached display bitmap and the DPI of its render.
type bmpEntry struct {
	bm  *ui.Bitmap
	dpi int
}

// inflightKey guards against double-requesting one render.
type inflightKey struct {
	tabID string
	key   pdf.CacheKey
}

// pageBox is one page's place in the strip, in DIPs from the strip top.
type pageBox struct {
	top, w, h float32
}

// geom is the laid-out strip: every page's box, the zoom it was built for
// and the total content height.
type geom struct {
	z        float64
	boxes    []pageBox
	contentH float32
}

// zoomAnchor keeps the content point under the pointer across a zoom step.
type zoomAnchor struct {
	page    int
	local   float32 // offset into the page at the old zoom
	anchorY float32 // pointer Y within the viewport
	ratio   float32 // newZ / oldZ
}

// tabViewState is what the viewer remembers about one tab between builds.
// zoom, renderZ, seenPage, gen, geom and anchor are only touched on the UI
// thread (build + input callbacks); settle and timer are also touched by
// the debounce goroutine, always under viewerMu.
type tabViewState struct {
	zoom     float64 // last built zoom
	renderZ  float64 // zoom whose DPI renders are requested at
	settle   bool    // debounce fired: adopt the current zoom next build
	timer    *time.Timer
	seenPage int // CurPage value the viewer last saw (-1: align on next build)
	gen      int // last ReloadGen seen
	anchor   *zoomAnchor
}

var (
	viewerMu     sync.Mutex
	viewerStates = map[string]*tabViewState{}
	bmpCache     = map[bmpKey]*bmpEntry{}
	bmpOrder     []bmpKey // eviction order for bmpCache
	inflight     = map[inflightKey]bool{}
	viewerGen    = map[string]int{} // mirror of Tab.ReloadGen, readable by workers

	// viewerScroll tracks the page strip's scroll offset; the app reads
	// where it is, scrolls it, and the user's scrolling writes it. UI
	// thread only.
	viewerScroll ui.ScrollState

	activeViewerTab string
	lastTabCount    int

	// deviceScale is the display pixel ratio, captured from the painter
	// every frame (Painter.Scale). Render DPI = 96 * zoom * deviceScale.
	deviceScale float32 = 1
)

// viewer builds the page area of the active tab.
func viewer(c *ui.Context, s *AppState) {
	t := s.Active()
	pruneViewerState(s)
	if t == nil || t.Doc == nil || t.NumPages <= 0 {
		viewerEmpty(c, s, t)
		return
	}

	// Viewport box committed by the previous frame (zero on the first);
	// FitZoom falls back to Tab.Zoom until it is known.
	var viewW, viewH float64
	if b := viewerScroll.Bounds(c); b.W > 1 && b.H > 1 {
		viewW = float64(max(b.W-2*pagePadX, 1))
		viewH = float64(max(b.H, 1))
	}
	z := t.FitZoom(viewW, viewH)

	st := viewerStateFor(t.ID)
	if activeViewerTab != t.ID { // tab switched: align with its CurPage
		activeViewerTab = t.ID
		st.seenPage = -1
	}
	if st.gen != t.ReloadGen { // reloaded: bitmaps and queued renders are stale
		st.gen = t.ReloadGen
		viewerMu.Lock()
		viewerGen[t.ID] = t.ReloadGen
		bmpClearTab(t.ID)
		inflightClearTab(t.ID)
		viewerMu.Unlock()
	}

	// Zoom bookkeeping: display at z right away; request renders at
	// renderZ once zooming settles (armZoomDebounce).
	viewerMu.Lock()
	if st.renderZ <= 0 {
		st.renderZ, st.zoom = z, z
	} else {
		if st.settle {
			st.settle = false
			st.renderZ = z
		}
		if st.zoom != z {
			st.zoom = z
			armZoomDebounce(t.ID, st)
		}
	}
	renderZ := st.renderZ
	viewerMu.Unlock()

	g := computeGeom(t, z)

	if a := st.anchor; a != nil { // zoom gesture: keep the pointer's content in place
		st.anchor = nil
		if a.page >= 0 && a.page < len(g.boxes) {
			viewerScroll.Y = max(0, g.boxes[a.page].top+a.local*a.ratio-a.anchorY)
		}
	}
	if st.seenPage != t.CurPage { // someone else jumped: glide to the page top
		st.seenPage = t.CurPage
		viewerScroll.ScrollTo(0, g.boxes[t.CurPage].top-pagePadY, ui.Smooth)
	} else if !viewerScroll.Scrolling() { // else: follow the user's scrolling
		if mv := mostVisible(g, viewerScroll.Y, float32(viewH)); mv != t.CurPage {
			t.SetCurrentPage(mv) // silent, no history
			st.seenPage = mv
		}
	}

	// The strip. Only the pages around the scroll offset are built; two
	// spacers reserve the exact height of the unbuilt rest, so the scroll
	// container's content is the full strip (its offsets, its maximum and
	// the scrollbar all stay truthful).
	c.Key("gopdf-viewer")
	lo, hi := visibleRange(g, viewerScroll.Y, float32(viewH))
	topSpacer := max(g.boxes[lo].top-pageGap, 0)
	builtH := float32(0)
	for i := lo; i <= hi; i++ {
		builtH += g.boxes[i].h
	}
	botSpacer := max(g.contentH-pageGap*float32(hi-lo+2)-topSpacer-builtH, 0)
	ui.Scroll(c).Fill().Grow(1).TrackScroll(&viewerScroll).
		Draw(func(p *ui.Painter, _ ui.Rect) { deviceScale = p.Scale() }).
		HandleInput(func(ev ui.InputEvent) bool { return viewerInput(s, g, ev) }).
		Children(func() {
			ui.Column(c).Gap(pageGap).Padding(0, pagePadX).
				AlignItems(ui.Center).Shrink(0).
				Children(func() {
					ui.Box(c).Height(topSpacer).Shrink(0)
					// Build outward from the current page so its render is
					// requested FIRST on jumps — the visible page is what the
					// user is waiting for, not its neighbors.
					for _, i := range centerOut(lo, hi, t.CurPage) {
						buildPage(c, s, t, i, g.boxes[i], renderZ, z)
					}
					ui.Box(c).Height(botSpacer).Shrink(0)
				})
		})
}

// viewerEmpty is the viewer area with nothing to show: the drop hint for
// no tab or a launcher tab (identical states: drop a PDF or click open),
// a placeholder for empty documents otherwise.
func viewerEmpty(c *ui.Context, s *AppState, t *Tab) {
	th := c.Theme()
	c.Key("gopdf-viewer-empty")
	e := ui.Column(c).Fill().Grow(1).Center().Gap(th.Space(3))
	if fs := e.DroppedFiles(); len(fs) > 0 { // a drop works here too
		openDropped(s, fs)
	}
	e.Children(func() {
		if t == nil || t.IsEmpty() { // launcher: drop hint + recents
			ui.Text(c, "Drop a PDF anywhere").FontSize(24).Bold()
			ui.Text(c, "or open one from the toolbar").TextColor(th.TextMuted)
			if ui.PrimaryButton(c, "Open PDF…").Clicked() {
				openFilePicker()
			}
			if len(s.Recents) > 0 {
				ui.Text(c, "Recent documents").TextColor(th.TextMuted).FontSize(12)
				ui.Row(c).Gap(th.Space(3)).Children(func() {
					for _, p := range s.Recents[:min(len(s.Recents), 8)] {
						recentCard(c, th, p)
					}
				})
			}
			return
		}
		ui.Text(c, "This document has no pages.").TextColor(th.TextMuted)
	})
}

// recentCard is one recent-document card: the first page's thumbnail over
// the file name; clicking opens it.
func recentCard(c *ui.Context, th *ui.Theme, path string) {
	card := ui.ButtonBase(c.Key("recent:" + path)).
		Width(112).AlignItems(ui.Center).
		Radius(th.Space(1)).Cursor(ui.CursorPointer).Label("Open " + filepath.Base(path))
	if card.Hovered() {
		card.Background(th.SurfaceHover)
	}
	// ButtonBase is a ROW (it is the tab strip's row base): its direct
	// children would sit side by side and the name would be clipped away
	// by Width(112). One Column child stacks the surface and the name
	// vertically instead; FillWidth lets the column center its children
	// across the card's full 112 DIPs.
	card.Children(func() {
		ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(th.Space(1)).
			Children(func() {
				// Same frame treatment as the sidebar thumbnails: a
				// DrawOver stroke paints after children, so the image can
				// never cover the frame's right/bottom edges.
				surface := ui.Box(c).Size(104, 136).Radius(th.Space(0.5)).
					Background(th.Surface)
				surface.DrawOver(func(p *ui.Painter, r ui.Rect) {
					p.Stroke(r, th.TextMuted.Alpha(0.40), th.Space(0.5), 1)
				})
				if bm := recentThumbFor(path); bm != nil {
					surface.Children(func() {
						ui.Image(c, bm).Size(104, 136).Fit(ui.FillBox).Radius(th.Space(0.5))
					})
				}
				ui.Text(c, filepath.Base(path)).SingleLine().Ellipsis("…").
					MaxWidth(104).FontSize(11).TextColor(th.TextMuted)
			})
	})
	card.OnClick(func() { OpenPath(path) })
}

// recentThumbFor returns the cached first-page thumbnail of a recent
// document, kicking an on-demand render through a transient pdf.Doc the
// first time it is asked for. Nothing here touches AppState; the callback
// stores goroutine-safe bits only.
var (
	recentMu       sync.Mutex
	recentThumbs   = map[string]*ui.Bitmap{} // path → thumb; nil entry = tried and failed
	recentOrder    []string                  // insertion order, for the recentThumbCap
	recentInflight = map[string]bool{}
)

// recentThumbCap bounds the thumbnail cache so a long session browsing
// many recents cannot grow it without end (24 thumbnails at ThumbDPI are
// a few MB at most).
const recentThumbCap = 24

// recentThumbPut stores the thumbnail under path, evicting the oldest
// entries past recentThumbCap in FIFO order (like thumbOrder). The caller
// holds recentMu. Failures are cached as nil, exactly like successes, so
// a broken PDF is not retried every build.
func recentThumbPut(path string, bm *ui.Bitmap) {
	if _, ok := recentThumbs[path]; !ok {
		recentOrder = append(recentOrder, path)
	}
	recentThumbs[path] = bm
	for len(recentOrder) > recentThumbCap {
		delete(recentThumbs, recentOrder[0])
		recentOrder = recentOrder[1:]
	}
}

func recentThumbFor(path string) *ui.Bitmap {
	recentMu.Lock()
	bm, done := recentThumbs[path]
	busy := recentInflight[path]
	recentMu.Unlock()
	if done || busy {
		return bm
	}
	recentMu.Lock()
	recentInflight[path] = true
	recentMu.Unlock()
	go func() {
		doc, err := pdf.Open(path)
		if err != nil {
			recentMu.Lock()
			recentThumbPut(path, nil)
			delete(recentInflight, path)
			recentMu.Unlock()
			return
		}
		doc.RenderAsync(pdf.RenderReq{Page: 0, DPI: pdf.ThumbDPI, Kind: pdf.KindThumb},
			func(img *image.RGBA, err error) {
				var out *ui.Bitmap
				if err == nil && img != nil {
					out = ui.NewBitmap(img)
				}
				recentMu.Lock()
				recentThumbPut(path, out)
				delete(recentInflight, path)
				recentMu.Unlock()
				if window != nil {
					window.Invalidate()
				}
				// Close off the worker itself: Close waits for the worker
				// to drain, which cannot happen from inside a task.
				go doc.Close()
			})
	}()
	return nil
}

// openDropped opens the first dropped PDF in a new tab. Launcher tabs are
// closed by OpenTab itself, so a drop on a New Tab page replaces it.
func openDropped(s *AppState, paths []string) {
	for _, p := range paths {
		if strings.EqualFold(filepath.Ext(p), ".pdf") {
			if _, err := s.OpenTab(p); err == nil {
				return
			}
		}
	}
}

// Search-highlight colors, the classic reader scheme: pale yellow under
// every hit, warm orange under the current one. Pages are white in both
// themes, so fixed on-page hues work; the dark variants sit a touch
// warmer and stronger so the marks hold up against dimmed chrome.
var (
	hitOtherLight = ui.RGBA(0xFF, 0xE9, 0x8A, 0.55) // pale yellow, light theme
	hitOtherDark  = ui.RGBA(0xFF, 0xE0, 0x6E, 0.60) // pale yellow, dark theme
	hitCurLight   = ui.RGBA(0xFF, 0xA2, 0x2B, 0.55) // warm orange, light theme
	hitCurDark    = ui.RGBA(0xFF, 0x98, 0x1F, 0.60) // warm orange, dark theme
)

// buildPage builds one page: a white surface with shadow and radius
// (spec §4), the rendered bitmap scaled to fit, a placeholder until the
// render arrives, and the search-hit highlights painted over it.
func buildPage(c *ui.Context, s *AppState, t *Tab, i int, box pageBox, renderZ, z float64) {
	c.Key(fmt.Sprintf("gopdf-page-%d", i))
	e := ui.Box(c).Size(box.w, box.h).Shrink(0).
		Background(pageWhite).
		Radius(pageRadius).
		Shadow(0, 6, 24, 0, pageShadow).
		Clip()

	// Highlights are computed at build time (plain data) and painted in
	// the draw callback, which may run repeatedly.
	hits := hitRects(t, i, z)
	var curCol, otherCol ui.Color
	if len(hits) > 0 {
		curCol, otherCol = hitCurLight, hitOtherLight
		if c.Theme().Dark {
			curCol, otherCol = hitCurDark, hitOtherDark
		}
	}

	bm := pageBitmap(t, i, renderZ, z)
	e.Children(func() {
		if bm != nil {
			// The bitmap is the settled render at another size while
			// zooming; scaling it here is the instant zoom (spec §3).
			ui.Image(c, bm).Size(box.w, box.h).Fit(ui.FillBox)
			return
		}
		ui.Column(c).Fill().Center().Children(func() {
			ui.Textf(c, "Page %d", i+1).TextColor(c.Theme().TextMuted)
		})
	})
	if len(hits) > 0 {
		e.DrawOver(func(p *ui.Painter, r ui.Rect) {
			for _, h := range hits {
				col := otherCol
				if h.current {
					col = curCol
				}
				p.Fill(ui.Rect{
					X: r.X + h.rect[0],
					Y: r.Y + h.rect[1],
					W: h.rect[2],
					H: h.rect[3],
				}, col, 2)
			}
		})
	}
}

// pageBitmap returns the bitmap to show for page i: the render at the
// wanted DPI when it is in the tab's PageCache (converting it once), or a
// request for it. While the wanted render is on its way — or while the
// zoom is still changing (renderZ != z) — the bitmap at the previous DPI
// keeps showing, scaled by the resized page (spec §3).
func pageBitmap(t *Tab, i int, renderZ, z float64) *ui.Bitmap {
	k := bmpKey{t.ID, i, t.Rotate}
	dpi := dpiFor(renderZ)
	viewerMu.Lock()
	e := bmpCache[k]
	viewerMu.Unlock()
	if e != nil && e.dpi == dpi {
		return e.bm
	}
	if renderZ == z { // settled: the wanted render is worth having now
		key := pdf.CacheKey{Page: i, DPI: dpi, Rotate: t.Rotate}
		if img := t.Cache.Get(key); img != nil {
			bm := ui.NewBitmap(img) // zero-copy for the packed RGBA fitz emits
			viewerMu.Lock()
			bmpPut(k, bm, dpi)
			viewerMu.Unlock()
			return bm
		}
		ik := inflightKey{t.ID, key}
		viewerMu.Lock()
		busy := inflight[ik]
		if !busy {
			inflight[ik] = true
		}
		viewerMu.Unlock()
		if !busy {
			gen := genFor(t.ID)
			t.Doc.RenderAsync(pdf.RenderReq{Page: i, DPI: dpi, Rotate: t.Rotate, Kind: pdf.KindPage},
				func(img *image.RGBA, err error) { // doc worker goroutine
					viewerMu.Lock()
					delete(inflight, ik)
					viewerMu.Unlock()
					if err != nil || img == nil || genFor(t.ID) != gen {
						return // failed, tab closed, or reload superseded the render
					}
					bm := ui.NewBitmap(img) // goroutine-safe: atomic id, pixels kept
					viewerMu.Lock()
					bmpPut(k, bm, key.DPI)
					viewerMu.Unlock()
					t.Cache.Put(key, img) // thread-safe; also the completion signal
					if window != nil {
						window.Invalidate() // safe from any goroutine
					}
				})
		}
	}
	if e != nil {
		return e.bm
	}
	// Nothing rendered for this zoom yet — an upscaled 72-DPI thumbnail
	// gives instant (soft) feedback on far jumps while the full render is
	// on its way; the real render replaces it when it lands. Reads a thumb
	// the sidebar rendered (rotate 0 only: rotated views keep the text
	// placeholder rather than a sideways preview).
	if t.Rotate == 0 {
		if img := t.Cache.Get(pdf.CacheKey{Page: i, DPI: pdf.ThumbDPI, Rotate: 0}); img != nil {
			bm := ui.NewBitmap(img)
			viewerMu.Lock()
			bmpPut(k, bm, -1) // -1 tags the placeholder; e.dpi != dpi replaces it
			viewerMu.Unlock()
			return bm
		}
	}
	return nil
}

// viewerInput takes the zoom gestures over the strip: pinches and
// touchpad zoom arrive as InputZoom; on Windows a mouse wheel's notches
// with Ctrl arrive as InputScroll with Ctrl in Mods. Plain scrolling
// returns false so the scroll container scrolls it.
func viewerInput(s *AppState, g geom, ev ui.InputEvent) bool {
	t := s.Active()
	if t == nil || t.Doc == nil {
		return false
	}
	switch {
	case ev.Kind == ui.InputZoom:
		viewerZoom(s, t, g, float64(ev.Scale), ev.Y)
		return true
	case ev.Kind == ui.InputScroll && ev.Mods&ui.Ctrl != 0:
		step := 1 / ZoomStep
		if ev.DY < 0 { // a notch up zooms in
			step = ZoomStep
		}
		viewerZoom(s, t, g, step, ev.Y)
		return true
	}
	return false
}

// viewerZoom multiplies the zoom by factor (switching to ZoomCustom) and
// records what content point sits under viewport Y anchorY, so the next
// build can keep it under the pointer.
func viewerZoom(s *AppState, t *Tab, g geom, factor float64, anchorY float32) {
	oldZ := g.z
	newZ := clampZoom(oldZ * factor)
	if newZ == oldZ {
		return
	}
	page, local := locateY(g.boxes, viewerScroll.Y+anchorY)
	t.SetZoom(newZ) // clamps and switches to ZoomCustom
	if page >= 0 {
		viewerStateFor(t.ID).anchor = &zoomAnchor{
			page: page, local: local, anchorY: anchorY, ratio: float32(newZ / oldZ),
		}
	}
}

// computeGeom lays the strip out on paper: each page's DIP size at zoom z
// and its top offset. tops start at pagePadY and grow by h + pageGap —
// the same numbers the built strip reproduces with its spacer, gap and
// page boxes — so paper offsets and scroll offsets are interchangeable.
func computeGeom(t *Tab, z float64) geom {
	g := geom{z: z, boxes: make([]pageBox, 0, t.NumPages)}
	top := pagePadY
	for i := 0; i < t.NumPages; i++ {
		w, h := pageDipSize(t, i, z)
		g.boxes = append(g.boxes, pageBox{top: top, w: w, h: h})
		top += h + pageGap
	}
	if len(g.boxes) > 0 {
		top += pagePadY - pageGap
	}
	g.contentH = top
	return g
}

// pageDipScale is the exact pt→DIP scale page is DRAWN at: dipPerPt*z
// with the maxPageW reading-column cap the fit modes apply (FitZoom's z
// is uncapped, so plain dipPerPt*z maps content oversized on windows
// wider than maxPageW — the misaligned-highlights bug). ONE source of
// truth: every pt→DIP mapping of page content (highlights, selections)
// and the shown zoom percent go through here.
func pageDipScale(t *Tab, page int, z float64) float64 {
	pw, _ := t.PageSizePt(page)
	if t.ZoomMode != ZoomCustom && pw*dipPerPt*z > float64(maxPageW) {
		return float64(maxPageW) / pw
	}
	return dipPerPt * z
}

// pageDipSize is page's shown size in DIPs at zoom z: points scaled by
// 96/72 * z, swapped for 90/270 rotation (Tab.PageSizePt does that). The
// maxPageW cap (spec §4's 900px reading column) applies to the automatic
// fit modes only — an explicit zoom shows the page at exactly the asked
// size (capping there froze all growth past ~110% on a letter page).
func pageDipSize(t *Tab, page int, z float64) (w, h float32) {
	pw, ph := t.PageSizePt(page)
	k := pageDipScale(t, page, z)
	return float32(pw * k), float32(ph * k)
}

// centerOut returns lo..hi ordered outward from center: center itself,
// then center+1, center-1, … so the current page's render request goes to
// the worker before its neighbors'.
func centerOut(lo, hi, center int) []int {
	n := hi - lo + 1
	if n <= 0 {
		return nil
	}
	out := make([]int, 0, n)
	seen := make([]bool, n)
	add := func(i int) {
		if i >= lo && i <= hi && !seen[i-lo] {
			seen[i-lo] = true
			out = append(out, i)
		}
	}
	add(center)
	for off := 1; len(out) < n; off++ {
		add(center + off)
		add(center - off)
	}
	return out
}

// visibleRange returns the pages [lo, hi] to build: those overlapping the
// viewport, plus one page of slack on each side (spec §3 window).
func visibleRange(g geom, y, vh float32) (lo, hi int) {
	n := len(g.boxes)
	if n == 0 {
		return 0, -1
	}
	top, bot := y-pagePadY, y+vh+pagePadY
	lo, hi = n-1, 0
	for i, b := range g.boxes {
		if b.top+b.h >= top && b.top <= bot {
			lo = min(lo, i)
			hi = max(hi, i)
		}
	}
	return max(0, lo-1), min(n-1, hi+1)
}

// mostVisible returns the page covering the viewport center, or the one
// overlapping the viewport most.
func mostVisible(g geom, y, vh float32) int {
	if len(g.boxes) == 0 {
		return 0
	}
	center := y + vh/2
	for i, b := range g.boxes {
		if center >= b.top && center < b.top+b.h {
			return i
		}
	}
	best, bestOv := 0, float32(0)
	for i, b := range g.boxes {
		ov := min(b.top+b.h, y+vh) - max(b.top, y)
		if ov > bestOv {
			best, bestOv = i, ov
		}
	}
	return best
}

// locateY finds which page the content point y is on and how far into it.
func locateY(boxes []pageBox, y float32) (page int, local float32) {
	for i, b := range boxes {
		if y < b.top+b.h {
			return i, max(0, y-b.top)
		}
	}
	if n := len(boxes); n > 0 {
		return n - 1, boxes[n-1].h
	}
	return -1, 0
}

// dpiFor is the render resolution whose pixels match the shown size: a
// page is pt * 96/72 * z DIPs = pt * 96/72 * z * deviceScale pixels, and
// pixels = pt/72 * DPI, so DPI = 96 * z * deviceScale.
func dpiFor(z float64) int {
	dpi := int(math.Round(96 * z * float64(max(deviceScale, 0.25))))
	return clampInt(dpi, pdf.MinDPI, pdf.MaxDPI)
}

// armZoomDebounce (re)starts the tab's 150ms re-render timer, coalescing
// bursts of zoom steps into one settled re-render (spec §3). When it
// fires, the next build adopts the current zoom as renderZ and requests
// the settled renders. cur.timer == tm keeps a newer timer's flag safe.
func armZoomDebounce(tabID string, st *tabViewState) {
	if st.timer != nil {
		st.timer.Stop()
	}
	var tm *time.Timer
	tm = time.AfterFunc(zoomDebounce, func() {
		viewerMu.Lock()
		if cur := viewerStates[tabID]; cur != nil && cur.timer == tm {
			cur.timer = nil
			cur.settle = true
		}
		viewerMu.Unlock()
		if window != nil {
			window.Invalidate()
		}
	})
	st.timer = tm
}

// viewerStateFor returns the view state of tab id, creating it on first
// use (seenPage -1 makes the next build align with CurPage).
func viewerStateFor(id string) *tabViewState {
	viewerMu.Lock()
	defer viewerMu.Unlock()
	st := viewerStates[id]
	if st == nil {
		st = &tabViewState{seenPage: -1}
		viewerStates[id] = st
	}
	return st
}

// pruneViewerState drops per-tab viewer state when tabs close. Called
// from the build; the tab-count check keeps it cheap.
func pruneViewerState(s *AppState) {
	if len(s.Tabs) == lastTabCount {
		return
	}
	lastTabCount = len(s.Tabs)
	live := make(map[string]bool, len(s.Tabs))
	for _, t := range s.Tabs {
		live[t.ID] = true
	}
	viewerMu.Lock()
	defer viewerMu.Unlock()
	for id, st := range viewerStates {
		if !live[id] {
			if st.timer != nil {
				st.timer.Stop()
			}
			delete(viewerStates, id)
			delete(viewerGen, id)
		}
	}
	for k := range bmpCache {
		if !live[k.tabID] {
			bmpDelete(k)
		}
	}
	for k := range inflight {
		if !live[k.tabID] {
			delete(inflight, k)
		}
	}
}

// genFor reads a tab's mirrored ReloadGen; -1 when unknown, which never
// matches a request's generation, so results for unknown tabs are dropped.
func genFor(tabID string) int {
	viewerMu.Lock()
	defer viewerMu.Unlock()
	if g, ok := viewerGen[tabID]; ok {
		return g
	}
	return -1
}

// bmpPut stores bm under k at the given render DPI, evicting the oldest
// entries past bitmapCap. The caller holds viewerMu.
func bmpPut(k bmpKey, bm *ui.Bitmap, dpi int) {
	if _, ok := bmpCache[k]; !ok {
		bmpOrder = append(bmpOrder, k)
	}
	bmpCache[k] = &bmpEntry{bm: bm, dpi: dpi}
	for len(bmpCache) > bitmapCap {
		bmpDelete(bmpOrder[0])
	}
}

// bmpDelete drops k from the cache and the eviction order. The caller
// holds viewerMu.
func bmpDelete(k bmpKey) {
	if _, ok := bmpCache[k]; !ok {
		return
	}
	delete(bmpCache, k)
	if i := slices.Index(bmpOrder, k); i >= 0 {
		bmpOrder = slices.Delete(bmpOrder, i, i+1)
	}
}

// bmpClearTab drops all bitmaps of one tab. The caller holds viewerMu.
func bmpClearTab(tabID string) {
	for k := range bmpCache {
		if k.tabID == tabID {
			bmpDelete(k)
		}
	}
}

// inflightClearTab forgets a tab's queued renders (after a reload). The
// caller holds viewerMu.
func inflightClearTab(tabID string) {
	for k := range inflight {
		if k.tabID == tabID {
			delete(inflight, k)
		}
	}
}

// hitRect is one search-highlight rectangle, in DIPs from the page's top
// left corner.
type hitRect struct {
	rect    [4]float32
	current bool
}

// hitRects maps the search hits on page into the page's DIP space at
// zoom z, marking the current hit. The scale is the one the page is
// DRAWN at — pageDipScale, cap included — or the rects drift off their
// glyphs in the capped fit modes.
func hitRects(t *Tab, page int, z float64) []hitRect {
	if len(t.Search.Hits) == 0 {
		return nil
	}
	var out []hitRect
	k := pageDipScale(t, page, z)
	for hi, h := range t.Search.Hits {
		if h.Page != page {
			continue
		}
		cur := hi == t.Search.Current
		if len(h.Rects) > 0 {
			// Exact geometry from MuPDF's own search — the estimated
			// spans below are the fallback for text-only hits.
			for _, r := range h.Rects {
				out = append(out, hitRect{rect: quadDipRect(t, page, r, k), current: cur})
			}
			continue
		}
		for _, sp := range h.Spans {
			out = append(out, hitRect{rect: spanDipRect(t, page, sp, k), current: cur})
		}
	}
	return out
}

// quadDipRect maps an exact hit rectangle (points on the unrotated page,
// top-left origin) through the view rotation, as spanDipRect does.
func quadDipRect(t *Tab, page int, r pdf.HitRect, k float64) [4]float32 {
	w, h := r.X1-r.X0, r.Y1-r.Y0
	return spanDipRect(t, page, pdf.TextSpan{X: r.X0, Y: r.Y0, W: w, H: h}, k)
}

// spanDipRect maps a text span (points on the unrotated page, origin top
// left, y down) to DIPs on the rotated view. The render is rotated
// clockwise by Rotate, so the overlay follows the same turns:
//
//	90°:  (x, y) -> (h0 - y, x)    180°: (x, y) -> (w0 - x, h0 - y)
//	270°: (x, y) -> (y, w0 - x)
//
// where w0, h0 are the unrotated page size in points.
func spanDipRect(t *Tab, page int, sp pdf.TextSpan, k float64) [4]float32 {
	w0, h0 := t.Doc.PageSize(page)
	// Page-space coordinates (spans, search quads) are reported against
	// the box origin; the view is 0,0-anchored, so subtract it — a
	// CropBox not starting at 0,0 shifts every highlight otherwise.
	ox, oy := t.Doc.PageOrigin(page)
	sp.X, sp.Y = sp.X-ox, sp.Y-oy
	var x, y, w, h float64
	switch t.Rotate {
	case 90:
		x, y = (h0-sp.Y-sp.H)*k, sp.X*k
		w, h = sp.H*k, sp.W*k
	case 180:
		x, y = (w0-sp.X-sp.W)*k, (h0-sp.Y-sp.H)*k
		w, h = sp.W*k, sp.H*k
	case 270:
		x, y = sp.Y*k, (w0-sp.X-sp.W)*k
		w, h = sp.H*k, sp.W*k
	default:
		x, y = sp.X*k, sp.Y*k
		w, h = sp.W*k, sp.H*k
	}
	return [4]float32{float32(x), float32(y), float32(max(w, 0)), float32(max(h, 0))}
}
