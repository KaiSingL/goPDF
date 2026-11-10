// undo.go is the annotation undo/redo engine (issue 6): a per-tab pair
// of stacks on AnnotState, fed by every mutation the annotation tools
// issue (annotate.go's creators and deleteSelectedAnnot) and drained by
// the toolbar buttons, Ctrl+Z / Ctrl+Y / Ctrl+Shift+Z and the palette.
//
// # Model
//
// One entry per mutation, storing the entry's FORWARD half and the full
// creation payload (a pdf.PageAnnot snapshot in the facade's RestoreEdit
// dialect), plus the annot's CURRENT object number — re-creates hand out
// fresh numbers, so the entry's id is remapped in the mutation callback
// every time a re-create lands. Undo applies the inverse (a created
// annot is deleted, a deleted one re-created through CreateAnnotAsync
// with RestoreEdit); redo re-applies the forward half. The entry hops to
// the opposite stack inside the callback, the one place success is known
// and a fresh ID exists; a failed apply (stale ref, closed doc) drops
// the entry with the shared StatusNotice error routing.
//
// A fresh USER mutation clears the redo stack (the standard rule);
// undo/redo-driven applies never do. All applies and user mutations set
// the single-flight AnnotState.mutating flag, cleared in every
// annotDoneEx hop, so a second undo cannot interleave with an apply
// whose record has not landed.
//
// Scope and limits, on purpose: annotations only (page organization has
// no UI to undo), one tab's stacks die with it, and the Dirty flag stays
// set after undoing back to the opened state — object renumbering makes
// byte-identity impossible, so "clean" would lie. Restoration is exact
// for every type the fork's Annots/Edit dialect can express (markup
// kinds with quads, notes, freetext, squares, squiggly included);
// geometry the dialect cannot carry (ink paths, stamps) restores as its
// rect and colors alone.
package app

import (
	"gopdf/internal/pdf"
)

// undoOp is one entry's forward half: what the entry DID.
type undoOp uint8

const (
	opAdded   undoOp = iota // the entry created the annot
	opDeleted               // the entry deleted it
)

// undoEntry is one stack frame: the forward op, the page, the annot's
// current ID (remapped on every re-create) and the snapshot the
// create-half of either direction runs from.
type undoEntry struct {
	op   undoOp
	page int
	id   int
	snap pdf.PageAnnot
}

// CanUndo / CanRedo drive the toolbar buttons' disabled state (and the
// palette's re-checks): a stack exists to drain and no mutation of this
// tab is mid-flight.
func (t *Tab) CanUndo() bool {
	return t != nil && len(t.Annot.Undo) > 0 && !t.Annot.mutating
}

func (t *Tab) CanRedo() bool {
	return t != nil && len(t.Annot.Redo) > 0 && !t.Annot.mutating
}

// Undo applies the inverse of the most recent entry. No-op without a
// document, the annotate permission, a drainable stack or while a
// mutation is in flight.
func (s *AppState) Undo(t *Tab) {
	if !s.undoReady(t) || len(t.Annot.Undo) == 0 {
		return
	}
	e := t.Annot.Undo[len(t.Annot.Undo)-1]
	t.Annot.Undo = t.Annot.Undo[:len(t.Annot.Undo)-1]
	s.applyEntry(t, e, true)
}

// Redo re-applies the most recently undone entry; see Undo.
func (s *AppState) Redo(t *Tab) {
	if !s.undoReady(t) || len(t.Annot.Redo) == 0 {
		return
	}
	e := t.Annot.Redo[len(t.Annot.Redo)-1]
	t.Annot.Redo = t.Annot.Redo[:len(t.Annot.Redo)-1]
	s.applyEntry(t, e, false)
}

// undoReady is the shared guard of Undo and Redo.
func (s *AppState) undoReady(t *Tab) bool {
	return t != nil && t.Doc != nil && t.CanAnnotate() && !t.Annot.mutating
}

// applyEntry runs one entry's inverse (undoing) or forward (redoing)
// half. undoing an add and redoing a delete both DELETE; the other two
// combinations re-create from the snapshot. The entry re-lands on the
// opposite stack inside the mutation callback; the create callback also
// captures the fresh ID into the entry first (the old number died with
// the delete).
func (s *AppState) applyEntry(t *Tab, e undoEntry, undoing bool) {
	t.Annot.mutating = true
	what := "undo"
	if !undoing {
		what = "redo"
	}
	land := func(cur *Tab) { // push onto the opposite stack, in the hop
		if undoing {
			cur.Annot.Redo = append(cur.Annot.Redo, e)
		} else {
			cur.Annot.Undo = append(cur.Annot.Undo, e)
		}
	}
	if deletes := undoing == (e.op == opAdded); deletes {
		t.Doc.DeleteAnnotAsync(e.page, e.id, s.annotDoneEx(t, what, land))
		return
	}
	t.Doc.CreateAnnotAsync(e.page, e.snap.Type, e.snap.RestoreEdit(),
		func(id int, err error) {
			s.annotDoneEx(t, what, func(cur *Tab) {
				old := e.id // the number that died with the delete
				e.id = id   // the re-create's fresh number
				land(cur)
				remapAnnotID(cur, old, id)
			})(err)
		})
}

// remapAnnotID updates every stack entry that tracked the dead object
// number old to the re-create's fresh id: ONE annot's entries (its
// create and its delete) must share its CURRENT number, whichever stack
// they sit on — otherwise a later undo of the create deletes the dead
// number and dies on fitz.ErrStaleRef. Object numbers are unique
// document-wide, so the id alone identifies the annot.
func remapAnnotID(cur *Tab, old, fresh int) {
	if old == 0 || old == fresh {
		return
	}
	for _, stack := range []*[]undoEntry{&cur.Annot.Undo, &cur.Annot.Redo} {
		for i := range *stack {
			if (*stack)[i].id == old {
				(*stack)[i].id = fresh
			}
		}
	}
}

// recordCreate is the user creators' callback (annotate.go): the shared
// success finish plus the undo push. The snapshot is built at ISSUE
// time — the exact quads, rect, text and style the gesture carried — so
// a redo re-creates the annot without depending on a listing, and the
// fresh ID from the facade lands in the entry inside the hop.
func (s *AppState) recordCreate(t *Tab, what string, page int, snap pdf.PageAnnot) func(id int, err error) {
	return func(id int, err error) {
		s.annotDoneEx(t, what, func(cur *Tab) {
			cur.Annot.Undo = append(cur.Annot.Undo,
				undoEntry{op: opAdded, page: page, id: id, snap: snap})
			cur.Annot.Redo = nil
		})(err)
	}
}
