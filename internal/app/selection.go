// selection.go is text selection in the viewer: a left-button drag over
// a page covers the text spans it crosses, the page overlay paints them,
// and Ctrl+C copies (shortcuts.go; Esc clears).
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
//	spanDipRect  page points → local DIPs (shared with hitRects)
//
// and both scale with pageDipScale — the exact scale the page is DRAWN
// at, cap included — so the overlay cannot drift from the glyphs.
//
// The span set needs the doc's text index. A selection that starts
// before the index exists shows the raw drag rect immediately and
// resolves its spans when the build lands (the worker → UI hop of
// components.go, same as search); with the index already cached — the
// common case after any search — every move resolves synchronously.
package app

import (
	"strings"

	"gopdf/internal/pdf"
)

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

// abs64 is |x| — the tiny float helper selection.go and the highlight
// tests share (Go's builtins cover min/max).
func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// StartSelection begins a drag at x,y — local DIPs on page — replacing
// any previous selection, and makes sure the text index is coming.
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
	s.ensureSelectionIndex(t)
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
	if t.Index() != nil {
		t.resolveSelSpans() // cached index: every move resolves at once
	} else {
		s.ensureSelectionIndex(t)
	}
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

// ensureSelectionIndex starts the index build if the tab has none, and
// resolves the live selection when it lands (worker → UI hop; the
// pending flag keeps moves from stacking requests).
func (s *AppState) ensureSelectionIndex(t *Tab) {
	if t.Index() != nil || t.Sel.pending {
		return
	}
	t.Sel.pending = true
	t.Doc.IndexAsync(func(ix *pdf.Index, err error) {
		updateFromWorker(func() {
			cur := s.Active()
			if cur == nil || cur.ID != t.ID || !cur.Sel.Active || cur.Index() != nil {
				return // tab closed, selection gone, or another path built it
			}
			cur.Sel.pending = false
			if err == nil && ix != nil {
				cur.SetIndex(ix)
				cur.resolveSelSpans()
			}
		})
	})
}

// resolveSelSpans recomputes the covered spans from the index (UI
// thread; called on moves and when the index lands). The spans are the
// index's page order — the extraction's reading order.
func (t *Tab) resolveSelSpans() {
	ix := t.Index()
	if ix == nil || !t.Sel.Active {
		return
	}
	x0, y0, x1, y1 := t.selRectPts()
	t.Sel.Spans = spansOverlapping(ix, t.Sel.Page, pdf.HitRect{X0: x0, Y0: y0, X1: x1, Y1: y1})
	t.Sel.HaveSpans = true
}

// SelectionText is the selection as copyable text: spans joined with
// spaces within a line and newlines between lines. Two spans share a
// line when their baselines sit within 60% of the shorter span's height.
func (t *Tab) SelectionText() string {
	if !t.Sel.Active || len(t.Sel.Spans) == 0 {
		return ""
	}
	var b strings.Builder
	var prevY, prevH float64
	for i, sp := range t.Sel.Spans {
		if i > 0 {
			if abs64(sp.Y-prevY) < 0.6*min(sp.H, prevH) {
				b.WriteByte(' ')
			} else {
				b.WriteByte('\n')
			}
		}
		b.WriteString(sp.Text)
		prevY, prevH = sp.Y, sp.H
	}
	return b.String()
}

// selPaintRects is the selection overlay of page at the drawn scale k
// (viewer.go paints them like search hits): the covered spans once
// resolved, else the raw drag rect for instant feedback while the index
// is still on its way. A resolved-but-empty selection paints nothing.
func selPaintRects(t *Tab, page int, k float64) [][4]float32 {
	if !t.Sel.Active || t.Sel.Page != page {
		return nil
	}
	if t.Sel.HaveSpans {
		if len(t.Sel.Spans) == 0 {
			return nil
		}
		out := make([][4]float32, 0, len(t.Sel.Spans))
		for _, sp := range t.Sel.Spans {
			out = append(out, spanDipRect(t, page, sp, k))
		}
		return out
	}
	x0, y0, x1, y1 := t.selRectPts()
	return [][4]float32{
		spanDipRect(t, page, pdf.TextSpan{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}, k),
	}
}
