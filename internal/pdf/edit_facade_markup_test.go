package pdf

// edit_facade_markup_test.go covers the issue-3 pure additions to the
// editing facade (AddUnderlineAsync, AddStrikeoutAsync): creation over
// selection-style quads, the facade's own listing, and the reopen oracle
// that pins geometry and style through a save. The validation halves
// (ErrBadQuads, ErrBadParam) ride the shared addMarkupAsync body, whose
// checks are byte-identical to AddHighlightAsync's; those paths are
// exercised inline here rather than through a copy of the highlight
// suite.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gen2brain/go-fitz"
)

// markupQuads is one selection-style quad band on the first text line.
func markupQuads() []fitz.Quad {
	return []fitz.Quad{{
		UL: fitz.Point{X: 72, Y: 60}, UR: fitz.Point{X: 220, Y: 60},
		LL: fitz.Point{X: 72, Y: 88}, LR: fitz.Point{X: 220, Y: 88},
	}}
}

// TestFacadeUnderlineStrikeoutRoundTrip: both creators persist their
// subtype, quads, color and opacity through a save; the IDs surface in
// AnnotsAsync with the right types.
func TestFacadeUnderlineStrikeoutRoundTrip(t *testing.T) {
	path := writeTestPDF(t, genPDFBytes([]string{textContent("markup me")}, nil, nil))
	d := openTestDoc(t, path)

	uCh := make(chan idReply, 1)
	d.AddUnderlineAsync(0, markupQuads(), RGB{}, 1, func(id int, err error) {
		uCh <- idReply{id, err}
	})
	uID := waitID(t, uCh, "add underline")

	sCh := make(chan idReply, 1)
	d.AddStrikeoutAsync(0, markupQuads(), RGB{R: 1, G: 0, B: 0}, 0.75, func(id int, err error) {
		sCh <- idReply{id, err}
	})
	sID := waitID(t, sCh, "add strikeout")

	// The facade's listing maps both, quads carried, no text.
	aCh := make(chan annotsReply, 1)
	d.AnnotsAsync(0, func(as []PageAnnot, err error) { aCh <- annotsReply{as, err} })
	annots := waitAnnots(t, aCh, "list markup annots")
	byID := make(map[int]PageAnnot, len(annots))
	for _, a := range annots {
		byID[a.ID] = a
	}
	if u, ok := byID[uID]; !ok || u.Type != fitz.AnnotUnderline || len(u.Quads) != 1 || u.Text != "" {
		t.Fatalf("underline mapping: present %v %+v", ok, byID[uID])
	}
	if s, ok := byID[sID]; !ok || s.Type != fitz.AnnotStrikeOut || len(s.Quads) != 1 {
		t.Fatalf("strikeout mapping: present %v %+v", ok, byID[sID])
	}

	// Reopen oracle: subtypes, geometry and style survive the save.
	out := filepath.Join(t.TempDir(), "markup.pdf")
	svCh := make(chan error, 1)
	d.SaveAsync(out, fitz.SaveOptions{}, func(err error) { svCh <- err })
	waitErr(t, svCh, "save markup")
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	g := openGen(t, raw)
	persisted, err := g.Annots(0)
	if err != nil {
		t.Fatalf("reopen markup annots: %v", err)
	}
	pByType := make(map[fitz.AnnotType]fitz.Annot, len(persisted))
	for _, a := range persisted {
		pByType[a.Type] = a
	}
	u := pByType[fitz.AnnotUnderline]
	if len(u.QuadPoints) != 1 {
		t.Fatalf("persisted underline quads: %d, want 1", len(u.QuadPoints))
	}
	if q := u.QuadPoints[0]; !near(q.UL.X, 72) || !near(q.UL.Y, 60) || !near(q.LR.X, 220) || !near(q.LR.Y, 88) {
		t.Fatalf("persisted underline quad drifted: %+v", q)
	}
	if !near(u.Opacity, 1) {
		t.Fatalf("persisted underline opacity: %v, want 1", u.Opacity)
	}
	s := pByType[fitz.AnnotStrikeOut]
	if !near(s.Opacity, 0.75) {
		t.Fatalf("persisted strikeout opacity: %v, want 0.75", s.Opacity)
	}
	if len(s.Color) != 3 || !near(s.Color[0], 1) || !near(s.Color[1], 0) || !near(s.Color[2], 0) {
		t.Fatalf("persisted strikeout color: %v, want [1 0 0]", s.Color)
	}
}

// TestFacadeMarkupRejections: the shared validation rejects empty quads
// and out-of-range style with the facade's own errors, before any worker
// round trip matters.
func TestFacadeMarkupRejections(t *testing.T) {
	path := writeTestPDF(t, genPDFBytes([]string{textContent("markup me")}, nil, nil))
	d := openTestDoc(t, path)

	qCh := make(chan idReply, 1)
	d.AddUnderlineAsync(0, nil, RGB{}, 1, func(id int, err error) { qCh <- idReply{id, err} })
	if r := <-qCh; !errors.Is(r.err, ErrBadQuads) {
		t.Fatalf("empty quads: %v, want ErrBadQuads", r.err)
	}
	cCh := make(chan idReply, 1)
	d.AddStrikeoutAsync(0, markupQuads(), RGB{G: 2}, 1, func(id int, err error) { cCh <- idReply{id, err} })
	if r := <-cCh; !errors.Is(r.err, ErrBadParam) {
		t.Fatalf("bad color: %v, want ErrBadParam", r.err)
	}
	oCh := make(chan idReply, 1)
	d.AddUnderlineAsync(0, markupQuads(), RGB{}, 1.5, func(id int, err error) { oCh <- idReply{id, err} })
	if r := <-oCh; !errors.Is(r.err, ErrBadParam) {
		t.Fatalf("bad opacity: %v, want ErrBadParam", r.err)
	}
}
