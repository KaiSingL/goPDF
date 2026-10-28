// titlebar.go renders the integrated title bar: the app icon, the inline
// File / View / Go menus, and the drag region, drawn in the band the
// window controls otherwise share (main.go creates the window with
// mygo.TitleBarHidden, which hides the native title bar but keeps the
// min/max/close buttons top-right). It replaces the native menu bar:
// the menus live here now, while the keyboard shortcuts stay window-scope
// in shortcuts.go, so every accelerator keeps working without a menu.
//
// API notes (verified against mygo v0.6.4 source):
//   - c.TitleBar() is the room the window controls take (zero in windows
//     without a hidden title bar — and headless, so tests build a 32-high
//     bar): pad the row clear of the buttons on the right and let it drag
//     the window (DragWindow; double-click maximizes). Only the innermost
//     pointer target decides: the menu buttons are interactive children,
//     so a press on them opens the menu instead of dragging, while the
//     icon and the empty stretches drag.
//   - Element.Menu makes the element a pop-up menu button; its build runs
//     when the menu opens and again in the frame after a choice, where the
//     item's Chosen reports it (the same idiom as toolbar.go's more menu).
//     Accelerator labels are cosmetic here on purpose: the real bindings
//     are the window shortcuts, and a shown key must not also be claimed
//     by the menu (the native menu bar had to care; a view menu does not).
package app

import (
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// appIcon is the title bar's app icon, decoded by main from
// resources/icon.png before Run (SetAppIcon). Nil — the file is missing
// next to the exe, or not an image — and the bar renders without it.
var appIcon *ui.Bitmap

// SetAppIcon installs the title bar's app icon; main calls it at startup,
// before Run. Optional: without it the bar simply starts with the menus.
func SetAppIcon(bm *ui.Bitmap) { appIcon = bm }

// titleBarView renders the title bar row: the app icon (when set), then
// the inline menus. The row is the drag handle everywhere except over its
// menu buttons.
func titleBarView(c *ui.Context, s *AppState) {
	th := c.Theme()
	// The row fills the title-bar band and clears the window buttons on
	// the right (bar.Left is the macOS traffic lights' inset). 32 is the
	// height mygo documents for windows that report no band (headless
	// tests, full screen): keep a usable bar there too.
	bar := c.TitleBar()
	ui.Row(c).Height(max(bar.Height, 32)).
		Padding(0, bar.Right, 0, bar.Left).
		Gap(th.Space(2)).AlignItems(ui.Center).DragWindow().
		Children(func() {
			if appIcon != nil {
				ui.Image(c, appIcon).Size(18, 18)
			}
			titleFileMenu(c, s)
			titleViewMenu(c, s)
			titleGoMenu(c, s)
		})
}

// titleMenuButton is one flat menu button of the bar: muted label, no look
// until hovered (the chrome icon-button idiom of tabs.go), opening its
// menu on pointer-down — or Enter/Space/Down while focused — and taking
// the focus, so it is reachable by keyboard. Menu pops over the drag row:
// the press lands on the button, not the drag.
func titleMenuButton(c *ui.Context, label string, build func(m *ui.Menu)) {
	th := c.Theme()
	b := ui.ButtonBase(c).Label(label).
		PaddingX(th.Space(2)).Radius(th.Space(1))
	if b.Hovered() {
		b.Background(th.SurfaceHover)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(th.TextMuted)
	})
	b.Menu(build)
}

// menuItem adds one item that runs fn when chosen.
func menuItem(m *ui.Menu, label string, fn func()) {
	if m.Item(label).Chosen() {
		fn()
	}
}

// withActiveTab guards a menu action on the active tab, as the native
// menu's withActive did: no tab (or a launcher tab) and it is a no-op.
func withActiveTab(s *AppState, fn func(t *Tab)) func() {
	return func() {
		if t := s.Active(); t != nil {
			fn(t)
		}
	}
}

// titleFileMenu is the File menu: opening and printing, the document
// properties dialog, tab handling, quit — the native menu bar's items,
// with the same actions. Opening a recent goes through OpenPath, the one
// entry point that opens documents.
func titleFileMenu(c *ui.Context, s *AppState) {
	titleMenuButton(c, "File", func(m *ui.Menu) {
		menuItem(m, "Open…", OpenFilePicker)
		// Recent documents, newest first (state.addRecent). The native
		// menu let mygo fill its RoleOpenRecent itself; here the submenu
		// reads s.Recents, which OpenPath keeps current.
		m.Submenu("Open Recent", func(m *ui.Menu) {
			if len(s.Recents) == 0 {
				m.Item("No Recent Documents").Disabled(true)
				return
			}
			for _, p := range s.Recents {
				menuItem(m, filepath.Base(p), func() { OpenPath(p) })
			}
		})
		m.Separator()
		menuItem(m, "Print…", PrintActive)
		menuItem(m, "Document Properties", func() {
			s.ShowProperties = s.Active() != nil
		})
		m.Separator()
		menuItem(m, "Reopen Closed Tab", func() { s.ReopenClosedTab() })
		menuItem(m, "Close Tab", func() { s.CloseActiveTab() })
		m.Separator()
		menuItem(m, "Quit", mygo.App.Quit)
	})
}

// titleViewMenu is the View menu: zoom and fit (the zoom actions are the
// shortcut helpers of shortcuts.go — the same guarded closures), rotate,
// the sidebar, and dark mode. Zoom and fit need a document; without one
// the guard is a no-op, as it was for the native menu.
func titleViewMenu(c *ui.Context, s *AppState) {
	titleMenuButton(c, "View", func(m *ui.Menu) {
		menuItem(m, "Zoom In", zoomIn(s))
		menuItem(m, "Zoom Out", zoomOut(s))
		menuItem(m, "Reset Zoom", zoomReset(s))
		m.Separator()
		menuItem(m, "Fit Width", withActiveTab(s, func(t *Tab) { t.SetZoomMode(ZoomFitWidth) }))
		menuItem(m, "Fit Page", withActiveTab(s, func(t *Tab) { t.SetZoomMode(ZoomFitPage) }))
		menuItem(m, "Rotate Clockwise", withActiveTab(s, func(t *Tab) { t.RotateBy() }))
		m.Separator()
		menuItem(m, "Toggle Sidebar", func() { s.ToggleSidebar() })
		menuItem(m, "Dark Mode", ToggleTheme)
	})
}

// titleGoMenu is the Go menu: page turns and jumps (the shortcut helpers),
// then per-tab history.
func titleGoMenu(c *ui.Context, s *AppState) {
	titleMenuButton(c, "Go", func(m *ui.Menu) {
		menuItem(m, "Next Page", nextPage(s))
		menuItem(m, "Previous Page", prevPage(s))
		menuItem(m, "First Page", firstPage(s))
		menuItem(m, "Last Page", lastPage(s))
		m.Separator()
		menuItem(m, "Back", withActiveTab(s, func(t *Tab) { t.Back() }))
		menuItem(m, "Forward", withActiveTab(s, func(t *Tab) { t.Forward() }))
	})
}
