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
	"github.com/egoist/mygo/ui"
)

// window is the main window, set by main.go (D) immediately after creation.
var window *mygo.Window

// update runs fn on the UI thread. Safe from any goroutine. Before the
// window exists (unit tests) fn runs inline on the calling goroutine.
func update(fn func()) {
	if window != nil {
		window.Update(fn)
		return
	}
	fn()
}

// ---- B, viewer.go ------------------------------------------------------
// viewer renders the scrollable page area for the active tab: requests
// page renders through Doc.RenderAsync, caches bitmaps, applies zoom/fit/
// rotation with a 150ms debounced re-render at the settled DPI, tracks the
// current page from scroll position, draws search-hit highlight rectangles
// scaled by dpi/72, and centers pages (max width 900 DIPs) with shadow.
func viewer(c *ui.Context, s *AppState) {}

// ---- C, tabs.go --------------------------------------------------------
// tabBar renders the tab strip: one tab per s.Tabs (title, close button,
// active state), a "+" button calling openFilePicker, double-click on empty
// strip space also opening the picker, and middle-click/Close on a tab
// calling s.CloseTab. Keep stable keys: c.Key(t.ID).
func tabBar(c *ui.Context, s *AppState) {}

// ---- C, toolbar.go -----------------------------------------------------
// toolbar renders the second bar: back/forward (enabled from Tab.History/
// Future, calling Tab.Back/Forward), reload (Tab.Reload), a search field
// bound to the active tab's Search.Query (submit → s.StartSearch + s.
// RunSearch; Enter/F3 → s.StepSearch(+1), Shift+F3/Enter → -1), zoom out /
// percent display / zoom in (Tab.ZoomOut / ZoomPercent / ZoomIn), a zoom
// mode control (Tab.SetZoomMode), share + comment buttons (disabled, v1)
// and a more menu (rotate, sidebar toggle, palette).
func toolbar(c *ui.Context, s *AppState) {}

// ---- C, sidebar.go -----------------------------------------------------
// sidebar renders the left panel when s.ShowSidebar: a Thumbnails/Outline
// switch (s.SetSidebarTab), then either the virtualized thumbnail list
// (lazy 72 DPI renders via Doc.RenderAsync + Tab.Cache, current page
// highlighted, click → Tab.Navigate) or the outline tree from Tab.Outline
// (click → Tab.Navigate; -1 pages disabled).
func sidebar(c *ui.Context, s *AppState) {}

// ---- C, statusbar.go ---------------------------------------------------
// statusBar renders the bottom bar: "Page X of Y" with an editable page
// number (submit → Tab.Navigate), the fit mode, zoom percent, a dark-mode
// toggle (toggleTheme) and the app title on empty state.
func statusBar(c *ui.Context, s *AppState) {}

// ---- D, palette.go -----------------------------------------------------
// paletteOverlay renders the Ctrl+K command palette over the UI when
// s.ShowPalette: a query field and results for recent documents
// (s.Recents → s.OpenTab), outline entries of the active tab (→ Navigate),
// "page N" number jump, and commands (zoom in/out/reset, rotate, toggle
// sidebar, toggle theme, close tab). Escape/selection closes it.
func paletteOverlay(c *ui.Context, s *AppState) {}

// ---- D, shortcuts.go / actions -----------------------------------------
// openFilePicker shows the system open dialog (PDF filter) and opens the
// chosen file with s.OpenTab (+ App.AddRecentDocument).
func openFilePicker() {}

// toggleTheme flips the manual theme override (auto → dark → light → auto)
// and applies it immediately.
func toggleTheme() {}
