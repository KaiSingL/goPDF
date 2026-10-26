package pdf

import (
	"strings"
	"testing"
	"time"

	"github.com/gen2brain/go-fitz"
)

// observedHTML is the raw doc.HTML(0,false) output for a 612x792 page with
// two text lines ("Hello modern-pdf" at PDF 72,720 and "second line here" at
// 72,684), captured verbatim from the pinned go-fitz v1.28.2 (MuPDF 1.28).
// TestHTMLFormatProbe re-dumps it live; TestExtractSpansObserved parses this
// snapshot as a pinned-format regression canary.
const observedHTML = `<div id="page0" style="width:612.0pt;height:792.0pt">
<p style="top:52.8pt;left:72.0pt;line-height:24.0pt"><span style="font-family:Arial,sans-serif;font-size:24.0pt;color:#000000">Hello modern-pdf</span></p>
<p style="top:88.8pt;left:72.0pt;line-height:24.0pt"><span style="font-family:Arial,sans-serif;font-size:24.0pt;color:#000000">second line here</span></p>
</div>
`

// HTMLFormatProbe dumps the real MuPDF stext HTML for a generated page and
// checks that live output extracts identically to the pinned snapshot above.
// If MuPDF's format ever changes, this fails first and observedHTML (and
// extractSpans) must be updated together.
func TestHTMLFormatProbe(t *testing.T) {
	path := writePDFFile(t, []string{textContent("Hello modern-pdf", "second line here")}, nil, nil)
	fit, err := fitz.New(path)
	if err != nil {
		t.Fatalf("fitz.New: %v", err)
	}
	defer fit.Close()

	html, err := fit.HTML(0, false)
	if err != nil {
		t.Fatalf("HTML(0,false): %v", err)
	}
	t.Logf("live HTML(0,false):\n%s", html)

	live := extractSpans(html, 0)
	want := extractSpans(observedHTML, 0)
	if len(live) != len(want) {
		t.Fatalf("live HTML yields %d spans, pinned snapshot %d\nlive: %+v\npinned: %+v",
			len(live), len(want), live, want)
	}
	for i := range want {
		if live[i].Text != want[i].Text ||
			diff(live[i].X, want[i].X) || diff(live[i].Y, want[i].Y) ||
			diff(live[i].W, want[i].W) || diff(live[i].H, want[i].H) {
			t.Fatalf("span %d differs from pinned snapshot:\nlive:   %+v\npinned: %+v", i, live[i], want[i])
		}
	}
}

// diff reports whether two coordinates disagree beyond rounding noise.
func diff(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d > 0.01
}

// TestExtractSpansObserved parses the pinned real-world MuPDF HTML and
// asserts the span contract: found text, top-left origin, plausible box.
func TestExtractSpansObserved(t *testing.T) {
	spans := extractSpans(observedHTML, 3)
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2: %+v", len(spans), spans)
	}

	s0 := spans[0]
	if s0.Page != 3 || s0.Text != "Hello modern-pdf" {
		t.Errorf("span0 = %+v, want text %q page 3", s0, "Hello modern-pdf")
	}
	// Baseline at PDF y=720 with a 24pt font: top = 792-720-ascent(19.2) =
	// 52.8pt from the page top (top-left origin, y grows down).
	if diff(s0.X, 72) || s0.Y <= 0 || s0.Y >= 100 {
		t.Errorf("span0 pos = (%g,%g), want x=72, y in (0,100) near page top", s0.X, s0.Y)
	}
	if s0.W <= 0 || s0.X+s0.W > 612 || s0.H <= 0 || s0.Y+s0.H > 792 {
		t.Errorf("span0 box = %+v outside page bounds 612x792", s0)
	}
	if diff(s0.H, 24) {
		t.Errorf("span0 height = %g, want line-height 24", s0.H)
	}

	if spans[1].Text != "second line here" {
		t.Errorf("span1 text = %q", spans[1].Text)
	}
	if spans[1].Y <= s0.Y {
		t.Errorf("span1 y %g should be below span0 y %g (reading order)", spans[1].Y, s0.Y)
	}
}

// TestExtractSpansEmpty verifies nil results for non-text pages.
func TestExtractSpansEmpty(t *testing.T) {
	if got := extractSpans(`<div id="page0" style="width:612.0pt;height:792.0pt"></div>`, 0); got != nil {
		t.Errorf("empty page: got %+v, want nil", got)
	}
	if got := extractSpans("", 0); got != nil {
		t.Errorf("empty html: got %+v, want nil", got)
	}
}

// indexFor builds the search index of doc and waits (bounded) for it.
func indexFor(t *testing.T, d *Doc) *Index {
	t.Helper()
	ch := make(chan *Index, 1)
	d.IndexAsync(func(ix *Index, err error) {
		if err != nil {
			t.Errorf("IndexAsync: %v", err)
		}
		ch <- ix
	})
	select {
	case ix := <-ch:
		if ix == nil {
			t.Fatal("IndexAsync delivered nil index")
		}
		return ix
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for search index")
		return nil
	}
}

// TestIndexAsyncBuild checks the build-once contract: the second call
// returns the same index synchronously.
func TestIndexAsyncBuild(t *testing.T) {
	d := openTestDoc(t, writeTextPDF(t, "alpha page", "beta page"))
	ix1 := indexFor(t, d)

	done := make(chan *Index, 1)
	d.IndexAsync(func(ix *Index, err error) { done <- ix })
	select {
	case ix2 := <-done:
		if ix2 != ix1 {
			t.Error("second IndexAsync returned a different index; want reuse of the built one")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second IndexAsync should fire synchronously")
	}
}

// TestSearchFindsTextAndSpans is the critical end-to-end search test: the
// index finds known text on the right page with covered, plausible spans.
func TestSearchFindsTextAndSpans(t *testing.T) {
	d := openTestDoc(t, writeTextPDF(t,
		"Hello modern-pdf",
		"Second page: The quick brown fox",
		"unrelated filler"))
	ix := indexFor(t, d)

	if got := ix.NumPages(); got != 3 {
		t.Fatalf("NumPages = %d, want 3", got)
	}

	// Case-insensitive match on page 1.
	hits := ix.Search("QUICK BROWN")
	if len(hits) != 1 {
		t.Fatalf("Search(QUICK BROWN) = %d hits, want 1: %+v", len(hits), hits)
	}
	h := hits[0]
	if h.Page != 1 {
		t.Errorf("hit page = %d, want 1", h.Page)
	}
	if len(h.Spans) != 1 || !strings.Contains(h.Spans[0].Text, "quick brown fox") {
		t.Errorf("hit spans = %+v, want the one span carrying the text", h.Spans)
	}
	sp := h.Spans[0]
	if sp.X < 0 || sp.Y < 0 || sp.X+sp.W > 612 || sp.Y+sp.H > 792 {
		t.Errorf("span %+v outside page bounds", sp)
	}

	// Page text accessors agree.
	if txt := ix.PageText(1); !strings.Contains(strings.ToLower(txt), "quick brown") {
		t.Errorf("PageText(1) = %q, want the fox", txt)
	}
}

// TestSearchAcrossSpans searches a query that spans a line break: the two
// lines of one page are separate spans, joined with a single space.
func TestSearchAcrossSpans(t *testing.T) {
	d := openTestDoc(t, writePDFFile(t, []string{
		textContent("The quick brown", "fox jumps over"),
		textContent("nothing to see"),
	}, nil, nil))
	ix := indexFor(t, d)

	hits := ix.Search("brown fox")
	if len(hits) != 1 {
		t.Fatalf("Search(brown fox) = %d hits, want 1: %+v", len(hits), hits)
	}
	if hits[0].Page != 0 || len(hits[0].Spans) != 2 {
		t.Fatalf("hit = %+v, want page 0 covering 2 spans", hits[0])
	}
	if hits[0].Spans[0].Text != "The quick brown" || hits[0].Spans[1].Text != "fox jumps over" {
		t.Errorf("covered spans = %q, %q", hits[0].Spans[0].Text, hits[0].Spans[1].Text)
	}

	// Same query in lowercase also matches (haystack is lowercased).
	if got := ix.Search("brown fox"); len(got) != 1 {
		t.Errorf("repeat search = %d hits, want 1", len(got))
	}
}

// TestSearchMultipleAndRepeated checks ordering and overlapping matches.
func TestSearchMultipleAndRepeated(t *testing.T) {
	d := openTestDoc(t, writeTextPDF(t,
		"needle here",
		"nothing",
		"needle and needle again"))
	ix := indexFor(t, d)

	hits := ix.Search("needle")
	if len(hits) != 3 {
		t.Fatalf("Search(needle) = %d hits, want 3 (page 0 once, page 2 twice): %+v", len(hits), hits)
	}
	if hits[0].Page != 0 || hits[1].Page != 2 || hits[2].Page != 2 {
		t.Errorf("hit pages = %d,%d,%d, want 0,2,2 (ordered)", hits[0].Page, hits[1].Page, hits[2].Page)
	}

	if got := ix.Search("absent"); got != nil {
		t.Errorf("Search(absent) = %+v, want nil", got)
	}
	if got := ix.Search(""); got != nil {
		t.Errorf("Search(empty) = %+v, want nil", got)
	}
	if got := ix.Search("   "); got != nil {
		t.Errorf("Search(whitespace) = %+v, want nil", got)
	}
}

// TestSearchFallbackNoSpans exercises the PageText-only path used when a
// page has no positioned spans (white-box: spans nil, text present).
func TestSearchFallbackNoSpans(t *testing.T) {
	ix := &Index{
		pages: 2,
		Spans: [][]TextSpan{nil, nil},
		Text:  []string{"", "hidden needle in plain text"},
	}
	hits := ix.Search("NEEDLE")
	if len(hits) != 1 {
		t.Fatalf("Search = %+v, want 1 jump-to-page hit", hits)
	}
	if hits[0].Page != 1 || len(hits[0].Spans) != 0 {
		t.Errorf("hit = %+v, want page 1 without spans", hits[0])
	}

	// Spans beat the fallback: pages with spans never double-report.
	ix2 := &Index{
		pages: 1,
		Spans: [][]TextSpan{{{Page: 0, X: 10, Y: 10, W: 50, H: 12, Text: "visible needle"}}},
		Text:  []string{"visible needle"},
	}
	hits = ix2.Search("needle")
	if len(hits) != 1 || len(hits[0].Spans) != 1 {
		t.Errorf("Search with spans = %+v, want exactly the span hit", hits)
	}
}

// TestIndexAccessors pins the out-of-range behavior.
func TestIndexAccessors(t *testing.T) {
	d := openTestDoc(t, writeTextPDF(t, "one", "two"))
	ix := indexFor(t, d)

	if got := ix.PageText(-1); got != "" {
		t.Errorf("PageText(-1) = %q", got)
	}
	if got := ix.PageText(99); got != "" {
		t.Errorf("PageText(99) = %q", got)
	}
	if got := ix.PageSpans(-1); got != nil {
		t.Errorf("PageSpans(-1) = %+v", got)
	}
	if got := ix.PageSpans(99); got != nil {
		t.Errorf("PageSpans(99) = %+v", got)
	}
	spans := ix.PageSpans(0)
	if len(spans) != 1 || spans[0].Text != "one" {
		t.Errorf("PageSpans(0) = %+v, want the single line", spans)
	}
	// MuPDF's plain text carries a trailing newline; PageText passes it
	// through unmodified.
	if txt := ix.PageText(0); strings.TrimRight(txt, "\n") != "one" {
		t.Errorf("PageText(0) = %q, want %q (+ trailing newline)", txt, "one")
	}
}
