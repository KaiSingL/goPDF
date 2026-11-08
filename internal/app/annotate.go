// annotate.go is the annotation-creation state machine (issue 3): a
// pending tool kind on the tab, the selection-to-markup flow that turns
// the NEXT finished text selection into a highlight, underline or
// strikeout over the selection pipeline's exact quads, and the error
// routing into StatusNotice. Note placement and the square drag arrive
// with commit 2 (note + square); the existing-annot overlay with commit 3.
//
// # Flow (markup kinds)
//
// Arming a tool (toolbar, Ctrl+Shift+H/U/K, palette) only sets
// Tab.Annot.Tool. The user then drags text exactly as before
// (selection.go); when the drag finishes AND the worker's exact quads
// have landed (either order — the release may beat the last reply),
// maybeCreateMarkup fires the facade call over the resolved quads, exits
// the mode and clears the selection. The mutation callback (worker
// goroutine) hops to the UI thread, re-checks the tab and either reports
// through StatusNotice or drops the page bitmaps (Tab.Reload): the
// render pipeline draws annotation appearances, so the new annot appears
// through a fresh render, not by painting it here.
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
package app

import (
	"fmt"
	"path/filepath"

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
// underline and strikeout black at full opacity.
var (
	highlightColor = pdf.RGB{R: 1, G: 0.9, B: 0.3}
	markupBlack    = pdf.RGB{R: 0, G: 0, B: 0}
)

const highlightOpacity = 0.5

// AnnotState is the annotation state of one tab. UI thread only, like the
// rest of Tab; worker results land through updateFromWorker hops that
// re-check the tab.
type AnnotState struct {
	// Tool is the pending creation tool; the next selection (markup) or
	// click/drag (note, square) consumes it.
	Tool AnnotToolKind
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

// annotToolButton is one toolbar button of the annotation group (commit
// 1: the three markup kinds). The armed tool reads as the active tab
// does: an accent-tinted surface.
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

	done := func(id int, err error) { // doc worker goroutine
		updateFromWorker(func() { // hop, never inline
			cur := s.Active()
			if cur == nil || cur.ID != t.ID || cur.Doc == nil {
				return // stale: tab closed while the edit was in flight
			}
			if err != nil {
				// ErrBadPage/ErrBadQuads/ErrBadParam cannot arise from a
				// live selection, but they pass through here untouched if
				// they ever do.
				s.StatusNotice = fmt.Sprintf("%s: %s: %v",
					filepath.Base(cur.Path), annotToolLabel(k), err)
				return
			}
			cur.Reload() // the render draws the new appearance (freshness contract)
			if window != nil {
				window.Invalidate()
			}
		})
	}
	switch k {
	case AnnotToolHighlight:
		t.Doc.AddHighlightAsync(page, quads, highlightColor, highlightOpacity, done)
	case AnnotToolUnderline:
		t.Doc.AddUnderlineAsync(page, quads, markupBlack, 1, done)
	case AnnotToolStrikeout:
		t.Doc.AddStrikeoutAsync(page, quads, markupBlack, 1, done)
	}
}
