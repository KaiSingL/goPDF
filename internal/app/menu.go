// menu.go builds the native application menu bar shown on macOS, where
// menus live in the system menu bar rather than in the window — the
// integrated title bar of titlebar.go is the Windows convention and
// stays there. The template mirrors the inline menus of titlebar.go
// (same actions, same guards) plus the Mac-standard structure: the app
// menu (About / Services / Hide / Quit — mygo fills the roles), an Edit
// menu of editing roles so ⌘C/⌘V are menu-backed the Mac way, and the
// Window menu role.
//
// main.go installs it with mygo.App.SetMenu, darwin only. Menu
// accelerators share keys with the window shortcuts of shortcuts.go on
// purpose: the native menu performs the key equivalent itself, so the
// key runs once through the menu's Click (the same precedence the old
// Windows menu bar had).
package app

import (
	"github.com/egoist/mygo"
)

// themeMenuItems holds the three Theme checkboxes of the native View
// menu so their checkmarks can follow theme changes made elsewhere
// (palette, inline menus on Windows, the theme cycle). Nil until
// BuildMenu runs.
var themeMenuItems = map[string]*mygo.MenuItem{}

// syncThemeMenu re-checks the native Theme submenu's checkbox for the
// current ThemeMode. ApplyTheme calls it after every theme change;
// before the menu exists (startup on Windows) the map is empty and it
// is a no-op.
func syncThemeMenu() {
	for mode, it := range themeMenuItems {
		it.SetChecked(mode == ThemeMode)
	}
}

// BuildMenu builds the native application menu bar; main calls
// mygo.App.SetMenu with it before Run. The click wrappers hop through
// Update like every other entry point that arrives from outside the
// frame, and the guarded shortcut closures of shortcuts.go are reused
// so menu, inline menus and keyboard shortcuts share one behavior.
func BuildMenu(s *AppState) *mygo.Menu {
	// run mutates the state on the UI thread from a menu click.
	run := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { Update(fn) }
	}
	// withActive applies fn to the active tab, if any (withActiveTab of
	// titlebar.go, in Click signature).
	withActive := func(fn func(t *Tab)) func(*mygo.MenuItem, *mygo.Window) {
		return run(func() {
			if t := s.Active(); t != nil {
				fn(t)
			}
		})
	}
	// themeItem is one checkbox of the Theme submenu, the check
	// marking the current ThemeMode (syncThemeMenu keeps it current).
	themeItem := func(label, mode string) *mygo.MenuItem {
		it := &mygo.MenuItem{
			Label:   label,
			Type:    mygo.MenuItemCheckbox,
			Checked: ThemeMode == mode,
			Click:   run(func() { SetThemeMode(mode) }),
		}
		themeMenuItems[mode] = it
		return it
	}

	return mygo.NewMenu([]*mygo.MenuItem{
		// The app menu: About, Services, Hide, Quit. Quit owns ⌘Q here,
		// so the File menu repeats none of it.
		{Role: mygo.RoleAppMenu},
		{Label: "File", Submenu: []*mygo.MenuItem{
			{Label: "Open…", Accelerator: "CmdOrCtrl+O",
				Click: run(OpenFilePicker)},
			// mygo fills this submenu from its recent documents; an
			// entry arrives through App.OnOpenFile (main.go), the same
			// funnel as every other opener.
			{Role: mygo.RoleOpenRecent},
			mygo.Separator(),
			{Label: "Print…", Accelerator: "CmdOrCtrl+P", Click: run(PrintActive)},
			{Label: "Document Properties",
				Click: run(func() { s.ShowProperties = s.Active() != nil })},
			mygo.Separator(),
			{Label: "Reopen Closed Tab", Accelerator: "CmdOrCtrl+Shift+T",
				Click: run(func() { s.ReopenClosedTab() })},
			{Label: "Close Tab", Accelerator: "CmdOrCtrl+W",
				Click: run(func() { s.CloseActiveTab() })},
		}},
		// Undo/redo/cut/copy/paste/select-all with their standard
		// accelerators — the search field's text editing is menu-backed.
		{Role: mygo.RoleEditMenu},
		{Label: "View", Submenu: []*mygo.MenuItem{
			{Label: "Zoom In", Accelerator: "CmdOrCtrl+=",
				Click: run(zoomIn(s))},
			{Label: "Zoom Out", Accelerator: "CmdOrCtrl+-",
				Click: run(zoomOut(s))},
			{Label: "Reset Zoom", Accelerator: "CmdOrCtrl+0",
				Click: run(zoomReset(s))},
			mygo.Separator(),
			{Label: "Fit Width",
				Click: withActive(func(t *Tab) { t.SetZoomMode(ZoomFitWidth) })},
			{Label: "Fit Page",
				Click: withActive(func(t *Tab) { t.SetZoomMode(ZoomFitPage) })},
			{Label: "Rotate Clockwise",
				Click: withActive(func(t *Tab) { t.RotateBy() })},
			mygo.Separator(),
			{Label: "Toggle Sidebar",
				Click: run(func() { s.ToggleSidebar() })},
			{Label: "Theme", Submenu: []*mygo.MenuItem{
				themeItem("Follow System", "auto"),
				themeItem("Dark", "dark"),
				themeItem("Light", "light"),
			}},
		}},
		{Label: "Go", Submenu: []*mygo.MenuItem{
			{Label: "Next Page", Accelerator: "PageDown",
				Click: withActive(func(t *Tab) { t.Navigate(t.CurPage+1, 0) })},
			{Label: "Previous Page", Accelerator: "PageUp",
				Click: withActive(func(t *Tab) { t.Navigate(t.CurPage-1, 0) })},
			{Label: "First Page", Accelerator: "Home",
				Click: withActive(func(t *Tab) { t.Navigate(0, 0) })},
			{Label: "Last Page", Accelerator: "End",
				Click: withActive(func(t *Tab) { t.Navigate(t.NumPages-1, 0) })},
			mygo.Separator(),
			{Label: "Back", Accelerator: "Alt+Left",
				Click: withActive(func(t *Tab) { t.Back() })},
			{Label: "Forward", Accelerator: "Alt+Right",
				Click: withActive(func(t *Tab) { t.Forward() })},
		}},
		// Minimize/Zoom and the open-windows list (macOS).
		{Role: mygo.RoleWindowMenu},
	})
}
