// toolbar.go renders the second bar (spec §4 update: "back/forward per tab,
// reload, search toggle, zoom - / 100% / +, share, comment (disabled v1),
// more menu"; the search input itself moved into the right-side results
// panel, searchpanel.go, and reload moved into the more menu as its first
// item). One of the four chrome modules (Agent C).
//
// API notes (verified against mygo v0.6.4 source):
//   - ui.Toolbar lays its controls out flat-until-hovered, moves overflow
//     into an end menu, and is a focus group — use it as the container.
//   - Segmented writes its choice through the bound *int at construction,
//     so binding it to (*int)(&t.ZoomMode) applies fit-mode changes
//     directly to the model (bound values before OnClick — skill rule).
package app

import "github.com/egoist/mygo/ui"

// toolbar renders the second bar for the active tab; it shows nothing
// without a tab.
func toolbar(c *ui.Context, s *AppState) {
	t := s.Active()
	if t == nil {
		return
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

		// Search toggles the right-side results panel (searchpanel.go);
		// the field itself lives there now.
		search := ui.Button(c, "").Label("Search").Tooltip("Search (Ctrl+F)")
		search.Children(func() { ui.Icon(c, chromeIcons.search).FontSize(16) })
		search.OnClick(func() { s.ShowSearchPanel = !s.ShowSearchPanel })

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
