// toolbar.go renders the second bar in four sections, separated by thin
// dividers:
//
//	[sidebar | back | forward] │ [zoom − 100% + | fit segmented] │
//	[prev page | n / N | next page] ⟨spacer⟩ [search | share | print | more]
//
// The page indicator is a button: it opens the "Go to page" row that
// appears directly under the toolbar (gotoRow, below) — the inline page
// field left the status bar for here. Comment stays out of v1 (spec §2)
// and its dead disabled button with it.
//
// API notes (verified against mygo v0.6.4 source):
//   - ui.Toolbar lays its controls out flat-until-hovered, moves overflow
//     into an end menu, and is a focus group — use it as the container.
//   - Segmented writes its choice through the bound *int at construction,
//     so binding it to (*int)(&t.ZoomMode) applies fit-mode changes
//     directly to the model (bound values before OnClick — skill rule).
package app

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo/ui"
)

// toolbar renders the second bar for the active tab; it shows nothing
// without a tab.
func toolbar(c *ui.Context, s *AppState) {
	t := s.Active()
	if t == nil {
		return
	}
	ui.Toolbar(c, func() {
		// Section 1 — document navigation: the sidebar toggle (spec §4
		// update) and the per-tab history.
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

		ui.Divider(c)

		// Section 2 — zoom (spec §5: 25%–400%, stepping by ZoomStep). The
		// − and + are icon buttons; the percent button resets to 100%
		// (t.SetZoom(1)) and its label is the capped, on-screen zoom.
		zout := ui.Button(c, "").Label("Zoom out").Tooltip("Zoom out")
		zout.Children(func() { ui.Icon(c, chromeIcons.minus).FontSize(16) })
		zout.OnClick(t.ZoomOut)

		reset := ui.Button(c, t.ZoomPercent()).Label("Zoom level").Tooltip("Reset zoom to 100%")
		reset.OnClick(func() { t.SetZoom(1) })

		zin := ui.Button(c, "").Label("Zoom in").Tooltip("Zoom in")
		zin.Children(func() { ui.Icon(c, chromeIcons.plus).FontSize(16) })
		zin.OnClick(t.ZoomIn)

		// Fit mode (spec §4). The bound write applies ZoomMode directly.
		ui.Segmented(c, (*int)(&t.ZoomMode), "Fit Width", "Fit Page", "Custom")

		ui.Divider(c)

		// Section 3 — pages: page-turn buttons (the shortcuts.go helpers
		// behind Page Up/Page Down and the arrow keys) around the page
		// indicator, which opens the go-to row under this bar.
		prev := ui.Button(c, "").Label("Previous page").Tooltip("Previous page (Page Up)").
			Disabled(t.CurPage <= 0)
		prev.Children(func() { ui.Icon(c, chromeIcons.chevLeft).FontSize(16) })
		prev.OnClick(prevPage(s))

		indicator := ui.Button(c, fmt.Sprintf("%d / %d", t.CurPage+1, t.NumPages)).
			Label("Go to page").Tooltip("Go to page")
		indicator.OnClick(func() { s.ToggleGoTo() })

		next := ui.Button(c, "").Label("Next page").Tooltip("Next page (Page Down)").
			Disabled(t.CurPage >= t.NumPages-1)
		next.Children(func() { ui.Icon(c, chromeIcons.chevRight).FontSize(16) })
		next.OnClick(nextPage(s))

		// The spacer absorbs the leftover width, keeping the right section
		// flush right instead of everything piling onto the left (ui.Spacer
		// is ui.Toolbar's own mechanism for this).
		ui.Spacer(c)

		// Section 4 — flush right: search (it toggles the right-side
		// results panel, searchpanel.go), share (v1 non-goal, spec §2),
		// print (the same PrintActive as Ctrl+P and the File menu) and
		// the more menu. The comment feature is not in v1, and a
		// permanently disabled button is noise, not a promise.
		search := ui.Button(c, "").Label("Search").Tooltip("Search (Ctrl+F)")
		search.Children(func() { ui.Icon(c, chromeIcons.search).FontSize(16) })
		search.OnClick(func() { s.ShowSearchPanel = !s.ShowSearchPanel })

		share := ui.Button(c, "").Label("Share").Disabled(true).Tooltip("Sharing is not available in this version")
		share.Children(func() { ui.Icon(c, chromeIcons.share).FontSize(16) })

		print := ui.Button(c, "").Label("Print").Tooltip("Print (Ctrl+P)")
		print.Children(func() { ui.Icon(c, chromeIcons.printer).FontSize(16) })
		print.OnClick(PrintActive)

		more := ui.Button(c, "").Label("More").Tooltip("More actions")
		more.Children(func() { ui.Icon(c, chromeIcons.more).FontSize(18) })
		more.Menu(func(m *ui.Menu) {
			// Reload lives here now (it left the bar to the more menu):
			// first item, ahead of the rarer actions.
			if m.Item("Reload Document").Chosen() {
				t.Reload()
			}
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

// goToFocusSeq numbers GoTo focus requests so every open is a fresh,
// unique token (mirrors searchFocusSeq: FocusBind needs a new value to
// re-focus an already-built field).
var goToFocusSeq atomic.Uint64

// ToggleGoTo opens or closes the go-to row, refreshing the focus token
// on open so the field is ready to type in.
func (s *AppState) ToggleGoTo() {
	s.ShowGoTo = !s.ShowGoTo
	if s.ShowGoTo {
		GoToFocus = fmt.Sprintf("goto-%d", goToFocusSeq.Add(1))
	}
}

// pageEdits holds one go-to string per tab (pruned lazily).
var pageEdits = map[string]*string{}

// pageEditFor returns the tab's go-to string, starting empty so the
// placeholder shows the committed page.
func pageEditFor(t *Tab) *string {
	if e := pageEdits[t.ID]; e != nil {
		return e
	}
	e := new(string)
	pageEdits[t.ID] = e
	return e
}

// gotoRow is the "Go to page" field that appears under the toolbar when
// the page indicator opens it (View builds it between the toolbar and
// the content): Enter goes to the typed page and closes the row; invalid
// input resets to the placeholder, as the old status-bar field did.
func gotoRow(c *ui.Context, s *AppState) {
	t := s.Active()
	if t == nil {
		return
	}
	th := c.Theme()
	edit := pageEditFor(t)
	ui.Row(c.Key("goto-row")).Height(34).PaddingX(th.Space(2)).
		Gap(th.Space(2)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Go to page").FontSize(12).TextColor(th.TextMuted)
		in := ui.TextInput(c, edit).
			Label("Go to page").
			Width(72).
			Placeholder(fmt.Sprintf("%d", t.CurPage+1)).
			FontSize(12)
		in.FocusBind(&GoToFocus, GoToFocus) // opening the row focuses it
		if in.Submitted() {
			n, err := strconv.Atoi(strings.TrimSpace(*edit))
			*edit = "" // reset in every case; the placeholder shows the page
			if err == nil {
				t.Navigate(n-1, 0)
				s.ShowGoTo = false // it went: close the row
			}
		}
		ui.Textf(c, "of %d", t.NumPages).FontSize(12).TextColor(th.TextMuted)
	})
}
