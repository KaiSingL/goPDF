// toolbar.go renders the second bar (spec §4: "back/forward per tab,
// reload, search input, zoom - / 100% / +, share, comment (disabled v1),
// more menu"). One of the four chrome modules (Agent C).
//
// API notes (verified against mygo v0.6.4 source):
//   - ui.Toolbar lays its controls out flat-until-hovered, moves overflow
//     into an end menu, and is a focus group — use it as the container.
//   - The search field is composed from ui.TextInputBase (see searchBar:
//     ui.SearchField exposes only its wrapper, which FocusBind cannot type
//     into); Changed reports each edit and Submitted fires on Enter.
//   - FocusBind(field, value) binds desired focus to &SearchFocus. The
//     field is bound with the CURRENT value of SearchFocus, so whatever
//     unique token a shortcut (Agent D) stores there lands on this field
//     on the next build; the binding writes the token back once focus has
//     landed and clears it when focus moves elsewhere.
//   - Segmented writes its choice through the bound *int at construction,
//     so binding it to (*int)(&t.ZoomMode) applies fit-mode changes
//     directly to the model (bound values before OnClick — skill rule).
package app

import "github.com/egoist/mygo/ui"

// searchQueries holds one search-field string per tab, keyed by tab ID.
// The field must edit storage that outlives the build pass; per-tab entries
// keep each tab's query when the active tab changes. Entries are pruned
// lazily for closed tabs.
var searchQueries = map[string]*string{}

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

// toolbar renders the second bar for the active tab; it shows nothing
// without a tab.
func toolbar(c *ui.Context, s *AppState) {
	t := s.Active()
	if t == nil {
		return
	}
	if len(searchQueries) > 64 {
		pruneByTabID(searchQueries, s)
	}
	ui.Toolbar(c, func() {
		// Leftmost: the sidebar toggle, icon-only (spec §4 update).
		panel := ui.Button(c, "").Label("Toggle sidebar").Tooltip("Toggle sidebar")
		panel.Children(func() { ui.Icon(c, chromeIcons.panel).FontSize(16) })
		panel.OnClick(func() { s.ToggleSidebar() })

		back := ui.Button(c, "").Label("Back").Tooltip("Go back").
			Disabled(len(t.History) == 0)
		back.Children(func() { ui.Icon(c, chromeIcons.back).FontSize(16) })
		back.OnClick(func() { t.Back() }) // the viewer scrolls itself

		fwd := ui.Button(c, "").Label("Forward").Tooltip("Go forward").
			Disabled(len(t.Future) == 0)
		fwd.Children(func() { ui.Icon(c, chromeIcons.forward).FontSize(16) })
		fwd.OnClick(func() { t.Forward() })

		reload := ui.Button(c, "").Label("Reload").Tooltip("Reload document")
		reload.Children(func() { ui.Icon(c, chromeIcons.reload).FontSize(15) })
		reload.OnClick(t.Reload)

		searchBar(c, s, t)

		// The spacer absorbs the leftover width, keeping the zoom, fit and
		// overflow controls flush right instead of everything piling onto
		// the left (ui.Spacer is ui.Toolbar's own mechanism for this).
		ui.Spacer(c)

		// Zoom controls (spec §5: 25%–400%, stepping by ZoomStep). The
		// percent button resets to 100% (t.SetZoom(1)).
		zout := ui.Button(c, "−").Label("Zoom out").Tooltip("Zoom out")
		zout.OnClick(t.ZoomOut)

		reset := ui.Button(c, t.ZoomPercent()).Label("Zoom level").Tooltip("Reset zoom to 100%")
		reset.OnClick(func() { t.SetZoom(1) })

		zin := ui.Button(c, "+").Label("Zoom in").Tooltip("Zoom in")
		zin.OnClick(t.ZoomIn)

		// Fit mode (spec §4). The bound write applies ZoomMode directly.
		ui.Segmented(c, (*int)(&t.ZoomMode), "Fit Width", "Fit Page", "Custom")

		// v1 non-goals (spec §2): share and comments stay disabled.
		share := ui.Button(c, "").Label("Share").Disabled(true).Tooltip("Sharing is not available in this version")
		share.Children(func() { ui.Icon(c, chromeIcons.share).FontSize(16) })
		comment := ui.Button(c, "").Label("Comment").Disabled(true).Tooltip("Comments are not available in this version")
		comment.Children(func() { ui.Icon(c, chromeIcons.comment).FontSize(16) })

		more := ui.Button(c, "").Label("More").Tooltip("More actions")
		more.Children(func() { ui.Icon(c, chromeIcons.more).FontSize(18) })
		more.Menu(func(m *ui.Menu) {
			if m.Item("Rotate Clockwise").Chosen() {
				t.RotateBy()
			}
			m.Separator()
			if m.Item("Command Palette…").Chosen() {
				s.ShowPalette = true
			}
		})
	}).Label("Document")
}

// searchBar is the search field bound to the active tab's query.
//
// Composed from ui.TextInputBase — the base mygo's own SearchField wraps —
// so that FocusBind lands on the editor itself: focusing a SearchField
// wrapper takes focus without taking text (verified in the pinned source;
// ui.SearchField exposes only the wrapper). Submit runs a new search for a
// changed query; Enter on existing hits steps to the next hit, Shift+Enter
// to the previous; Escape clears, as SearchField does.
func searchBar(c *ui.Context, s *AppState, t *Tab) {
	th := c.Theme()
	q := searchQueryFor(t)
	box := ui.Row(c).AlignItems(ui.Center).Gap(th.Space(1.5)).
		Padding(th.Space(1.5), th.Space(2.5)).
		Background(th.Surface).Border(1, th.Border).Radius(th.Radius).
		Grow(1).MaxWidth(320).Cursor(ui.CursorText)
	box.Label("Search document")
	var in ui.Element
	box.Children(func() {
		mag := ui.Icon(c, chromeIcons.search).FontSize(13)
		mag.TextColor(th.TextMuted)
		e := ui.TextInputBase(c, q).Grow(1).MinWidth(th.Space(15)).Placeholder("Search")
		in = e
		if *q != "" { // clear button, as SearchField's; the edit's Changed
			// notice runs the emptied search on the next pass
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
	run := func(query string) {
		t.StartSearch(query)
		s.RunSearch(t)
		if t.Doc == nil {
			// No document: RunSearch returns early but StartSearch already
			// flagged work in flight; nothing can be in flight (frozen-state
			// workaround, see final report).
			t.Search.Running = false
		}
	}
	if in.Submitted() {
		if *q != t.Search.Query {
			run(*q)
		} else if len(t.Search.Hits) > 0 {
			delta := 1
			if c.Modifiers()&ui.Shift != 0 {
				delta = -1
			}
			s.StepSearch(t, delta)
		}
	}
	if in.Changed() && *q != t.Search.Query {
		run(*q) // debounce-lite: every edit re-runs; the index builds once
	}
	if *q != "" && in.Shortcut(0, ui.KeyEscape) {
		*q = ""
		run("")
	}
}
