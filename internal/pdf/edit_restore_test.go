package pdf

// edit_restore_test.go pins the exact-restoration half of the editing
// facade (issue 6, undo/redo): a PageAnnot snapshot plus CreateAnnotAsync
// with RestoreEdit re-creates an annotation verbatim, the invertibility
// DeleteAnnotAsync breaks. The reopen oracle (save, reopen with plain
// fitz) proves the restoration survives on disk, not just in the live
// document.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gen2brain/go-fitz"
)

// restoreOnce runs one create-delete-restore round for one snapshot and
// returns the restored annotation as the fork sees it after a reopen.
func restoreOnce(t *testing.T, d *Doc, page int, snap PageAnnot) fitz.Annot {
	t.Helper()

	// Delete, then restore through the generic creator.
	dCh := make(chan error, 1)
	d.DeleteAnnotAsync(page, snap.ID, func(err error) { dCh <- err })
	waitErr(t, dCh, "delete "+snap.Type.String())

	cCh := make(chan idReply, 1)
	d.CreateAnnotAsync(page, snap.Type, snap.RestoreEdit(), func(id int, err error) {
		cCh <- idReply{id, err}
	})
	newID := waitID(t, cCh, "restore "+snap.Type.String())
	if newID == snap.ID {
		t.Fatalf("%s: restore reused the deleted ID %d", snap.Type.String(), newID)
	}

	// Reopen oracle: the restored annotation must persist with the
	// snapshot's geometry, color, opacity and text.
	out := filepath.Join(t.TempDir(), "restored.pdf")
	sCh := make(chan error, 1)
	d.SaveAsync(out, fitz.SaveOptions{}, func(err error) { sCh <- err })
	waitErr(t, sCh, "save restored")
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	g := openGen(t, raw)
	annots, err := g.Annots(page)
	if err != nil {
		t.Fatalf("reopen annots: %v", err)
	}
	for _, a := range annots {
		if int(a.ID) == newID {
			return a
		}
	}
	t.Fatalf("%s: restored ID %d missing from the reopened listing: %+v",
		snap.Type.String(), newID, annots)
	return fitz.Annot{}
}

// TestFacadeRestoreRoundTrip: snapshots taken from the facade's own
// listing, restored through CreateAnnotAsync + RestoreEdit, must match
// the originals through a save/reopen — per kind the fields that kind
// carries.
func TestFacadeRestoreRoundTrip(t *testing.T) {
	path := writeTestPDF(t, genPDFBytes([]string{textContent("restore me")}, nil, nil))
	d := openTestDoc(t, path)

	quads := []fitz.Quad{{
		UL: fitz.Point{X: 72, Y: 60}, UR: fitz.Point{X: 220, Y: 60},
		LL: fitz.Point{X: 72, Y: 88}, LR: fitz.Point{X: 220, Y: 88},
	}}
	hCh := make(chan idReply, 1)
	d.AddHighlightAsync(0, quads, RGB{R: 1, G: 1}, 0.5, func(id int, err error) {
		hCh <- idReply{id, err}
	})
	waitID(t, hCh, "add highlight")

	nCh := make(chan idReply, 1)
	d.AddTextAsync(0, fitz.Rect{X0: 400, Y0: 100, X1: 420, Y1: 120}, "undone note", func(id int, err error) {
		nCh <- idReply{id, err}
	})
	waitID(t, nCh, "add note")

	// The listing must carry the full restoration payload, not just the
	// projection the overlay paints from.
	aCh := make(chan annotsReply, 1)
	d.AnnotsAsync(0, func(as []PageAnnot, err error) { aCh <- annotsReply{as, err} })
	annots := waitAnnots(t, aCh, "list annots")
	if len(annots) != 2 {
		t.Fatalf("annots: %d, want 2", len(annots))
	}
	var hSnap, nSnap PageAnnot
	for _, a := range annots {
		switch a.Type {
		case fitz.AnnotHighlight:
			hSnap = a
		case fitz.AnnotText:
			nSnap = a
		}
	}
	if hSnap.ID == 0 || nSnap.ID == 0 {
		t.Fatalf("listing missed a kind: %+v", annots)
	}
	if hSnap.Contents != "" || len(hSnap.Color) != 3 || !near(hSnap.Color[2], 0) || !near(hSnap.Opacity, 0.5) {
		t.Fatalf("highlight snapshot lost style: %+v", hSnap)
	}
	if nSnap.Contents != "undone note" {
		t.Fatalf("note snapshot lost contents: %+v", nSnap)
	}

	// Highlight: geometry, color and opacity must survive the round trip.
	hNew := restoreOnce(t, d, 0, hSnap)
	if len(hNew.QuadPoints) != 1 {
		t.Fatalf("restored highlight quads: %d, want 1", len(hNew.QuadPoints))
	}
	q := hNew.QuadPoints[0]
	if !near(q.UL.X, 72) || !near(q.UL.Y, 60) || !near(q.LR.X, 220) || !near(q.LR.Y, 88) {
		t.Fatalf("restored quad drifted: %+v", q)
	}
	if len(hNew.Color) != 3 || !near(hNew.Color[0], 1) || !near(hNew.Color[1], 1) || !near(hNew.Color[2], 0) {
		t.Fatalf("restored highlight color: %v, want [1 1 0]", hNew.Color)
	}
	if !near(hNew.Opacity, 0.5) {
		t.Fatalf("restored highlight opacity: %v, want 0.5", hNew.Opacity)
	}

	// Note: the body text must survive (the rect re-anchors the icon).
	nNew := restoreOnce(t, d, 0, nSnap)
	if nNew.Contents != "undone note" {
		t.Fatalf("restored note contents: %q", nNew.Contents)
	}
}

// TestFacadeCreateAnnotRejections: the generic creator applies the same
// validation dialect as the typed ones.
func TestFacadeCreateAnnotRejections(t *testing.T) {
	path := writeTestPDF(t, genPDFBytes([]string{textContent("reject me")}, nil, nil))
	d := openTestDoc(t, path)

	bad := []struct {
		what string
		t    fitz.AnnotType
		e    fitz.AnnotEdit
		want error
	}{{
		what: "markup without quads",
		t:    fitz.AnnotHighlight,
		want: ErrBadQuads,
	}, {
		what: "squiggly without quads",
		t:    fitz.AnnotSquiggly,
		want: ErrBadQuads,
	}, {
		what: "color out of range",
		t:    fitz.AnnotSquare,
		e:    fitz.AnnotEdit{Color: []float64{1, 0, 2}},
		want: ErrBadParam,
	}, {
		what: "opacity out of range",
		t:    fitz.AnnotSquare,
		e: fitz.AnnotEdit{
			Opacity: func() *float64 { v := 1.5; return &v }(),
		},
		want: ErrBadParam,
	}}
	for _, b := range bad {
		ch := make(chan idReply, 1)
		d.CreateAnnotAsync(0, b.t, b.e, func(id int, err error) { ch <- idReply{id, err} })
		if r := waitIDErr(t, ch, b.what); !errors.Is(r, b.want) {
			t.Fatalf("%s: %v, want %v", b.what, r, b.want)
		}
	}
}

// waitIDErr waits for one fn(id, err) callback and returns the error.
func waitIDErr(t *testing.T, ch <-chan idReply, what string) error {
	t.Helper()
	select {
	case r := <-ch:
		return r.err
	case <-time.After(30 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
		return nil
	}
}
