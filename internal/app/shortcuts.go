// shortcuts.go owns the app-wide keyboard bindings and the open-file
// plumbing shared by the shell: window shortcuts (Ctrl+O/W/T/F/K…, zoom,
// page turns), the system open dialog, and OpenPath, which everything
// that opens a document goes through (dialog, drag & drop, file
// association double-click, second instance, Open Recent, palette).
//
// The bindings are declared with c.OnShortcut in the window's root scope
// (the root view calls shortcuts(c, s) first — see components.go), which
// makes them fire wherever the focus is, unless a focused element takes
// the key first (verified against ui.Context.Shortcut in the pinned
// MyGo v0.6.4 source). On Windows the menu bar takes the keys of its
// non-role items before the view sees them, so an accelerator that is
// both a menu item and a binding here runs exactly once.
package app

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	gopdfprint "gopdf/internal/print"
)

// current is the app state, installed by main at startup so the argument-
// less contract functions (openFilePicker) and menu handlers can reach it.
// Nil before main runs and in the view tests of the other modules.
var current *AppState

// SetState installs the app state; main calls it once, before Run.
func SetState(s *AppState) { current = s }

// searchFocusSeq numbers SearchFocus requests so every one is a fresh,
// unique token (the search panel's field FocusBinds to it; equal values
// would not re-trigger focus).
var searchFocusSeq atomic.Uint64

// shortcuts declares the window-scope keyboard bindings (spec §5).
func shortcuts(c *ui.Context, s *AppState) {
	c.OnShortcut(ui.Ctrl, ui.KeyO, openFilePicker)

	c.OnShortcut(ui.Ctrl|ui.Shift, ui.KeyT, func() { s.ReopenClosedTab() })
	c.OnShortcut(ui.Ctrl, ui.KeyW, func() { s.CloseActiveTab() })
	c.OnShortcut(ui.Ctrl, ui.KeyP, PrintActive)

	// Ctrl+F opens the search panel AND stores a fresh focus token, which
	// the panel's field (searchpanel.go) FocusBinds to: the panel opens
	// already focused, so typing starts immediately.
	c.OnShortcut(ui.Ctrl, ui.KeyF, func() {
		s.ShowSearchPanel = true
		SearchFocus = fmt.Sprintf("search-%d", searchFocusSeq.Add(1))
	})

	// Zoom: Ctrl+= / Ctrl++ (Shift+=) in, Ctrl+- out, Ctrl+0 reset.
	c.OnShortcut(ui.Ctrl, ui.KeyEqual, zoomIn(s))
	c.OnShortcut(ui.Ctrl|ui.Shift, ui.KeyEqual, zoomIn(s))
	c.OnShortcut(ui.Ctrl, ui.KeyMinus, zoomOut(s))
	c.OnShortcut(ui.Ctrl, ui.Key0, zoomReset(s))

	// Text selection (selection.go): Ctrl+C copies the viewer selection.
	// A focused text input takes Ctrl+C first for its own copy — this is
	// the window-scope fallback, as with the arrows.
	c.OnShortcut(ui.Ctrl, ui.KeyC, func() {
		if t := s.Active(); t != nil {
			if text := t.SelectionText(); text != "" {
				c.WriteClipboard(text)
			}
		}
	})

	// Page turns and jumps. Left/Right turn pages too: focused text inputs
	// take the arrows first for the caret and focused tree items to
	// open/close (editor.wants / arrows() in the pinned source), so these
	// fire when the reader itself has the keyboard. Declaring the shortcut
	// also claims the keys before the engine's arrow-to-scroll fallback.
	c.OnShortcut(0, ui.KeyPageDown, nextPage(s))
	c.OnShortcut(0, ui.KeyPageUp, prevPage(s))
	c.OnShortcut(0, ui.KeyRight, nextPage(s))
	c.OnShortcut(0, ui.KeyLeft, prevPage(s))
	c.OnShortcut(0, ui.KeyHome, firstPage(s))
	c.OnShortcut(0, ui.KeyEnd, lastPage(s))

	// Command palette and in-document search stepping.
	c.OnShortcut(ui.Ctrl, ui.KeyK, func() { s.ShowPalette = !s.ShowPalette })
	c.OnShortcut(0, ui.KeyF3, func() { s.StepSearch(s.Active(), +1) })
	c.OnShortcut(ui.Shift, ui.KeyF3, func() { s.StepSearch(s.Active(), -1) })

	// Escape closes the palette, else the search panel, else clears a
	// text selection (one binding: a duplicate would shadow whichever
	// registered first). While the palette's modal shows, window
	// shortcuts wait and the modal closes on Escape by itself; this takes
	// the frame where the flag flipped but the modal is not built yet.
	// (When the panel's focused field takes Escape itself - to clear the
	// query, or to close on an empty field - this binding does not fire:
	// a focused element takes the key first.)
	c.OnShortcut(0, ui.KeyEscape, func() {
		switch {
		case s.ShowPalette:
			s.ShowPalette = false
		case s.ShowSearchPanel:
			s.ShowSearchPanel = false
		default:
			if t := s.Active(); t != nil && t.Sel.Active {
				t.ClearSelection()
			}
		}
	})
}

func zoomIn(s *AppState) func() {
	return func() {
		if t := s.Active(); t != nil {
			t.ZoomIn()
		}
	}
}

func zoomOut(s *AppState) func() {
	return func() {
		if t := s.Active(); t != nil {
			t.ZoomOut()
		}
	}
}

func zoomReset(s *AppState) func() {
	return func() {
		if t := s.Active(); t != nil {
			t.SetZoom(1)
		}
	}
}

func nextPage(s *AppState) func() {
	return func() {
		if t := s.Active(); t != nil {
			t.Navigate(t.CurPage+1, 0)
		}
	}
}

func prevPage(s *AppState) func() {
	return func() {
		if t := s.Active(); t != nil {
			t.Navigate(t.CurPage-1, 0)
		}
	}
}

func firstPage(s *AppState) func() {
	return func() {
		if t := s.Active(); t != nil {
			t.Navigate(0, 0)
		}
	}
}

func lastPage(s *AppState) func() {
	return func() {
		if t := s.Active(); t != nil && t.NumPages > 0 {
			t.Navigate(t.NumPages-1, 0)
		}
	}
}

// OpenPath opens a document in a tab and records it with the system
// (Windows jump list) — the one entry point for "user opened this file".
// Safe from any goroutine and from main-thread callbacks that arrive
// before the window exists: the state change hops through Window.Update
// once the window is set, and runs inline before that (main thread).
func OpenPath(path string) {
	update(func() {
		if current == nil {
			return
		}
		if _, err := current.OpenTab(path); err != nil {
			log.Printf("gopdf: open %s: %v", path, err)
			return
		}
		// OpenTab recorded the path in s.Recents; mirror it into the
		// system's recent documents (jump list). AddRecentDocument panics
		// on the main thread before App.Run, which the window != nil check
		// rules out.
		if window != nil {
			if err := mygo.App.AddRecentDocument(path); err != nil {
				log.Printf("gopdf: recent %s: %v", path, err)
			}
		}
	})
}

// OpenFilePicker shows the system open dialog; exported for the File menu.
func OpenFilePicker() { openFilePicker() }

// PrintActive prints the active tab's document: the dialog blocks, so it
// runs off the UI thread (internal/print guards reentry itself). No tab
// or a launcher tab is a no-op.
func PrintActive() {
	update(func() {
		if current == nil {
			return
		}
		t := current.Active()
		if t == nil || t.Doc == nil {
			return
		}
		doc, name := t.Doc, t.Title
		go func() {
			if _, err := gopdfprint.Print(doc, name); err != nil {
				log.Printf("gopdf: print: %v", err)
			}
		}()
	})
}

// openFilePicker shows the native open dialog (PDF filter, per spec v1)
// and opens what the user chose. Dialog.Open blocks until dismissed, so
// it runs off the UI thread; the results come back through OpenPath's
// Window.Update hop.
func openFilePicker() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title:   "Open PDF",
			Filters: []mygo.FileFilter{{Name: "PDF Documents", Extensions: []string{"pdf"}}},
		})
		if err != nil {
			log.Printf("gopdf: open dialog: %v", err)
			return
		}
		for _, p := range paths {
			OpenPath(p)
		}
	}()
}

// LooksLikePDF reports whether path has the PDF extension (case-insensitive).
func LooksLikePDF(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".pdf")
}
