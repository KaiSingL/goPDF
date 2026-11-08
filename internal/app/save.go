// save.go is the save wiring (issue 3, part 4): the tab dirty flag's
// consumers, the same-path save (toolbar, Ctrl+S, File menu), Save-As
// through the native picker (Ctrl+Shift+S), and the close-with-unsaved
// notice. The annot mutations mark the dirty flag (annotate.go's
// annotDone); this file only saves it and shows it.
//
// # Save routing (the facade's contract, restated for the app side)
//
// Same-path saves go through SaveAsync(t.Path, SaveOptions{}) and the
// facade decides the mechanics: an atomic sibling-temp + rename on POSIX,
// demoted to an incremental save on Windows; the app does not care. A
// foreign path (Save-As) takes the fork's own atomic temp+rename, which
// is what leaves the ORIGINAL file untouched.
//
// SaveOptions stays zero-value on purpose: the fork's EditAnnot
// regenerates annotation appearances on every edit (verified in the
// pinned pdf_edit_cgo.go), so facade-created annots already carry /AP
// and the Appearance flag would change nothing.
//
// # Close-ordering promise
//
// SaveAsync's callback is a promise the app honors: Close drops queued
// lo tasks, so a save still queued when the doc closes is silently
// lost. This wiring never closes a doc to save it — the tab stays open
// through Ctrl+S, the callback fires, and the dot clears only in that
// callback's success half. Closing a DIRTY tab deliberately closes
// WITHOUT saving (tabs.go has no confirm pattern; see the commit for
// the choice) and says so in the status bar. A save pending at QUIT is
// dropped the same way; the app prefers that visible state (the dot,
// the status notice) over a synchronous save on the UI thread during
// teardown, which the facade's own docs rule out.
package app

import (
	"fmt"
	"log"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/gen2brain/go-fitz"
)

// saveTab writes the tab's document back to its own path through the
// facade's same-path routing. A no-op on clean tabs (plain reads never
// touch the file). The dot clears in the callback's success half only;
// a failure surfaces through StatusNotice and keeps it.
func (s *AppState) saveTab(t *Tab) {
	if t == nil || t.Doc == nil || !t.Dirty {
		return
	}
	path := t.Path
	t.Doc.SaveAsync(path, fitz.SaveOptions{}, func(err error) {
		updateFromWorker(func() { // hop, never inline
			cur := s.tabByID(t.ID)
			if cur == nil || cur.Doc == nil {
				return // stale: tab closed while the save was in flight
			}
			if err != nil {
				s.StatusNotice = fmt.Sprintf("%s: save: %v", filepath.Base(cur.Path), err)
				return // the dot stays until a save succeeds
			}
			cur.Dirty = false
			if window != nil {
				window.Invalidate()
			}
		})
	})
}

// saveActiveAs saves the active tab's document to a user-chosen path:
// the native save dialog blocks, so it runs off the UI thread and the
// choice comes back through a Window.Update hop (the openFilePicker
// pattern). The original file stays untouched: the facade routes the
// foreign target through the fork's atomic temp+rename.
func (s *AppState) saveActiveAs() {
	t := s.Active()
	if t == nil || t.Doc == nil || window == nil { // headless tests: no dialogs before Run
		return
	}
	tabID, suggest := t.ID, t.Path
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Title:       "Save As",
			DefaultPath: suggest,
			Filters:     []mygo.FileFilter{{Name: "PDF Documents", Extensions: []string{"pdf"}}},
		})
		if err != nil {
			log.Printf("gopdf: save dialog: %v", err)
			return
		}
		if path == "" {
			return // canceled
		}
		update(func() {
			if current == nil {
				return
			}
			if cur := current.tabByID(tabID); cur != nil && cur.Doc != nil {
				current.saveTabAs(cur, path)
			}
		})
	}()
}

// saveTabAs writes the tab's document to the chosen foreign path. The
// edits are persisted (the dot clears — the in-memory doc now equals a
// saved file), and the status bar names the new file; the tab keeps
// pointing at the original.
func (s *AppState) saveTabAs(t *Tab, path string) {
	if t == nil || t.Doc == nil {
		return
	}
	t.Doc.SaveAsync(path, fitz.SaveOptions{}, func(err error) {
		updateFromWorker(func() { // hop, never inline
			cur := s.tabByID(t.ID)
			if cur == nil || cur.Doc == nil {
				return
			}
			if err != nil {
				s.StatusNotice = fmt.Sprintf("%s: save as: %v", filepath.Base(cur.Path), err)
				return
			}
			cur.Dirty = false
			s.StatusNotice = fmt.Sprintf("saved as %s", filepath.Base(path))
			if window != nil {
				window.Invalidate()
			}
		})
	})
}

// tabLabel is the tab strip's label: the title, plus a bullet while the
// document has unsaved edits (never for plain reads).
func tabLabel(t *Tab) string {
	if t.Dirty {
		return t.Title + " •"
	}
	return t.Title
}
