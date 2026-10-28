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

// toolbar renders the second bar for the active document tab and returns
// the page indicator (the go-to popover anchors to it). Launcher tabs
// (no document) render nothing — they show the launcher page, the same
// chrome as the initial no-tabs state.
func toolbar(c *ui.Context, s *AppState) ui.Element {
	t := s.Active()
	if t == nil || t.IsEmpty() {
		return ui.Element{}
	}
	var indicator ui.Element // returned: the go-to popover anchors to it
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

		indicator = ui.Button(c, fmt.Sprintf("%d / %d", t.CurPage+1, t.NumPages)).
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
		// results panel, searchpanel.go), print (the same PrintActive as
		// Ctrl+P and the File menu) and the more menu. Share and comment
		// are v1 non-goals (spec §2), and permanently disabled buttons
		// are noise, not promises — they stay out.
		search := ui.Button(c, "").Label("Search").Tooltip("Search (Ctrl+F)")
		search.Children(func() { ui.Icon(c, chromeIcons.search).FontSize(16) })
		search.OnClick(func() { s.ShowSearchPanel = !s.ShowSearchPanel })

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
	return indicator
}

// goToFocusSeq numbers GoTo focus requests so every open is a fresh,
// unique token (mirrors searchFocusSeq: FocusBind needs a new value to
// re-focus an already-built field).
var goToFocusSeq atomic.Uint64

// ToggleGoTo opens or closes the go-to field, refreshing the focus token
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

// gotoField is the "Go to page" popover: a small bordered panel floating
// under the toolbar's page indicator — ui.Overlay + AttachTo, mygo's
// popover idiom, so it follows the indicator and flips aside where it
// would overflow the window. Enter goes to the typed page and closes the
// panel; a press anywhere else closes it (PressedOutside, which counts a
// press on the indicator itself as inside, so the toggle stays clean);
// Escape in the field closes it too. View builds it after the toolbar,
// in the same frame as the indicator.
func gotoField(c *ui.Context, s *AppState, indicator ui.Element) {
	t := s.Active()
	if t == nil || !s.ShowGoTo || indicator == (ui.Element{}) {
		return
	}
	th := c.Theme()
	edit := pageEditFor(t)
	ui.Overlay(c, func() {
		panel := ui.Column(c).Width(200).Padding(th.Space(1.5)).
			Gap(th.Space(1)).
			Background(th.Surface).Border(1, th.Border).Radius(th.Space(1)).
			Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.25)).
			AttachTo(indicator, ui.AnchorBottomLeft, ui.AnchorTopLeft)
		panel.Children(func() {
			ui.Text(c, "Go to page").FontSize(12).TextColor(th.TextMuted)
			in := ui.TextInput(c, edit).
				Label("Go to page").
				Placeholder(fmt.Sprintf("%d", t.CurPage+1)).
				FontSize(12)
			in.FocusBind(&GoToFocus, GoToFocus) // opening the panel focuses it
			if in.Submitted() {
				n, err := strconv.Atoi(strings.TrimSpace(*edit))
				*edit = "" // reset in every case; the placeholder shows the page
				if err == nil {
					t.Navigate(n-1, 0)
					s.ShowGoTo = false // it went: close the panel
				}
			}
			if in.Shortcut(0, ui.KeyEscape) {
				s.ShowGoTo = false
			}
			ui.Textf(c, "of %d", t.NumPages).FontSize(12).TextColor(th.TextMuted)
		})
		if panel.PressedOutside() {
			s.ShowGoTo = false
		}
	})
}
