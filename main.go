// Command gopdf is the GoPDF app shell: lifecycle, single instance, the
// main window with its integrated title bar (app icon, inline menus and
// drag region — internal/app/titlebar.go), and everything that arrives
// from outside the app — file-association double-clicks, the jump list and
// recent documents (through App.OnOpenFile), a PDF on the command line, a
// second launch's arguments, and files dropped on the window.
//
// All of those funnel into app.OpenPath, which opens a tab and records
// the document with the system; the window itself is the contract view of
// internal/app (state.View), created in WhenReady.
package main

import (
	_ "embed"
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"gopdf/internal/app"
)

// iconPNG is the app icon the title bar shows, embedded so it loads from
// any working directory (same source file mygo embeds as the exe icon).
//
//go:embed resources/icon.png
var iconPNG []byte

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

	// The title bar's app icon (internal/app/titlebar.go), decoded before
	// Run: DecodeBitmap needs no running app. The PNG is EMBEDDED in the
	// binary — a path relative to the working directory would miss when
	// the OS launches the exe from elsewhere (a file-association
	// double-click starts it with the PDF's folder as CWD).
	if bm, err := ui.DecodeBitmap(iconPNG); err == nil {
		app.SetAppIcon(bm)
	}

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

	// Persist the settings (and the theme mode) when a quit starts.
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		f := app.SettingsFile{Settings: state.Settings, ThemeMode: app.ThemeMode}
		if err := app.SaveSettings(f); err != nil {
			log.Printf("gopdf: save settings: %v", err)
		}
	})

	mygo.App.WhenReady(func() {
		// Hidden native title bar: the view draws the whole window and
		// the native min/max/close buttons stay top-right, over the band
		// internal/app's title bar leaves clear (its drag region and its
		// inline menus replace the caption; the menus replace the native
		// menu bar, whose accelerators survive as window shortcuts).
		// 40 is tall enough for the icon, the menus and the name.
		win = mygo.NewWindow(mygo.WindowOptions{
			Title:          "GoPDF",
			Width:          1200,
			Height:         800,
			MinWidth:       800,
			MinHeight:      600,
			TitleBarStyle:  mygo.TitleBarHidden,
			TitleBarHeight: 40,
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
		// Recent Documents section and the title bar's File ▸ Open
		// Recent submenu (s.Recents).
		app.Update(func() {
			app.SeedRecents(state, mygo.App.RecentDocuments())
		})
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
