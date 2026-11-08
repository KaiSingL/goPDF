package pdf

// edit_facade_review_test.go pins the contracts the issue-1 review found
// unpinned: the stMu locking under a -race hammer, render-vs-vanished-page
// error paths, the delete-to-empty edge, the full-save-then-incremental
// sequence, and the synchronous Save's observable completion.

import (
	"image"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gen2brain/go-fitz"
)

// idleFence queues one no-op hi task and waits for it. When the fence
// returns, every task queued before it (and its resync) has completed, so
// the test goroutine can read the derived state deterministically.
func idleFence(t *testing.T, d *Doc) {
	t.Helper()
	ch := make(chan struct{}, 1)
	d.hi <- func(*fitz.Document) { ch <- struct{}{} }
	select {
	case <-ch:
	case <-time.After(30 * time.Second):
		t.Fatal("timeout on idle fence")
	}
}

// TestFacadeHammerDerivedState mutates the page structure while another
// goroutine hammers NumPages/PageSize/PageOrigin/Outline in a loop, under
// -race. A regression to an unlocked derived-state read shows up as a race
// report; a torn count as a value other than 1 or 2; a panic as a crash.
func TestFacadeHammerDerivedState(t *testing.T) {
	d := openTestDoc(t, writeTextPDF(t, "hammer page"))

	stop := make(chan struct{})
	done := make(chan struct{})
	var (
		mu         sync.Mutex
		observed   = map[int]int{}
		neverEmpty = true
	)
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			n := d.NumPages()
			mu.Lock()
			observed[n]++
			if n == 0 {
				neverEmpty = false
			}
			mu.Unlock()
			_, _ = d.PageSize(0)
			_, _ = d.PageOrigin(0)
			_ = d.Outline()
		}
	}()

	for i := 0; i < 30; i++ {
		inCh := make(chan error, 1)
		d.InsertBlankPageAsync(-1, [2]float64{300, 300}, func(err error) { inCh <- err })
		waitErr(t, inCh, "hammer insert")
		delCh := make(chan error, 1)
		d.DeletePageAsync(0, func(err error) { delCh <- err })
		waitErr(t, delCh, "hammer delete")
	}
	close(stop)
	<-done
	idleFence(t, d)
	if n := d.NumPages(); n != 1 {
		t.Fatalf("NumPages after hammer settle: %d, want 1", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if !neverEmpty {
		t.Fatal("hammer observed an empty page count mid-mutation")
	}
	for n := range observed {
		if n != 1 && n != 2 {
			t.Fatalf("hammer observed page count %d, only 1 or 2 valid", n)
		}
	}
}

func TestFacadeRenderOfVanishedPage(t *testing.T) {
	d := openTestDoc(t, writeTextPDF(t, "kept", "vanished"))

	// The deletion is queued first (hi is FIFO): the render of page 1
	// runs after the page no longer exists, no matter whether the
	// caller-goroutine bounds check (racing the mutation callback)
	// accepted it.
	xCh := make(chan error, 1)
	d.DeletePageAsync(1, func(err error) { xCh <- err })
	imgCh := make(chan *image.RGBA, 1)
	rCh := make(chan error, 1)
	d.RenderAsync(RenderReq{Page: 1, DPI: 72, Kind: KindPage}, func(img *image.RGBA, err error) {
		if img != nil {
			imgCh <- img
			return
		}
		rCh <- err
	})

	select {
	case err := <-rCh:
		if err == nil {
			t.Fatal("render of vanished page: nil error with nil image")
		}
	case img := <-imgCh:
		t.Fatalf("render of vanished page returned pixels (%dx%d), want an error",
			img.Bounds().Dx(), img.Bounds().Dy())
	case <-time.After(30 * time.Second):
		t.Fatal("timeout: render of a vanished page never delivered")
	}
}

func TestFacadeDeleteToEmpty(t *testing.T) {
	d := openTestDoc(t, writeTextPDF(t, "last page standing"))

	xCh := make(chan error, 1)
	d.DeletePageAsync(0, func(err error) { xCh <- err })
	waitErr(t, xCh, "delete only page")

	if n := d.NumPages(); n != 0 {
		t.Fatalf("NumPages of emptied doc: %d, want 0", n)
	}
	// PageSize survives the emptied doc (dead page falls through its
	// letter-size fallback) instead of indexing nil.
	w, h := d.PageSize(0)
	if w <= 0 || h <= 0 {
		t.Fatalf("PageSize of empty doc: %fx%f, want the letter fallback", w, h)
	}
}

// TestFacadeFullThenIncrementalSave: a FULL same-path save must survive via
// the temp+rename dance (never an in-place write of the bytes the open
// doc's fz_stream reads lazily: that corrupted later documents' cgo heap),
// and the incremental save that follows is demoted to another full save
// because the rename de-anchors the doc's xref from the file's end. Both
// annots must survive: reopen and assert.
func TestFacadeFullThenIncrementalSave(t *testing.T) {
	path := writeTextPDF(t, "both annots stay")
	d := openTestDoc(t, path)

	idCh := make(chan idReply, 1)
	d.AddSquareAsync(0, fitz.Rect{X0: 10, Y0: 10, X1: 50, Y1: 50}, RGB{G: 1}, 1,
		func(id int, err error) { idCh <- idReply{id, err} })
	waitID(t, idCh, "first square")

	sCh := make(chan error, 1)
	d.SaveAsync(path, fitz.SaveOptions{}, func(err error) { sCh <- err })
	waitErr(t, sCh, "full same-path save (temp+rename)")

	idCh = make(chan idReply, 1)
	d.AddSquareAsync(0, fitz.Rect{X0: 100, Y0: 100, X1: 140, Y1: 140}, RGB{B: 1}, 1,
		func(id int, err error) { idCh <- idReply{id, err} })
	waitID(t, idCh, "second square")

	// Incremental request after the rename-overwrite: the facade demotes
	// it to a full save so the dead anchor cannot corrupt the file.
	sCh = make(chan error, 1)
	d.SaveAsync(path, fitz.SaveOptions{Incremental: true}, func(err error) { sCh <- err })
	waitErr(t, sCh, "incremental-after-full (demoted) save")
	// And the doc stays editable: a third annot lands and persists.
	idCh = make(chan idReply, 1)
	d.AddSquareAsync(0, fitz.Rect{X0: 200, Y0: 10, X1: 240, Y1: 50}, RGB{R: 1}, 1,
		func(id int, err error) { idCh <- idReply{id, err} })
	waitID(t, idCh, "third square")
	sCh = make(chan error, 1)
	d.SaveAsync(path, fitz.SaveOptions{}, func(err error) { sCh <- err })
	waitErr(t, sCh, "final full save")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	g := openGen(t, raw) // openGen registers its own Close cleanup (t.Cleanup)
	annots, err := g.Annots(0)
	if err != nil {
		t.Fatalf("reopen annots: %v", err)
	}
	if len(annots) != 3 {
		t.Fatalf("annots after demoted incremental chain: %d, want 3", len(annots))
	}
}

// TestFacadeSyncSaveObservable: the synchronous Save returns the real error
// when the task runs, and ErrClosed (never a hang) when Close races it and
// drops the task.
func TestFacadeSyncSaveObservable(t *testing.T) {
	t.Run("runs", func(t *testing.T) {
		d := openTestDoc(t, writeTextPDF(t, "sync save"))
		out := filepath.Join(t.TempDir(), "synced.pdf")
		if err := d.Save(out, fitz.SaveOptions{}); err != nil {
			t.Fatalf("sync save: %v", err)
		}
		if _, err := os.Stat(out); err != nil {
			t.Fatalf("sync save produced no file: %v", err)
		}
	})

	t.Run("close races", func(t *testing.T) {
		d := openTestDoc(t, writeTextPDF(t, "raced save"))
		out := filepath.Join(t.TempDir(), "raced.pdf")
		res := make(chan error, 1)
		go func() { res <- d.Save(out, fitz.SaveOptions{}) }()
		d.Close()
		select {
		case err := <-res:
			if err != nil && err != ErrClosed {
				t.Fatalf("raced sync save: %v, want nil or ErrClosed", err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("raced sync save hung past Close")
		}
	})
}

func TestFacadeBadStyle(t *testing.T) {
	d := openTestDoc(t, writeTextPDF(t, "style check"))

	// Opacity past 1.
	hCh := make(chan error, 1)
	d.AddHighlightAsync(0, []fitz.Quad{{UL: fitz.Point{X: 1, Y: 1}, UR: fitz.Point{X: 90, Y: 1},
		LL: fitz.Point{X: 1, Y: 20}, LR: fitz.Point{X: 90, Y: 20}}}, RGB{R: 1}, 1.7,
		func(id int, err error) { hCh <- err })
	if err := recvErr(t, hCh, "opacity 1.7"); err != ErrBadParam {
		t.Fatalf("opacity 1.7: %v, want ErrBadParam", err)
	}

	// Negative color channel on a square.
	sCh := make(chan error, 1)
	d.AddSquareAsync(0, fitz.Rect{X0: 5, Y0: 5, X1: 40, Y1: 40}, RGB{R: -0.2}, 1,
		func(id int, err error) { sCh <- err })
	if err := recvErr(t, sCh, "negative color"); err != ErrBadParam {
		t.Fatalf("negative color: %v, want ErrBadParam", err)
	}
}
