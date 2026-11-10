package app

// undo_test.go drives the annotation undo/redo engine (undo.go, issue 6)
// end to end through real documents: a create undone and redone through
// the keyboard chord, a delete restored verbatim (the ID remap included),
// and the full create/delete round trip walked in both directions. The
// stacks' single-flight discipline is asserted between every step.

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/gen2brain/go-fitz"
	"gopdf/internal/pdf"
)

// pumpUntil frames the tester until cond holds or the deadline passes.
func pumpUntil(t *testing.T, tt *ui.Tester, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s", what)
}

// exactAnnots polls page's listing through the facade until EXACTLY n
// annots exist (waitTabAnnots' >= would pass an empty page instantly).
func exactAnnots(t *testing.T, tt *ui.Tester, tab *Tab, page, n int) []pdf.PageAnnot {
	t.Helper()
	ch := make(chan annotsListReply, 1)
	list := func() int {
		tab.Doc.AnnotsAsync(page, func(as []pdf.PageAnnot, err error) {
			ch <- annotsListReply{as, err}
		})
		select {
		case r := <-ch:
			if r.err != nil {
				t.Fatalf("AnnotsAsync: %v", r.err)
			}
			return len(r.annots)
		case <-time.After(5 * time.Second):
			t.Fatal("AnnotsAsync timed out")
			return -1
		}
	}
	pumpUntil(t, tt, func() bool { return list() == n },
		"annotation count never settled")
	// One more listing for the caller to inspect.
	tab.Doc.AnnotsAsync(page, func(as []pdf.PageAnnot, err error) {
		ch <- annotsListReply{as, err}
	})
	r := <-ch
	if r.err != nil {
		t.Fatalf("AnnotsAsync: %v", r.err)
	}
	return r.annots
}

// placeNote creates one sticky note through the state machine (the note
// flow minus the modal), returning once the undo entry has landed.
func placeNote(t *testing.T, tt *ui.Tester, s *AppState, tab *Tab, body string) {
	t.Helper()
	s.PlaceNote(tab, 0, 600, 400)
	if !tab.Annot.NoteOpen {
		t.Fatal("placement did not open the editor state")
	}
	tab.Annot.NoteDraft = body
	s.commitNote(tab)
	pumpUntil(t, tt, func() bool { return len(tab.Annot.Undo) == 1 && !tab.Annot.mutating },
		"note creation never recorded an undo entry")
}

// TestUndoRedoKeyboard: a committed note, undone through Ctrl+Z and
// restored through Ctrl+Y — the window-scope chord, not the method.
func TestUndoRedoKeyboard(t *testing.T) {
	s := reproState(t, 3)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	pumpToPage0(t, tt)

	placeNote(t, tt, s, tab, "undone by keyboard")
	if len(tab.Annot.Redo) != 0 {
		t.Fatal("a fresh create left a redo stack behind")
	}

	tt.Key(ui.Ctrl, ui.KeyZ)
	pumpUntil(t, tt, func() bool {
		return len(tab.Annot.Undo) == 0 && len(tab.Annot.Redo) == 1 && !tab.Annot.mutating
	}, "Ctrl+Z never undid the create")
	if got := exactAnnots(t, tt, tab, 0, 0); len(got) != 0 {
		t.Fatalf("undo left annots behind: %+v", got)
	}
	if !tab.Dirty {
		t.Fatal("undoing to the opened state must keep the dirty flag")
	}

	tt.Key(ui.Ctrl, ui.KeyY)
	pumpUntil(t, tt, func() bool {
		return len(tab.Annot.Undo) == 1 && len(tab.Annot.Redo) == 0
	}, "Ctrl+Y never redid the create")
	annots := exactAnnots(t, tt, tab, 0, 1)
	if annots[0].Contents != "undone by keyboard" || annots[0].Type != fitz.AnnotText {
		t.Fatalf("redone note: %+v", annots[0])
	}
	if tab.Annot.Undo[0].id != annots[0].ID {
		t.Fatalf("redo did not remap the entry id: entry %d, annot %d",
			tab.Annot.Undo[0].id, annots[0].ID)
	}
	// The second redo chord is the same action: undo once more, then
	// bring the note back with Ctrl+Shift+Z.
	tt.Key(ui.Ctrl, ui.KeyZ)
	pumpUntil(t, tt, func() bool { return len(tab.Annot.Undo) == 0 && len(tab.Annot.Redo) == 1 },
		"the second Ctrl+Z never undid the redo")
	exactAnnots(t, tt, tab, 0, 0)

	tt.Key(ui.Ctrl|ui.Shift, ui.KeyZ)
	pumpUntil(t, tt, func() bool { return len(tab.Annot.Undo) == 1 && len(tab.Annot.Redo) == 0 },
		"Ctrl+Shift+Z never redid")
	annots = exactAnnots(t, tt, tab, 0, 1)
	if annots[0].Contents != "undone by keyboard" {
		t.Fatalf("redone note body: %q", annots[0].Contents)
	}
}

// TestUndoDeleteRestores: deleting a square is undone into a faithful
// re-creation, and the whole create/delete history walks both ways
// without losing an entry or an id.
func TestUndoDeleteRestores(t *testing.T) {
	s := reproState(t, 2)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	pumpToPage0(t, tt)

	// One square through the drag state machine.
	s.StartSquareDrag(tab, 0, 500, 300)
	s.ExtendSquareDrag(tab, 0, 700, 480)
	s.CommitSquareDrag(tab)
	pumpUntil(t, tt, func() bool { return len(tab.Annot.Undo) == 1 && !tab.Annot.mutating },
		"square creation never recorded an undo entry")
	created := exactAnnots(t, tt, tab, 0, 1)
	if created[0].Type != fitz.AnnotSquare {
		t.Fatalf("created annot not a square: %+v", created[0])
	}

	// Delete through the context panel's path, with the listing the
	// snapshot reads from in place.
	tab.Annot.Listed = map[int][]pdf.PageAnnot{0: created}
	tab.Annot.SelOpen, tab.Annot.SelID, tab.Annot.SelPage = true, created[0].ID, 0
	s.deleteSelectedAnnot(tab)
	pumpUntil(t, tt, func() bool { return len(tab.Annot.Undo) == 2 && !tab.Annot.mutating },
		"delete never recorded an undo entry")
	exactAnnots(t, tt, tab, 0, 0)

	// Undo the delete: a faithful re-creation (new id, same geometry).
	s.Undo(tab)
	pumpUntil(t, tt, func() bool { return len(tab.Annot.Undo) == 1 && len(tab.Annot.Redo) == 1 },
		"undo never restored the deleted square")
	restored := exactAnnots(t, tt, tab, 0, 1)
	if restored[0].Type != fitz.AnnotSquare {
		t.Fatalf("restored annot not a square: %+v", restored[0])
	}
	if restored[0].ID == created[0].ID {
		t.Fatal("restoration reused the dead object number")
	}
	o, r := created[0].Rect, restored[0].Rect
	if abs64(o.X0-r.X0) > 1 || abs64(o.Y0-r.Y0) > 1 || abs64(o.X1-r.X1) > 1 || abs64(o.Y1-r.Y1) > 1 {
		t.Fatalf("restored rect drifted: was %v, now %v", o, r)
	}

	// Walk the rest of the history: undo the create, redo the create,
	// redo the delete. Every step must land exactly one annot.
	s.Undo(tab)
	pumpUntil(t, tt, func() bool { return len(tab.Annot.Undo) == 0 && len(tab.Annot.Redo) == 2 },
		"undo never removed the created square")
	exactAnnots(t, tt, tab, 0, 0)

	s.Redo(tab)
	pumpUntil(t, tt, func() bool { return len(tab.Annot.Undo) == 1 && len(tab.Annot.Redo) == 1 },
		"redo never re-created the square")
	exactAnnots(t, tt, tab, 0, 1)

	s.Redo(tab)
	pumpUntil(t, tt, func() bool { return len(tab.Annot.Undo) == 2 && len(tab.Annot.Redo) == 0 },
		"redo never re-deleted the square")
	exactAnnots(t, tt, tab, 0, 0)

	// The whole walk must have gone through without one error notice
	// (the stale-ref failure class the id remap exists to prevent).
	if s.StatusNotice != "" {
		t.Fatalf("the round trip surfaced an error: %q", s.StatusNotice)
	}
}

// TestUndoToolbarButton: the toolbar button routes through the same
// engine (disabled state included), not its own mutation path.
func TestUndoToolbarButton(t *testing.T) {
	s := reproState(t, 2)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	pumpToPage0(t, tt)

	placeNote(t, tt, s, tab, "button undo")
	if err := tt.Click("Undo"); err != nil {
		t.Fatalf("click Undo: %v", err)
	}
	pumpUntil(t, tt, func() bool { return len(tab.Annot.Undo) == 0 && len(tab.Annot.Redo) == 1 },
		"the Undo button never drained the stack")
	exactAnnots(t, tt, tab, 0, 0)

	// A no-op click on an empty stack must stay a no-op.
	if err := tt.Click("Undo"); err != nil {
		t.Fatalf("click disabled Undo: %v", err)
	}
	pumpUntil(t, tt, func() bool { return len(tab.Annot.Redo) == 1 }, "redo stack changed")

	if err := tt.Click("Redo"); err != nil {
		t.Fatalf("click Redo: %v", err)
	}
	pumpUntil(t, tt, func() bool { return len(tab.Annot.Undo) == 1 && len(tab.Annot.Redo) == 0 },
		"the Redo button never restored the note")
	annots := exactAnnots(t, tt, tab, 0, 1)
	if annots[0].Contents != "button undo" {
		t.Fatalf("redone note body: %q", annots[0].Contents)
	}
}
