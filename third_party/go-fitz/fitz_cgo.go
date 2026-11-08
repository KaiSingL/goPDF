//go:build cgo && !nocgo

package fitz

/*
#include <mupdf/fitz.h>
#include <stdlib.h>

const char *fz_version = FZ_VERSION;
#if defined(_WIN32)
	typedef unsigned long long store;
#else
	typedef unsigned long store;
#endif

fz_document *open_document(fz_context *ctx, const char *filename) {
	fz_document *doc;

	fz_try(ctx) {
		doc = fz_open_document(ctx, filename);
	}
	fz_catch(ctx) {
		return NULL;
	}

	return doc;
}

fz_document *open_document_with_stream(fz_context *ctx, const char *magic, fz_stream *stream) {
	fz_document *doc;

	fz_try(ctx) {
		doc = fz_open_document_with_stream(ctx, magic, stream);
	}
	fz_catch(ctx) {
		return NULL;
	}

	return doc;
}

fz_page *load_page(fz_context *ctx, fz_document *doc, int number) {
	fz_page *page;

	fz_try(ctx) {
		page = fz_load_page(ctx, doc, number);
	}
	fz_catch(ctx) {
		return NULL;
	}

	return page;
}

int run_page_contents(fz_context *ctx, fz_page *page, fz_device *dev, fz_matrix transform, fz_cookie *cookie) {
	fz_try(ctx) {
		fz_run_page_contents(ctx, page, dev, transform, cookie);
	}
	fz_catch(ctx) {
		return 0;
	}

	return 1;
}

// GoPDF patch: run the whole page — contents AND annotations — so renders
// show the highlights, squares and other marks the file carries.
int run_page_full(fz_context *ctx, fz_page *page, fz_device *dev, fz_matrix transform, fz_cookie *cookie) {
	fz_try(ctx) {
		fz_run_page(ctx, page, dev, transform, cookie);
	}
	fz_catch(ctx) {
		return 0;
	}

	return 1;
}

// GoPDF patch: exact search hit quads (fz_search_page), so highlights land
// on the matched glyphs instead of estimated rectangles.
int search_page(fz_context *ctx, fz_page *page, const char *needle, fz_quad *quads, int hit_max) {
	int n = 0;
	fz_try(ctx) {
		n = fz_search_page(ctx, page, needle, NULL, quads, hit_max);
	}
	fz_catch(ctx) {
		return -1;
	}

	return n;
}

// GoPDF patch: the structured text of a page, run exactly as
// fz_search_page runs the page — the whole page under the identity ctm —
// so selection quads and search quads share one coordinate space. Caller
// owns the result (fz_drop_stext_page).
fz_stext_page *stext_from_page(fz_context *ctx, fz_page *page) {
	fz_stext_page *text = NULL;
	fz_device *dev = NULL;

	fz_try(ctx) {
		fz_rect b = fz_bound_page(ctx, page);
		text = fz_new_stext_page(ctx, b);
		fz_stext_options opts = {0};
		dev = fz_new_stext_device(ctx, text, &opts);
		fz_run_page(ctx, page, dev, fz_identity, NULL);
		fz_close_device(ctx, dev);
	}
	fz_always(ctx) {
		fz_drop_device(ctx, dev);
	}
	fz_catch(ctx) {
		fz_drop_stext_page(ctx, text);
		return NULL;
	}

	return text;
}

// GoPDF patch: exact selection geometry — fz_highlight_selection quads
// plus fz_copy_selection text over the stext page built above. n < 0 is
// an error; *out_text (NULL for an empty selection) is fz_malloc'd —
// free it with drop_string.
int selection_quads(fz_context *ctx, fz_stext_page *text, float x0, float y0, float x1, float y1, fz_quad *quads, int max_quads, char **out_text) {
	int n = 0;
	fz_try(ctx) {
		fz_point a = fz_make_point(x0, y0);
		fz_point b = fz_make_point(x1, y1);
		n = fz_highlight_selection(ctx, text, a, b, quads, max_quads);
		*out_text = fz_copy_selection(ctx, text, a, b, '\n');
	}
	fz_catch(ctx) {
		return -1;
	}

	return n;
}

// GoPDF patch: free a string MuPDF allocated.
void drop_string(fz_context *ctx, char *s) {
	fz_free(ctx, s);
}

// GoPDF patch: OS CJK fallback font. The bundled MuPDF ships with TOFU_CJK
// (config.h), so it carries no CJK font and PDFs whose CJK fonts are NOT
// embedded fail their font load — MuPDF then substitutes its hail-mary
// Helvetica, painting mojibake glyphs and extracting garbage text. MuPDF
// asks the embedder for fonts through the hooks of
// fz_install_load_system_font_funcs:
//   - the CJK hook (name, ordering, serif) fires while loading a
//     non-embedded CIDFont of an Adobe-GB1/CNS1/Japan1/Korea1 collection
//     (pdf-font.c pdf_load_substitute_cjk_font) — the PDF route;
//   - the fallback hook (script, language, serif, bold, italic) fires from
//     fz_encode_character_with_fallback (fz_show_string/measure and the
//     story/HTML layout; the PDF content interpreter never calls it).
// Both hooks lazily load ONE font from an OS path and hand out kept
// references. The record rides in the context's user slot, so every
// Document context owns its private font and no state is shared across
// contexts (MuPDF requires one goroutine per context anyway).
struct gopdf_cjk_rec {
	char *path;    // OS font file, fz_strdup'd
	fz_font *font; // created on first request; this record holds one reference
	int failed;    // load failed once: never retry, render as before
};

// UCDN script numbers, from the ucdn.h MuPDF 1.28 bundles (not shipped in
// the fork's headers, so pinned here with its upstream path).
#define GOPDF_SCRIPT_COMMON 0
#define GOPDF_SCRIPT_INHERITED 40
#define GOPDF_SCRIPT_HANGUL 24
#define GOPDF_SCRIPT_HIRAGANA 32
#define GOPDF_SCRIPT_KATAKANA 33
#define GOPDF_SCRIPT_BOPOMOFO 34
#define GOPDF_SCRIPT_HAN 35

static struct gopdf_cjk_rec *gopdf_cjk_rec_of(fz_context *ctx) {
	return (struct gopdf_cjk_rec *)fz_user_context(ctx);
}

// gopdf_cjk_font returns a kept reference to the fallback font, loading the
// OS file on first use; NULL (rendering exactly as before) when this
// context has no install or the file cannot be loaded.
static fz_font *gopdf_cjk_font(fz_context *ctx) {
	struct gopdf_cjk_rec *rec = gopdf_cjk_rec_of(ctx);
	if (rec == NULL || rec->failed)
		return NULL;
	if (rec->font == NULL) {
		fz_try(ctx)
			// index 0: first face of .ttc collections (msyh.ttc,
			// PingFang.ttc, NotoSansCJK ttc), the only face of .ttf files
			rec->font = fz_new_font_from_file(ctx, "gopdf-cjk-fallback", rec->path, 0, 0);
		fz_catch(ctx) {
			rec->failed = 1;
			return NULL;
		}
	}
	return fz_keep_font(ctx, rec->font);
}

// The PDF route: pdf_load_substitute_cjk_font asks for a font by Adobe
// collection ordering. One OS font answers every ordering; name and style
// are ignored (a wrong-style CJK glyph beats no glyph at all).
static fz_font *gopdf_load_system_cjk_font(fz_context *ctx, const char *name, int ordering, int serif) {
	(void)name;
	(void)serif;
	if (ordering == FZ_ADOBE_CNS || ordering == FZ_ADOBE_GB ||
		ordering == FZ_ADOBE_JAPAN || ordering == FZ_ADOBE_KOREA)
		return gopdf_cjk_font(ctx);
	return NULL;
}

// The story/HTML route: only CJK scripts get the font. zh/ja/ko language
// tags additionally cover COMMON/INHERITED characters (CJK punctuation is
// script-common) without ever matching a Latin run.
static fz_font *gopdf_load_system_fallback_font(fz_context *ctx, int script, int language, int serif, int bold, int italic) {
	(void)serif;
	(void)bold;
	(void)italic;
	switch (script) {
	case GOPDF_SCRIPT_HANGUL:
	case GOPDF_SCRIPT_HIRAGANA:
	case GOPDF_SCRIPT_KATAKANA:
	case GOPDF_SCRIPT_BOPOMOFO:
	case GOPDF_SCRIPT_HAN:
		return gopdf_cjk_font(ctx);
	}
	if (script == GOPDF_SCRIPT_COMMON || script == GOPDF_SCRIPT_INHERITED) {
		switch (language) {
		case FZ_LANG_ko:
		case FZ_LANG_ja:
		case FZ_LANG_zh:
		case FZ_LANG_zh_Hans:
		case FZ_LANG_zh_Hant:
			return gopdf_cjk_font(ctx);
		}
	}
	return NULL;
}

// gopdf_install_cjk_fallback arms ctx's CJK system-font hooks with path as
// the fallback font file. Returns 1 when armed, 0 when path is empty or the
// context is already carrying an install (per-context once, a no-op) — the
// caller treats 0 as "render exactly as before". The font itself is NOT
// loaded here: documents that never hit the hook pay nothing.
int gopdf_install_cjk_fallback(fz_context *ctx, const char *path) {
	struct gopdf_cjk_rec *rec;
	char *dup = NULL;

	fz_var(dup);

	if (path == NULL || !*path || fz_user_context(ctx) != NULL)
		return 0;

	fz_try(ctx)
		dup = fz_strdup(ctx, path);
	fz_catch(ctx)
		return 0;

	fz_try(ctx) {
		rec = fz_malloc_struct(ctx, struct gopdf_cjk_rec);
		rec->path = dup;
	}
	fz_catch(ctx) {
		fz_free(ctx, dup);
		return 0;
	}

	fz_set_user_context(ctx, rec);
	fz_install_load_system_font_funcs(ctx, NULL, gopdf_load_system_cjk_font, gopdf_load_system_fallback_font);
	return 1;
}

// gopdf_drop_cjk_fallback releases the install's record and font reference.
// Call before fz_drop_context (Document.Close); fontdescs still holding
// kept references drop them as the context's store is emptied.
void gopdf_drop_cjk_fallback(fz_context *ctx) {
	struct gopdf_cjk_rec *rec = gopdf_cjk_rec_of(ctx);
	if (rec == NULL)
		return;
	fz_drop_font(ctx, rec->font);
	fz_free(ctx, rec->path);
	fz_free(ctx, rec);
	fz_set_user_context(ctx, NULL);
}

static void silent_warning(void *user, const char *message) {}

void silence_warnings(fz_context *ctx) {
	fz_set_warning_callback(ctx, silent_warning, NULL);
}

// page_info sets *dpi to the page's native image resolution; for a single full-page image with no text it also sets *box and returns 1.
int page_info(fz_context *ctx, fz_page *page, double *dpi, fz_rect *box) {
	fz_rect b = fz_bound_page(ctx, page);
	double parea = (b.x1 - b.x0) * (b.y1 - b.y0);
	fz_stext_page *text = fz_new_stext_page(ctx, b);
	fz_stext_options opts = {0};
	opts.flags = FZ_STEXT_PRESERVE_IMAGES;

	double nd = 0, barea = 0;
	int has_text = 0;
	fz_rect bb = fz_empty_rect;

	fz_try(ctx) {
		fz_device *dev = fz_new_stext_device(ctx, text, &opts);
		fz_run_page(ctx, page, dev, fz_identity, NULL);
		fz_close_device(ctx, dev);
		fz_drop_device(ctx, dev);

		for (fz_stext_block *blk = text->first_block; blk; blk = blk->next) {
			if (blk->type == FZ_STEXT_BLOCK_TEXT) {
				has_text = 1;
				continue;
			}
			if (blk->type != FZ_STEXT_BLOCK_IMAGE)
				continue;
			fz_image *img = blk->u.i.image;
			double bw = blk->bbox.x1 - blk->bbox.x0;
			double bh = blk->bbox.y1 - blk->bbox.y0;
			if (bw > 0 && img->w * 72.0 / bw > nd)
				nd = img->w * 72.0 / bw;
			if (bh > 0 && img->h * 72.0 / bh > nd)
				nd = img->h * 72.0 / bh;
			if (bw * bh > barea) {
				barea = bw * bh;
				bb = blk->bbox;
			}
		}
	}
	fz_always(ctx)
		fz_drop_stext_page(ctx, text);
	fz_catch(ctx) {
		nd = 0;
		has_text = 1;
	}

	*dpi = nd;
	*box = bb;

	return (!has_text && parea > 0 && barea >= 0.5 * parea) ? 1 : 0;
}
*/
import "C"

import (
	"image"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"
)

// Document represents fitz document.
type Document struct {
	ctx    *C.struct_fz_context
	data   []byte // binds data to the Document lifecycle avoiding premature GC
	doc    *C.struct_fz_document
	mtx    sync.Mutex
	stream *C.fz_stream

	// GoPDF patch: file the document was opened from ("" for memory
	// documents), so Save can enforce incremental-saves-target-the-open-file
	// and make cross-path saves atomic.
	path string

	// GoPDF patch: structured text of the last few pages SelectionQuads
	// touched, so drag updates reuse one extraction (f.mtx guards it, as
	// every Document use).
	stextPages map[int]*C.fz_stext_page
	stextOrder []int
}

// New returns new fitz document.
func New(filename string) (f *Document, err error) {
	f = &Document{}

	filename, err = filepath.Abs(filename)
	if err != nil {
		return
	}

	if _, e := os.Stat(filename); e != nil {
		err = ErrNoSuchFile
		return
	}

	f.ctx = (*C.struct_fz_context)(unsafe.Pointer(C.fz_new_context_imp(nil, nil, C.store(MaxStore), C.fz_version)))
	if f.ctx == nil {
		err = ErrCreateContext
		return
	}

	C.silence_warnings(f.ctx)

	C.fz_register_document_handlers(f.ctx)

	cfilename := C.CString(filename)
	defer C.free(unsafe.Pointer(cfilename))

	f.doc = C.open_document(f.ctx, cfilename)
	if f.doc == nil {
		err = ErrOpenDocument
		return
	}
	f.path = filename

	ret := C.fz_needs_password(f.ctx, f.doc)
	v := int(ret) != 0
	if v {
		err = ErrNeedsPassword
	}

	return
}

// NewFromMemory returns new fitz document from byte slice.
func NewFromMemory(b []byte) (f *Document, err error) {
	if len(b) == 0 {
		return nil, ErrEmptyBytes
	}
	f = &Document{}

	f.ctx = (*C.struct_fz_context)(unsafe.Pointer(C.fz_new_context_imp(nil, nil, C.store(MaxStore), C.fz_version)))
	if f.ctx == nil {
		err = ErrCreateContext
		return
	}

	C.silence_warnings(f.ctx)

	C.fz_register_document_handlers(f.ctx)

	f.stream = C.fz_open_memory(f.ctx, (*C.uchar)(&b[0]), C.size_t(len(b)))
	if f.stream == nil {
		err = ErrOpenMemory
		return
	}

	magic := contentType(b)
	if magic == "" {
		err = ErrOpenMemory
		return
	}

	f.data = b

	cmagic := C.CString(magic)
	defer C.free(unsafe.Pointer(cmagic))

	f.doc = C.open_document_with_stream(f.ctx, cmagic, f.stream)
	if f.doc == nil {
		err = ErrOpenDocument
	}

	ret := C.fz_needs_password(f.ctx, f.doc)
	v := int(ret) != 0
	if v {
		err = ErrNeedsPassword
	}

	return
}

// NewFromReader returns new fitz document from io.Reader.
func NewFromReader(r io.Reader) (f *Document, err error) {
	b, e := io.ReadAll(r)
	if e != nil {
		err = e
		return
	}

	f, err = NewFromMemory(b)

	return
}

// NumPage returns total number of pages in document.
func (f *Document) NumPage() int {
	return int(C.fz_count_pages(f.ctx, f.doc))
}

// Image returns the page at its native resolution (300 DPI if it has no images), cropped to a single full-page image.
func (f *Document) Image(pageNumber int) (*image.RGBA, error) {
	dpi, x0, y0, x1, y1, crop := f.pageInfo(pageNumber)
	if dpi <= 0 {
		dpi = 300.0
	}

	img, err := f.ImageDPI(pageNumber, dpi)
	if err != nil {
		return nil, err
	}

	if crop {
		img = cropImage(img, x0, y0, x1, y1, dpi)
	}

	return img, nil
}

// pageInfo returns the native resolution and, for a single full-page image, its bbox (points) with crop=true.
func (f *Document) pageInfo(pageNumber int) (dpi, x0, y0, x1, y1 float64, crop bool) {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	if pageNumber >= f.NumPage() {
		return
	}

	page := C.load_page(f.ctx, f.doc, C.int(pageNumber))
	if page == nil {
		return
	}

	defer C.fz_drop_page(f.ctx, page)

	var cdpi C.double
	var box C.fz_rect
	c := C.page_info(f.ctx, page, &cdpi, &box)

	return float64(cdpi), float64(box.x0), float64(box.y0), float64(box.x1), float64(box.y1), c != 0
}

// ImageDPI returns image for given page number and DPI.
func (f *Document) ImageDPI(pageNumber int, dpi float64) (*image.RGBA, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	if pageNumber >= f.NumPage() {
		return nil, ErrPageMissing
	}

	page := C.load_page(f.ctx, f.doc, C.int(pageNumber))
	if page == nil {
		return nil, ErrLoadPage
	}

	defer C.fz_drop_page(f.ctx, page)

	var bounds C.fz_rect
	bounds = C.fz_bound_page(f.ctx, page)

	var ctm C.fz_matrix
	ctm = C.fz_scale(C.float(dpi/72), C.float(dpi/72))

	var bbox C.fz_irect
	bounds = C.fz_transform_rect(bounds, ctm)
	bbox = C.fz_round_rect(bounds)

	pixmap := C.fz_new_pixmap_with_bbox(f.ctx, C.fz_device_rgb(f.ctx), bbox, nil, 1)
	if pixmap == nil {
		return nil, ErrCreatePixmap
	}

	C.fz_clear_pixmap_with_value(f.ctx, pixmap, C.int(0xff))
	defer C.fz_drop_pixmap(f.ctx, pixmap)

	device := C.fz_new_draw_device(f.ctx, ctm, pixmap)
	C.fz_enable_device_hints(f.ctx, device, C.FZ_NO_CACHE)
	defer C.fz_drop_device(f.ctx, device)

	drawMatrix := C.fz_identity
	ret := C.run_page_full(f.ctx, page, device, drawMatrix, nil) // GoPDF: contents + annotations
	if ret == 0 {
		return nil, ErrRunPageContents
	}

	C.fz_close_device(f.ctx, device)

	pixels := C.fz_pixmap_samples(f.ctx, pixmap)
	if pixels == nil {
		return nil, ErrPixmapSamples
	}

	img := image.NewRGBA(image.Rect(int(bbox.x0), int(bbox.y0), int(bbox.x1), int(bbox.y1)))
	copy(img.Pix, C.GoBytes(unsafe.Pointer(pixels), C.int(4*bbox.x1*bbox.y1)))

	return img, nil
}

// SearchQuad is the bounding box of one search hit, in page-space PDF
// points with a top-left origin (as fz pages bound). (GoPDF fork.)
type SearchQuad struct {
	X0, Y0, X1, Y1 float64
}

// searchHitMax caps fz_search_page's quad buffer per page.
const searchHitMax = 64

// SearchPage returns the quads of every occurrence of needle on the page
// (up to 64): exact geometry around the matched glyphs, unlike the span
// rectangles the HTML text extraction estimates. Case-insensitive, as
// fz_search is. Serialized with the other Document calls on the internal
// mutex. (GoPDF fork.)
func (f *Document) SearchPage(pageNumber int, needle string) ([]SearchQuad, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	if pageNumber >= f.NumPage() {
		return nil, ErrPageMissing
	}

	page := C.load_page(f.ctx, f.doc, C.int(pageNumber))
	if page == nil {
		return nil, ErrLoadPage
	}

	defer C.fz_drop_page(f.ctx, page)

	cneedle := C.CString(needle)
	defer C.free(unsafe.Pointer(cneedle))

	var quads [searchHitMax]C.fz_quad
	n := int(C.search_page(f.ctx, page, cneedle, &quads[0], searchHitMax))
	if n < 0 {
		return nil, ErrRunPageContents
	}

	out := make([]SearchQuad, 0, n)
	for i := 0; i < n; i++ {
		q := quads[i]
		x0 := math.Min(math.Min(float64(q.ul.x), float64(q.ur.x)), math.Min(float64(q.ll.x), float64(q.lr.x)))
		x1 := math.Max(math.Max(float64(q.ul.x), float64(q.ur.x)), math.Max(float64(q.ll.x), float64(q.lr.x)))
		y0 := math.Min(math.Min(float64(q.ul.y), float64(q.ur.y)), math.Min(float64(q.ll.y), float64(q.lr.y)))
		y1 := math.Max(math.Max(float64(q.ul.y), float64(q.ur.y)), math.Max(float64(q.ll.y), float64(q.lr.y)))
		out = append(out, SearchQuad{X0: x0, Y0: y0, X1: x1, Y1: y1})
	}
	return out, nil
}

// selectionQuadMax caps fz_highlight_selection's quad buffer per call: a
// drag can cross many lines, but no realistic single-page selection
// needs more. (GoPDF fork.)
const selectionQuadMax = 512

// stextPage returns the cached structured text of a page, building and
// caching it on first use (the four most recent pages stay). The caller
// holds f.mtx. (GoPDF fork.)
func (f *Document) stextPage(pageNumber int) *C.fz_stext_page {
	if t, ok := f.stextPages[pageNumber]; ok {
		return t
	}
	page := C.load_page(f.ctx, f.doc, C.int(pageNumber))
	if page == nil {
		return nil
	}
	defer C.fz_drop_page(f.ctx, page)

	t := C.stext_from_page(f.ctx, page)
	if t == nil {
		return nil
	}
	if f.stextPages == nil {
		f.stextPages = map[int]*C.fz_stext_page{}
	}
	f.stextPages[pageNumber] = t
	f.stextOrder = append(f.stextOrder, pageNumber)
	for len(f.stextOrder) > 4 {
		old := f.stextOrder[0]
		f.stextOrder = f.stextOrder[1:]
		if ot, ok := f.stextPages[old]; ok {
			C.fz_drop_stext_page(f.ctx, ot)
			delete(f.stextPages, old)
		}
	}
	return t
}

// SelectionQuads returns the exact highlight quads and plain text of the
// selection from (x0,y0) to (x1,y1) on the page, in page space (points,
// as SearchPage's quads are): fz_highlight_selection over the same
// structured text fz_search searches, so selection geometry cannot drift
// from the glyphs the way estimated span rectangles can. Serialized with
// the other Document calls on the internal mutex. (GoPDF fork.)
func (f *Document) SelectionQuads(pageNumber int, x0, y0, x1, y1 float64) ([]SearchQuad, string, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	if pageNumber >= f.NumPage() {
		return nil, "", ErrPageMissing
	}

	text := f.stextPage(pageNumber)
	if text == nil {
		return nil, "", ErrRunPageContents
	}

	var quads [selectionQuadMax]C.fz_quad
	var cstr *C.char
	n := int(C.selection_quads(f.ctx, text,
		C.float(x0), C.float(y0), C.float(x1), C.float(y1),
		&quads[0], selectionQuadMax, &cstr))
	if n < 0 {
		return nil, "", ErrRunPageContents
	}
	sel := ""
	if cstr != nil {
		sel = C.GoString(cstr)
		C.drop_string(f.ctx, cstr)
	}

	out := make([]SearchQuad, 0, n)
	for i := 0; i < n; i++ {
		q := quads[i]
		x0 := math.Min(math.Min(float64(q.ul.x), float64(q.ur.x)), math.Min(float64(q.ll.x), float64(q.lr.x)))
		x1 := math.Max(math.Max(float64(q.ul.x), float64(q.ur.x)), math.Max(float64(q.ll.x), float64(q.lr.x)))
		y0 := math.Min(math.Min(float64(q.ul.y), float64(q.ur.y)), math.Min(float64(q.ll.y), float64(q.lr.y)))
		y1 := math.Max(math.Max(float64(q.ul.y), float64(q.ur.y)), math.Max(float64(q.ll.y), float64(q.lr.y)))
		out = append(out, SearchQuad{X0: x0, Y0: y0, X1: x1, Y1: y1})
	}
	return out, sel, nil
}

// cjkFallbackCandidates lists, per platform, the OS font files probed as
// the CJK fallback, in order. A plain stat decides; nothing parses or
// probes the font itself. (GoPDF fork.)
func cjkFallbackCandidates() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{
			`C:\Windows\Fonts\msyh.ttc`,   // Microsoft YaHei
			`C:\Windows\Fonts\simhei.ttf`, // SimHei
			`C:\Windows\Fonts\simsun.ttc`, // SimSun
		}
	case "darwin":
		return []string{
			"/System/Library/Fonts/PingFang.ttc",
			"/System/Library/Fonts/Hiragino Sans GB.ttc",
			"/System/Library/Fonts/STHeiti Light.ttc",
			"/System/Library/Fonts/STHeiti Medium.ttc",
			"/System/Library/Fonts/Supplemental/Songti.ttc",
		}
	default: // linux and the BSDs: the usual CJK font packages
		return []string{
			"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/google-noto-sans-cjk-fonts/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/droid/DroidSansFallbackFull.ttf",
			"/usr/share/fonts/truetype/droid/DroidSansFallbackFull.ttf",
		}
	}
}

// CJKFallbackFontPath returns the first OS CJK font candidate that exists
// (a plain file stat; no font probing). goPDF arms MuPDF's system-font
// hooks with it so non-embedded-font CJK PDFs stop painting blanks. (GoPDF
// fork.)
func CJKFallbackFontPath() (string, bool) {
	for _, p := range cjkFallbackCandidates() {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

// InstallCJKFallback arms this document's context to fall back to the OS
// font at path when a document asks MuPDF for a CJK system font — the
// bundled libraries carry no CJK fonts, so PDFs whose CJK fonts are not
// embedded otherwise fail their font load. Best-effort by design: it
// returns false when the context is already armed or path is empty, and a
// font that cannot be loaded later is dropped silently, leaving rendering
// exactly as before. The font loads lazily on first use, so documents that
// never ask pay nothing. Call before first use (the opening code does);
// Close releases the install. (GoPDF fork.)
func (f *Document) InstallCJKFallback(path string) bool {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	return C.gopdf_install_cjk_fallback(f.ctx, cpath) != 0
}

// ImagePNG returns image for given page number as PNG bytes.
func (f *Document) ImagePNG(pageNumber int, dpi float64) ([]byte, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	if pageNumber >= f.NumPage() {
		return nil, ErrPageMissing
	}

	page := C.load_page(f.ctx, f.doc, C.int(pageNumber))
	if page == nil {
		return nil, ErrLoadPage
	}

	defer C.fz_drop_page(f.ctx, page)

	var bounds C.fz_rect
	bounds = C.fz_bound_page(f.ctx, page)

	var ctm C.fz_matrix
	ctm = C.fz_scale(C.float(dpi/72), C.float(dpi/72))

	var bbox C.fz_irect
	bounds = C.fz_transform_rect(bounds, ctm)
	bbox = C.fz_round_rect(bounds)

	pixmap := C.fz_new_pixmap_with_bbox(f.ctx, C.fz_device_rgb(f.ctx), bbox, nil, 1)
	if pixmap == nil {
		return nil, ErrCreatePixmap
	}

	C.fz_clear_pixmap_with_value(f.ctx, pixmap, C.int(0xff))
	defer C.fz_drop_pixmap(f.ctx, pixmap)

	device := C.fz_new_draw_device(f.ctx, ctm, pixmap)
	C.fz_enable_device_hints(f.ctx, device, C.FZ_NO_CACHE)
	defer C.fz_drop_device(f.ctx, device)

	drawMatrix := C.fz_identity
	ret := C.run_page_full(f.ctx, page, device, drawMatrix, nil) // GoPDF: contents + annotations
	if ret == 0 {
		return nil, ErrRunPageContents
	}

	C.fz_close_device(f.ctx, device)

	buf := C.fz_new_buffer_from_pixmap_as_png(f.ctx, pixmap, C.fz_default_color_params)
	defer C.fz_drop_buffer(f.ctx, buf)

	size := C.fz_buffer_storage(f.ctx, buf, nil)
	str := C.GoStringN(C.fz_string_from_buffer(f.ctx, buf), C.int(size))

	return []byte(str), nil
}

// Links returns slice of links for given page number.
func (f *Document) Links(pageNumber int) ([]Link, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	if pageNumber >= f.NumPage() {
		return nil, ErrPageMissing
	}

	page := C.load_page(f.ctx, f.doc, C.int(pageNumber))
	if page == nil {
		return nil, ErrLoadPage
	}

	defer C.fz_drop_page(f.ctx, page)

	links := C.fz_load_links(f.ctx, page)
	defer C.fz_drop_link(f.ctx, links)

	linkCount := 0
	for currLink := links; currLink != nil; currLink = currLink.next {
		linkCount++
	}

	if linkCount == 0 {
		return nil, nil
	}

	gLinks := make([]Link, linkCount)

	currLink := links
	for i := 0; i < linkCount; i++ {
		gLinks[i] = Link{
			URI: C.GoString(currLink.uri),
		}
		currLink = currLink.next
	}

	return gLinks, nil
}

// Text returns text for given page number.
func (f *Document) Text(pageNumber int) (string, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	if pageNumber >= f.NumPage() {
		return "", ErrPageMissing
	}

	page := C.load_page(f.ctx, f.doc, C.int(pageNumber))
	if page == nil {
		return "", ErrLoadPage
	}

	defer C.fz_drop_page(f.ctx, page)

	var bounds C.fz_rect
	bounds = C.fz_bound_page(f.ctx, page)

	var ctm C.fz_matrix
	ctm = C.fz_scale(C.float(72.0/72), C.float(72.0/72))

	text := C.fz_new_stext_page(f.ctx, bounds)
	defer C.fz_drop_stext_page(f.ctx, text)

	var opts C.fz_stext_options
	opts.flags = 0

	device := C.fz_new_stext_device(f.ctx, text, &opts)
	C.fz_enable_device_hints(f.ctx, device, C.FZ_NO_CACHE)
	defer C.fz_drop_device(f.ctx, device)

	var cookie C.fz_cookie
	ret := C.run_page_contents(f.ctx, page, device, ctm, &cookie)
	if ret == 0 {
		return "", ErrRunPageContents
	}

	C.fz_close_device(f.ctx, device)

	buf := C.fz_new_buffer_from_stext_page(f.ctx, text)
	defer C.fz_drop_buffer(f.ctx, buf)

	str := C.GoString(C.fz_string_from_buffer(f.ctx, buf))

	return str, nil
}

// HTML returns html for given page number.
func (f *Document) HTML(pageNumber int, header bool) (string, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	if pageNumber >= f.NumPage() {
		return "", ErrPageMissing
	}

	page := C.load_page(f.ctx, f.doc, C.int(pageNumber))
	if page == nil {
		return "", ErrLoadPage
	}

	defer C.fz_drop_page(f.ctx, page)

	var bounds C.fz_rect
	bounds = C.fz_bound_page(f.ctx, page)

	var ctm C.fz_matrix
	ctm = C.fz_scale(C.float(72.0/72), C.float(72.0/72))

	text := C.fz_new_stext_page(f.ctx, bounds)
	defer C.fz_drop_stext_page(f.ctx, text)

	var opts C.fz_stext_options
	opts.flags = C.FZ_STEXT_PRESERVE_IMAGES

	device := C.fz_new_stext_device(f.ctx, text, &opts)
	C.fz_enable_device_hints(f.ctx, device, C.FZ_NO_CACHE)
	defer C.fz_drop_device(f.ctx, device)

	var cookie C.fz_cookie
	ret := C.run_page_contents(f.ctx, page, device, ctm, &cookie)
	if ret == 0 {
		return "", ErrRunPageContents
	}

	C.fz_close_device(f.ctx, device)

	buf := C.fz_new_buffer(f.ctx, 1024)
	defer C.fz_drop_buffer(f.ctx, buf)

	out := C.fz_new_output_with_buffer(f.ctx, buf)
	defer C.fz_drop_output(f.ctx, out)

	if header {
		C.fz_print_stext_header_as_html(f.ctx, out)
	}
	C.fz_print_stext_page_as_html(f.ctx, out, text, C.int(pageNumber))
	if header {
		C.fz_print_stext_trailer_as_html(f.ctx, out)
	}

	C.fz_close_output(f.ctx, out)

	str := C.GoString(C.fz_string_from_buffer(f.ctx, buf))

	return str, nil
}

// SVG returns svg document for given page number.
func (f *Document) SVG(pageNumber int) (string, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	if pageNumber >= f.NumPage() {
		return "", ErrPageMissing
	}

	page := C.load_page(f.ctx, f.doc, C.int(pageNumber))
	if page == nil {
		return "", ErrLoadPage
	}

	defer C.fz_drop_page(f.ctx, page)

	var bounds C.fz_rect
	bounds = C.fz_bound_page(f.ctx, page)

	var ctm C.fz_matrix
	ctm = C.fz_scale(C.float(72.0/72), C.float(72.0/72))
	bounds = C.fz_transform_rect(bounds, ctm)

	buf := C.fz_new_buffer(f.ctx, 1024)
	defer C.fz_drop_buffer(f.ctx, buf)

	out := C.fz_new_output_with_buffer(f.ctx, buf)
	defer C.fz_drop_output(f.ctx, out)

	device := C.fz_new_svg_device(f.ctx, out, bounds.x1-bounds.x0, bounds.y1-bounds.y0, C.FZ_SVG_TEXT_AS_PATH, 1)
	C.fz_enable_device_hints(f.ctx, device, C.FZ_NO_CACHE)
	defer C.fz_drop_device(f.ctx, device)

	var cookie C.fz_cookie
	ret := C.run_page_contents(f.ctx, page, device, ctm, &cookie)
	if ret == 0 {
		return "", ErrRunPageContents
	}

	C.fz_close_device(f.ctx, device)
	C.fz_close_output(f.ctx, out)

	str := C.GoString(C.fz_string_from_buffer(f.ctx, buf))

	return str, nil
}

// ToC returns the table of contents (also known as outline).
func (f *Document) ToC() ([]Outline, error) {
	data := make([]Outline, 0)

	outline := C.fz_load_outline(f.ctx, f.doc)
	if outline == nil {
		return nil, ErrLoadOutline
	}
	defer C.fz_drop_outline(f.ctx, outline)

	var walk func(outline *C.fz_outline, level int)

	walk = func(outline *C.fz_outline, level int) {
		for outline != nil {
			res := Outline{}
			res.Level = level
			res.Title = C.GoString(outline.title)
			res.URI = C.GoString(outline.uri)
			res.Page = int(outline.page.page)
			res.Top = float64(outline.y)
			data = append(data, res)

			if outline.down != nil {
				walk(outline.down, level+1)
			}
			outline = outline.next
		}
	}

	walk(outline, 1)
	return data, nil
}

// Metadata returns the map with standard metadata.
func (f *Document) Metadata() map[string]string {
	data := make(map[string]string)

	// GoPDF patch: GoString the NUL-terminated C buffer — string(buf)
	// kept every trailing zero byte of the 256-byte scratch space.
	lookup := func(key string) string {
		ckey := C.CString(key)
		defer C.free(unsafe.Pointer(ckey))

		buf := make([]byte, 256)
		C.fz_lookup_metadata(f.ctx, f.doc, ckey, (*C.char)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)))

		return C.GoString((*C.char)(unsafe.Pointer(&buf[0])))
	}

	data["format"] = lookup("format")
	data["encryption"] = lookup("encryption")
	data["title"] = lookup("info:Title")
	data["author"] = lookup("info:Author")
	data["subject"] = lookup("info:Subject")
	data["keywords"] = lookup("info:Keywords")
	data["creator"] = lookup("info:Creator")
	data["producer"] = lookup("info:Producer")
	data["creationDate"] = lookup("info:CreationDate")
	data["modDate"] = lookup("info:ModDate")

	return data
}

// Bound gives the Bounds of a given Page in the document.
func (f *Document) Bound(pageNumber int) (image.Rectangle, error) {
	f.mtx.Lock()
	defer f.mtx.Unlock()

	if pageNumber >= f.NumPage() {
		return image.Rectangle{}, ErrPageMissing
	}

	page := C.load_page(f.ctx, f.doc, C.int(pageNumber))
	if page == nil {
		return image.Rectangle{}, ErrLoadPage
	}

	defer C.fz_drop_page(f.ctx, page)

	var bounds C.fz_rect
	bounds = C.fz_bound_page(f.ctx, page)

	return image.Rect(int(bounds.x0), int(bounds.y0), int(bounds.x1), int(bounds.y1)), nil
}

// Close closes the underlying fitz document.
func (f *Document) Close() error {
	// GoPDF patch: drop the cached stext pages and the CJK fallback
	// install (its font reference) before the context they live in.
	for _, t := range f.stextPages {
		C.fz_drop_stext_page(f.ctx, t)
	}
	f.stextPages, f.stextOrder = nil, nil

	C.gopdf_drop_cjk_fallback(f.ctx)

	if f.stream != nil {
		C.fz_drop_stream(f.ctx, f.stream)
	}

	C.fz_drop_document(f.ctx, f.doc)
	C.fz_drop_context(f.ctx)

	f.data = nil

	return nil
}
