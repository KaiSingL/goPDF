// view.go is the root view (module B). It composes the whole window from
// the components, in the order the map in components.go prescribes:
// shortcuts first so they are window-scoped, then the chrome, then the
// content row, and the palette overlay last so it draws above everything.
package app

import "github.com/egoist/mygo/ui"

// View builds the window's interface from state. MyGo calls it on the main
// thread whenever a frame is due.
func (s *AppState) View(c *ui.Context) {
	shortcuts(c, s) // D: app-wide keyboard bindings, window scope (first)

	// Fill the window: the chrome bars take their natural height and the
	// content row grows into the rest. Dividers separate the bands and the
	// sidebar from the viewer so the chrome doesn't read as one surface.
	ui.Column(c).Fill().Children(func() {
		tabBar(c, s)  // C: browser-style tab strip
		ui.Divider(c) // ─ under the tab strip
		toolbar(c, s) // C: back/forward, reload, search, zoom
		ui.Divider(c) // ─ under the toolbar

		// Content: sidebar (C) beside the page area (B), which fills the
		// rest, and — when toggled — the search results panel (D) on the
		// right. No document open — no tabs or a launcher tab — means no
		// sidebar or search panel: nothing to thumb, outline or search
		// (matches the initial page).
		ui.Row(c).Fill().Grow(1).Children(func() {
			if s.ShowSidebar && s.Active() != nil && !s.Active().IsEmpty() {
				sidebar(c, s) // C: thumbnails / outline, SidebarWidth wide
				ui.Divider(c) // │ between sidebar and pages
			}
			viewer(c, s) // B: the scrollable page area
			if s.ShowSearchPanel && s.Active() != nil {
				ui.Divider(c)     // │ between pages and the search panel
				searchPanel(c, s) // D: right-side search results
			}
		})

		ui.Divider(c)        // ─ over the status bar
		statusBar(c, s)      // C: page X/Y, fit mode, zoom, theme toggle
		paletteOverlay(c, s) // D: Ctrl+K palette, drawn above everything
	})
}
