package pdf

import (
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// openTestDoc opens path as a *Doc, failing the test on error, and closes it
// at cleanup.
func openTestDoc(t *testing.T, path string) *Doc {
	t.Helper()
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// recvImg waits (bounded) for one render callback result.
func recvImg(t *testing.T, ch <-chan *image.RGBA, what string) *image.RGBA {
	t.Helper()
	select {
	case img := <-ch:
		return img
	case <-time.After(30 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
		return nil
	}
}

func TestOpenBasics(t *testing.T) {
	path := writeTextPDF(t, "page zero", "page one", "page two")
	d := openTestDoc(t, path)

	if got := d.NumPages(); got != 3 {
		t.Errorf("NumPages = %d, want 3", got)
	}
	if w, h := d.PageSize(0); w != 612 || h != 792 {
		t.Errorf("PageSize(0) = %gx%g, want 612x792", w, h)
	}
	if w, h := d.PageSize(2); w != 612 || h != 792 {
		t.Errorf("PageSize(2) = %gx%g, want 612x792", w, h)
	}
	// Out-of-range falls back to page 0 (US Letter if the doc were empty).
	if w, h := d.PageSize(-1); w != 612 || h != 792 {
		t.Errorf("PageSize(-1) = %gx%g, want 612x792", w, h)
	}
	if w, h := d.PageSize(99); w != 612 || h != 792 {
		t.Errorf("PageSize(99) = %gx%g, want 612x792", w, h)
	}

	if !filepath.IsAbs(d.Path()) || filepath.Base(d.Path()) != "gen.pdf" {
		t.Errorf("Path = %q, want absolute .../gen.pdf", d.Path())
	}

	// Outline-less document: empty tree, no error surfaced.
	if got := d.Outline(); len(got) != 0 {
		t.Errorf("Outline = %+v, want empty", got)
	}

	// IDs are unique per open instance even for the same path.
	d2 := openTestDoc(t, path)
	if d.ID() == "" || d.ID() == d2.ID() {
		t.Errorf("IDs %q vs %q: want non-empty and unique", d.ID(), d2.ID())
	}
}

func TestMeta(t *testing.T) {
	path := writePDFFile(t, []string{textContent("meta page")}, map[string]string{
		"Title":    "Meta Test Doc",
		"Producer": "goPDF testgen",
	}, nil)
	d := openTestDoc(t, path)
	m := d.Meta()

	// go-fitz v1.28 returns NUL-padded values; Doc.Meta must trim them.
	if got := m["title"]; got != "Meta Test Doc" {
		t.Errorf("meta title = %q, want %q (untrimmed NULs?)", got, "Meta Test Doc")
	}
	if got := m["producer"]; got != "goPDF testgen" {
		t.Errorf("meta producer = %q, want %q", got, "goPDF testgen")
	}
	if got := m["author"]; got != "" {
		t.Errorf("meta author = %q, want empty", got)
	}
	// Pinned lib reports this repaired file as a generic modern PDF.
	if re := regexp.MustCompile(`^PDF 1\.\d$`); !re.MatchString(m["format"]) {
		t.Errorf("meta format = %q, want \"PDF 1.x\"", m["format"])
	}
	for k, v := range m {
		if strings.ContainsRune(v, 0) {
			t.Errorf("meta[%q] = %q contains NUL padding", k, v)
		}
	}
}

// TestRenderAsync150DPI checks size, aspect and 0-origin of a 150 DPI render
// of a 612x792 page (612*150/72 = 1275, 792*150/72 = 1650).
func TestRenderAsync150DPI(t *testing.T) {
	d := openTestDoc(t, writeTextPDF(t, "render me"))
	ch := make(chan *image.RGBA, 1)
	d.RenderAsync(RenderReq{Page: 0, DPI: 150}, func(img *image.RGBA, err error) {
		if err != nil {
			t.Errorf("render: %v", err)
		}
		ch <- img
	})
	img := recvImg(t, ch, "150dpi render")
	if img == nil {
		t.Fatal("nil image")
	}
	if img.Rect.Min != (image.Point{}) {
		t.Errorf("origin = %v, want (0,0)", img.Rect.Min)
	}
	if got := img.Rect.Dx(); got != 1275 {
		t.Errorf("width = %d, want 1275", got)
	}
	if got := img.Rect.Dy(); got != 1650 {
		t.Errorf("height = %d, want 1650", got)
	}
	if got := len(img.Pix); got != 1275*1650*4 {
		t.Errorf("Pix len = %d, want %d", got, 1275*1650*4)
	}
	// MuPDF clears the pixmap to opaque white before drawing.
	if bg := img.RGBAAt(637, 100); bg.A != 255 {
		t.Errorf("background alpha = %d, want 255", bg.A)
	}
}

// TestRenderClampAndBadPage pins DPI clamping and the bad-page error path.
func TestRenderClampAndBadPage(t *testing.T) {
	d := openTestDoc(t, writeTextPDF(t, "clamp"))

	// DPI below MinDPI clamps to 36: 612*36/72=306, 792*36/72=396.
	ch := make(chan *image.RGBA, 1)
	d.RenderAsync(RenderReq{Page: 0, DPI: 5}, func(img *image.RGBA, err error) {
		if err != nil {
			t.Errorf("render 5dpi: %v", err)
		}
		ch <- img
	})
	if img := recvImg(t, ch, "36dpi render"); img.Rect.Dx() != 306 || img.Rect.Dy() != 396 {
		t.Errorf("clamped render = %vx%v, want 306x396", img.Rect.Dx(), img.Rect.Dy())
	}

	// Invalid rotation is clamped to 0 (no dimension swap at 72 DPI).
	ch2 := make(chan *image.RGBA, 1)
	d.RenderAsync(RenderReq{Page: 0, DPI: 72, Rotate: 137}, func(img *image.RGBA, err error) {
		if err != nil {
			t.Errorf("render rot137: %v", err)
		}
		ch2 <- img
	})
	if img := recvImg(t, ch2, "rot-clamped render"); img.Rect.Dx() != 612 || img.Rect.Dy() != 792 {
		t.Errorf("rot137 render = %vx%v, want unrotated 612x792", img.Rect.Dx(), img.Rect.Dy())
	}

	// Out-of-range page: ErrBadPage, nil image.
	ech := make(chan error, 1)
	d.RenderAsync(RenderReq{Page: 9, DPI: 150}, func(img *image.RGBA, err error) {
		if img != nil {
			t.Error("bad page delivered non-nil image")
		}
		ech <- err
	})
	select {
	case err := <-ech:
		if err != ErrBadPage {
			t.Errorf("bad page err = %v, want ErrBadPage", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for bad-page callback")
	}
}

// TestRotate90 renders a page with a known red square in its top-left corner
// (PDF rect 0,780..12,792 = image rect 0..12 x 0..12 at 72 DPI) and checks
// that 90-degree rotation swaps dimensions and moves the corner to top-right.
func TestRotate90(t *testing.T) {
	path := writePDFFile(t, []string{redSquareContent(0, 780, 12, 12)}, nil, nil)
	d := openTestDoc(t, path)

	render := func(rot int) *image.RGBA {
		ch := make(chan *image.RGBA, 1)
		d.RenderAsync(RenderReq{Page: 0, DPI: 72, Rotate: rot}, func(img *image.RGBA, err error) {
			if err != nil {
				t.Errorf("render rot%d: %v", rot, err)
			}
			ch <- img
		})
		return recvImg(t, ch, "rot render")
	}

	base := render(0)
	if base.Rect.Dx() != 612 || base.Rect.Dy() != 792 {
		t.Fatalf("base = %vx%v, want 612x792", base.Rect.Dx(), base.Rect.Dy())
	}
	isRed := func(c color.RGBA) bool { return c.R > 200 && c.G < 80 && c.B < 80 }
	if c := base.RGBAAt(6, 6); !isRed(c) {
		t.Fatalf("base(6,6) = %v, want red square center", c)
	}

	rot := render(90)
	if rot.Rect.Dx() != 792 || rot.Rect.Dy() != 612 {
		t.Fatalf("rot90 = %vx%v, want 792x612", rot.Rect.Dx(), rot.Rect.Dy())
	}
	// (x,y) -> (h-1-y, x): square center (6,6) lands on (785,6), top-right.
	if c := rot.RGBAAt(785, 6); !isRed(c) {
		t.Errorf("rot90(785,6) = %v, want red (top-left corner moved to top-right)", c)
	}
	if c := rot.RGBAAt(6, 6); isRed(c) {
		t.Error("rot90(6,6) is red; square must not stay top-left")
	}
	if c := rot.RGBAAt(6, 785); isRed(c) {
		t.Error("rot90(6,785) is red; square must not move to bottom-left")
	}

	rot270 := render(270)
	if rot270.Rect.Dx() != 792 || rot270.Rect.Dy() != 612 {
		t.Errorf("rot270 = %vx%v, want 792x612", rot270.Rect.Dx(), rot270.Rect.Dy())
	}
	// 270 CW (= 90 CCW): (x,y) -> (y, w-1-x) with w=612; the square center
	// lands at (6, 605), bottom-left.
	if c := rot270.RGBAAt(6, 605); !isRed(c) {
		t.Errorf("rot270(6,605) = %v, want red (top-left corner moved to bottom-left)", c)
	}
}

func TestOutlineTree(t *testing.T) {
	d := openTestDoc(t, writeOutlinePDF(t))
	toc := d.Outline()
	if len(toc) != 2 {
		t.Fatalf("Outline = %d roots, want 2: %+v", len(toc), toc)
	}

	c1 := toc[0]
	if c1.Title != "Chapter 1" || c1.Page != 0 {
		t.Errorf("root0 = %+v, want Chapter 1 page 0", c1)
	}
	if len(c1.Children) != 2 {
		t.Fatalf("root0 children = %+v, want 2", c1.Children)
	}
	if c1.Children[0].Title != "Section 1.1" || c1.Children[0].Page != 1 {
		t.Errorf("child0 = %+v, want Section 1.1 page 1", c1.Children[0])
	}
	if c1.Children[1].Title != "Section 1.2" || c1.Children[1].Page != 2 {
		t.Errorf("child1 = %+v, want Section 1.2 page 2", c1.Children[1])
	}
	if len(c1.Children[0].Children) != 0 || len(c1.Children[1].Children) != 0 {
		t.Error("level-2 nodes must have no children")
	}

	c2 := toc[1]
	if c2.Title != "Chapter 2" || c2.Page != 3 {
		t.Errorf("root1 = %+v, want Chapter 2 page 3", c2)
	}
	if len(c2.Children) != 0 {
		t.Errorf("root1 children = %+v, want none", c2.Children)
	}
}

// TestCloseIdempotent closes twice and verifies no callbacks fire after
// Close returns; everything is bounded so a regression cannot hang the run.
func TestCloseIdempotent(t *testing.T) {
	d, err := Open(writeTextPDF(t, "close me"))
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan struct{}, 1)
	d.RenderAsync(RenderReq{Page: 0, DPI: 72}, func(*image.RGBA, error) { ch <- struct{}{} })

	closed := make(chan struct{})
	go func() { d.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(30 * time.Second):
		t.Fatal("first Close hung")
	}
	done := make(chan struct{})
	go func() { d.Close(); close(done) }() // idempotent, must not block
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("second Close hung")
	}

	// Anything delivered by now was queued before Close; drain it.
	for len(ch) > 0 {
		<-ch
	}
	// After Close returns, the worker has exited: no new callbacks. This is
	// deterministic (closed check precedes queueing), not a timing guess.
	rch := make(chan *image.RGBA, 1)
	d.RenderAsync(RenderReq{Page: 0, DPI: 72}, func(img *image.RGBA, err error) { rch <- img })
	select {
	case <-rch:
		t.Error("RenderAsync after Close fired its callback")
	case <-time.After(150 * time.Millisecond):
	}
}

// TestCloseRightAfterOpen hammers open->(maybe render)->close to flush out
// ownership races; run the suite with -race.
func TestCloseRightAfterOpen(t *testing.T) {
	path := writeTextPDF(t, "race me")
	for i := 0; i < 25; i++ {
		d, err := Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if i%2 == 0 {
			d.RenderAsync(RenderReq{Page: 0, DPI: 72}, func(*image.RGBA, error) {})
			d.IndexAsync(func(*Index, error) {})
		}
		closed := make(chan struct{})
		go func() { d.Close(); close(closed) }()
		select {
		case <-closed:
		case <-time.After(30 * time.Second):
			t.Fatalf("Close %d hung", i)
		}
	}
}

// TestConcurrentUse exercises all public methods from many goroutines on one
// doc, then closes while a render is in flight.
func TestConcurrentUse(t *testing.T) {
	d := openTestDoc(t, writeTextPDF(t, "one", "two", "three", "four"))

	const workers = 8
	errs := make(chan error, workers*4)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			page := w % 4
			for i := 0; i < 5; i++ {
				if w, h := d.PageSize(page); w != 612 || h != 792 {
					errs <- fmt.Errorf("PageSize(%d) = %gx%g", page, w, h)
				}
				_ = d.NumPages()
				_ = d.Meta()
				_ = d.Outline()
				ch := make(chan struct{}, 1)
				d.RenderAsync(RenderReq{Page: page, DPI: 72}, func(img *image.RGBA, err error) {
					if err != nil {
						errs <- err
					}
					ch <- struct{}{}
				})
				select {
				case <-ch:
				case <-time.After(30 * time.Second):
					errs <- fmt.Errorf("render timeout w%d", w)
				}
			}
		}(w)
	}
	// Index build racing with the renders above.
	ixch := make(chan *Index, 1)
	d.IndexAsync(func(ix *Index, err error) {
		if err != nil {
			errs <- err
		}
		ixch <- ix
	})
	wg.Wait()
	select {
	case <-ixch:
	case <-time.After(30 * time.Second):
		t.Fatal("index timeout")
	}
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// Close while another render is queued: must return promptly.
	d2, err := Open(writeTextPDF(t, "inflight"))
	if err != nil {
		t.Fatal(err)
	}
	d2.RenderAsync(RenderReq{Page: 0, DPI: 150}, func(*image.RGBA, error) {})
	closed := make(chan struct{})
	go func() { d2.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(30 * time.Second):
		t.Fatal("Close with in-flight render hung")
	}
}

// Perf probe (spec §9): open a 100-page document and time Open plus the
// first 150 DPI render and the full-text index build. Reported, not
// asserted — the fixtures are synthetic and far smaller than real docs.
func TestPerfProbe100Pages(t *testing.T) {
	pages := make([]string, 100)
	for i := range pages {
		pages[i] = textContent("Page "+strings.Repeat("lorem ipsum ", 4)+"number", "filler line")
	}
	path := writePDFFile(t, pages, nil, nil)

	t0 := time.Now()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	openTime := time.Since(t0)

	ch := make(chan *image.RGBA, 1)
	t1 := time.Now()
	d.RenderAsync(RenderReq{Page: 0, DPI: 150}, func(img *image.RGBA, err error) {
		if err != nil {
			t.Errorf("render: %v", err)
		}
		ch <- img
	})
	img := recvImg(t, ch, "perf render")
	renderTime := time.Since(t1)

	ixch := make(chan *Index, 1)
	t2 := time.Now()
	d.IndexAsync(func(ix *Index, err error) {
		if err != nil {
			t.Errorf("index: %v", err)
		}
		ixch <- ix
	})
	<-ixch
	indexTime := time.Since(t2)

	t.Logf("PERF 100 pages: open=%v first-render-150dpi=%v (%dx%d px) index-build=%v",
		openTime, renderTime, img.Rect.Dx(), img.Rect.Dy(), indexTime)
}
