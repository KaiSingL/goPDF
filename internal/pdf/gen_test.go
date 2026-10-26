package pdf

// Test-only PDF generation helpers. They emit tiny, valid PDF 1.4 files by
// hand so the tests exercise real MuPDF parsing/rendering without committing
// binary fixtures. Mirrors cmd/smoke's minimalPDF, extended with multi-line
// pages, an optional /Info dict and a two-level outline tree.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TOCEntry is one outline entry of a generated test PDF.
type TOCEntry struct {
	Title string
	Page  int // 0-based destination page
	Level int // 1 = root entry, 2 = child of the nearest preceding level-1 entry
}

// pdfStr renders s as a PDF literal string, escaping the delimiters.
func pdfStr(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	return "(" + r.Replace(s) + ")"
}

// textContent builds a page content stream with each line at x=72, the first
// at y=720 (PDF user space, origin bottom-left), each subsequent line 36pt
// lower. Each line becomes a separate positioned span in MuPDF's stext.
func textContent(lines ...string) string {
	var sb strings.Builder
	sb.WriteString("BT /F1 24 Tf")
	for i, ln := range lines {
		if i == 0 {
			sb.WriteString(" 72 720 Td")
		} else {
			sb.WriteString(" 0 -36 Td")
		}
		sb.WriteString(" " + pdfStr(ln) + " Tj")
	}
	sb.WriteString(" ET")
	return sb.String()
}

// redSquareContent returns a content stream filling a solid red square at
// PDF coords (x,y) sized w×h (origin bottom-left). Used to place a known
// color at a known corner for rotation tests.
func redSquareContent(x, y, w, h float64) string {
	return fmt.Sprintf("q 1 0 0 rg %g %g %g %g re f Q", x, y, w, h)
}

// pdfFile accumulates numbered objects and serializes xref/trailer.
type pdfFile struct {
	buf     bytes.Buffer
	offsets map[int]int
	maxObj  int
}

func (f *pdfFile) obj(num int, body string) {
	if f.offsets == nil {
		f.offsets = make(map[int]int)
	}
	f.offsets[num] = f.buf.Len()
	fmt.Fprintf(&f.buf, "%d 0 obj\n%s\nendobj\n", num, body)
	if num > f.maxObj {
		f.maxObj = num
	}
}

// finish appends the xref table and trailer (with extra trailer entries,
// e.g. "/Info 9 0 R ") and returns the complete file bytes.
func (f *pdfFile) finish(extra string) []byte {
	xref := f.buf.Len()
	fmt.Fprintf(&f.buf, "xref\n0 %d\n", f.maxObj+1)
	f.buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= f.maxObj; i++ {
		if off, ok := f.offsets[i]; ok {
			fmt.Fprintf(&f.buf, "%010d 00000 n \n", off)
		} else {
			f.buf.WriteString("0000000000 65535 f \n")
		}
	}
	fmt.Fprintf(&f.buf, "trailer\n<< /Size %d /Root 1 0 R %s>>\nstartxref\n%d\n%%%%EOF\n", f.maxObj+1, extra, xref)
	return f.buf.Bytes()
}

// pageObjNum is the object number of page i (pages start at object 3, in
// pairs with their content streams).
func pageObjNum(i int) int { return 3 + 2*i }

// genPDFBytes builds a complete PDF: one content stream per page, optional
// /Info dict, optional two-level outline.
func genPDFBytes(pages []string, info map[string]string, toc []TOCEntry) []byte {
	n := len(pages)
	if n == 0 {
		panic("gen: at least one page required")
	}
	for i, e := range toc {
		if e.Level != 1 && e.Level != 2 {
			panic(fmt.Sprintf("gen: toc entry %d level %d (want 1 or 2)", i, e.Level))
		}
		if e.Page < 0 || e.Page >= n {
			panic(fmt.Sprintf("gen: toc entry %q dest page %d out of range", e.Title, e.Page))
		}
	}

	f := &pdfFile{}
	fontObj := 3 + 2*n
	next := fontObj + 1

	var infoObj int
	if len(info) > 0 {
		infoObj = next
		next++
	}
	var outlineRoot int
	var itemObjs []int
	if len(toc) > 0 {
		outlineRoot = next
		next++
		itemObjs = make([]int, len(toc))
		for i := range toc {
			itemObjs[i] = next + i
		}
		next += len(toc)
	}

	// Catalog + page tree.
	catalog := "<< /Type /Catalog /Pages 2 0 R"
	if outlineRoot > 0 {
		catalog += fmt.Sprintf(" /Outlines %d 0 R", outlineRoot)
	}
	catalog += " >>"
	f.obj(1, catalog)

	kids := make([]string, n)
	for i := range pages {
		kids[i] = fmt.Sprintf("%d 0 R", pageObjNum(i))
	}
	f.obj(2, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n))

	for i, content := range pages {
		f.obj(pageObjNum(i), fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /Font << /F1 %d 0 R >> >> >>",
			pageObjNum(i)+1, fontObj))
		f.obj(pageObjNum(i)+1, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}

	f.obj(fontObj, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	var extra string
	if infoObj > 0 {
		var sb strings.Builder
		sb.WriteString("<< ")
		for k, v := range info {
			fmt.Fprintf(&sb, "/%s %s ", k, pdfStr(v))
		}
		sb.WriteString(">>")
		f.obj(infoObj, sb.String())
		extra = fmt.Sprintf("/Info %d 0 R ", infoObj)
	}

	if outlineRoot > 0 {
		// Outline root: /Count is the number of visible entries with all
		// items open; MuPDF walks First/Next/Down regardless.
		firstRoot, lastRoot := -1, -1
		for i, e := range toc {
			if e.Level == 1 {
				if firstRoot < 0 {
					firstRoot = i
				}
				lastRoot = i
			}
		}
		f.obj(outlineRoot, fmt.Sprintf("<< /Type /Outlines /First %d 0 R /Last %d 0 R /Count %d >>",
			itemObjs[firstRoot], itemObjs[lastRoot], len(toc)))

		for i, e := range toc {
			var sb strings.Builder
			fmt.Fprintf(&sb, "<< /Title %s ", pdfStr(e.Title))
			if e.Level == 1 {
				fmt.Fprintf(&sb, "/Parent %d 0 R ", outlineRoot)
			} else {
				parent := -1
				for j := i - 1; j >= 0; j-- {
					if toc[j].Level == 1 {
						parent = j
						break
					}
				}
				fmt.Fprintf(&sb, "/Parent %d 0 R ", itemObjs[parent])
			}
			// Prev/next siblings: nearest entry of the same level that does
			// not cross a shallower entry.
			for j := i - 1; j >= 0; j-- {
				if toc[j].Level < e.Level {
					break
				}
				if toc[j].Level == e.Level {
					fmt.Fprintf(&sb, "/Prev %d 0 R ", itemObjs[j])
					break
				}
			}
			for j := i + 1; j < len(toc); j++ {
				if toc[j].Level < e.Level {
					break
				}
				if toc[j].Level == e.Level {
					fmt.Fprintf(&sb, "/Next %d 0 R ", itemObjs[j])
					break
				}
			}
			if e.Level == 1 {
				first, last, count := -1, -1, 0
				for j := i + 1; j < len(toc) && toc[j].Level == 2; j++ {
					if first < 0 {
						first = j
					}
					last, count = j, count+1
				}
				if first >= 0 {
					fmt.Fprintf(&sb, "/First %d 0 R /Last %d 0 R /Count %d ", itemObjs[first], itemObjs[last], count)
				}
			}
			fmt.Fprintf(&sb, "/Dest [%d 0 R /Fit] >>", pageObjNum(e.Page))
			f.obj(itemObjs[i], sb.String())
		}
	}

	return f.finish(extra)
}

// writePDFFile generates a PDF into a fresh temp dir and returns its path.
func writePDFFile(t *testing.T, pages []string, info map[string]string, toc []TOCEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gen.pdf")
	if err := os.WriteFile(path, genPDFBytes(pages, info, toc), 0o644); err != nil {
		t.Fatalf("write generated pdf: %v", err)
	}
	return path
}

// writeTextPDF writes a one-line-per-page text PDF (no outline, no info).
func writeTextPDF(t *testing.T, pagesText ...string) string {
	t.Helper()
	pages := make([]string, len(pagesText))
	for i, s := range pagesText {
		pages[i] = textContent(s)
	}
	return writePDFFile(t, pages, nil, nil)
}

// writeOutlinePDF writes the canonical outline fixture: 4 pages,
// "Chapter 1"(p0) with children "Section 1.1"(p1) and "Section 1.2"(p2),
// plus root "Chapter 2"(p3).
func writeOutlinePDF(t *testing.T) string {
	t.Helper()
	pages := []string{
		textContent("Chapter 1 content"),
		textContent("Section 1.1 content"),
		textContent("Section 1.2 content"),
		textContent("Chapter 2 content"),
	}
	toc := []TOCEntry{
		{Title: "Chapter 1", Page: 0, Level: 1},
		{Title: "Section 1.1", Page: 1, Level: 2},
		{Title: "Section 1.2", Page: 2, Level: 2},
		{Title: "Chapter 2", Page: 3, Level: 1},
	}
	return writePDFFile(t, pages, nil, toc)
}
