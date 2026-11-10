// annotate_test.go covers the issue-3 annotation tools (annotate.go):
// the toolbar group's permission gating, the armed-mode flow from tool
// choice through a finished selection to the created annot (verified
// through the facade's own listing), mode exit and selection cleanup,
// the keyboard bindings, and Escape disarming.
package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/gen2brain/go-fitz"
	"gopdf/internal/pdf"
)

// waitTabAnnots lists page's annots through the facade until n exist or
// the deadline passes (the creation callback lands through the frame-
// drained worker queue in tests).
func waitTabAnnots(t *testing.T, tt *ui.Tester, tab *Tab, page, n int) []pdf.PageAnnot {
	t.Helper()
	ch := make(chan annotsListReply, 1)
	list := func() []pdf.PageAnnot {
		tab.Doc.AnnotsAsync(page, func(as []pdf.PageAnnot, err error) { ch <- annotsListReply{as, err} })
		select {
		case r := <-ch:
			if r.err != nil {
				t.Fatalf("AnnotsAsync: %v", r.err)
			}
			return r.annots
		case <-time.After(5 * time.Second):
			t.Fatal("AnnotsAsync timed out")
			return nil
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if as := list(); len(as) >= n {
			return as
		}
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected %d annots on page %d, found %d", n, page, len(list()))
	return nil
}

// annotsListReply is the channel shape of waitTabAnnots.
type annotsListReply struct {
	annots []pdf.PageAnnot
	err    error
}

// TestAnnotToolbarGatedOnPermission: the annotation group builds for a
// permissive document and not for one that denies PermAnnotate.
func TestAnnotToolbarGatedOnPermission(t *testing.T) {
	s := reproState(t, 1)
	tab := s.Active()
	tt := ui.NewTester(toolbarView(s), 1200, 120)

	for _, label := range []string{"Highlight", "Underline", "Strikeout"} {
		if err := tt.Click(label); err != nil {
			t.Fatalf("permissive doc: %s button missing: %v", label, err)
		}
	}
	if tab.Annot.Tool != AnnotToolStrikeout {
		t.Fatalf("sequential clicks left tool %d, want Strikeout armed", tab.Annot.Tool)
	}

	tab.Permissions.Annotate = false // the open-time snapshot denies it
	tt.Frame()
	for _, label := range []string{"Highlight", "Underline", "Strikeout"} {
		if err := tt.Click(label); err == nil {
			t.Fatalf("denied doc: %s button still clickable", label)
		}
	}
}

// TestAnnotToolbarToggleArmsAndDisarms: clicking a markup button arms
// it, clicking it again disarms, clicking another switches.
func TestAnnotToolbarToggleArmsAndDisarms(t *testing.T) {
	s := reproState(t, 1)
	tab := s.Active()
	tt := ui.NewTester(toolbarView(s), 1200, 120)

	if err := tt.Click("Highlight"); err != nil {
		t.Fatal(err)
	}
	if tab.Annot.Tool != AnnotToolHighlight {
		t.Fatalf("after Highlight click: tool %d", tab.Annot.Tool)
	}
	if err := tt.Click("Underline"); err != nil {
		t.Fatal(err)
	}
	if tab.Annot.Tool != AnnotToolUnderline {
		t.Fatalf("after Underline click: tool %d", tab.Annot.Tool)
	}
	if err := tt.Click("Underline"); err != nil {
		t.Fatal(err)
	}
	if tab.Annot.Tool != AnnotToolNone {
		t.Fatalf("second Underline click: tool %d, want disarmed", tab.Annot.Tool)
	}
}

// TestMarkupFromSelection: the armed highlight consumes the next
// finished selection; the facade lists exactly one Highlight annot with
// quads over the selection's text, the mode exits and the selection
// clears.
func TestMarkupFromSelection(t *testing.T) {
	s := reproState(t, 3)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()

	// Arm through the binding (shortcuts.go), not the toolbar: covers
	// the same path users reach with Ctrl+Shift+H.
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyH)
	tt.Frame()
	if tab.Annot.Tool != AnnotToolHighlight {
		t.Fatalf("Ctrl+Shift+H did not arm the tool: %d", tab.Annot.Tool)
	}

	// Drag around a known word, exactly like TestSelectionDragSelectsText.
	buildIndex(t, tab)
	tab.StartSearch("page-1")
	s.RunSearch(tab)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(tab.Search.Hits) == 0 {
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}
	if len(tab.Search.Hits) == 0 || len(tab.Search.Hits[0].Rects) == 0 {
		t.Fatal("no exact hit to anchor the drag")
	}
	r := tab.Search.Hits[0].Rects[0]
	k := pageDipScale(tab, 0, tab.Zoom)
	s.StartSelection(tab, 0, float32((r.X0-40)*k), float32((r.Y0-6)*k))
	s.ExtendSelection(tab, 0, float32((r.X1+40)*k), float32((r.Y1+6)*k))
	s.FinishSelection(tab)

	annots := waitTabAnnots(t, tt, tab, 0, 1)
	if len(annots) != 1 {
		t.Fatalf("annots after markup: %d, want 1", len(annots))
	}
	if annots[0].Type.String() != "Highlight" || len(annots[0].Quads) == 0 {
		t.Fatalf("created annot not a quadded highlight: %+v", annots[0])
	}
	if tab.Annot.Tool != AnnotToolNone {
		t.Fatal("the mode did not exit on creation")
	}
	if tab.Sel.Active {
		t.Fatal("the selection did not clear after becoming an annot")
	}
	if s.StatusNotice != "" {
		t.Fatalf("creation surfaced an error: %q", s.StatusNotice)
	}
}

// TestMarkupShortcutGatedOnPermission: Ctrl+Shift+H is a no-op for a
// tab that denies PermAnnotate.
func TestMarkupShortcutGatedOnPermission(t *testing.T) {
	s := reproState(t, 1)
	tab := s.Active()
	tab.Permissions.Annotate = false
	tt := ui.NewTester(s.View, 1200, 800)

	tt.Key(ui.Ctrl|ui.Shift, ui.KeyH)
	tt.Frame()
	if tab.Annot.Tool != AnnotToolNone {
		t.Fatalf("binding armed a denied tab: %d", tab.Annot.Tool)
	}
}

// TestEscapeDisarmsAnnotTool: Escape exits the pending mode before it
// ever touches the selection cascade.
func TestEscapeDisarmsAnnotTool(t *testing.T) {
	s := reproState(t, 1)
	tab := s.Active()
	tt := ui.NewTester(s.View, 1200, 800)

	s.ToggleAnnotTool(tab, AnnotToolStrikeout)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tab.Annot.Tool != AnnotToolNone {
		t.Fatal("Escape did not disarm the pending tool")
	}
}

// TestNotePlacementFlow: the armed note tool consumes the next page
// click, the editor opens, typed text commits into a sticky-note annot
// anchored near the click, and the mode is gone.
func TestNotePlacementFlow(t *testing.T) {
	s := reproState(t, 3)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	pumpToPage0(t, tt)

	if err := tt.Click("Sticky Note"); err != nil {
		t.Fatalf("note button: %v", err)
	}
	if tab.Annot.Tool != AnnotToolNote {
		t.Fatalf("note tool not armed: %d", tab.Annot.Tool)
	}

	tt.Press(600, 400) // over page 0's middle
	tt.Release(600, 400)
	if !tab.Annot.NoteOpen {
		t.Fatal("the click did not open the note editor")
	}
	if tab.Annot.Tool != AnnotToolNone {
		t.Fatal("the note tool did not exit on placement")
	}

	tt.Type("a note from the test")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if tab.Annot.NoteOpen {
		t.Fatal("Enter did not close the editor")
	}

	annots := waitTabAnnots(t, tt, tab, 0, 1)
	if len(annots) != 1 || annots[0].Type.String() != "Text" {
		t.Fatalf("created annot not a sticky note: %+v", annots)
	}
	if annots[0].Contents != "a note from the test" {
		t.Fatalf("note body: %q", annots[0].Contents)
	}
	// The anchor sits at the click point: within the 40x36 rect's reach.
	r := annots[0].Rect
	if r.X1-r.X0 > 45 || r.Y1-r.Y0 > 41 {
		t.Fatalf("anchor rect %v, want ~40x36pt", r)
	}
	if s.StatusNotice != "" {
		t.Fatalf("creation surfaced an error: %q", s.StatusNotice)
	}
}

// TestNoteEmptyEnterCancels: an empty commit drops the placement with
// nothing created.
func TestNoteEmptyEnterCancels(t *testing.T) {
	s := reproState(t, 1)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	pumpToPage0(t, tt)

	s.ToggleAnnotTool(tab, AnnotToolNote)
	tt.Press(600, 400)
	tt.Release(600, 400)
	if !tab.Annot.NoteOpen {
		t.Fatal("the click did not open the note editor")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if tab.Annot.NoteOpen {
		t.Fatal("empty Enter did not cancel")
	}
	if as := waitTabAnnots(t, tt, tab, 0, 0); len(as) != 0 {
		t.Fatalf("empty Enter created an annot: %+v", as)
	}
}

// TestSquareDragFlow: the armed square tool turns a page drag into a
// square annot over the dragged rect; the mode exits on commit.
func TestSquareDragFlow(t *testing.T) {
	s := reproState(t, 3)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	pumpToPage0(t, tt)

	if err := tt.Click("Square"); err != nil {
		t.Fatalf("square button: %v", err)
	}
	tt.Press(600, 400)
	tt.Move(700, 500)
	if !tab.Annot.SquareDrag {
		t.Fatal("press did not start the square drag")
	}
	tt.Release(700, 500)
	if tab.Annot.SquareDrag || tab.Annot.Tool != AnnotToolNone {
		t.Fatalf("release: drag %v tool %d, want committed and disarmed",
			tab.Annot.SquareDrag, tab.Annot.Tool)
	}

	annots := waitTabAnnots(t, tt, tab, 0, 1)
	if len(annots) != 1 || annots[0].Type.String() != "Square" {
		t.Fatalf("created annot not a square: %+v", annots)
	}
	r := annots[0].Rect
	if r.X1-r.X0 < 10 || r.Y1-r.Y0 < 10 {
		t.Fatalf("square rect %v, want a real drag-sized rect", r)
	}
}

// TestSquareClickKeepsTool: a click-sized drag creates nothing and keeps
// the tool armed for a retry.
func TestSquareClickKeepsTool(t *testing.T) {
	s := reproState(t, 1)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	pumpToPage0(t, tt)

	s.ToggleAnnotTool(tab, AnnotToolSquare)
	tt.Press(600, 400)
	tt.Release(600, 400)
	if tab.Annot.Tool != AnnotToolSquare {
		t.Fatal("a click-sized drag disarmed the square tool")
	}
	if as := waitTabAnnots(t, tt, tab, 0, 0); len(as) != 0 {
		t.Fatalf("a click created a square: %+v", as)
	}
}

// createHighlightOver places a highlight quad band over the page-space
// point through the facade directly (the mutation callback's Reload is
// what the UI flow does; the test mimics it) and returns once the edit
// applied.
func createHighlightOver(t *testing.T, tab *Tab, page int, px, py float64) {
	t.Helper()
	quads := []fitz.Quad{{
		UL: fitz.Point{X: px - 8, Y: py - 4}, UR: fitz.Point{X: px + 52, Y: py - 4},
		LL: fitz.Point{X: px - 8, Y: py + 4}, LR: fitz.Point{X: px + 52, Y: py + 4},
	}}
	done := make(chan error, 1)
	tab.Doc.AddHighlightAsync(page, quads, pdf.RGB{R: 1, G: 0.9, B: 0.3}, 0.5,
		func(id int, err error) { done <- err })
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("create highlight: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("create highlight timed out")
	}
	// The mutation callback's refresh path (annotDone) rides Reload;
	// mimic it so the listing refetches.
	tab.Reload()
}

// TestAnnotOverlayPaintsAndHitTests: after a highlight exists, the
// overlay listing lands, the paint list carries the translucent fill
// shape, and a click on the annot opens the context panel instead of a
// selection.
func TestAnnotOverlayPaintsAndHitTests(t *testing.T) {
	s := reproState(t, 2)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	pumpToPage0(t, tt)

	// Learn what window point (600, 400) maps to in page space.
	tt.Press(600, 400)
	ax, ay := tab.Sel.Anchor[0], tab.Sel.Anchor[1]
	tt.Release(600, 400) // the click clears the selection

	createHighlightOver(t, tab, 0, ax, ay)

	// Pump until the listing lands (request → worker → hop → next frame).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tt.Frame()
		if tab.Annot.Listed != nil && len(tab.Annot.Listed[0]) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if tab.Annot.Listed == nil || len(tab.Annot.Listed[0]) == 0 {
		t.Fatal("the annot listing never landed")
	}

	paints := annotPaintList(tab, 0, pageDipScale(tab, 0, tab.Zoom))
	if len(paints) == 0 || !paints[0].fill {
		t.Fatalf("paint list for a highlight: %+v", paints)
	}

	// The click on the annot opens the context panel, not a selection.
	tt.Press(600, 400)
	tt.Release(600, 400)
	if !tab.Annot.SelOpen || tab.Annot.SelID != tab.Annot.Listed[0][0].ID {
		t.Fatalf("click on the annot: panel %v id %d", tab.Annot.SelOpen, tab.Annot.SelID)
	}
	if tab.Sel.Active {
		t.Fatal("the annot click started a text selection")
	}
	if !tt.HasText("Highlight") {
		t.Fatalf("context panel lacks the type name; texts %q", tt.Texts())
	}
}

// TestAnnotDeleteFromContextPanel: Delete in the panel (and the Delete
// key) removes the annot; the overlay refreshes to an empty listing.
func TestAnnotDeleteFromContextPanel(t *testing.T) {
	s := reproState(t, 2)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	pumpToPage0(t, tt)

	tt.Press(600, 400)
	ax, ay := tab.Sel.Anchor[0], tab.Sel.Anchor[1]
	tt.Release(600, 400)
	createHighlightOver(t, tab, 0, ax, ay)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tt.Frame()
		if tab.Annot.Listed != nil && len(tab.Annot.Listed[0]) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	id := tab.Annot.Listed[0][0].ID

	// Open the panel by clicking the annot (a click, since it opens at
	// release), then Delete via the KEY (the button path is the same
	// call). The panel must be attached to THAT annot.
	tt.Press(600, 400)
	tt.Release(600, 400)
	if !tab.Annot.SelOpen {
		t.Fatal("context panel did not open")
	}
	if tab.Annot.SelID != id {
		t.Fatalf("context panel annot: %d, want the clicked annot %d", tab.Annot.SelID, id)
	}
	tt.Key(0, ui.KeyDelete)
	tt.Frame()
	if tab.Annot.SelOpen {
		t.Fatal("Delete did not close the panel")
	}

	// The overlay refreshes through the delete callback's Reload.
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tt.Frame()
		if as, ok := tab.Annot.Listed[0]; ok && len(as) == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if as, ok := tab.Annot.Listed[0]; !ok || len(as) != 0 {
		t.Fatalf("listing after delete: present %v annots %+v", ok, as)
	}
	ch := make(chan annotsListReply, 1)
	tab.Doc.AnnotsAsync(0, func(as []pdf.PageAnnot, err error) { ch <- annotsListReply{as, err} })
	if r := <-ch; len(r.annots) != 0 || r.err != nil {
		t.Fatalf("facade listing after delete: %+v err %v", r.annots, r.err)
	}
}

// TestAnnotOverlayGatedWhenDenied: a tab that cannot annotate never
// requests a listing, so the overlay stays entirely off (the rendered
// appearance is all there is).
func TestAnnotOverlayGatedWhenDenied(t *testing.T) {
	s := reproState(t, 1)
	tab := s.Active()
	tab.Permissions.Annotate = false
	tt := ui.NewTester(s.View, 1200, 800)
	for i := 0; i < 5; i++ {
		tt.Frame()
	}
	if tab.Annot.Listed != nil {
		t.Fatalf("denied tab requested a listing: %+v", tab.Annot.Listed)
	}
}
