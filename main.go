// Command gopdf is the GoPDF app shell: lifecycle, single instance, the
// application menu, the main window, and everything that arrives from
// outside the app — file-association double-clicks, the jump list and the
// Open Recent menu (both through App.OnOpenFile), a PDF on the command
// line, a second launch's arguments, and files dropped on the window.
//
// All of those funnel into app.OpenPath, which opens a tab and records
// the document with the system; the window itself is the contract view of
// internal/app (state.View), created in WhenReady.
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"gopdf/internal/app"
)

func main() {
	state := app.NewAppState()

	// Persisted settings and theme, before the window: mygo documents
	// Theme.SetSource as callable before App.Run (Run applies it).
	if f, err := app.LoadSettings(); err != nil {
		if !os.IsNotExist(err) {
			log.Printf("gopdf: load settings: %v (using defaults)", err)
		}
	} else {
		state.Settings = f.Merged()
		app.ThemeMode = app.NormalizeThemeMode(f.ThemeMode)
	}
	app.SetState(state)
	app.ApplyThemeAtStartup()

	// Single instance: in a later launch this returns false after handing
	// the command line to the first instance, which must then exit.
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

	// The window, created in WhenReady; OnSecondInstance below focuses it.
	var win *mygo.Window

	// A second launch: open the PDFs among its arguments (relative to its
	// working directory) and bring the first instance to the front. Files
	// among the args also arrive through OnOpenFile; OpenTab dedupes.
	mygo.App.OnSecondInstance(func(args []string, workingDir string) {
		for _, a := range args {
			if !app.LooksLikePDF(a) {
				continue
			}
			p := a
			if !filepath.IsAbs(p) {
				p = filepath.Join(workingDir, p)
			}
			app.OpenPath(p)
		}
		if win != nil {
			win.Focus()
		}
	})

	// File-association double-clicks, jump-list and Open Recent choices,
	// and the PDFs on this launch's command line all arrive here.
	mygo.App.OnOpenFile(func(path string) { app.OpenPath(path) })

	// Menu bar (Windows/Linux). Built once; MyGo maintains the Open
	// Recent submenu itself as documents are opened.
	mygo.App.SetMenu(buildMenu(state))

	// Persist the settings (and the theme mode) when a quit starts.
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		f := app.SettingsFile{Settings: state.Settings, ThemeMode: app.ThemeMode}
		if err := app.SaveSettings(f); err != nil {
			log.Printf("gopdf: save settings: %v", err)
		}
	})

	mygo.App.WhenReady(func() {
		win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "GoPDF",
			Width:     1200,
			Height:    800,
			MinWidth:  800,
			MinHeight: 600,
			// Restores where the user left the window.
			StateKey: "main",
			// The window shows the interface MyGo draws, not a web page.
			Content: ui.View(state.View),
		})
		// Before anything else can hop to the UI thread.
		app.SetWindow(win)

		// Drop a PDF on the window to open it.
		win.OnFileDrop(func(e *mygo.FileDropEvent) {
			for _, p := range e.Paths {
				if app.LooksLikePDF(p) {
					app.OpenPath(p)
				}
			}
		})

		// Documents from earlier runs, newest first, feed the palette's
		// Recent Documents section. (The File ▸ Open Recent menu is
		// MyGo's own RoleOpenRecent.)
		app.Update(func() {
			app.SeedRecents(state, mygo.App.RecentDocuments())
		})
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// buildMenu builds the application menu: File, View, Go and the standard
// Window roles. Menu accelerators share keys with the window shortcuts of
// internal/app/shortcuts.go on purpose: on Windows a menu item without a
// role takes its key before the view sees it (surface.menuShortcut in the
// pinned MyGo source), so each press runs exactly once.
func buildMenu(state *app.AppState) *mygo.Menu {
	// run mutates the state on the UI thread from a menu click.
	run := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { app.Update(fn) }
	}
	// withActive applies fn to the active tab, if any.
	withActive := func(fn func(t *app.Tab)) func(*mygo.MenuItem, *mygo.Window) {
		return run(func() {
			if t := state.Active(); t != nil {
				fn(t)
			}
		})
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "File", Submenu: []*mygo.MenuItem{
			{Label: "Open…", Accelerator: "CmdOrCtrl+O",
				Click: func(*mygo.MenuItem, *mygo.Window) { app.OpenFilePicker() }},
			// MyGo fills this submenu from its recent documents; an
			// entry opens through OnOpenFile above.
			{Role: mygo.RoleOpenRecent},
			mygo.Separator(),
			{Label: "Print…", Accelerator: "CmdOrCtrl+P",
				Click: func(*mygo.MenuItem, *mygo.Window) { app.PrintActive() }},
			{Label: "Document Properties",
				Click: run(func() { state.ShowProperties = state.Active() != nil })},
			{Label: "Reopen Closed Tab", Accelerator: "CmdOrCtrl+Shift+T",
				Click: run(func() { state.ReopenClosedTab() })},
			{Label: "Close Tab", Accelerator: "CmdOrCtrl+W",
				Click: run(func() { state.CloseActiveTab() })},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}},
		{Label: "View", Submenu: []*mygo.MenuItem{
			{Label: "Zoom In", Accelerator: "CmdOrCtrl+=",
				Click: withActive(func(t *app.Tab) { t.ZoomIn() })},
			{Label: "Zoom Out", Accelerator: "CmdOrCtrl+-",
				Click: withActive(func(t *app.Tab) { t.ZoomOut() })},
			{Label: "Reset Zoom", Accelerator: "CmdOrCtrl+0",
				Click: withActive(func(t *app.Tab) { t.SetZoom(1) })},
			mygo.Separator(),
			{Label: "Fit Width",
				Click: withActive(func(t *app.Tab) { t.SetZoomMode(app.ZoomFitWidth) })},
			{Label: "Fit Page",
				Click: withActive(func(t *app.Tab) { t.SetZoomMode(app.ZoomFitPage) })},
			{Label: "Rotate Clockwise",
				Click: withActive(func(t *app.Tab) { t.RotateBy() })},
			mygo.Separator(),
			{Label: "Toggle Sidebar", Click: run(func() { state.ToggleSidebar() })},
			{Label: "Dark Mode", Click: run(func() { app.ToggleTheme() })},
		}},
		{Label: "Go", Submenu: []*mygo.MenuItem{
			{Label: "Next Page", Accelerator: "PageDown",
				Click: withActive(func(t *app.Tab) { t.Navigate(t.CurPage+1, 0) })},
			{Label: "Previous Page", Accelerator: "PageUp",
				Click: withActive(func(t *app.Tab) { t.Navigate(t.CurPage-1, 0) })},
			{Label: "First Page", Accelerator: "Home",
				Click: withActive(func(t *app.Tab) { t.Navigate(0, 0) })},
			{Label: "Last Page", Accelerator: "End",
				Click: withActive(func(t *app.Tab) { t.Navigate(t.NumPages-1, 0) })},
			mygo.Separator(),
			{Label: "Back", Accelerator: "Alt+Left",
				Click: withActive(func(t *app.Tab) { t.Back() })},
			{Label: "Forward", Accelerator: "Alt+Right",
				Click: withActive(func(t *app.Tab) { t.Forward() })},
		}},
		// Minimize and Close on Windows/Linux; the full window menu on
		// macOS.
		{Role: mygo.RoleWindowMenu},
	})
}
