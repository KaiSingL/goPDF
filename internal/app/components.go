// components.go is the seam between the four UI modules. Each function here
// is a stub that its owner replaces with a real implementation in the file
// named next to it; when you implement one, DELETE its stub here.
//
// Composition (owner B, view.go):
//
//	(s *AppState) View(c *ui.Context)
//	  └─ tabBar      (C, tabs.go)      browser-style tab strip
//	  └─ toolbar     (C, toolbar.go)   nav / search / zoom controls
//	  └─ sidebar     (C, sidebar.go)   thumbnails + outline (left, 280 DIPs)
//	  └─ viewer      (B, viewer.go)    the scrollable page area
//	  └─ statusBar   (C, statusbar.go) page X/Y, fit, zoom, theme toggle
//	  └─ paletteOverlay (D, palette.go) Ctrl+K overlay when s.ShowPalette
//
// Cross-module rules that keep the modules independent:
//
//   - Components talk to each other only through AppState actions and by
//     reading state, never by calling each other directly.
//   - The viewer follows Tab.CurPage / Tab.ReloadGen / Tab.ZoomMode changes
//     between builds (it keeps last-seen values keyed by tab id) — so the
//     sidebar, outline, toolbar and palette simply mutate state via
//     Navigate/SetZoom/etc. and the viewer reacts.
//   - When you implement a stub, delete exactly that stub from this file.
package app

import (
	"github.com/egoist/mygo"
)

// window is the main window, set by main.go (D) immediately after creation.
var window *mygo.Window

// SetWindow installs the main window; main.go calls it right after creating
// the window so background goroutines can hop to the UI thread via update.
func SetWindow(w *mygo.Window) { window = w }

// SearchFocus is the focus request channel for the toolbar search field:
// shortcuts set it to a non-empty token (e.g. "search-<n>", unique per
// request) and the toolbar's search field FocusBinds to it, taking focus on
// the next build. See the mygo-maintenance skill's FocusBind pattern.
var SearchFocus string

// update runs fn on the UI thread. Safe from any goroutine. Before the
// window exists (unit tests) fn runs inline on the calling goroutine.
func update(fn func()) {
	if window != nil {
		window.Update(fn)
		return
	}
	fn()
}

// Update is the exported variant of update for package main.
func Update(fn func()) { update(fn) }

// ---- C, tabs.go / toolbar.go / sidebar.go / statusbar.go ---------------
// Implemented in tabs.go (tab strip), toolbar.go (nav / search / zoom
// controls), sidebar.go (thumbnails + outline) and statusbar.go (page X/Y,
// fit, zoom, theme toggle) by Agent C.
