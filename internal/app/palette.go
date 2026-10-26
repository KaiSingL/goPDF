// palette.go is the Ctrl+K command palette: a modal panel over the UI
// (ui.Modal — centered, dimmed backdrop, Escape/click-outside close it,
// verified against the pinned MyGo v0.6.4 source) with a query field and
// a virtualized list of results.
//
// Results merge, in priority order with section headers: Commands, Pages
// (a number jumps, plus next/prev), the active tab's Outline, and the
// recent documents. Matching is a subsequence fuzzy match with a simple
// score. The palette's selection state lives in package vars (the
// components contract passes only c and s), reset whenever the palette
// opens or the query changes.
package app

import (
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"gopdf/internal/pdf"
)

// paletteItem is one row: a header when run is nil, else an entry.
type paletteItem struct {
	section string // header text, also the item's section
	label   string
	detail  string // right-aligned hint ("Page 3", a path)
	run     func() // nil for section headers
}

// The palette's state. palOpenSeen tracks the ShowPalette edge so opening
// starts a fresh query; the rest reset with it or on query changes.
var (
	palOpenSeen bool
	palQuery    string
	palSel      int
	palItems    []paletteItem
	palList     ui.ListState
)

// paletteOverlay renders the palette over the UI while s.ShowPalette.
// The root view (B, view.go) calls it after the rest of the layout.
func paletteOverlay(c *ui.Context, s *AppState) {
	if !s.ShowPalette {
		palOpenSeen = false
		return
	}
	if !palOpenSeen { // opened since the last build: start fresh
		palOpenSeen = true
		palQuery, palSel = "", 0
		rebuildPalette(s)
	}
	ui.Modal(c, &s.ShowPalette, func() { palettePanel(c, s) })
}

// palettePanel builds the query field and the results of the open palette.
func palettePanel(c *ui.Context, s *AppState) {
	t := c.Theme()

	// Window shortcuts wait behind a modal, so the palette binds its own:
	// Ctrl+K closes, Up/Down/Home/End move the selection.
	c.OnShortcut(ui.Ctrl, ui.KeyK, func() { s.ShowPalette = false })
	c.OnShortcut(0, ui.KeyDown, func() { palMove(+1) })
	c.OnShortcut(0, ui.KeyUp, func() { palMove(-1) })
	c.OnShortcut(0, ui.KeyHome, func() { palSel = 0; palSkipHeader(+1) })
	c.OnShortcut(0, ui.KeyEnd, func() { palSel = len(palItems) - 1; palSkipHeader(-1) })

	ui.Column(c).Width(640).MaxHeight(420).Gap(t.Space(2)).Children(func() {
		c.Key("palette-query")
		in := ui.TextInput(c, &palQuery).
			Placeholder("Search commands, pages, outline, recent documents…").AutoFocus()
		in.OnChange(func() { // the query changed: redo the results
			palSel = 0
			rebuildPalette(s)
		})
		if in.Submitted() {
			palRun(s)
		}
		if len(palItems) == 0 {
			ui.Text(c, "No matches").TextColor(t.TextMuted)
			return
		}
		ui.List(c, &palList, len(palItems), func(i int) { palRow(c, s, t, i) }).Grow(1)
	})
}

// palRow builds one result row: headers as muted small caps, entries with
// the selected one highlighted; a click runs the entry.
func palRow(c *ui.Context, s *AppState, t *ui.Theme, i int) {
	it := palItems[i]
	if it.run == nil {
		ui.Text(c, strings.ToUpper(it.section)).
			TextColor(t.TextMuted).FontSize(t.FontSize * 0.8).Bold()
		return
	}
	row := ui.Row(c).Fill().Padding(t.Space(1), t.Space(1.5)).Radius(t.Radius).Gap(t.Space(2))
	if i == palSel {
		row.Background(t.SurfaceHover)
	}
	row.OnClick(func() {
		palSel = i
		palRun(s)
	})
	row.Children(func() {
		ui.Text(c, it.label).Grow(1).SingleLine()
		if it.detail != "" {
			ui.Text(c, it.detail).TextColor(t.TextMuted).SingleLine()
		}
	})
}

// palMove moves the selection by delta, skipping section headers.
func palMove(delta int) {
	palSel += delta
	palSkipHeader(delta)
}

// palSkipHeader steps the selection off a header row, further along delta.
func palSkipHeader(delta int) {
	if !palClampSel() {
		return
	}
	for i := 0; i < len(palItems) && palItems[palSel].run == nil; i++ {
		palSel += delta
		palClampSel()
	}
}

func palClampSel() bool {
	if palSel < 0 {
		palSel = 0
	}
	if palSel >= len(palItems) {
		palSel = len(palItems) - 1
	}
	return palSel >= 0
}

// palRun runs the selected result and closes the palette.
func palRun(s *AppState) {
	if !palClampSel() {
		s.ShowPalette = false
		return
	}
	it := palItems[palSel]
	s.ShowPalette = false
	if it.run != nil {
		it.run()
	}
}

// rebuildPalette fills palItems for the current query. Empty queries list
// the commands and the recents; matching is a fuzzy subsequence match.
func rebuildPalette(s *AppState) {
	q := strings.ToLower(strings.TrimSpace(palQuery))
	items := make([]paletteItem, 0, 32)
	add := func(it paletteItem) { items = append(items, it) }
	match := func(text string) bool {
		_, ok := fuzzyScore(q, text)
		return ok
	}

	// Commands.
	add(paletteItem{section: "Commands"})
	cmd := func(label string, run func()) {
		if match(label) {
			add(paletteItem{section: "Commands", label: label, run: run})
		}
	}
	cmd("Toggle Theme", toggleTheme)
	cmd("Toggle Sidebar", func() { s.ToggleSidebar() })
	cmd("Zoom In", func() {
		if t := s.Active(); t != nil {
			t.ZoomIn()
		}
	})
	cmd("Zoom Out", func() {
		if t := s.Active(); t != nil {
			t.ZoomOut()
		}
	})
	cmd("Reset Zoom", func() {
		if t := s.Active(); t != nil {
			t.SetZoom(1)
		}
	})
	cmd("Rotate Clockwise", func() {
		if t := s.Active(); t != nil {
			t.RotateBy()
		}
	})
	cmd("Close Tab", func() { s.CloseActiveTab() })

	// Pages: a "Page N" jump when the query is a number, and stepping.
	add(paletteItem{section: "Pages"})
	if n, err := strconv.Atoi(strings.TrimSpace(q)); err == nil && n >= 1 {
		page := n
		add(paletteItem{section: "Pages", label: "Page " + strconv.Itoa(page),
			detail: "Go to page", run: func() {
				if t := s.Active(); t != nil {
					t.Navigate(page-1, 0)
				}
			}})
	}
	if match("Next Page") {
		add(paletteItem{section: "Pages", label: "Next Page", detail: "PageDown",
			run: func() {
				if t := s.Active(); t != nil {
					t.Navigate(t.CurPage+1, 0)
				}
			}})
	}
	if match("Previous Page") {
		add(paletteItem{section: "Pages", label: "Previous Page", detail: "PageUp",
			run: func() {
				if t := s.Active(); t != nil {
					t.Navigate(t.CurPage-1, 0)
				}
			}})
	}

	// Outline of the active tab, flattened, page-less entries dropped.
	add(paletteItem{section: "Outline"})
	if t := s.Active(); t != nil {
		for _, e := range flattenOutline(t.Outline, 0, maxPaletteOutline) {
			if e.title == "" || !match(e.title) {
				continue
			}
			page := e.page
			add(paletteItem{section: "Outline", label: e.title,
				detail: "Page " + strconv.Itoa(page+1), run: func() {
					if cur := s.Active(); cur != nil {
						cur.Navigate(page, 0)
					}
				}})
		}
	}

	// Recent documents.
	add(paletteItem{section: "Recent Documents"})
	for _, p := range s.Recents {
		base := filepath.Base(p)
		if !match(base) && !match(p) {
			continue
		}
		path := p
		add(paletteItem{section: "Recent Documents", label: base, detail: path,
			run: func() { OpenPath(path) }})
	}

	palItems = items
}

// maxPaletteOutline caps how many outline entries the palette considers.
const maxPaletteOutline = 60

// outlineEntry is a flattened outline row.
type outlineEntry struct {
	title string
	page  int // 0-based
}

// flattenOutline walks the outline tree depth-first, keeping entries with
// a target page, at most max entries.
func flattenOutline(nodes []pdf.OutlineNode, depth, max int) []outlineEntry {
	if depth > 8 || max <= 0 {
		return nil
	}
	var out []outlineEntry
	for _, n := range nodes {
		if n.Page >= 0 {
			out = append(out, outlineEntry{title: n.Title, page: n.Page})
			if len(out) >= max {
				return out
			}
		}
		out = append(out, flattenOutline(n.Children, depth+1, max-len(out))...)
		if len(out) >= max {
			return out[:max]
		}
	}
	return out
}

// fuzzyScore scores text against the query as a case-insensitive
// subsequence match: higher is better; ok reports whether the query is a
// subsequence of the text at all (an empty query matches everything).
func fuzzyScore(q, text string) (score int, ok bool) {
	if q == "" {
		return 0, true
	}
	t := strings.ToLower(text)
	ti, streak := 0, 0
	for _, r := range q {
		found := false
		for ti < len(t) {
			r2, size := utf8.DecodeRuneInString(t[ti:])
			ti += size
			if r2 == r {
				found = true
				streak++
				score += 2 + streak // contiguous runs score higher
				break
			}
			streak = 0
			score--
		}
		if !found {
			return 0, false
		}
	}
	if strings.HasPrefix(t, q) {
		score += 10
	}
	return score, true
}
