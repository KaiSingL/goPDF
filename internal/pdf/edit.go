package pdf

// Editing facade over the go-fitz fork's PDF-layer bindings (pdf_edit.go):
// annotations, page organization and saving, exposed as the same kind of
// thin async calls as RenderAsync. The app never calls fitz directly for
// editing; every operation below runs as a task on the Doc's worker
// goroutine, so edits, renders, selections and searches are totally ordered
// by the single worker and the one-Document-per-goroutine rule keeps
// holding with no fitz-side locking by callers.
//
// # Freshness contract
//
// Mutating calls queue on the hi lane: user edits are rare but awaited, and
// must not sit behind a wall of thumbnails or an index build (lo lane).
// Lane priority never breaks ordering, only urgency: the worker runs one
// task at a time. A render queued before an edit may still deliver
// pre-edit pixels (queue order); that is the app's cue to re-render, not a
// bug. Callbacks are best-effort exactly like RenderAsync's: dropped after
// Close, or when the doc closed before the task ran.
//
// Every mutating callback fires only after the mutation itself AND the
// derived-state re-sync finished. Therefore, once AddHighlightAsync,
// AddTextAsync, AddFreeTextAsync, AddSquareAsync, DeleteAnnotAsync,
// InsertBlankPageAsync, RotatePageAsync, DeletePageAsync or GraftPageAsync
// reports nil error:
//
//   - NumPages, PageSize, PageOrigin and Outline return post-edit values
//     (annotation calls skip the re-sync: they change no page geometry).
//   - Any AnnotsAsync, RenderAsync, SelectionAsync or SearchQuadsAsync
//     issued after that callback observes the edited document. Doc keeps
//     no bitmap cache; RenderAsync always rasterizes live worker state.
//   - Calls issued before the mutation callback fired observe old or new
//     state in queue order; a render whose page index vanished mid-flight
//     gets the MuPDF page error through its callback, never a panic.
//   - All callers validate positions against the CACHED page count on their
//     own goroutine (check-then-act): when a structure mutation is in
//     flight, AnnotsAsync or DeleteAnnotAsync may reject a page about to
//     exist (ErrBadPage); the reverse window degrades to a fork error
//     callback. Neither case panics, matching RenderAsync's shape.
//
// The failure halves are worth spelling out: a mutation that applied but
// whose derived-state re-sync failed reports ErrResyncFailed, NOT a plain
// error: the edit IS in the live document, so a blind retry duplicates it
// (InsertBlankPage/Graft would add again; delete/rotate/delete-annot
// retries fail loudly at the fork instead). A SaveAsync callback MUST be
// awaited before Close: Close prefers the hi lane's sentinel and drops the
// queued lo task, so an unawaited async save is silently lost — the
// synchronous Save makes completion observable whatever the Close ordering
// (it also runs inline when called from inside a callback, where plain
// wait-on-self would deadlock the worker). (Cataloging: callbacks run on
// the worker; queueing MORE than the buffer's 64 hi tasks from inside one
// callback deadlocks the worker — queue bounded batches or hop to another
// goroutine.)
//
// Save mechanics: a successful save guarantees the bytes at the target
// hold the current in-memory state. Same-path FULL saves: sibling temp +
// rename on POSIX (durable — the previous good bytes stay until the new
// ones are complete; the doc keeps its open inode as its lazy-read
// source), demoted to incremental on Windows (MuPDF 1.28 removes the
// target before writing, and remove() of a file the doc holds open is a
// sharing violation there; incremental appends coexist with it). An
// incremental save appends an update section that MuPDF reconciles
// against the doc's open-time bytes; the fork rejects incremental saves
// on any path other than Path().
//
// Page-bitmap caches are the app's concern (PageCache lives outside Doc):
// the app drops its own entries for the affected pages when a mutation
// callback fires. The text index Doc does own is dropped by the re-sync;
// IndexAsync rebuilds it on the next call.
//
// Errors pass through from the fork unwrapped: fitz.ErrNeedsPassword and
// fitz.ErrNotPDF keep their meaning, and a DeleteAnnotAsync whose ID is
// gone surfaces fitz.ErrStaleRef. Page numbers are 0-based; rects and
// quads are page space in PDF points, top-left origin, the same space
// SearchQuadsAsync and SelectionAsync use.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/gen2brain/go-fitz"
)

// Errors returned by the editing facade.
var (
	// ErrBadQuads: markup annotations draw their geometry from quad
	// points; an empty slice would create an invisible annot.
	ErrBadQuads = fmt.Errorf("pdf: markup annotation needs at least one quad")
	// ErrBadSize: a blank page needs positive, finite dimensions.
	ErrBadSize = fmt.Errorf("pdf: bad page size")
	// ErrBadParam: an annotation color component or opacity is outside
	// 0..1, or NaN. MuPDF writes the raw value into /C//CA and most
	// viewers clamp, so a rejected call beats a PDF that renders
	// differently in every reader.
	ErrBadParam = fmt.Errorf("pdf: annot color/opacity out of range")
	// ErrResyncFailed is reported when a structural mutation APPLIED but
	// the derived state (page count/sizes/origins/outline) could not be
	// re-derived: the edit is live, retrying it would duplicate it (see
	// the retry table in the freshness contract above), and some reads
	// (PageSize, NumPages, Outline) may return stale values until the
	// doc is closed and reopened.
	ErrResyncFailed = fmt.Errorf("pdf: edit applied but page state resync failed")
)

// RGB is one annotation color. Components run 0..1, the scale PDF color
// operators and MuPDF's pdf_set_annot_color speak natively.
type RGB struct{ R, G, B float64 }

// PageAnnot is one annotation as the app consumes it. ID is the PDF object
// number DeleteAnnotAsync takes: stable while the document stays open, not
// across reopens (a save may renumber objects).
type PageAnnot struct {
	ID   int
	Type fitz.AnnotType
	Rect fitz.Rect // page space, points, top-left origin
	// Quads carries the markup geometry (highlight); empty for other types.
	Quads []fitz.Quad
	// Text is the body of sticky notes (AnnotText) and FreeText annots,
	// the two kinds this facade creates with text; empty otherwise.
	Text string
}

// AnnotsAsync lists one page's annotations on the hi lane (overlay painting
// and context menus are interactive) and calls fn on the worker goroutine.
// Form widgets are not listed; the facade does not expose them.
func (d *Doc) AnnotsAsync(page int, fn func([]PageAnnot, error)) {
	if d.closed.Load() || fn == nil {
		return
	}
	if page < 0 || page >= d.pageCount() {
		go fn(nil, ErrBadPage)
		return
	}
	d.hi <- func(f *fitz.Document) {
		if d.closed.Load() {
			return
		}
		annots, err := f.Annots(page)
		if err != nil {
			fn(nil, err)
			return
		}
		out := make([]PageAnnot, len(annots))
		for i, a := range annots {
			pa := PageAnnot{ID: int(a.ID), Type: a.Type, Rect: a.Rect, Quads: a.QuadPoints}
			if a.Type == fitz.AnnotText || a.Type == fitz.AnnotFreeText {
				pa.Text = a.Contents
			}
			out[i] = pa
		}
		fn(out, nil)
	}
}

// addAnnot queues one CreateAnnot round and delivers the new annotation's
// ID. Rect-based callers pass the position through e.Rect: the fork
// creates the annot first and positions it in the property-edit step (its
// own oracle tests use that sequence); markup callers (highlight) set
// quads instead, where a rect write would be rejected by MuPDF.
func (d *Doc) addAnnot(page int, t fitz.AnnotType, e fitz.AnnotEdit, fn func(id int, err error)) {
	if d.closed.Load() || fn == nil {
		return
	}
	if page < 0 || page >= d.pageCount() {
		go fn(0, ErrBadPage)
		return
	}
	d.hi <- func(f *fitz.Document) {
		if d.closed.Load() {
			return
		}
		id, err := f.CreateAnnot(page, t, fitz.Rect{}, e)
		fn(int(id), err)
	}
}

// badStyle reports color components or opacity outside 0..1 (NaN fails
// both comparisons). Only the kinds that carry /C or /CA are checked.
func badStyle(color RGB, opacity float64) bool {
	for _, c := range []float64{color.R, color.G, color.B, opacity} {
		if !(c >= 0 && c <= 1) {
			return true
		}
	}
	return false
}

// AddHighlightAsync highlights text over quads (page space, points,
// top-left origin, fz_quad corner order) with color and opacity (0..1).
// Markup annots derive their rect from the quads; none is stored.
func (d *Doc) AddHighlightAsync(page int, quads []fitz.Quad, color RGB, opacity float64, fn func(id int, err error)) {
	if d.closed.Load() || fn == nil {
		return
	}
	if len(quads) == 0 {
		go fn(0, ErrBadQuads)
		return
	}
	if badStyle(color, opacity) {
		go fn(0, ErrBadParam)
		return
	}
	d.addAnnot(page, fitz.AnnotHighlight, fitz.AnnotEdit{
		QuadPoints: quads,
		Color:      []float64{color.R, color.G, color.B},
		Opacity:    &opacity,
	}, fn)
}

// AddTextAsync adds a sticky note anchored at rect (an icon-sized spot in
// page space) with note as its body. The text lives in the annotation's
// contents, not on the page.
func (d *Doc) AddTextAsync(page int, rect fitz.Rect, note string, fn func(id int, err error)) {
	d.addAnnot(page, fitz.AnnotText, fitz.AnnotEdit{
		Rect: &rect, Contents: note, SetContents: true,
	}, fn)
}

// AddFreeTextAsync adds a FreeText annotation: text drawn on the page
// inside rect (page space, points).
func (d *Doc) AddFreeTextAsync(page int, rect fitz.Rect, text string, fn func(id int, err error)) {
	d.addAnnot(page, fitz.AnnotFreeText, fitz.AnnotEdit{
		Rect: &rect, Contents: text, SetContents: true,
	}, fn)
}

// AddSquareAsync adds a rectangle annotation stroked with color and
// opacity (0..1). No fill: the fork's edit layer has no interior-color
// setter, and adding one would mean patching the fork, which this facade
// must not do.
func (d *Doc) AddSquareAsync(page int, rect fitz.Rect, color RGB, opacity float64, fn func(id int, err error)) {
	if d.closed.Load() || fn == nil {
		return
	}
	if badStyle(color, opacity) {
		go fn(0, ErrBadParam)
		return
	}
	d.addAnnot(page, fitz.AnnotSquare, fitz.AnnotEdit{
		Rect: &rect, Color: []float64{color.R, color.G, color.B}, Opacity: &opacity,
	}, fn)
}

// DeleteAnnotAsync removes the annotation with the given ID from page. A
// stale ID (already deleted, or held from an earlier save) surfaces the
// fork's fitz.ErrStaleRef unchanged.
func (d *Doc) DeleteAnnotAsync(page, id int, fn func(err error)) {
	if d.closed.Load() || fn == nil {
		return
	}
	if page < 0 || page >= d.pageCount() {
		go fn(ErrBadPage)
		return
	}
	d.hi <- func(f *fitz.Document) {
		if d.closed.Load() {
			return
		}
		fn(f.DeleteAnnot(page, int64(id)))
	}
}

// mutatePages runs one page-structure mutation and, when it succeeds,
// re-derives the cached state through the same code path Open uses (page
// count, sizes, origins, outline) and drops the text index built for the
// old page set. fn fires only after both steps, which is what pins the
// freshness contract above. A re-sync failure after a successful mutation
// reports ErrResyncFailed (wrapped closest to the cause): the edit IS in
// the live document and blind retries would duplicate it, while parts of
// the cached state may be stale until close/reopen.
func (d *Doc) mutatePages(mutate func(f *fitz.Document) error, fn func(err error)) {
	if d.closed.Load() || fn == nil {
		return
	}
	d.hi <- func(f *fitz.Document) {
		if d.closed.Load() {
			return
		}
		if err := mutate(f); err != nil {
			fn(err)
			return
		}
		if err := d.resyncPages(f); err != nil {
			fn(fmt.Errorf("%w: %v", ErrResyncFailed, err))
			return
		}
		fn(nil)
	}
}

// resyncPages is the worker-side half of mutatePages; see there.
func (d *Doc) resyncPages(f *fitz.Document) error {
	pages, sizes, origins, toc, err := deriveGeometry(f)
	if err != nil {
		return err
	}
	d.stMu.Lock()
	d.pages, d.sizes, d.origins, d.toc = pages, sizes, origins, toc
	d.stMu.Unlock()
	// The old index maps text to dead page numbers after any structure
	// change; forcing a rebuild is cheaper than wrong search results.
	d.ixMu.Lock()
	d.index = nil
	d.ixMu.Unlock()
	return nil
}

// InsertBlankPageAsync inserts a blank page of size {w,h} points after
// page index after: -1 inserts at the front, NumPages()-1 appends.
func (d *Doc) InsertBlankPageAsync(after int, size [2]float64, fn func(err error)) {
	if d.closed.Load() || fn == nil {
		return
	}
	w, h := size[0], size[1]
	// NaN fails both comparisons; +Inf would build an absurd MediaBox and
	// only blow up later inside MuPDF, so reject it here with a clear error.
	if !(w > 0 && h > 0) || math.IsInf(w, 0) || math.IsInf(h, 0) {
		go fn(ErrBadSize)
		return
	}
	if after < -1 || after >= d.pageCount() {
		go fn(ErrBadPage)
		return
	}
	d.mutatePages(func(f *fitz.Document) error { return f.InsertBlankPage(after+1, w, h) }, fn)
}

// RotatePageAsync sets page's rotation to deg, an absolute value of 0, 90,
// 180 or 270 (0 un-rotates; 90-degree steps only). Absolute, not relative:
// that is what the fork and mutool write, and the only sane shape for a UI
// control that reads the current value before deciding the next. Bound
// swaps at 90/270, so the re-sync updates that page's PageSize.
func (d *Doc) RotatePageAsync(page, deg int, fn func(err error)) {
	if d.closed.Load() || fn == nil {
		return
	}
	switch deg {
	case 0, 90, 180, 270:
	default:
		go fn(ErrBadRotate)
		return
	}
	if page < 0 || page >= d.pageCount() {
		go fn(ErrBadPage)
		return
	}
	d.mutatePages(func(f *fitz.Document) error { return f.SetPageRotation(page, deg) }, fn)
}

// DeletePageAsync removes the page at index page.
func (d *Doc) DeletePageAsync(page int, fn func(err error)) {
	if d.closed.Load() || fn == nil {
		return
	}
	if page < 0 || page >= d.pageCount() {
		go fn(ErrBadPage)
		return
	}
	d.mutatePages(func(f *fitz.Document) error { return f.DeletePage(page) }, fn)
}

// GraftPageAsync appends one page of a foreign PDF: srcPage (0-based) of
// the file at srcPath is grafted in after page index after of this
// document (-1 for the front). The fork opens the source inside this
// document's MuPDF context on the worker goroutine, the one-context trick
// cross-document grafting needs; the source may be open in another Doc at
// the same time, since that Doc owns a different document object.
func (d *Doc) GraftPageAsync(srcPath string, srcPage, after int, fn func(err error)) {
	if d.closed.Load() || fn == nil {
		return
	}
	// The upper bound of srcPage is checked by the fork against the live
	// source file; here only the sign is knowable without touching the
	// filesystem from the caller's goroutine.
	if srcPage < 0 || after < -1 || after >= d.pageCount() {
		go fn(ErrBadPage)
		return
	}
	d.mutatePages(func(f *fitz.Document) error {
		return f.AppendPagesFromFile(srcPath, []int{srcPage}, after+1)
	}, fn)
}

// SaveAsync writes the document on the worker. The mechanics split by
// target (see savePath): a full save to a FOREIGN path is atomic through
// the fork's own sibling-temp+rename dance; a full save to Path() on
// POSIX gets the same dance from the facade (the previous good file
// survives a failed save; the doc keeps its open inode for lazy reads)
// while on Windows it demotes to an incremental save, because MuPDF
// 1.28's in-place writer removes the path first — and remove() of a file
// the document holds open is a sharing violation there. An incremental
// save (opts.Incremental) must target Path() (the fork rejects anything
// else) and appends an update section that MuPDF reconciles against the
// doc's open-time bytes on the way in, so edits from earlier in the
// session survive even a sideways save history.
//
// Saving mutates nothing in memory, so renders may legitimately overtake a
// queued save on the hi lane (they show the same state either way) and no
// re-sync runs; that is why Save queues on lo: a slow full save must not
// freeze page turns. Everything queued before the save is in the file;
// hi tasks that overtake a still-queued save also end up in it, which is
// harmless (the file is written from the same in-memory state).
//
// The callback is a promise the app MUST honor: Close drops queued lo
// tasks (its sentinel rides the hi lane, which always wins), so a save
// that is still queued when Close runs is silently lost and fn never
// fires — data loss, unlike a dropped render. Quit-time saving goes
// through the synchronous Save instead.
func (d *Doc) SaveAsync(path string, opts fitz.SaveOptions, fn func(err error)) {
	if d.closed.Load() || fn == nil {
		return
	}
	d.lo <- func(f *fitz.Document) {
		if d.closed.Load() {
			return
		}
		fn(d.savePath(f, path, opts))
	}
}

// savePath routes one save by target and platform (worker-only; see
// SaveAsync's comment). Errors from unresolvable paths pass through to the
// fork, which fails them at its own open/write.
func (d *Doc) savePath(f *fitz.Document, path string, opts fitz.SaveOptions) error {
	absOpen, oerr := filepath.Abs(d.path)
	var absTarget string
	var aerr error
	if oerr == nil {
		absTarget, aerr = filepath.Abs(path)
	}
	if oerr != nil || aerr != nil || absTarget != absOpen {
		// A foreign target takes the fork's own atomic temp+rename dance;
		// unresolvable paths error inside the fork. Incremental saves are
		// rejected by the fork everywhere but Path().
		return f.Save(path, opts)
	}
	if opts.Incremental {
		// Appended update section; reconciliation against the doc's
		// open-time bytes makes any earlier sideways save irrelevant.
		// Windows-safe: fz fopen("rb+") coexists with the doc's read
		// stream.
		return f.Save(path, opts)
	}
	if runtime.GOOS == "windows" {
		// MuPDF 1.28's full-save writer removes the target BEFORE writing,
		// and remove() of a file the doc holds open is a sharing violation
		// on Windows: the primary save flow would always fail. Demote to
		// an incremental save instead: it appends reconciled updates and
		// is the same platform the fork's own incremental path serves.
		// Two exception shapes on this branch (both loud, not silent):
		// compaction requests are stripped (MuPDF refuses incremental
		// writes with garbage collection), and a same-path save that wants
		// to CHANGE encryption has no incremental analogue — Save-As is
		// the supported route for that on Windows.
		fullOpts := opts
		fullOpts.Incremental = true
		fullOpts.Garbage = 0
		if fullOpts.Encryption != fitz.EncryptKeep {
			// EncryptKeep is "keep the doc's existing encryption", every
			// other value wants to set NEW encryption: incremental saves
			// reject that, so the honest answer is an explicit error.
			return fmt.Errorf("pdf: encryption-change same-path saves need Save-As on Windows")
		}
		return f.Save(path, fullOpts)
	}
	// POSIX same-path full save: sibling temp + rename. Strictly more
	// durable than MuPDF's own in-place write (which unlinks the live
	// file BEFORE writing: a mid-write failure strands the previous good
	// bytes), and the rename leaves the doc's open inode untouched, so
	// lazy reads never lose their bytes and no anchor bookkeeping is
	// needed.
	return d.saveRenamedOver(f, absTarget, opts)
}

// saveRenamedOver saves to a sibling temp file of absTarget and renames it
// over absTarget atomically (the fork's dance for a foreign target, applied
// from the facade where MuPDF would remove+recreate the live file).
func (d *Doc) saveRenamedOver(f *fitz.Document, absTarget string, opts fitz.SaveOptions) error {
	tmp, err := os.CreateTemp(filepath.Dir(absTarget), ".gopdf-save-*.pdf")
	if err != nil {
		return fmt.Errorf("fitz: save: %w", err)
	}
	tmpName := tmp.Name()
	tmp.Close()
	os.Remove(tmpName) // MuPDF creates the file itself
	err = f.Save(tmpName, opts)
	if err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, absTarget); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("fitz: save: replace %s: %w", absTarget, err)
	}
	return nil
}

// Save writes the document synchronously: it queues on the same lo lane as
// SaveAsync (everything about path handling applies equally) and blocks the
// CALLING goroutine until the save completes — or the doc closes, which
// returns ErrClosed rather than hanging. Quit-time "save before leaving"
// flows use this: waiting on the error beats losing the file. UI code must
// not call it from the UI thread (use SaveAsync elsewhere). Calling it from
// inside a facade callback is safe but pointless — callbacks run on the
// worker, so Save detects that and saves inline instead of waiting on
// itself (a plain enqueue-and-wait would deadlock the worker with one
// call).
func (d *Doc) Save(path string, opts fitz.SaveOptions) error {
	if d.closed.Load() {
		return ErrClosed
	}
	if g := d.loopGid.Load(); g != 0 && g == currentGid() {
		// On the worker (a callback context): the queued task would
		// only run when this goroutine returns to the loop, so run it
		// here against the same document.
		return d.savePath(d.fit, path, opts)
	}
	e := make(chan error, 1)
	task := func(f *fitz.Document) {
		e <- d.savePath(f, path, opts)
	}
	select {
	case d.lo <- task:
	case <-d.done:
		// Close landed (or landed first): the task was dropped by the
		// stop sentinel; saving is unreachable.
		return ErrClosed
	}
	select {
	case err := <-e:
		return err
	case <-d.done:
		// The save raced a concurrent Close. If the worker ran the
		// task, the error is still queued; if Close dropped the task,
		// `e` can receive nothing more (only the worker sends). Drain
		// once so a completed save is never misreported as ErrClosed.
		select {
		case err := <-e:
			return err
		default:
			return ErrClosed
		}
	}
}

// currentGid returns a stable identity for the calling goroutine by parsing
// its own stack header (goroutine IDs increase monotonically; two live
// goroutines never share one). The facade only compares it against the
// worker's id to detect callback contexts — never to renumber anything.
func currentGid() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// "goroutine 123 [running]:" — the id sits after the space.
	line := buf[:n]
	for i := len("goroutine "); i < len(line); i++ {
		if line[i] == ' ' {
			id, err := strconv.ParseUint(string(line[len("goroutine "):i]), 10, 64)
			if err == nil {
				return id
			}
			break
		}
	}
	return 0
}
