// sidebar.go renders the left panel content (spec §4: "Left sidebar 280px
// collapsible: Toggle between Thumbnails / Outline"). view.go decides
// whether the sidebar shows at all; this file always builds the panel for
// the active tab. One of the four chrome modules (Agent C).
//
// API notes (verified against mygo v0.6.4 source):
//   - ui.List(c, state, n) + .ItemKey + .Rows is the virtualized list: row
//     callbacks run only for visible rows, which is what makes the lazy
//     thumbnail requests cheap. The ListState must persist per tab (scroll
//     position), so it lives in a package map keyed by tab ID.
//   - ui.Tree + ui.TreeItem(label, open *bool, children) is the outline
//     tree; OnClick on the item reports choosing it after the build pass.
//     Open/closed state lives in a package map keyed by tab and node path.
//   - ui.NewBitmap(img) converts an *image.RGBA once; Bitmap.Update swaps
//     pixels in place on the UI thread (both called here only from the
//     view, never from the render worker).
//   - RenderAsync callbacks run on the pdf.Doc worker goroutine: they only
//     touch the thread-safe pdf.PageCache and hop to the UI thread via
//     update, re-checking tab liveness there (state.go threading rules).
package app

import (
	"fmt"
	"image"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"gopdf/internal/pdf"
)

// Thumbnail panel geometry (DIPs).
const (
	thumbRowHeight = 148
	thumbHeight    = 120
	thumbMaxWidth  = 168
)

// Per-tab chrome state, kept out of AppState (which stores plain data only:
// state.go forbids ui values there). Every map is touched on the UI thread
// only (view builds) and pruned lazily for closed tabs.
var (
	thumbLists      = map[string]*ui.ListState{}  // tab → thumbnail list scroll state
	thumbGens       = map[string]int{}            // tab → ReloadGen the requested set was built for
	thumbsRequested = map[string]map[int]bool{}   // tab → pages with a render in flight
	thumbLastPage   = map[string]int{}            // tab → last CurPage scrolled into view
	thumbBitmaps    = map[thumbKey]*thumbBitmap{} // (tab, page) → ui bitmap
	thumbOrder      []thumbKey                    // bitmap insertion order, for the cap
	outlineOpen     = map[string]*bool{}          // "tabID|path" → expanded
)

type thumbKey struct {
	tabID string
	page  int
}

// thumbBitmap pairs a ui.Bitmap with the image it was built from, so a
// re-render can Update it in place instead of allocating a new Bitmap
// ("Create it once: converting an image is not free").
type thumbBitmap struct {
	bmp *ui.Bitmap
	img *image.RGBA
}

// sidebar renders the panel: a Thumbnails/Outline switch, then the panel
// of the active tab. Doc-less tabs show placeholders and never panic.
func sidebar(c *ui.Context, s *AppState) {
	th := c.Theme()
	pruneChromeState(s)
	ui.Column(c).Width(SidebarWidth).FillHeight().Gap(th.Space(1)).Padding(th.Space(1), 0, 0, 0).Children(func() {
		sidebarSwitch(c, s)
		t := s.Active()
		if t == nil {
			ui.Text(c, "No document open").TextColor(th.TextMuted).FontSize(13)
			return
		}
		if s.SidebarTab == SidebarOutline {
			outlinePanel(c, t)
			return
		}
		thumbsPanel(c, s, t)
	})
}

// sidebarSwitch is the Thumbnails/Outline choice as two equal-width
// segments. ui.Segmented sizes its segments to their labels (a growing gap
// trails the last one), so it is rebuilt here from two buttons that each
// Grow(1) inside a shared track — same look (surface, border, radius,
// active tint), but splitting the panel width evenly between the two.
// The buttons MUST be built inside track.Children: constructors nest into
// their parent only inside that callback, otherwise they land in the
// enclosing column (stacking one per row).
func sidebarSwitch(c *ui.Context, s *AppState) {
	th := c.Theme()
	track := ui.Row(c).AlignItems(ui.Stretch).Gap(th.Space(0.5)).
		Padding(th.Space(0.5)).Radius(th.Radius).Background(th.Surface).
		Border(1, th.Border).FillWidth()
	track.Label("Sidebar panel")
	track.Children(func() {
		for i, label := range [...]string{"Thumbnails", "Outline"} {
			seg := ui.Button(c, label).Grow(1)
			if int(s.SidebarTab) == i {
				seg.Background(th.Accent.Alpha(0.15)).TextColor(th.Accent)
			}
			seg.OnClick(func() { s.SetSidebarTab(SidebarTab(i)) })
		}
	})
}

// ---- Thumbnails ---------------------------------------------------------

// thumbsPanel is the virtualized thumbnail list of pages 0..NumPages-1.
func thumbsPanel(c *ui.Context, s *AppState, t *Tab) {
	if t.NumPages <= 0 {
		ui.Text(c, "No pages").FontSize(13)
		return
	}
	lst := thumbLists[t.ID]
	if lst == nil {
		lst = &ui.ListState{}
		thumbLists[t.ID] = lst
	}
	// Follow the viewer's current page: scroll there once per change.
	if thumbLastPage[t.ID] != t.CurPage {
		thumbLastPage[t.ID] = t.CurPage
		lst.ScrollIntoView(t.CurPage)
	}
	ui.List(c, lst, t.NumPages).
		ItemKey(func(i int) any { return i }). // row identity = page number
		Fill().
		Rows(func(r ui.ListRow) { thumbRow(c, s, t, r.Index) })
}

// thumbRow is one page: a small page surface (aspect from PageSizePt) with
// its page number, the current page ringed in the accent color. The image
// comes from Tab.Cache; a miss kicks off one 72 DPI render — only for rows
// the list actually builds, i.e. visible ones.
func thumbRow(c *ui.Context, s *AppState, t *Tab, p int) {
	th := c.Theme()
	key := pdf.CacheKey{Page: p, DPI: pdf.ThumbDPI, Rotate: 0}
	var img *image.RGBA
	if t.Cache != nil {
		img = t.Cache.Get(key)
	}
	if img == nil {
		requestThumb(s, t, p, key)
	}

	w, h := thumbSize(t, p)
	row := ui.ButtonBase(c).
		Height(thumbRowHeight).Grow(1).
		Gap(th.Space(1)).AlignItems(ui.Center).
		Cursor(ui.CursorPointer)
	row.Label(fmt.Sprintf("Page %d", p+1))
	if p == t.CurPage {
		row.Border(2, th.Accent).Radius(th.Space(1))
	}
	row.Children(func() {
		// The page surface always carries a border: the thumbnail itself is
		// white, and without a border it merges with the light-mode panel
		// background (the placeholder-only border was the bug).
		surface := ui.Box(c).Size(w, h).Radius(th.Space(0.5)).
			Background(th.Surface).Border(1, th.Border)
		if img != nil {
			surface.Children(func() {
				ui.Image(c, thumbBitmapFor(t, p, img)).Size(w, h).
					Fit(ui.FillBox).Radius(th.Space(0.5))
			})
		}
		num := ui.Textf(c, "%d", p+1).FontSize(11)
		if p != t.CurPage {
			num.TextColor(th.TextMuted)
		}
	})
	row.OnClick(func() { t.Navigate(p, 0) })
}

// thumbSize returns the on-screen size of page p from its rotated page
// size, capped to the panel. Without a document it assumes US Letter.
func thumbSize(t *Tab, p int) (w, h float32) {
	pw, ph := 612.0, 792.0
	if t.Doc != nil {
		pw, ph = t.PageSizePt(p)
	}
	if pw <= 0 || ph <= 0 {
		pw, ph = 612, 792
	}
	h = thumbHeight
	w = float32(pw / ph * float64(h))
	if w > thumbMaxWidth {
		w = thumbMaxWidth
		h = w * float32(ph/pw)
	}
	return w, h
}

// thumbMu guards thumbsRequested and thumbGens: builds mark pages in
// flight on the UI thread and the worker completion clears the mark, so
// the two can overlap.
var thumbMu sync.Mutex

// requestThumb asks the doc worker for one thumbnail, at most once per
// (tab, page) IN FLIGHT. The mark clears when the render completes — not
// permanently — because the shared PageCache is an LRU: search jumps fill
// it with full-page renders, evicted thumbnails must be re-requestable or
// they stay blank forever.
func requestThumb(s *AppState, t *Tab, p int, key pdf.CacheKey) {
	if t.Doc == nil || t.Cache == nil {
		return
	}
	thumbMu.Lock()
	if thumbGens[t.ID] != t.ReloadGen {
		thumbsRequested[t.ID] = map[int]bool{} // Reload cleared the cache
		thumbGens[t.ID] = t.ReloadGen
	}
	req := thumbsRequested[t.ID]
	if req == nil {
		req = map[int]bool{}
		thumbsRequested[t.ID] = req
	}
	if req[p] {
		thumbMu.Unlock()
		return // already in flight
	}
	req[p] = true
	thumbMu.Unlock()

	doc, cache := t.Doc, t.Cache
	doc.RenderAsync(pdf.RenderReq{Page: p, DPI: pdf.ThumbDPI, Rotate: 0, Kind: pdf.KindThumb},
		func(img *image.RGBA, err error) {
			thumbMu.Lock()
			if m := thumbsRequested[t.ID]; m != nil {
				delete(m, p) // done: may be requested again after an eviction
			}
			thumbMu.Unlock()
			if err != nil || img == nil {
				return
			}
			// Worker goroutine: touch only thread-safe things. The cache
			// store plus a redraw request is the whole job — no AppState
			// reads here (update() runs inline in tests, which would race
			// the UI thread; the next build finds the cached image, and in
			// production Invalidate is the documented goroutine-safe redraw
			// ask — the same pattern the viewer's completions use).
			cache.Put(key, img)
			if window != nil {
				window.Invalidate()
			}
		})
}

// thumbBitmapFor returns the ui.Bitmap of a rendered thumbnail, creating
// or updating it on the UI thread. The cache is capped at ~200 entries;
// full-resolution images live in pdf.PageCache, not here.
func thumbBitmapFor(t *Tab, p int, img *image.RGBA) *ui.Bitmap {
	k := thumbKey{t.ID, p}
	if e := thumbBitmaps[k]; e != nil {
		if e.img != img { // re-rendered (reload, …): swap the pixels in place
			e.bmp.Update(img)
			e.img = img
		}
		return e.bmp
	}
	e := &thumbBitmap{bmp: ui.NewBitmap(img), img: img}
	thumbBitmaps[k] = e
	thumbOrder = append(thumbOrder, k)
	for len(thumbOrder) > 200 {
		delete(thumbBitmaps, thumbOrder[0])
		thumbOrder = thumbOrder[1:]
	}
	return e.bmp
}

// ---- Outline ------------------------------------------------------------

// outlinePanel renders t.Outline as a tree; -1 pages are muted and dead.
// The section the reader is currently in is highlighted.
func outlinePanel(c *ui.Context, t *Tab) {
	if len(t.Outline) == 0 {
		ui.Text(c, "No outline").TextColor(c.Theme().TextMuted).FontSize(13)
		return
	}
	current := currentOutlinePath(t)
	ui.Scroll(c).Fill().Children(func() {
		ui.Tree(c, func() { walkOutline(c, t, t.Outline, "o", current) })
	})
}

// currentOutlinePath returns the path of the outline entry the reader is
// currently in: the last entry in document order whose target page is at or
// before CurPage. Entries with no page target (-1) never become current.
func currentOutlinePath(t *Tab) string {
	current := ""
	var walk func(nodes []pdf.OutlineNode, path string)
	walk = func(nodes []pdf.OutlineNode, path string) {
		for i, n := range nodes {
			p := fmt.Sprintf("%s/%d", path, i)
			if n.Page >= 0 && n.Page <= t.CurPage {
				current = p
			}
			walk(n.Children, p)
		}
	}
	walk(t.Outline, "o")
	return current
}

// walkOutline builds one level of the tree. path identifies each node
// stably across builds for its open/closed state; the node matching
// current (the reader's section) is highlighted in the accent color.
func walkOutline(c *ui.Context, t *Tab, nodes []pdf.OutlineNode, path, current string) {
	th := c.Theme()
	for i, n := range nodes {
		p := fmt.Sprintf("%s/%d", path, i)
		label := n.Title
		if label == "" {
			label = "(untitled)"
		}
		var open *bool
		if len(n.Children) > 0 {
			key := t.ID + "|" + p
			open = outlineOpen[key]
			if open == nil {
				open = new(bool)
				outlineOpen[key] = open
			}
		}
		it := ui.TreeItem(c.Key(p), label, open, func() {
			walkOutline(c, t, n.Children, p, current)
		})
		switch {
		case p == current: // where the reader is: accent, tinted row
			it.TextColor(th.Accent).Background(th.Accent.Alpha(0.10))
		case n.Page < 0: // no target: grayed, not navigable
			it.TextColor(th.TextMuted)
			continue
		}
		page := n.Page
		it.OnClick(func() { t.Navigate(page, 0) })
	}
}

// ---- bookkeeping --------------------------------------------------------

// pruneChromeState drops per-tab chrome state of closed tabs, lazily (the
// maps are small; the sweep runs only once they grow).
func pruneChromeState(s *AppState) {
	if len(thumbBitmaps) > 400 {
		live := tabIDSet(s)
		for k := range thumbBitmaps {
			if !live[k.tabID] {
				delete(thumbBitmaps, k)
			}
		}
		for i := 0; i < len(thumbOrder); {
			if live[thumbOrder[i].tabID] {
				i++
				continue
			}
			thumbOrder = append(thumbOrder[:i], thumbOrder[i+1:]...)
		}
	}
	if len(thumbLists) > 64 {
		pruneByTabID(thumbLists, s)
		thumbMu.Lock()
		pruneByTabID(thumbsRequested, s)
		pruneByTabID(thumbGens, s)
		thumbMu.Unlock()
		pruneByTabID(thumbLastPage, s)
		pruneByTabScoped(outlineOpen, s)
	}
}

// tabIDSet returns the ids of the open tabs.
func tabIDSet(s *AppState) map[string]bool {
	out := make(map[string]bool, len(s.Tabs))
	for _, t := range s.Tabs {
		out[t.ID] = true
	}
	return out
}

// pruneByTabID deletes map entries keyed by a bare tab ID of closed tabs.
func pruneByTabID[T any](m map[string]T, s *AppState) {
	live := tabIDSet(s)
	for id := range m {
		if !live[id] {
			delete(m, id)
		}
	}
}

// pruneByTabScoped deletes map entries keyed "tabID|…" of closed tabs.
func pruneByTabScoped[T any](m map[string]T, s *AppState) {
	live := tabIDSet(s)
	for id := range m {
		tab, _, ok := strings.Cut(id, "|")
		if !ok || !live[tab] {
			delete(m, id)
		}
	}
}
