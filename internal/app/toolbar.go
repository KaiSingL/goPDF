// toolbar.go renders the second bar in four sections, separated by thin
// dividers (user-requested layout):
//
//	[sidebar | back | forward] │ [zoom − 100% + | fit segmented] │
//	[prev page | n / N | next page] ⟨spacer⟩ [search | share | comment | more]
//
// The search button sits flush RIGHT (it moved from the left group), and
// the page section is new: page-turn buttons beside a "current / total"
// indicator. ui.Divider is vertical inside a Row and, being a plain box,
// is invisible to the Toolbar's overflow accounting (only clickable and
// menu children register as toolbarItems), so the dividers and the inert
// page indicator never corrupt the end-of-bar overflow menu.
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

	"github.com/egoist/mygo/ui"
)

// toolbar renders the second bar for the active tab; it shows nothing
// without a tab.
func toolbar(c *ui.Context, s *AppState) {
	t := s.Active()
	if t == nil {
		return
	}
	th := c.Theme()
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
		// percent button resets to 100% (t.SetZoom(1)); the label is the
		// capped, on-screen zoom (Tab.ZoomPercent).
		zout := ui.Button(c, "−").Label("Zoom out").Tooltip("Zoom out")
		zout.OnClick(t.ZoomOut)

		reset := ui.Button(c, t.ZoomPercent()).Label("Zoom level").Tooltip("Reset zoom to 100%")
		reset.OnClick(func() { t.SetZoom(1) })

		zin := ui.Button(c, "+").Label("Zoom in").Tooltip("Zoom in")
		zin.OnClick(t.ZoomIn)

		// Fit mode (spec §4). The bound write applies ZoomMode directly.
		ui.Segmented(c, (*int)(&t.ZoomMode), "Fit Width", "Fit Page", "Custom")

		ui.Divider(c)

		// Section 3 — pages: page-turn buttons (the shortcuts.go helpers
		// behind Page Up/Page Down and the arrow keys) around a
		// "current / total" indicator. Inert text: the toolbar's overflow
		// walk only registers clickable children.
		prev := ui.Button(c, "").Label("Previous page").Tooltip("Previous page (Page Up)").
			Disabled(t.CurPage <= 0)
		prev.Children(func() { ui.Icon(c, chromeIcons.chevLeft).FontSize(16) })
		prev.OnClick(prevPage(s))

		ui.Text(c, fmt.Sprintf("%d / %d", t.CurPage+1, t.NumPages)).
			FontSize(12).TextColor(th.TextMuted)

		next := ui.Button(c, "").Label("Next page").Tooltip("Next page (Page Down)").
			Disabled(t.CurPage >= t.NumPages-1)
		next.Children(func() { ui.Icon(c, chromeIcons.chevRight).FontSize(16) })
		next.OnClick(nextPage(s))

		// The spacer absorbs the leftover width, keeping the right section
		// flush right instead of everything piling onto the left (ui.Spacer
		// is ui.Toolbar's own mechanism for this).
		ui.Spacer(c)

		// Section 4 — flush right: search (moved here from the left group;
		// it toggles the right-side results panel, searchpanel.go), the v1
		// non-goals (spec §2) and the more menu.
		search := ui.Button(c, "").Label("Search").Tooltip("Search (Ctrl+F)")
		search.Children(func() { ui.Icon(c, chromeIcons.search).FontSize(16) })
		search.OnClick(func() { s.ShowSearchPanel = !s.ShowSearchPanel })

		share := ui.Button(c, "").Label("Share").Disabled(true).Tooltip("Sharing is not available in this version")
		share.Children(func() { ui.Icon(c, chromeIcons.share).FontSize(16) })
		comment := ui.Button(c, "").Label("Comment").Disabled(true).Tooltip("Comments are not available in this version")
		comment.Children(func() { ui.Icon(c, chromeIcons.comment).FontSize(16) })

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
