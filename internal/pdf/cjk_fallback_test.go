package pdf

// CJK glyph fallback tests (issue 5). The bundled MuPDF ships without CJK
// fonts, so a PDF whose CJK font is NOT embedded paints no glyphs. The fork
// arms MuPDF's system-font hooks with an OS CJK font at open time.
//
// The oracle is pixel-based, per the design note: rendering the same
// non-embedded-CJK page with and without the fallback installed must
// DIFFER, the installed render must track the codepoints (two different
// CJK strings paint different glyphs), a Latin page must stay
// pixel-IDENTICAL with the install armed, and text extraction must not
// blank out. Everything skips silently on machines with no OS CJK font,
// where the feature is inert by construction.

import (
	"bytes"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/gen2brain/go-fitz"
)

// utf16BEHex renders s as the big-endian UTF-16 hex string UniGB-UCS2-H
// takes in a PDF content stream (<4E2D6587> for "中文").
func utf16BEHex(s string) string {
	var sb strings.Builder
	for _, u := range utf16.Encode([]rune(s)) {
		fmt.Fprintf(&sb, "%04X", u)
	}
	return sb.String()
}

// genCJKPDFBytes builds a one-page PDF whose only font is a Type0
// CIDFontType2 (Adobe-GB1, UniGB-UCS2-H encoding) with NO embedded font
// file — the shape of real-world CJK PDFs that assume a viewer-supplied
// font. The codes in the content stream are UTF-16BE, so any string works.
// This lives here, not in gen_test.go, so the committed generator keeps
// its standard-font shape.
func genCJKPDFBytes(text string) []byte {
	content := "BT /F1 24 Tf 72 720 Td <" + utf16BEHex(text) + "> Tj ET"
	f := &pdfFile{}
	f.obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	f.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	f.obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")
	f.obj(4, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	// Type0 parent + CIDFontType2 descendant. The FontDescriptor carries
	// no FontFile2/FontFile3: MuPDF must ask its system-font hooks.
	f.obj(5, "<< /Type /Font /Subtype /Type0 /BaseFont /STSong-Light /Encoding /UniGB-UCS2-H /DescendantFonts [6 0 R] >>")
	f.obj(6, "<< /Type /Font /Subtype /CIDFontType2 /BaseFont /STSong-Light /CIDSystemInfo << /Registry (Adobe) /Ordering (GB1) /Supplement 5 >> /FontDescriptor 7 0 R /DW 1000 >>")
	f.obj(7, "<< /Type /FontDescriptor /FontName /STSong-Light /Flags 4 /ItalicAngle 0 /Ascent 880 /Descent -120 /CapHeight 880 /StemV 93 >>")
	return f.finish("")
}

// renderFixturePDF opens raw fixture bytes in a fresh fitz document (no
// CJK fallback unless armed) and renders page 0 at 150 DPI.
func renderFixturePDF(t *testing.T, b []byte, arm bool) *image.RGBA {
	t.Helper()
	doc, err := fitz.NewFromMemory(b)
	if err != nil {
		t.Fatalf("NewFromMemory: %v", err)
	}
	defer doc.Close()
	if arm {
		path, ok := fitz.CJKFallbackFontPath()
		if env := os.Getenv("GOPDF_TEST_CJK_FONT"); env != "" {
			path, ok = env, true
		}
		if !ok {
			t.Fatalf("arm requested but no OS CJK font candidate exists")
		}
		doc.InstallCJKFallback(path)
	}
	img, err := doc.ImageDPI(0, 150)
	if err != nil {
		t.Fatalf("ImageDPI: %v", err)
	}
	return normalizeRGBA(img)
}

// countDarkPixels counts pixels darker than mid-gray: text strokes at 150
// DPI are pure black on white.
func countDarkPixels(img *image.RGBA) int {
	n := 0
	for i := 0; i+3 < len(img.Pix); i += 4 {
		if img.Pix[i] < 128 || img.Pix[i+1] < 128 || img.Pix[i+2] < 128 {
			n++
		}
	}
	return n
}

// countDiffPixels counts pixels whose channels differ between two renders.
func countDiffPixels(a, b *image.RGBA) int {
	if a.Rect != b.Rect || len(a.Pix) != len(b.Pix) {
		return len(a.Pix) + len(b.Pix) + 1 // different geometry: everything differs
	}
	n := 0
	for i := 0; i < len(a.Pix); i++ {
		if a.Pix[i] != b.Pix[i] {
			n++
		}
	}
	return n
}

// cjkFontOrSkip returns the OS CJK fallback candidate or skips the test:
// without one the feature is intentionally inert and every assertion below
// is vacuous. GOPDF_TEST_CJK_FONT overrides the candidate list (a CI box
// with an unusual font location); GOPDF_TEST_NO_CJK=1 forces the skip so
// fontless CI boxes still exercise every other test.
func cjkFontOrSkip(t *testing.T) string {
	t.Helper()
	if os.Getenv("GOPDF_TEST_NO_CJK") == "1" {
		t.Skip("GOPDF_TEST_NO_CJK=1: CJK fallback tests forced off")
	}
	if forced := os.Getenv("GOPDF_TEST_CJK_FONT"); forced != "" {
		if _, err := os.Stat(forced); err == nil {
			return forced
		}
		t.Fatalf("GOPDF_TEST_CJK_FONT=%s does not exist", forced)
	}
	path, ok := fitz.CJKFallbackFontPath()
	if !ok {
		t.Skip("no OS CJK font candidate found; fallback is inert")
	}
	return path
}

// TestCJKFallbackPaintsGlyphs is the core oracle. Without the install the
// fixture paints no glyphs (its font cannot load at all in this MuPDF
// build); with the install the page must paint real CJK glyphs that track
// the codepoints, while text extraction stays intact either way.
func TestCJKFallbackPaintsGlyphs(t *testing.T) {
	cjkFontOrSkip(t)

	plain := genCJKPDFBytes("中文文档")
	base := renderFixturePDF(t, plain, false)
	armed := renderFixturePDF(t, plain, true)

	if n := countDarkPixels(armed); n < 500 {
		t.Errorf("armed render paints only %d dark pixels; want >= 500 (four 24pt glyphs at 150 DPI)", n)
	}
	if n := countDiffPixels(base, armed); n < 500 {
		t.Errorf("fallback changed only %d pixels; want >= 500 (install must alter CJK rendering)", n)
	}

	// The armed render must track the codepoints: a different CJK string
	// paints different glyphs. (The unarmed render also varies — it paints
	// mojibake Latin through MuPDF's hail-mary Helvetica, one garbage glyph
	// per code — so pixels alone cannot prove real glyphs; the extraction
	// check below can, and does.)
	otherArmed := renderFixturePDF(t, genCJKPDFBytes("国人文字"), true)
	if n := countDiffPixels(armed, otherArmed); n < 500 {
		t.Errorf("armed renders of two different CJK strings differ in only %d pixels; want >= 500 (glyphs must follow codepoints)", n)
	}

	// Extraction is the real oracle. Unarmed, the font load fails and
	// MuPDF substitutes its hail-mary font, so the text layer reads
	// mojibake. Armed, the Type0 font loads and extraction goes through
	// the CMaps: the stext layer carries the true CJK text (and search
	// works). Both must be non-empty — the fallback must not blank it out.
	for _, arm := range []bool{false, true} {
		doc, err := fitz.NewFromMemory(plain)
		if err != nil {
			t.Fatalf("NewFromMemory(arm=%v): %v", arm, err)
		}
		if arm {
			doc.InstallCJKFallback(cjkFontOrSkip(t))
		}
		txt, err := doc.Text(0)
		doc.Close()
		if err != nil {
			t.Fatalf("Text(arm=%v): %v", arm, err)
		}
		if strings.TrimSpace(txt) == "" {
			t.Errorf("Text(arm=%v) is blank; extraction must never blank out", arm)
		}
		if arm != strings.Contains(txt, "中文文档") {
			t.Errorf("Text(arm=%v) = %q; armed must extract the true CJK text, unarmed must not (hail-mary mojibake is the bug being fixed)", arm, txt)
		}
	}
}

// TestCJKFallbackLeavesLatinUnchanged pins the render pipeline: a page
// whose fonts are all base-14 (Helvetica) must render pixel-identical
// through pdf.Open — which arms the fallback — and through a raw fitz
// document that never installed it.
func TestCJKFallbackLeavesLatinUnchanged(t *testing.T) {
	cjkFontOrSkip(t)

	path := writeTextPDF(t, "Latin only text")
	d := openTestDoc(t, path)
	ch := make(chan *image.RGBA, 1)
	d.RenderAsync(RenderReq{Page: 0, DPI: 150, Kind: KindPage}, func(img *image.RGBA, err error) {
		if err != nil {
			t.Errorf("RenderAsync: %v", err)
			ch <- nil
			return
		}
		ch <- img
	})
	armed := recvImg(t, ch, "latin render through the facade")
	if armed == nil {
		t.Fatal("no latin render")
	}

	raw, err := fitz.New(path)
	if err != nil {
		t.Fatalf("fitz.New: %v", err)
	}
	defer raw.Close()
	plain, err := raw.ImageDPI(0, 150)
	if err != nil {
		t.Fatalf("raw ImageDPI: %v", err)
	}
	plain = normalizeRGBA(plain)

	if !bytes.Equal(armed.Pix, plain.Pix) {
		t.Errorf("latin render differs with the fallback armed: %d of %d pixels",
			countDiffPixels(armed, plain), len(armed.Pix)/4)
	}
}

// TestCJKFallbackThroughOpen exercises the production wiring: pdf.Open
// must arm the fallback by itself, so rendering the fixture through the
// facade paints glyphs a raw (unarmed) fitz render does not.
func TestCJKFallbackThroughOpen(t *testing.T) {
	cjkFontOrSkip(t)

	path := filepath.Join(t.TempDir(), "cjk.pdf")
	if err := os.WriteFile(path, genCJKPDFBytes("中文文档"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	d := openTestDoc(t, path)
	ch := make(chan *image.RGBA, 1)
	d.RenderAsync(RenderReq{Page: 0, DPI: 150, Kind: KindPage}, func(img *image.RGBA, err error) {
		if err != nil {
			t.Errorf("RenderAsync: %v", err)
			ch <- nil
			return
		}
		ch <- img
	})
	armed := recvImg(t, ch, "facade render of the CJK fixture")
	if armed == nil {
		t.Fatal("no facade render")
	}

	plain := renderFixturePDF(t, genCJKPDFBytes("中文文档"), false)
	if n := countDiffPixels(armed, plain); n < 500 {
		t.Errorf("pdf.Open render differs from the unarmed render in only %d pixels; want >= 500 (Open must arm the fallback)", n)
	}
	if n := countDarkPixels(armed); n < 500 {
		t.Errorf("pdf.Open render paints only %d dark pixels; want >= 500", n)
	}
}
