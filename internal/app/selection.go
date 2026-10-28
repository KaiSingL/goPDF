// selection.go is text selection in the viewer: a left-button drag over
// a page covers the text it crosses, the page overlay paints it, and
// Ctrl+C copies (shortcuts.go; Esc clears).
//
// Pointer events arrive at the page's input handler in LOCAL DIPs
// (relative to the page box, verified in mygo's engine.deliver), with
// press capture: moves and the release keep coming to the page the press
// started on even after the pointer leaves it, so the drag point is
// clamped to that page's bounds — one page per selection, v1 on purpose.
//
// The coordinate systems meet in two mappings, both rotation-aware and
// origin-aware like the search highlights':
//
//	dipToPagePt  local DIPs → page points (the inverse of spanDipRect)
//	quadDipRect  page points → local DIPs (shared with hitRects)
//
// both scaled by pageDipScale — the exact scale the page is DRAWN at,
// cap included — so the overlay cannot drift from the glyphs.
//
// GEOMETRY is MuPDF's own: every move asks the doc worker for
// fz_highlight_selection quads plus fz_copy_selection text over the
// page's structured text — the same text fz_search searches — which is
// exact where the index's span rectangles are estimates (the estimates
// that misaligned highlights on real documents before search switched
// to quads; selection hit the same wall). Each request carries the
// drag's generation counter, so a stale reply dies quietly; the fork
// caches a page's structured text, so only the first selection on a
// page pays the extraction. Until the first reply lands the raw drag
// rect paints — instant feedback, exact in a frame or two.
package app

import (
	"gopdf/internal/pdf"
)

// abs64 is |x| — the tiny float helper selection and the highlight
// tests share (Go's builtins cover min/max).
func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// dipToPagePt inverts spanDipRect for a point: coordinates local to the
// rotated, drawn page → page-space points (unrotated, box origin
// included, as spans and search quads are).
//
// The forward map (spanDipRect) for the four rotations, on the raw
// (unswapped) page size w0×h0:
//
//	0°:   (px, py) -> (px·k, py·k)
//	90°:  (px, py) -> ((h0-py)·k, px·k)
//	180°: (px, py) -> ((w0-px)·k, (h0-py)·k)
//	270°: (px, py) -> (py·k, (w0-px)·k)
func dipToPagePt(t *Tab, page int, k float64, x, y float32) (float64, float64) {
	w0, h0 := t.Doc.PageSize(page)
	var px, py float64
	switch t.Rotate {
	case 90:
		px, py = float64(y)/k, h0-float64(x)/k
	case 180:
		px, py = w0-float64(x)/k, h0-float64(y)/k
	case 270:
		px, py = w0-float64(y)/k, float64(x)/k
	default:
		px, py = float64(x)/k, float64(y)/k
	}
	ox, oy := t.Doc.PageOrigin(page)
	return px + ox, py + oy
}

// selRectPts is the selection's page-space rect, normalized.
func (t *Tab) selRectPts() (x0, y0, x1, y1 float64) {
	x0, y0 = t.Sel.Anchor[0], t.Sel.Anchor[1]
	x1, y1 = t.Sel.Cur[0], t.Sel.Cur[1]
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	return
}

// StartSelection begins a drag at x,y — local DIPs on page — replacing
// any previous selection, and asks the worker for the exact geometry.
func (s *AppState) StartSelection(t *Tab, page int, x, y float32) {
	if t == nil || t.Doc == nil {
		return
	}
	k := pageDipScale(t, page, t.Zoom)
	px, py := dipToPagePt(t, page, k, x, y)
	t.Sel = SelectionState{
		Active: true, Dragging: true, Page: page,
		Anchor: [2]float64{px, py}, Cur: [2]float64{px, py},
	}
	s.requestSelection(t)
}

// ExtendSelection moves the drag point. x,y are local DIPs on the
// pressed page and may lie outside it (the pointer can leave during a
// captured drag): they clamp to the page's bounds.
func (s *AppState) ExtendSelection(t *Tab, page int, x, y float32) {
	if t == nil || !t.Sel.Dragging || t.Sel.Page != page {
		return
	}
	k := pageDipScale(t, page, t.Zoom)
	px, py := dipToPagePt(t, page, k, x, y)
	w0, h0 := t.Doc.PageSize(page)
	ox, oy := t.Doc.PageOrigin(page)
	t.Sel.Cur = [2]float64{
		min(max(px, ox), ox+w0),
		min(max(py, oy), oy+h0),
	}
	s.requestSelection(t)
}

// FinishSelection ends the drag. A click — a press/release that moved
// less than a couple of points — clears instead: taps should not leave
// stray empty selections lying around.
func (s *AppState) FinishSelection(t *Tab) {
	if t == nil || !t.Sel.Dragging {
		return
	}
	t.Sel.Dragging = false
	x0, y0, x1, y1 := t.selRectPts()
	if x1-x0 < 2 && y1-y0 < 2 {
		t.ClearSelection()
	}
}

// ClearSelection drops the selection (Esc, a click, a new drag).
func (t *Tab) ClearSelection() { t.Sel = SelectionState{} }

// requestSelection asks the doc worker for the exact quads and text of
// the live selection. The generation counter kills replies that race a
// newer drag position (or a cleared selection).
func (s *AppState) requestSelection(t *Tab) {
	if !t.Sel.Active || t.Doc == nil {
		return
	}
	t.Sel.gen++
	gen := t.Sel.gen
	page := t.Sel.Page
	x0, y0, x1, y1 := t.selRectPts()
	t.Doc.SelectionAsync(page, x0, y0, x1, y1, func(res pdf.SelectionResult, err error) {
		updateFromWorker(func() { // worker-side: hop, never inline
			cur := s.Active()
			if cur == nil || cur.ID != t.ID || !cur.Sel.Active || cur.Sel.gen != gen {
				return // stale: newer drag point, cleared, or tab closed
			}
			if err != nil {
				return // keep the previous geometry; the next move retries
			}
			cur.Sel.Quads = res.Quads
			cur.Sel.Text = res.Text
			cur.Sel.resolved = true
		})
	})
}

// SelectionText is the selection as copyable text (fz_copy_selection —
// the same range the quads highlight, not a span join).
func (t *Tab) SelectionText() string {
	if !t.Sel.Active {
		return ""
	}
	return t.Sel.Text
}

// selPaintRects is the selection overlay of page at the drawn scale k
// (viewer.go paints them like search hits): the exact quads once the
// worker answered, else the raw drag rect for instant feedback. A
// resolved-but-empty selection paints nothing.
func selPaintRects(t *Tab, page int, k float64) [][4]float32 {
	if !t.Sel.Active || t.Sel.Page != page {
		return nil
	}
	if t.Sel.resolved {
		if len(t.Sel.Quads) == 0 {
			return nil
		}
		out := make([][4]float32, 0, len(t.Sel.Quads))
		for _, q := range t.Sel.Quads {
			out = append(out, quadDipRect(t, page, q, k))
		}
		return out
	}
	x0, y0, x1, y1 := t.selRectPts()
	return [][4]float32{
		spanDipRect(t, page, pdf.TextSpan{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}, k),
	}
}
