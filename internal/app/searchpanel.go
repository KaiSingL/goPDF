// searchpanel.go renders the right-side in-document search results panel
// (spec §4 update: search moved out of the toolbar into a panel). view.go
// builds it after the viewer whenever s.ShowSearchPanel is set and a tab
// is open; the toolbar's search icon and Ctrl+F (shortcuts.go) toggle it.
//
// API notes (verified against mygo v0.6.4 source):
//   - The query field is composed from ui.TextInputBase inside a styled
//     Row — the pattern the old toolbar searchBar used (ui.SearchField
//     exposes only its wrapper, which FocusBind cannot type into).
//   - FocusBind(field, value) binds desired focus to &SearchFocus, the
//     package var (components.go) that Ctrl+F fills with a fresh token.
//     The panel is only built while visible, so the token lands on the
//     field on the first build after the panel opens.
//   - The results list is a virtualized ui.List sized with Fill inside the
//     panel's FillHeight column. The column has a definite height (the
//     content row bounds it), which is what makes the list scroll and
//     virtualize — verified with ui.NewTester: only the visible rows
//     build, and ListState.ScrollIntoView reveals later rows.
//   - There is NO auto-search: the field's Changed notice is ignored
//     (deliberately, unlike the old toolbar field). Only Submitted —
//     Enter — runs a search; Enter on unchanged results steps to the next
//     hit, Shift+Enter to the previous.
package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"gopdf/internal/pdf"
)

// searchPanelWidth is the search panel's width in DIPs.
const searchPanelWidth = 300.0

// searchQueries holds one search-field string per tab, keyed by tab ID.
// (Moved here from toolbar.go with the field itself.) The field must edit
// storage that outlives the build pass; per-tab entries keep each tab's
// query when the active tab changes. Entries are pruned lazily for closed
// tabs.
var searchQueries = map[string]*string{}

// searchLists is the results list' scroll state per tab ID, like the
// sidebar's thumbLists; searchScrolled records the hit already scrolled
// into view, so user scrolling (wheel, scrollbar) is not snapped back to
// the current hit every frame. UI-thread only; ui values stay out of
// AppState (state.go threading rules). Pruned lazily for closed tabs.
var (
	searchLists    = map[string]*ui.ListState{}
	searchScrolled = map[string]int{}
)

// searchQueryFor returns the tab's search-field string, creating it from
// the tab's committed query on first use.
func searchQueryFor(t *Tab) *string {
	if q := searchQueries[t.ID]; q != nil {
		return q
	}
	q := t.Search.Query
	searchQueries[t.ID] = &q
	return &q
}

// searchPanel builds the panel: the query field, the result counter with
// the previous/next steppers, and the virtualized hit list.
func searchPanel(c *ui.Context, s *AppState) {
	t := s.Active()
	if t == nil {
		return
	}
	th := c.Theme()
	if len(searchQueries) > 64 { // prune lazily, like the sidebar's maps
		pruneByTabID(searchQueries, s)
		pruneByTabID(searchLists, s)
		pruneByTabID(searchScrolled, s)
	}
	ui.Column(c).Width(searchPanelWidth).FillHeight().
		Padding(th.Space(2)).Gap(th.Space(2)).Children(func() {
		searchField(c, s, t)
		searchMeta(c, s, t)
		searchResults(c, t)
	})
}

// searchField is the panel's query input: the composed-field pattern the
// old toolbar searchBar used (ui.TextInputBase inside a styled Row, with
// a magnifier icon and a clear button), bound to &SearchFocus so Ctrl+F
// focuses it. It never auto-searches: editing only updates the field's
// storage; Enter (searchField's Submitted handling) commits.
func searchField(c *ui.Context, s *AppState, t *Tab) {
	th := c.Theme()
	q := searchQueryFor(t)
	box := ui.Row(c).AlignItems(ui.Center).Gap(th.Space(1.5)).
		Padding(th.Space(1.5), th.Space(2.5)).
		Background(th.Surface).Border(1, th.Border).Radius(th.Radius).
		FillWidth().Cursor(ui.CursorText)
	box.Label("Search document")
	var in ui.Element
	box.Children(func() {
		mag := ui.Icon(c, chromeIcons.search).FontSize(13)
		mag.TextColor(th.TextMuted)
		e := ui.TextInputBase(c, q).Grow(1).MinWidth(th.Space(15)).Placeholder("Search")
		in = e
		if *q != "" { // clear button, as SearchField's
			clear := ui.ButtonBase(c).
				Size(th.Space(3), th.Space(3)).Radius(th.Space(1.5)).
				Background(th.TextMuted).Label("Clear search")
			clear.Children(func() {
				ui.Icon(c, chromeIcons.close).FontSize(9).TextColor(th.Surface)
			})
			clear.OnClick(func() { *q = "" })
		}
	})
	if box.Clicked() {
		in.Focus() // as field() focuses the inner input, in the pinned source
	}
	in.FocusBind(&SearchFocus, SearchFocus) // Ctrl+F (Agent D) focuses this
	if in.Submitted() {
		if *q != t.Search.Query {
			runSearch(s, t, *q) // Enter runs (or re-runs) the search
		} else if len(t.Search.Hits) > 0 {
			delta := 1
			if c.Modifiers()&ui.Shift != 0 {
				delta = -1
			}
			s.StepSearch(t, delta) // Enter again steps, Shift+Enter back
		}
	}
	if in.Shortcut(0, ui.KeyEscape) {
		if *q != "" { // first Escape clears, as SearchField does…
			*q = ""
			runSearch(s, t, "")
		} else { // …an empty field's Escape closes the panel
			s.ShowSearchPanel = false
		}
	}
}

// runSearch commits the query and kicks the search. With no document,
// RunSearch returns early but StartSearch already flagged work in flight;
// nothing can be in flight, so settle the flag (frozen-state workaround,
// carried over from the old toolbar searchBar).
func runSearch(s *AppState, t *Tab, query string) {
	t.StartSearch(query)
	s.RunSearch(t)
	if t.Doc == nil {
		t.Search.Running = false
	}
}

// searchMeta is the row under the field: the result count — or the
// "Searching…" notice while the search is in flight — followed by the
// previous/next hit steppers and, once a hit is selected, its position.
// Nothing to count until a query is committed.
func searchMeta(c *ui.Context, s *AppState, t *Tab) {
	if t.Search.Query == "" {
		return
	}
	th := c.Theme()
	ui.Row(c).AlignItems(ui.Center).Gap(th.Space(1.5)).FillWidth().Children(func() {
		status := ui.Textf(c, "%d results", len(t.Search.Hits))
		if t.Search.Running {
			status = ui.Text(c, "Searching…")
		}
		status.TextColor(th.TextMuted).FontSize(13).Grow(1)

		prev := ui.Button(c, "").Label("Previous hit").Tooltip("Previous hit (Shift+Enter)").
			Disabled(len(t.Search.Hits) == 0)
		prev.Children(func() { ui.Icon(c, chromeIcons.chevUp).FontSize(15) })
		prev.OnClick(func() { s.StepSearch(t, -1) })
		if t.Search.Current >= 0 {
			ui.Textf(c, "%d of %d", t.Search.Current+1, len(t.Search.Hits)).FontSize(13)
		}
		next := ui.Button(c, "").Label("Next hit").Tooltip("Next hit (Enter)").
			Disabled(len(t.Search.Hits) == 0)
		next.Children(func() { ui.Icon(c, chromeIcons.chevDown).FontSize(15) })
		next.OnClick(func() { s.StepSearch(t, +1) })
	})
}

// searchResults is the hit list below the meta row, virtualized like the
// sidebar's thumbnail list (only visible rows build). Empty states: no
// committed query asks for one, a finished search without hits reports it;
// while the search runs the counter row already says "Searching…".
func searchResults(c *ui.Context, t *Tab) {
	th := c.Theme()
	if t.Search.Query == "" {
		ui.Text(c, "Type and press Enter").TextColor(th.TextMuted).FontSize(13)
		return
	}
	if len(t.Search.Hits) == 0 {
		if !t.Search.Running {
			ui.Text(c, "No results").TextColor(th.TextMuted).FontSize(13)
		}
		return
	}
	lst := searchLists[t.ID]
	if lst == nil {
		lst = &ui.ListState{}
		searchLists[t.ID] = lst
	}
	// Follow the current hit as stepping moves it — but only for a CHANGED
	// selection, so user scrolling survives (the palette's palScrolledSel
	// pattern).
	if cur := t.Search.Current; cur != searchScrolled[t.ID] {
		searchScrolled[t.ID] = cur
		if cur >= 0 {
			lst.ScrollIntoView(cur)
		}
	}
	ui.List(c, lst, len(t.Search.Hits)).
		ItemKey(func(i int) any { return i }). // row identity = hit position
		Fill().FillWidth().                    // FillHeight column bounds it: scrolls
		Rows(func(r ui.ListRow) { searchHitRow(c, t, r.Index) })
}

// searchHitRow is one hit: its page number (accent on the current hit)
// beside a snippet of the page text around the match, the match itself
// emphasized in the accent color. Clicking selects the hit and navigates
// to its page.
func searchHitRow(c *ui.Context, t *Tab, i int) {
	th := c.Theme()
	hit := t.Search.Hits[i]
	current := i == t.Search.Current
	row := ui.ButtonBase(c).FillWidth().AlignItems(ui.Center).
		Gap(th.Space(1.5)).Padding(th.Space(1), th.Space(1.5)).
		Radius(th.Space(0.75)).Cursor(ui.CursorPointer)
	row.Label(fmt.Sprintf("Hit %d", i+1))
	if current {
		row.Background(accentTintSoft(th))
	}
	row.Children(func() {
		page := ui.Textf(c, "Page %d", hit.Page+1)
		if current {
			page.TextColor(th.Accent)
		} else {
			page.TextColor(th.TextMuted)
		}
		if sn, ok := hitSnippet(t, hit); ok {
			snippetText(c, th, sn).Grow(1).SingleLine()
		}
	})
	row.OnClick(func() {
		t.Search.Current = i
		t.Navigate(hit.Page, 0)
	})
}

// snippetText lays a snippet out as one rich text — context, then the
// match in the accent color and bold, then context — so a row reads as
// text around its match at a glance.
func snippetText(c *ui.Context, th *ui.Theme, sn snip) ui.Element {
	var spans []ui.Span
	if sn.before != "" {
		spans = append(spans, ui.Span{Text: sn.before})
	}
	if sn.match != "" {
		spans = append(spans, ui.Span{Text: sn.match, Color: th.Accent, Weight: 700})
	}
	if sn.after != "" {
		spans = append(spans, ui.Span{Text: sn.after})
	}
	return ui.RichText(c, spans...)
}

// hitSnippet builds a hit row's snippet from the tab's built text index;
// with no index yet (still building) there is no snippet. The page's
// plain text and its spans come from separate extractions, so when the
// plain text has lost a match the search itself found (extraction
// quirks), the snippet locates within the hit's covering spans' text —
// where the search found it — instead.
func hitSnippet(t *Tab, h pdf.SearchHit) (snip, bool) {
	ix := t.Index()
	if ix == nil {
		return snip{}, false
	}
	if sn, ok := snippet(ix.PageText(h.Page), t.Search.Query); ok {
		return sn, true
	}
	txts := make([]string, 0, len(h.Spans))
	for _, sp := range h.Spans {
		txts = append(txts, sp.Text)
	}
	return snippet(strings.Join(txts, " "), t.Search.Query)
}

// Snippet bounds, in runes, the context kept around a match: a little
// more after than before, as the eye reads toward the match.
const (
	snippetBefore = 25
	snippetAfter  = 35
)

// snip is a snippet split around the match for the row to emphasize the
// match against its context. "…" is folded onto a side whose text was
// cut short, and onto no other.
type snip struct {
	before, match, after string
}

// snippet locates the query's first case-insensitive occurrence in text
// and returns up to snippetBefore runes before and snippetAfter runes
// after it, reporting found=false without one (or with an empty text or
// query). The match is kept whole; a cut side carries "…", so the
// snippet is never just the bare match when any context exists.
func snippet(text, query string) (snip, bool) {
	text = strings.TrimSpace(text)
	if text == "" || query == "" {
		return snip{}, false
	}
	rs := []rune(text)
	low := []rune(strings.ToLower(text))
	q := []rune(strings.ToLower(query))

	at := indexRunes(low, q) // first match, -1 without one
	if at < 0 {
		return snip{}, false
	}
	start := max(0, at-snippetBefore)
	end := min(len(rs), at+len(q)+snippetAfter)

	var sn snip
	sn.match = string(rs[at : at+len(q)])
	if start < at { // context before the match
		if start > 0 {
			sn.before = "…" + string(rs[start:at])
		} else {
			sn.before = string(rs[start:at])
		}
	}
	if endAfter := at + len(q); endAfter < end { // context after it
		sn.after = string(rs[endAfter:end])
		if end < len(rs) {
			sn.after += "…"
		}
	}
	return sn, true
}

// indexRunes returns the rune index of the first occurrence of needle in
// hay, or -1. (A lowered-strings strings.Index would drift from the rune
// offsets when a rune lowercases to several; this stays exact.)
func indexRunes(hay, needle []rune) int {
	if len(needle) == 0 {
		return 0
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		if slices.Equal(hay[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}
