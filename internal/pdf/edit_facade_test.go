package pdf

// edit_facade_test.go exercises the editing facade (edit.go) through its
// async surface: every call is driven through a channel (callbacks run on
// the worker), and ground truth comes from the reopen oracle — save, then
// reopen the bytes with plain fitz (NewFromMemory) and assert structure
// independently of the facade's cached state.

import (
	"bytes"
	"errors"
	"image"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gen2brain/go-fitz"
)

// idReply carries one Add* callback result back to the test goroutine.
type idReply struct {
	id  int
	err error
}

// annotsReply carries one AnnotsAsync callback result.
type annotsReply struct {
	annots []PageAnnot
	err    error
}

// waitErr waits for one fn(err) callback and fails the test unless it is nil.
func waitErr(t *testing.T, ch <-chan error, what string) {
	t.Helper()
	select {
	case err := <-ch:
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
	}
}

// recvErr waits for one fn(err) callback and returns its error (for the
// pass-through and rejection checks).
func recvErr(t *testing.T, ch <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(30 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
		return nil
	}
}

// waitID waits for one Add* callback and returns the new annotation's ID.
func waitID(t *testing.T, ch <-chan idReply, what string) int {
	t.Helper()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("%s: %v", what, r.err)
		}
		return r.id
	case <-time.After(30 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
		return 0
	}
}

// waitAnnots waits for one AnnotsAsync callback and returns the listing.
func waitAnnots(t *testing.T, ch <-chan annotsReply, what string) []PageAnnot {
	t.Helper()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("%s: %v", what, r.err)
		}
		return r.annots
	case <-time.After(30 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
		return nil
	}
}

// near compares floats that survived a PDF round trip (user space to page
// space and back) with point-level tolerance.
func near(a, b float64) bool { return math.Abs(a-b) < 1 }

func TestFacadeAnnotRoundTrip(t *testing.T) {
	path := writeTestPDF(t, genPDFBytes([]string{textContent("annotate me")}, nil, nil))
	d := openTestDoc(t, path)

	// Highlight over the first line (page space, upper edge y=60), a sticky
	// note in the margin, and a FreeText box below it.
	quads := []fitz.Quad{{
		UL: fitz.Point{X: 72, Y: 60}, UR: fitz.Point{X: 220, Y: 60},
		LL: fitz.Point{X: 72, Y: 88}, LR: fitz.Point{X: 220, Y: 88},
	}}
	hCh := make(chan idReply, 1)
	d.AddHighlightAsync(0, quads, RGB{R: 1, G: 1}, 0.5, func(id int, err error) {
		hCh <- idReply{id, err}
	})
	hID := waitID(t, hCh, "add highlight")

	nCh := make(chan idReply, 1)
	d.AddTextAsync(0, fitz.Rect{X0: 400, Y0: 100, X1: 420, Y1: 120}, "a note", func(id int, err error) {
		nCh <- idReply{id, err}
	})
	nID := waitID(t, nCh, "add note")

	fCh := make(chan idReply, 1)
	d.AddFreeTextAsync(0, fitz.Rect{X0: 200, Y0: 600, X1: 400, Y1: 650}, "free body", func(id int, err error) {
		fCh <- idReply{id, err}
	})
	fID := waitID(t, fCh, "add freetext")

	// The facade's own listing must map all three: right IDs and types,
	// quads on the highlight, Text only on the two text kinds.
	aCh := make(chan annotsReply, 1)
	d.AnnotsAsync(0, func(as []PageAnnot, err error) { aCh <- annotsReply{as, err} })
	annots := waitAnnots(t, aCh, "list annots")
	if len(annots) != 3 {
		t.Fatalf("facade annots: %d, want 3", len(annots))
	}
	byID := make(map[int]PageAnnot, len(annots))
	for _, a := range annots {
		byID[a.ID] = a
	}
	if _, ok := byID[hID]; !ok {
		t.Fatalf("highlight ID %d missing from listing: %+v", hID, annots)
	}
	if _, ok := byID[nID]; !ok {
		t.Fatalf("note ID %d missing from listing: %+v", nID, annots)
	}
	if _, ok := byID[fID]; !ok {
		t.Fatalf("freetext ID %d missing from listing: %+v", fID, annots)
	}
	if h := byID[hID]; h.Type != fitz.AnnotHighlight || len(h.Quads) != 1 || h.Contents != "" {
		t.Fatalf("highlight mapping: %+v", h)
	}
	if n := byID[nID]; n.Type != fitz.AnnotText || n.Contents != "a note" {
		t.Fatalf("note mapping: %+v", n)
	}
	if f := byID[fID]; f.Type != fitz.AnnotFreeText || f.Contents != "free body" {
		t.Fatalf("freetext mapping: %+v", f)
	}

	// Reopen oracle: save to a new path, read the bytes, reopen with plain
	// fitz and assert type, geometry and text survived.
	out := filepath.Join(t.TempDir(), "annotated.pdf")
	sCh := make(chan error, 1)
	d.SaveAsync(out, fitz.SaveOptions{}, func(err error) { sCh <- err })
	waitErr(t, sCh, "save annotated")
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	g := openGen(t, raw)
	persisted, err := g.Annots(0)
	if err != nil {
		t.Fatalf("reopen annots: %v", err)
	}
	if len(persisted) != 3 {
		t.Fatalf("persisted annots: %d, want 3", len(persisted))
	}
	pByType := make(map[fitz.AnnotType]fitz.Annot, len(persisted))
	for _, a := range persisted {
		pByType[a.Type] = a
	}
	h := pByType[fitz.AnnotHighlight]
	if len(h.QuadPoints) != 1 {
		t.Fatalf("persisted highlight quads: %d, want 1", len(h.QuadPoints))
	}
	q := h.QuadPoints[0]
	if !near(q.UL.X, 72) || !near(q.UL.Y, 60) || !near(q.LR.X, 220) || !near(q.LR.Y, 88) {
		t.Fatalf("persisted quad drifted: %+v", q)
	}
	if len(h.Color) != 3 || !near(h.Color[0], 1) || !near(h.Color[1], 1) || !near(h.Color[2], 0) {
		t.Fatalf("persisted highlight color: %v, want [1 1 0]", h.Color)
	}
	if !near(h.Opacity, 0.5) {
		t.Fatalf("persisted highlight opacity: %v, want 0.5", h.Opacity)
	}
	if n := pByType[fitz.AnnotText]; n.Contents != "a note" {
		t.Fatalf("persisted note contents: %q", n.Contents)
	}
	if f := pByType[fitz.AnnotFreeText]; f.Contents != "free body" {
		t.Fatalf("persisted freetext contents: %q", f.Contents)
	}

	// Delete the highlight; the stale-ref error must pass through unchanged.
	dCh := make(chan error, 1)
	d.DeleteAnnotAsync(0, hID, func(err error) { dCh <- err })
	waitErr(t, dCh, "delete highlight")
	stCh := make(chan error, 1)
	d.DeleteAnnotAsync(0, hID, func(err error) { stCh <- err })
	if err := recvErr(t, stCh, "stale delete"); !errors.Is(err, fitz.ErrStaleRef) {
		t.Fatalf("delete stale annot: %v, want fitz.ErrStaleRef", err)
	}

	aCh2 := make(chan annotsReply, 1)
	d.AnnotsAsync(0, func(as []PageAnnot, err error) { aCh2 <- annotsReply{as, err} })
	if left := waitAnnots(t, aCh2, "relist annots"); len(left) != 2 {
		t.Fatalf("annots after delete: %d, want 2", len(left))
	}

	out2 := filepath.Join(t.TempDir(), "deleted.pdf")
	s2Ch := make(chan error, 1)
	d.SaveAsync(out2, fitz.SaveOptions{}, func(err error) { s2Ch <- err })
	waitErr(t, s2Ch, "save deleted")
	raw2, err := os.ReadFile(out2)
	if err != nil {
		t.Fatal(err)
	}
	g2 := openGen(t, raw2)
	persisted2, err := g2.Annots(0)
	if err != nil {
		t.Fatalf("reopen deleted: %v", err)
	}
	if len(persisted2) != 2 {
		t.Fatalf("persisted annots after delete: %d, want 2", len(persisted2))
	}
	for _, a := range persisted2 {
		if a.Type == fitz.AnnotHighlight {
			t.Fatal("deleted highlight survived the save")
		}
	}
}

func TestFacadePageOrganizeAndGraft(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.pdf")
	if err := os.WriteFile(aPath, genPDFBytes([]string{
		textContent("one"), textContent("two"), textContent("three"),
	}, nil, nil), 0o644); err != nil {
		t.Fatal(err)
	}
	bPath := filepath.Join(dir, "b.pdf")
	if err := os.WriteFile(bPath, genPDFBytes([]string{
		textContent("G1"), textContent("G2"),
	}, nil, nil), 0o644); err != nil {
		t.Fatal(err)
	}
	d := openTestDoc(t, aPath)

	// Append a 300x400 blank; cached geometry must reflect it before any
	// save happens (the re-sync half of the contract).
	iCh := make(chan error, 1)
	d.InsertBlankPageAsync(2, [2]float64{300, 400}, func(err error) { iCh <- err })
	waitErr(t, iCh, "insert blank")
	if d.NumPages() != 4 {
		t.Fatalf("NumPages after insert: %d, want 4", d.NumPages())
	}
	if w, h := d.PageSize(3); w != 300 || h != 400 {
		t.Fatalf("PageSize(3) after insert: %gx%g, want 300x400", w, h)
	}

	// Absolute rotate of page 1 ("two"), which survives the delete below.
	rCh := make(chan error, 1)
	d.RotatePageAsync(1, 90, func(err error) { rCh <- err })
	waitErr(t, rCh, "rotate page 1")
	if w, h := d.PageSize(1); w != 792 || h != 612 {
		t.Fatalf("PageSize(1) after rotate: %gx%g, want 792x612", w, h)
	}

	// Delete page 0 ("one"): "two" slides to index 0 with its rotation.
	xCh := make(chan error, 1)
	d.DeletePageAsync(0, func(err error) { xCh <- err })
	waitErr(t, xCh, "delete page 0")
	if d.NumPages() != 3 {
		t.Fatalf("NumPages after delete: %d, want 3", d.NumPages())
	}
	if w, h := d.PageSize(0); w != 792 || h != 612 {
		t.Fatalf("PageSize(0) after delete: %gx%g, want 792x612", w, h)
	}

	// Graft page 1 of b.pdf ("G2") after page 0: two, G2, three, blank.
	gCh := make(chan error, 1)
	d.GraftPageAsync(bPath, 1, 0, func(err error) { gCh <- err })
	waitErr(t, gCh, "graft page")
	if d.NumPages() != 4 {
		t.Fatalf("NumPages after graft: %d, want 4", d.NumPages())
	}
	if w, h := d.PageSize(1); w != 612 || h != 792 {
		t.Fatalf("PageSize(1) after graft: %gx%g, want 612x792", w, h)
	}

	out := filepath.Join(dir, "organized.pdf")
	sCh := make(chan error, 1)
	d.SaveAsync(out, fitz.SaveOptions{}, func(err error) { sCh <- err })
	waitErr(t, sCh, "save organized")

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	g := openGen(t, raw)
	if g.NumPage() != 4 {
		t.Fatalf("reopened pages: %d, want 4", g.NumPage())
	}
	for i, want := range []string{"two", "G2", "three"} {
		if s := pageText(t, g, i); !bytes.Contains([]byte(s), []byte(want)) {
			t.Fatalf("reopened page %d text: %q, want %q", i, s, want)
		}
	}
	if s := pageText(t, g, 3); bytes.Contains([]byte(s), []byte("G")) {
		t.Fatalf("blank page 3 carries text: %q", s)
	}
	b0, err := g.Bound(0)
	if err != nil {
		t.Fatal(err)
	}
	if b0.Dx() != 792 || b0.Dy() != 612 {
		t.Fatalf("reopened bounds of rotated page: %v, want 792x612", b0)
	}
	b3, err := g.Bound(3)
	if err != nil {
		t.Fatal(err)
	}
	if b3.Dx() != 300 || b3.Dy() != 400 {
		t.Fatalf("reopened bounds of blank page: %v, want 300x400", b3)
	}
}

func TestFacadeSaveNewPathLeavesOriginal(t *testing.T) {
	path := writeTestPDF(t, genPDFBytes([]string{textContent("keep original")}, nil, nil))
	orig, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d := openTestDoc(t, path)

	nCh := make(chan idReply, 1)
	d.AddTextAsync(0, fitz.Rect{X0: 40, Y0: 40, X1: 60, Y1: 60}, "sidecar", func(id int, err error) {
		nCh <- idReply{id, err}
	})
	waitID(t, nCh, "add note")

	dir := t.TempDir()
	out := filepath.Join(dir, "copy.pdf")
	sCh := make(chan error, 1)
	d.SaveAsync(out, fitz.SaveOptions{}, func(err error) { sCh <- err })
	waitErr(t, sCh, "save as")

	// Atomic write: only the target exists, and the original is untouched.
	now, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(orig, now) {
		t.Fatal("save to a new path modified the original file")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "copy.pdf" {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	g := openGen(t, raw)
	annots, err := g.Annots(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(annots) != 1 || annots[0].Contents != "sidecar" {
		t.Fatalf("saved copy annots: %+v, want one sidecar note", annots)
	}
}

func TestFacadeIncrementalSaveSamePath(t *testing.T) {
	path := writeTestPDF(t, genPDFBytes([]string{textContent("title me")}, nil, nil))
	orig, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d := openTestDoc(t, path)

	nCh := make(chan idReply, 1)
	d.AddTextAsync(0, fitz.Rect{X0: 40, Y0: 40, X1: 60, Y1: 60}, "incremental note", func(id int, err error) {
		nCh <- idReply{id, err}
	})
	waitID(t, nCh, "add note")

	sCh := make(chan error, 1)
	d.SaveAsync(d.Path(), fitz.SaveOptions{Incremental: true}, func(err error) { sCh <- err })
	waitErr(t, sCh, "incremental save")

	now, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(now) <= len(orig) {
		t.Fatalf("incremental save shrank or kept size: %d -> %d", len(orig), len(now))
	}
	if !bytes.HasPrefix(now, orig) {
		t.Fatal("incremental save is not prefixed by the original bytes")
	}

	g := openGen(t, now)
	annots, err := g.Annots(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(annots) != 1 || annots[0].Contents != "incremental note" {
		t.Fatalf("reopened annots: %+v, want one incremental note", annots)
	}
}

// TestFacadeMutationWithRendersInFlight queues page renders, then mutates
// (delete a page, add a visible square) while they are queued. Nothing may
// panic or error; once the mutation callbacks have fired, the next render
// must show the edited document: the red square in the pixels and the page
// count dropped.
func TestFacadeMutationWithRendersInFlight(t *testing.T) {
	path := writeTextPDF(t, "kept page", "doomed page")
	d := openTestDoc(t, path)

	renderErrs := make(chan error, 16)
	for i := 0; i < 4; i++ {
		d.RenderAsync(RenderReq{Page: 0, DPI: 72, Kind: KindPage}, func(img *image.RGBA, err error) {
			renderErrs <- err
		})
	}

	// Mutations land behind the queued renders (hi lane is FIFO) but ahead
	// of any lo work; both complete before their callbacks fire.
	xCh := make(chan error, 1)
	d.DeletePageAsync(1, func(err error) { xCh <- err })
	sqCh := make(chan idReply, 1)
	d.AddSquareAsync(0, fitz.Rect{X0: 100, Y0: 100, X1: 260, Y1: 260}, RGB{R: 1}, 1, func(id int, err error) {
		sqCh <- idReply{id, err}
	})
	waitErr(t, xCh, "delete page")
	waitID(t, sqCh, "add square")

	if d.NumPages() != 1 {
		t.Fatalf("NumPages after mutation: %d, want 1", d.NumPages())
	}

	// The queued renders of page 0 all complete without error (their page
	// was never touched); whether they show the square is unspecified.
	for i := 0; i < 4; i++ {
		if err := recvErr(t, renderErrs, "queued render"); err != nil {
			t.Fatalf("queued render: %v", err)
		}
	}

	// A render issued after the mutation callbacks must be fresh: the red
	// square annotation must be in the pixels (fz_run_page renders annots).
	imgCh := make(chan *image.RGBA, 1)
	fErr := make(chan error, 1)
	d.RenderAsync(RenderReq{Page: 0, DPI: 144, Kind: KindPage}, func(img *image.RGBA, err error) {
		if err != nil {
			fErr <- err
			return
		}
		imgCh <- img
	})
	select {
	case err := <-fErr:
		t.Fatalf("fresh render: %v", err)
	case img := <-imgCh:
		red := 0
		for y := 0; y < img.Bounds().Dy(); y++ {
			for x := 0; x < img.Bounds().Dx(); x++ {
				c := img.RGBAAt(x, y)
				if c.R > 150 && c.G < 120 && c.B < 120 {
					red++
				}
			}
		}
		// The 160pt square's border at 144 DPI is thousands of pixels;
		// a stale render (no annotation) has none.
		if red < 100 {
			t.Fatalf("render after mutation lacks the square: %d red pixels", red)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for fresh render")
	}
}

func TestFacadeResyncAfterDeleteLastPage(t *testing.T) {
	path := writeTextPDF(t, "only page", "second page")
	d := openTestDoc(t, path)

	xCh := make(chan error, 1)
	d.DeletePageAsync(1, func(err error) { xCh <- err })
	waitErr(t, xCh, "delete last page")
	if d.NumPages() != 1 {
		t.Fatalf("NumPages after deleting the last page: %d, want 1", d.NumPages())
	}

	out := filepath.Join(t.TempDir(), "shrunk.pdf")
	sCh := make(chan error, 1)
	d.SaveAsync(out, fitz.SaveOptions{}, func(err error) { sCh <- err })
	waitErr(t, sCh, "save shrunk")

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	g := openGen(t, raw)
	if g.NumPage() != 1 {
		t.Fatalf("reopened pages: %d, want 1 (cached count out of sync)", g.NumPage())
	}
	if s := pageText(t, g, 0); !bytes.Contains([]byte(s), []byte("only page")) {
		t.Fatalf("reopened page 0: %q", s)
	}
}

func TestFacadeBadArgs(t *testing.T) {
	d := openTestDoc(t, writeTextPDF(t, "args"))

	// Out-of-range page: rejected without touching the worker.
	aCh := make(chan error, 1)
	d.AnnotsAsync(9, func(as []PageAnnot, err error) { aCh <- err })
	if err := recvErr(t, aCh, "annots bad page"); err != ErrBadPage {
		t.Fatalf("annots bad page: %v, want ErrBadPage", err)
	}

	// Highlight without quads.
	hCh := make(chan error, 1)
	d.AddHighlightAsync(0, nil, RGB{R: 1}, 1, func(id int, err error) { hCh <- err })
	if err := recvErr(t, hCh, "empty quads"); err != ErrBadQuads {
		t.Fatalf("empty quads: %v, want ErrBadQuads", err)
	}

	// Rotation not a 90-degree step.
	rCh := make(chan error, 1)
	d.RotatePageAsync(0, 45, func(err error) { rCh <- err })
	if err := recvErr(t, rCh, "bad rotation"); err != ErrBadRotate {
		t.Fatalf("bad rotation: %v, want ErrBadRotate", err)
	}

	// Blank page with a non-positive dimension.
	sCh := make(chan error, 1)
	d.InsertBlankPageAsync(-1, [2]float64{0, 792}, func(err error) { sCh <- err })
	if err := recvErr(t, sCh, "bad size"); err != ErrBadSize {
		t.Fatalf("bad size: %v, want ErrBadSize", err)
	}

	// Insert position beyond the end.
	pCh := make(chan error, 1)
	d.InsertBlankPageAsync(9, [2]float64{612, 792}, func(err error) { pCh <- err })
	if err := recvErr(t, pCh, "bad insert position"); err != ErrBadPage {
		t.Fatalf("bad insert position: %v, want ErrBadPage", err)
	}

	// Graft position beyond the end.
	gCh := make(chan error, 1)
	d.GraftPageAsync("whatever.pdf", 0, 9, func(err error) { gCh <- err })
	if err := recvErr(t, gCh, "bad graft position"); err != ErrBadPage {
		t.Fatalf("bad graft position: %v, want ErrBadPage", err)
	}
}
