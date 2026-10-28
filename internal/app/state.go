// Package app holds the reader's state and its MyGo native-UI views.
//
// # Threading rules (see also the mygo-maintenance skill)
//
//   - AppState and everything reachable from it is mutated on the UI thread
//     only: from the view (event queries) or from inside Window.Update.
//   - pdf.Doc callbacks arrive on a worker goroutine. They must only store
//     results into AppState from inside Window.Update, after re-checking
//     that the tab still exists and is still the intended target.
//   - Never store ui.Element values in AppState: elements expire between
//     build passes. Store plain data (and ui.Handle values in view files).
package app

import (
	"fmt"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync/atomic"

	"gopdf/internal/pdf"
)

// ZoomMode selects how Tab.Zoom is interpreted.
type ZoomMode int

const (
	ZoomFitWidth ZoomMode = iota // zoom so the page fills the viewport width
	ZoomFitPage                  // zoom so the whole page is visible
	ZoomCustom                   // use Tab.Zoom literally
)

// SidebarTab selects the sidebar panel.
type SidebarTab int

const (
	SidebarThumbnails SidebarTab = iota
	SidebarOutline
)

// Zoom limits per spec §5 (25%–400%) and the multiplier per zoom step.
const (
	MinZoom  = 0.25
	MaxZoom  = 4.0
	ZoomStep = 1.2
)

// SidebarWidth is the sidebar width in DIPs (spec §4: 280px).
const SidebarWidth = 280.0

// SearchState is the in-document search state of one tab.
type SearchState struct {
	Query   string
	Running bool // index build or search in flight
	Hits    []pdf.SearchHit
	Current int // index into Hits; -1 when no hit selected
}

// HistoryEntry is one back/forward navigation stop.
type HistoryEntry struct {
	Page    int
	OffsetY float64 // viewer scroll offset when the entry was recorded
}

// Tab is one open document in a tab (spec §6).
type Tab struct {
	ID       string
	Path     string
	Title    string
	Doc      *pdf.Doc
	NumPages int
	CurPage  int
	Zoom     float64 // last computed zoom factor; the source of truth for ZoomCustom
	ZoomMode ZoomMode
	Rotate   int // 0 | 90 | 180 | 270

	History []HistoryEntry // back stack (oldest first)
	Future  []HistoryEntry // forward stack

	Search  SearchState
	Outline []pdf.OutlineNode

	// Cache holds rendered page images; thread-safe (see pdf.PageCache).
	Cache *pdf.PageCache

	// ReloadGen increments on Reload; the viewer re-renders when it changes.
	ReloadGen int

	index *pdf.Index // set on the UI thread by SetIndex after IndexAsync
}

// SetIndex stores the built text index (UI thread only).
func (t *Tab) SetIndex(ix *pdf.Index) { t.index = ix }

// Index returns the built text index, or nil while unbuilt.
func (t *Tab) Index() *pdf.Index { return t.index }

// PageSizePt returns the rotated page size in points.
func (t *Tab) PageSizePt(page int) (w, h float64) {
	w, h = t.Doc.PageSize(page)
	if t.Rotate == 90 || t.Rotate == 270 {
		w, h = h, w
	}
	return w, h
}

// AppState is the whole reader UI state (spec §6).
type AppState struct {
	Tabs        []*Tab
	ActiveTab   int // index into Tabs; -1 when no tabs
	SidebarTab  SidebarTab
	ShowSidebar bool
	ShowPalette bool // Ctrl+K command palette overlay

	// ShowSearchPanel toggles the right-side in-document search results
	// panel (Ctrl+F, the toolbar's search icon); searchpanel.go builds it.
	ShowSearchPanel bool

	// ShowProperties toggles the Document Properties dialog (File ▸
	// Document Properties); docprops.go builds it.
	ShowProperties bool

	ClosedPaths []string // recently closed tab paths, newest first
	Recents     []string // recent document paths, newest first

	Settings Settings
}

// Settings are the persisted user preferences (spec §5 v1.0).
type Settings struct {
	DefaultZoomMode   ZoomMode
	DefaultZoom       float64 // for ZoomCustom
	StartWithSidebar  bool
	DefaultSidebarTab SidebarTab
	FollowSystemTheme bool
}

var tabSeq atomic.Uint64

// NewAppState returns the state with defaults applied.
func NewAppState() *AppState {
	return &AppState{
		ActiveTab:   -1,
		ShowSidebar: true,
		Settings: Settings{
			DefaultZoomMode:   ZoomFitWidth,
			DefaultZoom:       1.0,
			StartWithSidebar:  true,
			FollowSystemTheme: true,
		},
	}
}

// Active returns the active tab, or nil.
func (s *AppState) Active() *Tab {
	if s.ActiveTab < 0 || s.ActiveTab >= len(s.Tabs) {
		return nil
	}
	return s.Tabs[s.ActiveTab]
}

// OpenTab opens path in a new tab and activates it. If the path is already
// open its tab is activated and returned instead. The caller is responsible
// for triggering the first page render and for UI invalidation.
func (s *AppState) OpenTab(path string) (*Tab, error) {
	for _, t := range s.Tabs {
		if strings.EqualFold(t.Path, mustAbs(path)) {
			s.SelectTabBy(t.ID)
			return t, nil
		}
	}
	doc, err := pdf.Open(path)
	if err != nil {
		return nil, err
	}
	// A real document replaces leftover launcher tabs ("+ then opened a
	// file" should not strand New Tab pages, as browsers' new-tab pages).
	live := s.Tabs[:0]
	for _, existing := range s.Tabs {
		if existing.IsEmpty() && existing.ID != "" {
			continue
		}
		live = append(live, existing)
	}
	s.Tabs = live
	title := filepath.Base(doc.Path())
	if e := doc.Meta()["title"]; e != "" {
		title = e
	}
	t := &Tab{
		ID:       fmt.Sprintf("tab-%d", tabSeq.Add(1)),
		Path:     doc.Path(),
		Title:    title,
		Doc:      doc,
		NumPages: doc.NumPages(),
		CurPage:  0,
		Zoom:     1.0,
		ZoomMode: s.Settings.DefaultZoomMode,
		Rotate:   0,
		Outline:  doc.Outline(),
		Cache:    pdf.NewPageCache(256 << 20),
	}
	if s.Settings.DefaultZoomMode == ZoomCustom {
		t.Zoom = clampZoom(s.Settings.DefaultZoom)
	}
	s.Tabs = append(s.Tabs, t)
	s.ActiveTab = len(s.Tabs) - 1
	s.addRecent(t.Path)
	return t, nil
}

// SelectTab activates the tab at index i.
func (s *AppState) SelectTab(i int) {
	if i >= 0 && i < len(s.Tabs) {
		s.ActiveTab = i
	}
}

// SelectTabBy activates the tab with the given id.
func (s *AppState) SelectTabBy(id string) {
	for i, t := range s.Tabs {
		if t.ID == id {
			s.ActiveTab = i
			return
		}
	}
}

// NewEmptyTab appends and activates a launcher tab: no document, the
// viewer shows the drop hint with an Open button (the "+" in the tab bar,
// spec §4 updated). Opening a real document closes leftover empty tabs.
func (s *AppState) NewEmptyTab() *Tab {
	t := &Tab{
		ID:       fmt.Sprintf("tab-%d", tabSeq.Add(1)),
		Title:    "New Tab",
		Zoom:     1.0,
		ZoomMode: s.Settings.DefaultZoomMode,
	}
	s.Tabs = append(s.Tabs, t)
	s.ActiveTab = len(s.Tabs) - 1
	return t
}

// IsEmpty reports a launcher tab: no document behind it.
func (t *Tab) IsEmpty() bool { return t != nil && t.Doc == nil }

// CloseTab removes a tab by id, releases its document, and records its path
// for reopen-closed-tab. Returns the tab that should be active afterwards.
func (s *AppState) CloseTab(id string) {
	idx := -1
	for i, t := range s.Tabs {
		if t.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	t := s.Tabs[idx]
	s.Tabs = append(s.Tabs[:idx], s.Tabs[idx+1:]...)
	if t.Doc != nil { // Doc-less tabs (failed open, tests) close without a panic
		t.Doc.Close()
	}
	// Drop the tab's per-tab UI caches now (viewer bitmaps, sidebar
	// thumbnails, outline state) — releasing the doc alone leaves those
	// pixel buffers reachable. Lives in components.go so this file stays
	// mygo-free.
	teardownTabState(t.ID)
	s.ClosedPaths = append([]string{t.Path}, s.ClosedPaths...)
	if len(s.ClosedPaths) > 16 {
		s.ClosedPaths = s.ClosedPaths[:16]
	}
	switch {
	case len(s.Tabs) == 0:
		s.ActiveTab = -1
		// Last tab closed: the caches above (and the doc's renders) freed
		// a lot at once. Nudge the runtime to hand it to the OS in the
		// background instead of sitting on it (stdlib only).
		go debug.FreeOSMemory()
	case idx < s.ActiveTab:
		s.ActiveTab--
	case idx == s.ActiveTab:
		if s.ActiveTab >= len(s.Tabs) {
			s.ActiveTab = len(s.Tabs) - 1
		}
	}
}

// CloseActiveTab closes the active tab, if any.
func (s *AppState) CloseActiveTab() {
	if t := s.Active(); t != nil {
		s.CloseTab(t.ID)
	}
}

// ReopenClosedTab reopens the most recently closed tab, if any.
func (s *AppState) ReopenClosedTab() bool {
	if len(s.ClosedPaths) == 0 {
		return false
	}
	path := s.ClosedPaths[0]
	s.ClosedPaths = s.ClosedPaths[1:]
	_, err := s.OpenTab(path)
	return err == nil
}

// ToggleSidebar shows or hides the sidebar.
func (s *AppState) ToggleSidebar() { s.ShowSidebar = !s.ShowSidebar }

// SetSidebarTab switches the sidebar panel and shows the sidebar.
func (s *AppState) SetSidebarTab(tab SidebarTab) {
	s.SidebarTab = tab
	s.ShowSidebar = true
}

// addRecent records an opened path, most recent first, deduplicated.
func (s *AppState) addRecent(path string) {
	out := make([]string, 0, len(s.Recents)+1)
	out = append(out, path)
	for _, p := range s.Recents {
		if !strings.EqualFold(p, path) {
			out = append(out, p)
		}
	}
	if len(out) > 16 {
		out = out[:16]
	}
	s.Recents = out
}

// Navigate moves the tab to page (clamped) recording history from offsetY.
func (t *Tab) Navigate(page int, offsetY float64) {
	page = clampInt(page, 0, t.NumPages-1)
	if page == t.CurPage {
		return
	}
	t.pushHistory(HistoryEntry{Page: t.CurPage, OffsetY: offsetY})
	t.CurPage = page
}

// pushHistory records where we were and clears the forward stack.
func (t *Tab) pushHistory(e HistoryEntry) {
	if n := len(t.History); n > 0 && t.History[n-1].Page == e.Page {
		t.History[n-1] = e // update offset only
		return
	}
	t.History = append(t.History, e)
	if len(t.History) > 100 {
		t.History = t.History[len(t.History)-100:]
	}
	t.Future = nil
}

// Back pops the back stack; returns the entry and whether one existed.
func (t *Tab) Back() (HistoryEntry, bool) {
	if len(t.History) == 0 {
		return HistoryEntry{}, false
	}
	n := len(t.History)
	e := t.History[n-1]
	t.History = t.History[:n-1]
	t.Future = append(t.Future, HistoryEntry{Page: t.CurPage})
	t.CurPage = clampInt(e.Page, 0, t.NumPages-1)
	return e, true
}

// Forward pops the forward stack; returns the entry and whether one existed.
func (t *Tab) Forward() (HistoryEntry, bool) {
	if len(t.Future) == 0 {
		return HistoryEntry{}, false
	}
	n := len(t.Future)
	e := t.Future[n-1]
	t.Future = t.Future[:n-1]
	t.History = append(t.History, HistoryEntry{Page: t.CurPage})
	t.CurPage = clampInt(e.Page, 0, t.NumPages-1)
	return e, true
}

// SetZoom sets a custom zoom factor, clamped to [MinZoom, MaxZoom].
func (t *Tab) SetZoom(z float64) {
	t.ZoomMode = ZoomCustom
	t.Zoom = clampZoom(z)
}

// SetZoomMode switches fit mode.
func (t *Tab) SetZoomMode(m ZoomMode) { t.ZoomMode = m }

// ZoomIn and ZoomOut step the zoom by ZoomStep (switching to custom).
func (t *Tab) ZoomIn()  { t.SetZoom(t.Zoom * ZoomStep) }
func (t *Tab) ZoomOut() { t.SetZoom(t.Zoom / ZoomStep) }

// ZoomPercent returns the zoom for display, e.g. "125%".
// ZoomPercent is the zoom the current page is actually shown at: in the
// fit modes on a wide window the reading-column cap (pageDipScale)
// shrinks the page below the uncapped fit zoom, and the number on screen
// is the number the user sees.
func (t *Tab) ZoomPercent() string {
	z := t.Zoom
	if t.Doc != nil {
		z = pageDipScale(t, t.CurPage, t.Zoom) / dipPerPt
	}
	return fmt.Sprintf("%.0f%%", z*100)
}

// RotateBy rotates the view 90° clockwise.
func (t *Tab) RotateBy() {
	t.Rotate = (t.Rotate + 90) % 360
}

// SetCurrentPage updates the current page WITHOUT recording history — the
// viewer calls this as scrolling reveals new pages. Jumps (outline,
// thumbnails, page input, palette, search) use Navigate instead.
func (t *Tab) SetCurrentPage(p int) {
	t.CurPage = clampInt(p, 0, t.NumPages-1)
}

// Reload invalidates the tab's rendered images; the viewer re-renders the
// visible pages when ReloadGen changes.
func (t *Tab) Reload() {
	if t.Cache != nil {
		t.Cache.Clear()
	}
	t.ReloadGen++
}

// RunSearch executes the tab's current query. The text index is a fast
// PREFILTER of candidate pages; MuPDF's own fz_search then enumerates the
// hits — each quad IS a hit, so the highlight geometry cannot drift from
// the hit list the way two parallel matchers could (hyphenation,
// ligatures and case folding differ between them). Quads for every
// candidate page arrive on the doc worker — callbacks serialize there,
// so the accumulator needs no lock — and the finished hit list lands on
// the UI thread in one update. Call from the UI thread after StartSearch.
func (s *AppState) RunSearch(t *Tab) {
	if t == nil || t.Doc == nil {
		if t != nil {
			t.Search.Running = false // nothing to search; don't spin forever
		}
		return
	}
	q := t.Search.Query
	t.Search.Running = q != ""
	if q == "" {
		t.Search.Hits = nil
		t.Search.Current = -1
		return
	}
	t.Doc.IndexAsync(func(ix *pdf.Index, err error) {
		finish := func(hits []pdf.SearchHit) {
			updateFromWorker(func() { // worker-side: hop, never inline
				cur := s.Active()
				if cur == nil || cur.ID != t.ID || cur.Search.Query != q {
					return // stale: tab closed or query changed
				}
				cur.SetIndex(ix)
				cur.Search.Hits = hits
				cur.Search.Running = false
				cur.Search.Current = -1
				if page, _, ok := cur.GotoHit(1); ok { // land on the first hit
					cur.Navigate(page, 0)
				}
			})
		}
		if err != nil {
			finish(nil)
			return
		}

		// Candidate pages from the extracted text, deduplicated in order.
		var pages []int
		seen := map[int]bool{}
		for _, h := range ix.Search(q) {
			if !seen[h.Page] {
				seen[h.Page] = true
				pages = append(pages, h.Page)
			}
		}
		if len(pages) == 0 {
			finish(nil)
			return
		}

		// Exact hits per candidate page, accumulated on the worker.
		quads := map[int][]pdf.HitRect{}
		pending := len(pages)
		for _, p := range pages {
			t.Doc.SearchQuadsAsync(p, q, func(rects []pdf.HitRect, qerr error) {
				if qerr == nil && len(rects) > 0 {
					quads[p] = rects
				}
				pending--
				if pending > 0 {
					return
				}
				// Last page in: assemble one hit per quad, in reading
				// order, with the covering spans for snippets.
				var hits []pdf.SearchHit
				for _, page := range pages {
					for _, r := range quads[page] {
						hits = append(hits, pdf.SearchHit{
							Page:  page,
							Rects: []pdf.HitRect{r},
							Spans: spansOverlapping(ix, page, r),
						})
					}
				}
				finish(hits)
			})
		}
	})
}

// spansOverlapping returns the page's text spans whose rectangles
// intersect r (both in absolute page space) — the snippet source for a hit.
func spansOverlapping(ix *pdf.Index, page int, r pdf.HitRect) []pdf.TextSpan {
	var out []pdf.TextSpan
	for _, sp := range ix.PageSpans(page) {
		if sp.X < r.X1 && sp.X+sp.W > r.X0 && sp.Y < r.Y1 && sp.Y+sp.H > r.Y0 {
			out = append(out, sp)
		}
	}
	return out
}

// StepSearch moves to the next (+1) or previous (-1) hit and navigates the
// tab to it. Call from the UI thread.
func (s *AppState) StepSearch(t *Tab, delta int) {
	if t == nil {
		return
	}
	if page, _, ok := t.GotoHit(delta); ok {
		t.Navigate(page, 0)
	}
}

// FitZoom computes the zoom factor for the current page and fit mode,
// given the viewport size in DIPs (width, height) available for pages.
// Returns 1.0 for custom mode.
func (t *Tab) FitZoom(viewW, viewH float64) float64 {
	if t.ZoomMode == ZoomCustom || t.Doc == nil {
		return clampZoom(t.Zoom)
	}
	pw, ph := t.PageSizePt(t.CurPage)
	if pw <= 0 || ph <= 0 || viewW <= 0 || viewH <= 0 {
		return clampZoom(t.Zoom)
	}
	// DIPs per point: a page shown at 100% occupies 96/72 DIPs per point.
	const dipPerPt = 96.0 / 72.0
	z := 1.0
	switch t.ZoomMode {
	case ZoomFitPage:
		z = min(viewW/(pw*dipPerPt), viewH/(ph*dipPerPt))
	case ZoomFitWidth:
		z = viewW / (pw * dipPerPt)
	}
	t.Zoom = clampZoom(z)
	return t.Zoom
}

// StartSearch resets search state for a new query (hits cleared, current
// -1). The caller kicks the index build and the actual search.
func (t *Tab) StartSearch(q string) {
	t.Search.Query = q
	t.Search.Running = q != ""
	t.Search.Current = -1
	t.Search.Hits = nil
}

// GotoHit moves the current hit by delta (±1) wrapping around the results
// and returns the hit's page and whether to navigate. ok=false when there
// are no hits.
func (t *Tab) GotoHit(delta int) (page int, hit pdf.SearchHit, ok bool) {
	n := len(t.Search.Hits)
	if n == 0 {
		return 0, pdf.SearchHit{}, false
	}
	if t.Search.Current < 0 {
		if delta < 0 {
			t.Search.Current = n - 1
		} else {
			t.Search.Current = 0
		}
	} else {
		t.Search.Current = ((t.Search.Current+delta)%n + n) % n
	}
	h := t.Search.Hits[t.Search.Current]
	return h.Page, h, true
}

func clampZoom(z float64) float64 { return max(MinZoom, min(MaxZoom, z)) }

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func mustAbs(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}
