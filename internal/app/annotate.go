// annotate.go is the annotation-creation state machine (issue 3): a
// pending tool kind on the tab, the selection-to-markup flow that turns
// the NEXT finished text selection into a highlight, underline or
// strikeout over the selection pipeline's exact quads, the sticky-note
// placement with its inline editor, the square drag with its painted
// preview, and the shared error routing into StatusNotice. The
// existing-annot overlay arrives with the next commit.
//
// # Flow (markup kinds)
//
// Arming a tool (toolbar, Ctrl+Shift+H/U/K, palette) only sets
// Tab.Annot.Tool. The user then drags text exactly as before
// (selection.go); when the drag finishes AND the worker's exact quads
// have landed (either order — the release may beat the last reply),
// maybeCreateMarkup fires the facade call over the resolved quads, exits
// the mode and clears the selection. The note and square kinds instead
// take the next pointer gesture on a page (pageBoxInput routes it here):
// one click places the note anchor and opens the editor, one drag draws
// the square and commits on release.
//
// The mutation callbacks (worker goroutines) hop to the UI thread
// through annotDone, re-check the tab by ID and either report through
// StatusNotice or drop the page bitmaps (Tab.Reload): the render
// pipeline draws annotation appearances, so the new annot appears
// through a fresh render, not by painting it in the overlay.
//
// API notes (verified against the pinned mygo v0.6.4 source and the
// facade contract):
//   - pdf.AddHighlightAsync/AddUnderlineAsync/AddStrikeoutAsync take
//     []fitz.Quad in fz_quad corner order; selection quads are HitRects
//     (a rect per quad), so hitQuads converts corners 1:1. This is the
//     one place internal/app imports the fork: the facade's own markup
//     signatures speak fitz types.
//   - The facade's freshness contract: a render issued after a mutation
//     callback observes the edit, so Reload from inside the callback hop
//     is ordered correctly by construction.
//   - ui.Modal handles Escape and click-outside by clearing the open
//     flag (the password prompt's finding), which drives the note
//     editor's dismissal sweep.
package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/gen2brain/go-fitz"
	"gopdf/internal/pdf"
)

// AnnotToolKind selects the pending annotation tool of a tab. Exactly one
// kind can be armed; AnnotToolNone is the plain reader.
type AnnotToolKind int

const (
	AnnotToolNone AnnotToolKind = iota
	AnnotToolHighlight
	AnnotToolUnderline
	AnnotToolStrikeout
	AnnotToolNote   // commit 2
	AnnotToolSquare // commit 2
)

// Markup reports whether the kind draws over the text-selection quads
// (the selection-to-markup flow) rather than taking its own pointer
// gesture.
func (k AnnotToolKind) Markup() bool {
	return k == AnnotToolHighlight || k == AnnotToolUnderline || k == AnnotToolStrikeout
}

// annotToolLabel is the human name the toolbar, palette and notices use.
func annotToolLabel(k AnnotToolKind) string {
	switch k {
	case AnnotToolHighlight:
		return "Highlight"
	case AnnotToolUnderline:
		return "Underline"
	case AnnotToolStrikeout:
		return "Strikeout"
	case AnnotToolNote:
		return "Sticky Note"
	case AnnotToolSquare:
		return "Square"
	}
	return ""
}

// The markup styles (brief): highlight yellow 1/0.9/0.3 at opacity 0.5;
// underline and strikeout black at full opacity. The square stroke is
// the classic annotation red (a beta choice, the brief's to make): pages
// are white in both themes, so one fixed red reads everywhere.
var (
	highlightColor = pdf.RGB{R: 1, G: 0.9, B: 0.3}
	markupBlack    = pdf.RGB{R: 0, G: 0, B: 0}
	squareColor    = pdf.RGB{R: 0.85, G: 0.15, B: 0.15}
)

const highlightOpacity = 0.5

// noteAnchorW/H is the sticky-note anchor's size in PAGE-SPACE points:
// the icon is fixed on the page (zooming scales it like every other
// annot, the way PDF readers draw it), not fixed on screen.
const (
	noteAnchorW = 40.0
	noteAnchorH = 36.0
)

// AnnotState is the annotation state of one tab. UI thread only, like the
// rest of Tab; worker results land through updateFromWorker hops that
// re-check the tab.
type AnnotState struct {
	// Tool is the pending creation tool; the next selection (markup) or
	// click/drag (note, square) consumes it.
	Tool AnnotToolKind

	// Note placement: the click landed, the inline editor is up.
	NoteOpen bool
	NotePage int
	NotePt   [2]float64 // page space, clamped inside the page box

	// Square drag (page space, clamped inside the page box).
	SquareDrag   bool
	SquarePage   int
	SquareAnchor [2]float64
	SquareCur    [2]float64
}

// ToggleAnnotTool arms k as the tab's pending tool, or disarms it when k
// is already armed (the toolbar buttons and shortcuts toggle). Arming a
// different kind replaces the armed one. Notes and squares arriving with
// commit 2 arm the same way.
func (s *AppState) ToggleAnnotTool(t *Tab, k AnnotToolKind) {
	if t == nil {
		return
	}
	if t.Annot.Tool == k {
		t.Annot.Tool = AnnotToolNone
		return
	}
	t.Annot.Tool = k
}

// annotDone is the shared mutation-callback hop every annotation creator
// uses: it lands on the UI thread, re-checks the tab by ID (it may have
// closed while the edit was in flight), reports failures through
// StatusNotice, and on success drops the page bitmaps (Tab.Reload): the
// render draws annotation appearances, so the edit shows through a fresh
// render, ordered after the mutation by the facade's freshness contract.
func (s *AppState) annotDone(t *Tab, what string) func(error) {
	return func(err error) { // doc worker goroutine
		updateFromWorker(func() { // hop, never inline
			cur := s.tabByID(t.ID)
			if cur == nil || cur.Doc == nil {
				return // stale: tab closed while the edit was in flight
			}
			if err != nil {
				// ErrBadPage/ErrBadQuads/ErrBadParam cannot arise from
				// validated UI geometry, but they pass through here
				// untouched if they ever do.
				s.StatusNotice = fmt.Sprintf("%s: %s: %v",
					filepath.Base(cur.Path), what, err)
				return
			}
			cur.Reload()
			if window != nil {
				window.Invalidate()
			}
		})
	}
}

// tabByID finds a live tab by ID (the annot callbacks' liveness check).
func (s *AppState) tabByID(id string) *Tab {
	for _, t := range s.Tabs {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// ---- sticky note -------------------------------------------------------

// PlaceNote consumes an armed note tool: the click at x,y (local DIPs on
// page) becomes the anchor and the inline editor opens (the password
// panel pattern). The tool exits here — the next single click is what
// the tool promised.
func (s *AppState) PlaceNote(t *Tab, page int, x, y float32) {
	if t == nil || t.Doc == nil {
		return
	}
	k := pageDipScale(t, page, t.Zoom)
	px, py := dipToPagePt(t, page, k, x, y)
	w0, h0 := t.Doc.PageSize(page)
	ox, oy := t.Doc.PageOrigin(page)
	px = min(max(px, ox), ox+w0)
	py = min(max(py, oy), oy+h0)
	t.Annot.Tool = AnnotToolNone
	t.Annot.NoteOpen = true
	t.Annot.NotePage = page
	t.Annot.NotePt = [2]float64{px, py}
	notePlaced = true // the overlay's open edge clears the field
}

// noteRect is the placed anchor's rect in page space: noteAnchorW wide,
// noteAnchorH tall, centered on the click point and clamped inside the
// page box.
func (t *Tab) noteRect() fitz.Rect {
	px, py := t.Annot.NotePt[0], t.Annot.NotePt[1]
	w0, h0 := t.Doc.PageSize(t.Annot.NotePage)
	ox, oy := t.Doc.PageOrigin(t.Annot.NotePage)
	r := fitz.Rect{
		X0: px - noteAnchorW/2, Y0: py,
		X1: px + noteAnchorW/2, Y1: py + noteAnchorH,
	}
	r.X0 = max(r.X0, ox)
	r.Y0 = max(r.Y0, oy)
	r.X1 = min(r.X1, ox+w0)
	r.Y1 = min(r.Y1, oy+h0)
	return r
}

// commitNote creates the sticky note from the editor's text and closes
// it; an empty text cancels with nothing created.
func (s *AppState) commitNote(t *Tab) {
	if t == nil || !t.Annot.NoteOpen {
		return
	}
	t.Annot.NoteOpen = false
	note := strings.TrimSpace(noteValue)
	noteValue = ""
	if note == "" || t.Doc == nil {
		return // empty Enter cancels the placement
	}
	page := t.Annot.NotePage
	rect := t.noteRect()
	done := func(id int, err error) { s.annotDone(t, annotToolLabel(AnnotToolNote))(err) }
	t.Doc.AddTextAsync(page, rect, note, done)
}

// The note editor's field storage and open-edge flag (the password
// prompt's pwValue/pwOpenSeen pattern): notePlaced marks a fresh
// placement so the overlay starts an empty field, and any dismissal
// (Escape, backdrop, Cancel) leaves the flag down while NoteOpen is
// still set for a build — the sweep converts that into a dropped
// placement.
var (
	noteValue  string
	notePlaced bool
)

// noteEditorOverlay renders the sticky-note editor while the active tab
// has a placement open. ui.Modal handles Escape and click-outside by
// clearing the open flag (verified against the pinned mygo v0.6.4
// source, the password prompt's finding), which the sweep below converts
// into a cancelled placement. The placement targets the tab it started
// on; switching tabs hides the editor until that tab is active again.
func noteEditorOverlay(c *ui.Context, s *AppState) {
	t := s.Active()
	if t == nil || !t.Annot.NoteOpen {
		if t != nil {
			t.Annot.NoteOpen = false // sweep: dismissed, placement dropped
		}
		notePlaced = false
		return
	}
	if notePlaced { // opened since the last build: fresh field
		notePlaced = false
		noteValue = ""
	}
	ui.Modal(c, &t.Annot.NoteOpen, func() { noteEditorPanel(c, s, t) })
}

// noteEditorPanel is the editor itself: one text field, Enter commits
// (empty cancels), the button row mirrors it. Built while the modal is
// open; re-checks the tab like the password panel does.
func noteEditorPanel(c *ui.Context, s *AppState, t *Tab) {
	th := c.Theme()
	ui.Column(c).Width(440).Gap(th.Space(2)).Children(func() {
		ui.Text(c, "Sticky note").Bold().FontSize(th.FontSize * 1.15)
		ui.Text(c, fmt.Sprintf("Page %d", t.Annot.NotePage+1)).
			TextColor(th.TextMuted).FontSize(th.FontSize * 0.9).SingleLine()
		in := ui.TextInput(c, &noteValue).
			Placeholder("Note text (empty cancels)").AutoFocus()
		ui.Row(c).FillWidth().Justify(ui.End).Gap(th.Space(1)).Children(func() {
			if ui.Button(c, "Cancel").Clicked() {
				t.Annot.NoteOpen = false // sweep drops the placement
			}
			if ui.PrimaryButton(c, "Add note").Clicked() {
				s.commitNote(t)
			}
		})
		if in.Submitted() { // Enter in the field commits too
			s.commitNote(t)
		}
	})
}

// ---- square ------------------------------------------------------------

// StartSquareDrag consumes the press of an armed square tool and begins
// the drag at x,y (local DIPs on page), in page space.
func (s *AppState) StartSquareDrag(t *Tab, page int, x, y float32) {
	if t == nil || t.Doc == nil {
		return
	}
	k := pageDipScale(t, page, t.Zoom)
	px, py := dipToPagePt(t, page, k, x, y)
	w0, h0 := t.Doc.PageSize(page)
	ox, oy := t.Doc.PageOrigin(page)
	px = min(max(px, ox), ox+w0)
	py = min(max(py, oy), oy+h0)
	t.Annot.SquareDrag = true
	t.Annot.SquarePage = page
	t.Annot.SquareAnchor = [2]float64{px, py}
	t.Annot.SquareCur = [2]float64{px, py}
}

// ExtendSquareDrag moves the drag point (local DIPs, may lie outside the
// page during a captured drag; clamped to the page box).
func (s *AppState) ExtendSquareDrag(t *Tab, page int, x, y float32) {
	if t == nil || !t.Annot.SquareDrag || t.Annot.SquarePage != page {
		return
	}
	k := pageDipScale(t, page, t.Zoom)
	px, py := dipToPagePt(t, page, k, x, y)
	w0, h0 := t.Doc.PageSize(page)
	ox, oy := t.Doc.PageOrigin(page)
	t.Annot.SquareCur = [2]float64{
		min(max(px, ox), ox+w0),
		min(max(py, oy), oy+h0),
	}
}

// squareRectPts is the square drag's page-space rect, normalized.
func (t *Tab) squareRectPts() (x0, y0, x1, y1 float64) {
	x0, y0 = t.Annot.SquareAnchor[0], t.Annot.SquareAnchor[1]
	x1, y1 = t.Annot.SquareCur[0], t.Annot.SquareCur[1]
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	return
}

// CommitSquareDrag ends the drag: a real rect (2pt or more on an axis)
// creates the square annot and exits the tool; a click-sized drag
// cancels and keeps the tool armed (nothing was placed).
func (s *AppState) CommitSquareDrag(t *Tab) {
	if t == nil || !t.Annot.SquareDrag {
		return
	}
	t.Annot.SquareDrag = false
	x0, y0, x1, y1 := t.squareRectPts()
	if x1-x0 < 2 && y1-y0 < 2 {
		return // a click, not a drag: retry
	}
	t.Annot.Tool = AnnotToolNone
	page := t.Annot.SquarePage
	rect := fitz.Rect{X0: x0, Y0: y0, X1: x1, Y1: y1}
	done := func(id int, err error) { s.annotDone(t, annotToolLabel(AnnotToolSquare))(err) }
	t.Doc.AddSquareAsync(page, rect, squareColor, 1, done)
}
// annotToolButton is one toolbar button of the annotation group. The
// armed tool reads as the active tab does: an accent-tinted surface.
func annotToolButton(c *ui.Context, s *AppState, t *Tab, k AnnotToolKind, tip string, icon *ui.SVG) {
	b := ui.Button(c, "").Label(annotToolLabel(k)).Tooltip(tip)
	if t.Annot.Tool == k {
		b.Background(accentTint(c.Theme()))
	}
	b.Children(func() { ui.Icon(c, icon).FontSize(16) })
	b.OnClick(func() { s.ToggleAnnotTool(t, k) })
}

// hitQuads converts the selection pipeline's exact quads (HitRect: a
// page-space rect per quad, top-left origin) into the fz_quad corner
// order the facade's markup creators take.
func hitQuads(rects []pdf.HitRect) []fitz.Quad {
	out := make([]fitz.Quad, 0, len(rects))
	for _, r := range rects {
		out = append(out, fitz.Quad{
			UL: fitz.Point{X: r.X0, Y: r.Y0}, UR: fitz.Point{X: r.X1, Y: r.Y0},
			LL: fitz.Point{X: r.X0, Y: r.Y1}, LR: fitz.Point{X: r.X1, Y: r.Y1},
		})
	}
	return out
}

// maybeCreateMarkup fires the armed markup tool over the finished,
// resolved selection. Call from FinishSelection (pointer up) and from the
// selection reply hop: whichever runs last brings both halves together,
// and Tool is consumed synchronously here so it can fire exactly once.
// The selection is cleared afterwards: its job is done, and the annot's
// own rendered appearance takes over.
func (s *AppState) maybeCreateMarkup(t *Tab) {
	k := t.Annot.Tool
	if !k.Markup() || t.Doc == nil || !t.Sel.Active || t.Sel.Dragging ||
		!t.Sel.resolved || len(t.Sel.Quads) == 0 {
		return
	}
	t.Annot.Tool = AnnotToolNone
	page := t.Sel.Page
	quads := hitQuads(t.Sel.Quads)
	t.ClearSelection()

	done := func(id int, err error) { s.annotDone(t, annotToolLabel(k))(err) }
	switch k {
	case AnnotToolHighlight:
		t.Doc.AddHighlightAsync(page, quads, highlightColor, highlightOpacity, done)
	case AnnotToolUnderline:
		t.Doc.AddUnderlineAsync(page, quads, markupBlack, 1, done)
	case AnnotToolStrikeout:
		t.Doc.AddStrikeoutAsync(page, quads, markupBlack, 1, done)
	}
}
