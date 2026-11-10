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
//     (a rect per quad), so hitQuads converts corners 1:1. internal/app
//     touches fitz types ONLY at the facade boundary — Quad/Rect/Point/
//     AnnotType for markup creation and listings (annotate.go), and
//     SaveOptions for saves (save.go): every field is a facade
//     signature's own type.
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
	"time"

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

// fitzType is the facade's annotation subtype of the kind (the undo
// snapshot's Type, and what a restore re-creates).
func (k AnnotToolKind) fitzType() fitz.AnnotType {
	switch k {
	case AnnotToolHighlight:
		return fitz.AnnotHighlight
	case AnnotToolUnderline:
		return fitz.AnnotUnderline
	case AnnotToolStrikeout:
		return fitz.AnnotStrikeOut
	case AnnotToolNote:
		return fitz.AnnotText
	case AnnotToolSquare:
		return fitz.AnnotSquare
	}
	return fitz.AnnotUnknown
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
	NoteOpen  bool
	NotePage  int
	NotePt    [2]float64 // page space, clamped inside the page box
	NoteDraft string     // the editor's per-tab draft text

	// Square drag (page space, clamped inside the page box).
	SquareDrag   bool
	SquarePage   int
	SquareAnchor [2]float64
	SquareCur    [2]float64

	// Existing-annot overlay (the interactive layer over the rendered
	// appearances): one listing per page, requested lazily when a page
	// is built and refreshed by requestAnnots after every mutation and
	// reload. Page-space data re-maps on every build, so zoom, scroll
	// and rotation are inherently fresh.
	Listed      map[int][]pdf.PageAnnot
	listPending map[int]bool // an AnnotsAsync is in flight
	// listErrBackoff: pages whose listing errored recently; re-requesting
	// per frame would ping-pong a persistently failing page.
	listErrBackoff map[int]time.Time

	// The context panel (click an existing annot): SelOpen drives the
	// modal, the other fields identify the annot.
	SelOpen bool
	SelID   int
	SelPage int
	SelType string

	// Undo/redo (undo.go, issue 6): the per-tab annotation stacks, and
	// the single-flight flag that holds undo/redo off while a mutation
	// is between issue and callback. A fresh user mutation clears Redo.
	Undo, Redo []undoEntry
	mutating   bool
}

// ToggleAnnotTool arms k as the tab's pending tool, or disarms it when k
// is already armed (the toolbar buttons and shortcuts toggle). Arming a
// different kind replaces the armed one.
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
	return s.annotDoneEx(t, what, nil)
}

// annotDoneEx is annotDone with a success hook: ok runs inside the same
// UI hop after the shared finish (dirty, reload) — the undo engine
// records its stack moves there, the one place where the worker's reply
// has landed (and, for creators, the new annot's ID exists). Every hop
// also releases the single-flight mutating flag, success or failure.
func (s *AppState) annotDoneEx(t *Tab, what string, ok func(cur *Tab)) func(error) {
	return func(err error) { // doc worker goroutine
		updateFromWorker(func() { // hop, never inline
			cur := s.tabByID(t.ID)
			if cur == nil || cur.Doc == nil {
				return // stale: tab closed while the edit was in flight
			}
			defer func() { cur.Annot.mutating = false }()
			if err != nil {
				// ErrBadPage/ErrBadQuads/ErrBadParam cannot arise from
				// validated UI geometry, but they pass through here
				// untouched if they ever do.
				s.StatusNotice = fmt.Sprintf("%s: %s: %v",
					filepath.Base(cur.Path), what, err)
				return
			}
			cur.Dirty = true // unsaved edits: the tab label's bullet (save.go)
			cur.Reload()
			if ok != nil {
				ok(cur)
			}
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
// noteAnchorH tall, horizontally centered on the click point and clamped
// inside the page box (the click's point is the rect's TOP edge, not its
// center, so an edge clamped click still yields an anchored, non-degenerate
// rect).
func (t *Tab) noteRect() fitz.Rect {
	px, py := t.Annot.NotePt[0], t.Annot.NotePt[1]
	w0, h0 := t.Doc.PageSize(t.Annot.NotePage)
	ox, oy := t.Doc.PageOrigin(t.Annot.NotePage)
	// Clamp the click first so the rect's own clamp cannot collapse the
	// height on bottom-edge clicks.
	py = min(max(py, oy), oy+h0-noteAnchorH)
	px = min(max(px, ox+noteAnchorW/2), ox+w0-noteAnchorW/2)
	return fitz.Rect{
		X0: px - noteAnchorW/2, Y0: py,
		X1: px + noteAnchorW/2, Y1: py + noteAnchorH,
	}
}

// commitNote creates the sticky note from the editor's text and closes
// it; an empty text cancels with nothing created.
func (s *AppState) commitNote(t *Tab) {
	if t == nil || !t.Annot.NoteOpen {
		return
	}
	t.Annot.NoteOpen = false
	note := strings.TrimSpace(t.Annot.NoteDraft)
	t.Annot.NoteDraft = ""
	if note == "" || t.Doc == nil {
		return // empty Enter cancels the placement
	}
	page := t.Annot.NotePage
	rect := t.noteRect()
	snap := pdf.PageAnnot{Type: fitz.AnnotText, Rect: rect, Contents: note}
	t.Annot.mutating = true
	t.Doc.AddTextAsync(page, rect, note,
		s.recordCreate(t, annotToolLabel(AnnotToolNote), page, snap))
}

// The note editor's per-tab draft lives on the tab (Tab.Annot.NoteDraft,
// bound by the password prompt's pwValue pattern but scoped to the tab:
// several tabs can hold placements at once and tab switches must not
// silently wipe a half-typed draft); notePlaced marks a fresh placement
// so the overlay starts an empty field only for THAT tab, and any
// dismissal (Escape, backdrop, Cancel) leaves the flag down while
// NoteOpen is still set for a build — the sweep converts that into a
// dropped placement.
var notePlaced bool

// noteEditorOverlay renders the sticky-note editor while the active tab
// has a placement open. ui.Modal handles Escape and click-outside by
// clearing the open flag (verified against the pinned mygo v0.6.4
// source, the password prompt's finding), which the sweep below converts
// into a cancelled placement. The placement targets the tab it started
// on; switching tabs hides the editor until that tab is active again.
func noteEditorOverlay(c *ui.Context, s *AppState) {
	t := s.Active()
	if t == nil || !t.Annot.NoteOpen {
		notePlaced = false
		return
	}
	if notePlaced { // opened since the last build: fresh field, per tab
		notePlaced = false
		t.Annot.NoteDraft = ""
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
		in := ui.TextInput(c, &t.Annot.NoteDraft).
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
	snap := pdf.PageAnnot{
		Type: fitz.AnnotSquare, Rect: rect,
		Color:   []float64{squareColor.R, squareColor.G, squareColor.B},
		Opacity: 1,
	}
	t.Annot.mutating = true
	t.Doc.AddSquareAsync(page, rect, squareColor, 1,
		s.recordCreate(t, annotToolLabel(AnnotToolSquare), page, snap))
}

// ---- existing-annot overlay --------------------------------------------

// markupKind reports whether an annot type draws through quad points
// (the paint pass renders those as translucent fills or lines).
func markupKind(t fitz.AnnotType) bool {
	switch t {
	case fitz.AnnotHighlight, fitz.AnnotUnderline, fitz.AnnotSquiggly, fitz.AnnotStrikeOut:
		return true
	}
	return false
}

// overlayKind filters what the overlay paints: the fork lists every
// annot of the page, and links/popups/widgets are not this tooling's to
// decorate or delete through the annotation context.
func overlayKind(a pdf.PageAnnot) bool {
	switch a.Type {
	case fitz.AnnotLink, fitz.AnnotPopup, fitz.AnnotWidget, fitz.AnnotUnknown:
		return false
	}
	return true
}

// requestAnnots fetches page's annots once (the build calls it for every
// visible page): the listing lands on the UI thread through the worker
// hop, tagged with the ReloadGen it was issued under so a reply that
// raced a mutation is discarded instead of clobbering the refreshed
// state. Tab.Reload clears Listed, which is what makes every reload path
// (menu, a finished mutation) re-request.
func (s *AppState) requestAnnots(t *Tab, page int) {
	if t == nil || t.Doc == nil || !t.CanAnnotate() {
		return
	}
	if t.Annot.Listed == nil {
		t.Annot.Listed = map[int][]pdf.PageAnnot{}
	}
	if t.Annot.listPending == nil {
		t.Annot.listPending = map[int]bool{}
	}
	if backoff, ok := t.Annot.listErrBackoff[page]; ok && time.Now().Before(backoff) {
		return // a recent listing error: hold off rather than ping-pong per frame
	}
	if _, ok := t.Annot.Listed[page]; ok {
		return // have it
	}
	if t.Annot.listPending[page] {
		return // on its way
	}
	t.Annot.listPending[page] = true
	tabID, gen := t.ID, t.ReloadGen
	t.Doc.AnnotsAsync(page, func(as []pdf.PageAnnot, err error) {
		updateFromWorker(func() { // hop, never inline
			cur := s.tabByID(tabID)
			if cur == nil || cur.Doc == nil {
				return
			}
			if t := &cur.Annot; t.listPending != nil {
				delete(t.listPending, page)
			}
			if cur.ReloadGen != gen {
				return // a mutation superseded this listing; the next build re-requests
			}
			if err != nil {
				// Transient by construction (a page raced a structure
				// edit); back off 30s so a persistently failing listing
				// cannot re-request on every scroll frame.
				if cur.Annot.listErrBackoff == nil {
					cur.Annot.listErrBackoff = map[int]time.Time{}
				}
				cur.Annot.listErrBackoff[page] = time.Now().Add(30 * time.Second)
				return
			}
			cur.Annot.Listed[page] = as
			if window != nil {
				window.Invalidate() // paint the overlay now that data arrived
			}
		})
	})
}

// annotPaint is one overlay shape of an existing annot, computed at
// build time (plain data) and painted in the page's DrawOver pass.
type annotPaint struct {
	rect     [4]float32 // DIPs on the rotated, drawn page
	fill     bool       // highlight: translucent fill over the quads
	line     int        // 1 bottom edge (underline), 2 middle (strikeout/squiggly)
	marker   bool       // non-markup: a small corner badge
	selected bool
}

// annotPaintList maps page's listed annots into DIP-space shapes at the
// drawn scale k. The rendered bitmap already carries each annot's
// appearance; these shapes are the interactive affordance (hit feedback
// and selection), so the alphas stay subtle by design.
func annotPaintList(t *Tab, page int, k float64) []annotPaint {
	if !t.CanAnnotate() || t.Annot.Listed == nil {
		return nil
	}
	list := t.Annot.Listed[page]
	if len(list) == 0 {
		return nil
	}
	var out []annotPaint
	for i := range list {
		a := &list[i]
		if !overlayKind(*a) {
			continue
		}
		sel := t.Annot.SelOpen && t.Annot.SelID == a.ID && t.Annot.SelPage == page
		if markupKind(a.Type) {
			for _, q := range a.Quads {
				out = append(out, annotPaint{
					rect: quadDipRect(t, page, quadHitRect(q), k),
					fill: a.Type == fitz.AnnotHighlight,
					line: map[fitz.AnnotType]int{
						fitz.AnnotUnderline: 1,
						fitz.AnnotStrikeOut: 2, fitz.AnnotSquiggly: 2,
					}[a.Type],
					selected: sel,
				})
			}
			continue
		}
		// Non-markup: a small badge at the rect's top-left corner, the
		// annot's visible anchor on the rotated view.
		pt := spanDipRect(t, page, pdf.TextSpan{X: a.Rect.X0, Y: a.Rect.Y0}, k)
		out = append(out, annotPaint{rect: pt, marker: true, selected: sel})
	}
	return out
}

// quadHitRect reduces one quad to its bounding HitRect (min/max of the
// corners, robust against skewed quads).
func quadHitRect(q fitz.Quad) pdf.HitRect {
	return pdf.HitRect{
		X0: min(min(q.UL.X, q.UR.X), min(q.LL.X, q.LR.X)),
		Y0: min(min(q.UL.Y, q.UR.Y), min(q.LL.Y, q.LR.Y)),
		X1: max(max(q.UL.X, q.UR.X), max(q.LL.X, q.LR.X)),
		Y1: max(max(q.UL.Y, q.UR.Y), max(q.LL.Y, q.LR.Y)),
	}
}

// annotAt returns the topmost listed annot of page whose geometry
// contains the page-space point px,py (a few points of slop so thin
// underlines and small markers are clickable), or nil. Topmost is the
// LAST listing entry: the fork walks the page's /Annots in draw order.
func annotAt(t *Tab, page int, px, py float64) *pdf.PageAnnot {
	if !t.CanAnnotate() || t.Annot.Listed == nil {
		return nil
	}
	list := t.Annot.Listed[page]
	for i := len(list) - 1; i >= 0; i-- {
		a := &list[i]
		if !overlayKind(*a) {
			continue
		}
		if markupKind(a.Type) {
			for _, q := range a.Quads {
				r := quadHitRect(q)
				if px >= r.X0-2 && px <= r.X1+2 && py >= r.Y0-2 && py <= r.Y1+2 {
					return a
				}
			}
			continue
		}
		r := a.Rect
		if px >= r.X0-4 && px <= r.X1+4 && py >= r.Y0-4 && py <= r.Y1+4 {
			return a
		}
	}
	return nil
}

// selectAnnot opens the context panel for a hit-tested annot.
func (s *AppState) selectAnnot(t *Tab, page int, a *pdf.PageAnnot) {
	t.Annot.SelOpen = true
	t.Annot.SelID = a.ID
	t.Annot.SelPage = page
	t.Annot.SelType = a.Type.String()
}

// deleteSelectedAnnot removes the context panel's annot through the
// facade and closes the panel; the shared annotDone hop refreshes the
// overlay (Reload clears the listing; the next build re-requests it).
// The annot is snapshotted from the listing BEFORE the delete, so the
// undo entry can re-create it verbatim (undo.go); a selection missing
// from the listing deletes without an undo record.
func (s *AppState) deleteSelectedAnnot(t *Tab) {
	if t == nil || t.Doc == nil || !t.Annot.SelOpen || t.Annot.SelID == 0 {
		return
	}
	t.Annot.SelOpen = false
	page, id := t.Annot.SelPage, t.Annot.SelID
	var snap pdf.PageAnnot
	for _, a := range t.Annot.Listed[page] {
		if a.ID == id {
			snap = a
			break
		}
	}
	t.Annot.mutating = true
	if snap.ID == 0 {
		t.Doc.DeleteAnnotAsync(page, id, s.annotDone(t, "delete annotation"))
		return
	}
	t.Doc.DeleteAnnotAsync(page, id, s.annotDoneEx(t, "delete annotation",
		func(cur *Tab) {
			cur.Annot.Undo = append(cur.Annot.Undo,
				undoEntry{op: opDeleted, page: page, id: id, snap: snap})
			cur.Annot.Redo = nil
		}))
}

// annotContextOverlay renders the click-on-annot context panel: the type
// name, Delete and Close, with the Delete key bound while open. ui.Modal
// dismisses (Escape, backdrop) through its own flag-clearing; the sweep
// below drops the selection data with it.
func annotContextOverlay(c *ui.Context, s *AppState) {
	t := s.Active()
	if t == nil || !t.Annot.SelOpen || t.Annot.SelID == 0 {
		if t != nil {
			t.Annot.SelOpen = false // sweep: dismissed, selection dropped
		}
		return
	}
	ui.Modal(c, &t.Annot.SelOpen, func() { annotContextPanel(c, s, t) })
}

// annotContextPanel is the panel itself. The Delete key binds in the
// modal's own scope (window shortcuts wait behind a modal; the palette
// binds its keys the same way).
func annotContextPanel(c *ui.Context, s *AppState, t *Tab) {
	th := c.Theme()
	c.OnShortcut(0, ui.KeyDelete, func() { s.deleteSelectedAnnot(t) })
	ui.Column(c).Width(360).Gap(th.Space(2)).Children(func() {
		ui.Text(c, t.Annot.SelType).Bold().FontSize(th.FontSize * 1.15)
		ui.Text(c, fmt.Sprintf("Page %d", t.Annot.SelPage+1)).
			TextColor(th.TextMuted).FontSize(th.FontSize * 0.9).SingleLine()
		ui.Row(c).FillWidth().Justify(ui.End).Gap(th.Space(1)).Children(func() {
			if ui.Button(c, "Delete").Clicked() {
				s.deleteSelectedAnnot(t)
			}
			if ui.PrimaryButton(c, "Close").Clicked() {
				t.Annot.SelOpen = false
			}
		})
	})
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

	// The undo snapshot mirrors what the creator writes: quads carry the
	// geometry, color and opacity the style constants picked.
	rgb := func(c pdf.RGB) []float64 { return []float64{c.R, c.G, c.B} }
	snap := pdf.PageAnnot{Type: k.fitzType(), Quads: quads}
	var color pdf.RGB
	var opacity float64
	switch k {
	case AnnotToolHighlight:
		color, opacity = highlightColor, highlightOpacity
	case AnnotToolUnderline, AnnotToolStrikeout:
		color, opacity = markupBlack, 1
	}
	snap.Color, snap.Opacity = rgb(color), opacity

	t.Annot.mutating = true
	switch k {
	case AnnotToolHighlight:
		t.Doc.AddHighlightAsync(page, quads, highlightColor, highlightOpacity,
			s.recordCreate(t, annotToolLabel(k), page, snap))
	case AnnotToolUnderline:
		t.Doc.AddUnderlineAsync(page, quads, markupBlack, 1,
			s.recordCreate(t, annotToolLabel(k), page, snap))
	case AnnotToolStrikeout:
		t.Doc.AddStrikeoutAsync(page, quads, markupBlack, 1,
			s.recordCreate(t, annotToolLabel(k), page, snap))
	}
}
