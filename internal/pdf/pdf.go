// Package pdf wraps go-fitz (MuPDF) for the GoPDF reader: opening documents,
// rendering pages to *image.RGBA at a target DPI with rotation, outlines,
// page geometry, and an async text index for search.
//
// The package must stay free of MyGo imports so it can be tested headlessly.
//
// # Threading model
//
// MuPDF requires each goroutine to use its own Document and forbids racing
// with Close. Every Doc therefore owns exactly one background goroutine that
// holds the *fitz.Document for its whole life. Open does the cheap
// synchronous setup (page count, sizes, outline, metadata); everything that
// can be slow (rendering, text extraction) runs as a task on that goroutine.
//
// RenderAsync and IndexAsync deliver results through callbacks that run on
// the worker goroutine — never the UI thread. Callers hop to the UI thread
// themselves (mygo Window.Update). Callbacks may stop firing after Close, or
// when the doc was closed before the task ran: the UI must treat them as
// best-effort and re-check tab liveness inside the callback.
//
// # Coordinate systems
//
// Page sizes and TextSpan coordinates are in PDF points (1/72 inch), origin
// at the top-left of the page, y growing down. A rendered image at DPI d is
// scaled by d/72, so a span's pixel rectangle on a render is
// (X*d/72, Y*d/72, W*d/72, H*d/72). Rotation is NOT applied to PageSize or
// spans; the viewer swaps width/height and transforms overlay rectangles for
// 90/270 rotation itself.
package pdf

import (
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gen2brain/go-fitz"
)

// Kind distinguishes render requests so the UI can route results.
type Kind int

const (
	KindPage  Kind = iota // full page render at the viewer's DPI
	KindThumb             // small thumbnail render (sidebar), typically 72 DPI
)

// OutlineNode is one entry of the document's table of contents tree.
type OutlineNode struct {
	Title    string
	Page     int // 0-based page to jump to; -1 when the entry has no target
	Children []OutlineNode
}

// TextSpan is a positioned run of text on a page, in points (see package
// comment). Spans roughly correspond to source lines of text.
type TextSpan struct {
	Page int
	X, Y float64 // top-left of the span
	W, H float64 // width and height
	Text string
}

// SearchHit is one match: the page and every span the match covers.
type SearchHit struct {
	Page  int
	Spans []TextSpan
}

// CacheKey identifies a rendered page image.
type CacheKey struct {
	Page   int
	DPI    int
	Rotate int
}

// Key returns the cache key for a render request.
func (r RenderReq) Key() CacheKey { return CacheKey{r.Page, r.DPI, r.Rotate} }

// RenderReq describes one asynchronous render.
type RenderReq struct {
	Page   int
	DPI    int
	Rotate int // 0 | 90 | 180 | 270
	Kind   Kind
}

// DPI limits: 25% zoom of a 72dpi-equivalent view up to 400% on a retina
// screen. Values outside are clamped.
const (
	MinDPI = 36
	MaxDPI = 600
	// ThumbDPI is the sidebar thumbnail resolution (spec §13 default).
	ThumbDPI = 72
)

// Errors returned by the package.
var (
	ErrClosed    = fmt.Errorf("pdf: document closed")
	ErrBadPage   = fmt.Errorf("pdf: page out of range")
	ErrBadDPI    = fmt.Errorf("pdf: dpi out of range")
	ErrBadRotate = fmt.Errorf("pdf: rotation must be 0, 90, 180 or 270")
)

var docSeq atomic.Uint64

// Doc is one open document. Create with Open. All methods are safe from any
// goroutine; slow work happens on the doc's worker (see package comment).
type Doc struct {
	path  string
	id    string
	pages int
	meta  map[string]string
	sizes [][2]float64 // raw page size in points, per page
	toc   []OutlineNode

	fit    *fitz.Document // set by Open, read by Close after <-d.done
	tasks  chan func(*fitz.Document)
	closed atomic.Bool
	done   chan struct{}

	closeOnce sync.Once // guards the stop sequence; Close is idempotent

	ixMu  sync.Mutex
	index *Index // built at most once by IndexAsync
}

// Open opens the file at path and returns a Doc ready for rendering.
// Password-protected files return fitz.ErrNeedsPassword (v1 surfaces the
// error; v2 adds a password prompt).
func Open(path string) (*Doc, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fit, err := fitz.New(abs)
	if err != nil {
		return nil, fmt.Errorf("pdf: open %s: %w", abs, err)
	}

	d := &Doc{
		path:  abs,
		id:    fmt.Sprintf("doc-%d", docSeq.Add(1)),
		pages: fit.NumPage(),
		meta:  trimMeta(fit.Metadata()),
		tasks: make(chan func(*fitz.Document), 128),
		done:  make(chan struct{}),
	}
	d.sizes = make([][2]float64, d.pages)
	for i := 0; i < d.pages; i++ {
		b, err := fit.Bound(i)
		if err != nil {
			fit.Close()
			return nil, fmt.Errorf("pdf: page %d bounds: %w", i, err)
		}
		d.sizes[i] = [2]float64{float64(b.Dx()), float64(b.Dy())}
	}
	d.toc = outlineTree(fit) // empty outline is not an error (fitz quirk)

	// Hand the fitz document to the worker by passing it as an argument: the
	// `go` statement is the happens-before edge. (A handover task queued on
	// the channel is NOT safe: Close sets closed before queueing the stop
	// sentinel, so the worker's stale-work drop could swallow the handover
	// and leave d.fit nil — found by TestOpenBasics.)
	d.fit = fit
	go d.loop(fit)
	return d, nil
}

// loop is the Doc's worker goroutine: the only goroutine that uses fit.
// Tasks queued after Close are dropped; nil is the stop sentinel.
func (d *Doc) loop(fit *fitz.Document) {
	defer close(d.done)
	for task := range d.tasks {
		if task == nil {
			// Stop must be checked first: Close sets closed BEFORE queueing
			// nil, so a closed check in front would swallow the sentinel and
			// deadlock Close (found by TestOpenBasics).
			return
		}
		if d.closed.Load() {
			continue // drop stale work, keep draining
		}
		task(fit)
	}
}

// Path returns the absolute path the document was opened from.
func (d *Doc) Path() string { return d.path }

// ID returns a unique id for this open instance (a path can be open twice).
func (d *Doc) ID() string { return d.id }

// NumPages returns the number of pages.
func (d *Doc) NumPages() int { return d.pages }

// Meta returns document metadata (format, title, author, ...). Values are
// trimmed of the NUL padding go-fitz's fixed-buffer lookup leaves in place;
// absent fields are "".
func (d *Doc) Meta() map[string]string { return d.meta }

// trimMeta cuts each metadata value at its first NUL. go-fitz v1.28.2's
// Metadata converts a fixed 256-byte C buffer with string(buf) without
// locating the terminator, so every value arrives NUL-padded (verified
// empirically; see cache/pdf test suite).
func trimMeta(m map[string]string) map[string]string {
	for k, v := range m {
		if i := strings.IndexByte(v, 0); i >= 0 {
			m[k] = v[:i]
		}
	}
	return m
}

// PageSize returns the raw page size in points, before any viewer rotation.
// For 90/270 rotation the caller swaps the values.
func (d *Doc) PageSize(page int) (w, h float64) {
	if page < 0 || page >= d.pages {
		if len(d.sizes) == 0 {
			return 612, 792 // US Letter fallback
		}
		page = 0
	}
	return d.sizes[page][0], d.sizes[page][1]
}

// Outline returns the table of contents tree; empty when the file has none.
func (d *Doc) Outline() []OutlineNode { return d.toc }

// outlineTree converts fitz's flat leveled ToC into a tree. fitz returns
// ErrLoadOutline for documents that simply have no outline — not an error.
func outlineTree(fit *fitz.Document) []OutlineNode {
	flat, err := fit.ToC()
	if err != nil || len(flat) == 0 {
		return nil
	}
	var roots []OutlineNode
	// Simple stack walk (levels start at 1 and may skip).
	type frame struct {
		node  *OutlineNode
		level int
	}
	var frames []frame
	for _, e := range flat {
		n := OutlineNode{Title: e.Title, Page: e.Page}
		for len(frames) > 0 && e.Level <= frames[len(frames)-1].level {
			frames = frames[:len(frames)-1]
		}
		if len(frames) == 0 {
			roots = append(roots, n)
			frames = append(frames, frame{&roots[len(roots)-1], e.Level})
		} else {
			p := frames[len(frames)-1].node
			p.Children = append(p.Children, n)
			frames = append(frames, frame{&p.Children[len(p.Children)-1], e.Level})
		}
	}
	return roots
}

// RenderAsync renders one page and calls fn on the worker goroutine with the
// result. DPI is clamped to [MinDPI, MaxDPI] and rounded; rotate must be
// 0/90/180/270. The returned image is a fresh 0-origin RGBA owned by the
// caller. After Close the callback may never run.
func (d *Doc) RenderAsync(req RenderReq, fn func(*image.RGBA, error)) {
	if d.closed.Load() {
		return
	}
	dpi := req.DPI
	if dpi < MinDPI {
		dpi = MinDPI
	}
	if dpi > MaxDPI {
		dpi = MaxDPI
	}
	req.DPI = dpi
	switch req.Rotate {
	case 0, 90, 180, 270:
	default:
		req.Rotate = 0
	}
	if req.Page < 0 || req.Page >= d.pages {
		if fn != nil {
			go fn(nil, ErrBadPage)
		}
		return
	}
	d.tasks <- func(f *fitz.Document) {
		if d.closed.Load() || fn == nil {
			return
		}
		img, err := f.ImageDPI(req.Page, float64(req.DPI))
		if err != nil {
			fn(nil, err)
			return
		}
		img = normalizeRGBA(img)
		if req.Rotate != 0 {
			img = rotateRGBA(img, req.Rotate)
		}
		fn(img, nil)
	}
}

// IndexAsync builds (once) the searchable text index of the whole document
// and calls fn on the worker goroutine. Later calls reuse the built index
// and call fn synchronously from the calling goroutine when it is ready;
// before that they are ignored (the first callback still fires).
func (d *Doc) IndexAsync(fn func(*Index, error)) {
	d.ixMu.Lock()
	if d.index != nil {
		ix := d.index
		d.ixMu.Unlock()
		if fn != nil {
			fn(ix, nil)
		}
		return
	}
	d.ixMu.Unlock()
	if d.closed.Load() {
		return
	}
	d.tasks <- func(f *fitz.Document) {
		d.ixMu.Lock()
		if d.index != nil || d.closed.Load() {
			ix := d.index
			d.ixMu.Unlock()
			if fn != nil && ix != nil {
				fn(ix, nil)
			}
			return
		}
		ix := &Index{pages: d.pages}
		ix.Spans = make([][]TextSpan, d.pages)
		ix.Text = make([]string, d.pages)
		var err error
		for p := 0; p < d.pages; p++ {
			html, e := f.HTML(p, false)
			if e != nil {
				err = e
				break
			}
			ix.Spans[p] = extractSpans(html, p)
			if t, e := f.Text(p); e == nil {
				ix.Text[p] = t
			}
		}
		d.index = ix
		d.ixMu.Unlock()
		if fn != nil {
			fn(ix, err)
		}
	}
}

// Close stops the worker and releases the MuPDF document. Pending callbacks
// may be dropped. Close is idempotent and waits for the worker to finish;
// concurrent and repeated calls block until the first Close completed.
func (d *Doc) Close() {
	// The whole stop sequence must be inside the Once: a second Close must
	// not repeat _ = d.fit.Close() — that drops the MuPDF document twice and
	// crashes in cgo (found by TestCloseIdempotent).
	d.closeOnce.Do(func() {
		d.closed.Store(true)
		d.tasks <- nil
		<-d.done
		_ = d.fit.Close()
	})
}

// normalizeRGBA copies a possibly non-zero-origin fitz render into a fresh
// 0-origin image (MuPDF can emit negative origins for cropped pages).
func normalizeRGBA(img *image.RGBA) *image.RGBA {
	if img == nil {
		return nil
	}
	r := img.Rect
	if r.Min == (image.Point{}) {
		return img
	}
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		so := img.PixOffset(r.Min.X, r.Min.Y+y)
		do := out.PixOffset(0, y)
		copy(out.Pix[do:do+r.Dx()*4], img.Pix[so:so+r.Dx()*4])
	}
	return out
}

// rotateRGBA returns img rotated by 90/180/270 degrees clockwise, lossless.
// rot must already be validated.
func rotateRGBA(img *image.RGBA, rot int) *image.RGBA {
	if img == nil {
		return nil
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	var out *image.RGBA
	switch rot {
	case 90:
		out = image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				so := img.PixOffset(x, y)
				do := out.PixOffset(h-1-y, x)
				copy(out.Pix[do:do+4], img.Pix[so:so+4])
			}
		}
	case 180:
		out = image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				so := img.PixOffset(x, y)
				do := out.PixOffset(w-1-x, h-1-y)
				copy(out.Pix[do:do+4], img.Pix[so:so+4])
			}
		}
	case 270:
		out = image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				so := img.PixOffset(x, y)
				do := out.PixOffset(y, w-1-x)
				copy(out.Pix[do:do+4], img.Pix[so:so+4])
			}
		}
	default:
		return img
	}
	return out
}
