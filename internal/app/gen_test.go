// gen_test.go is a test-only helper: it builds a tiny valid PDF with one
// line of text per page (adapted from cmd/smoke/main.go) so the viewer
// tests exercise the real cgo MuPDF pipeline headlessly. Other tests in
// package app reuse genTestPDF.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// minimalPDF builds a valid little PDF with one line of text per page, on
// US Letter pages (612×792 pt).
func minimalPDF(pagesText []string) []byte {
	var buf []byte
	appendStr := func(s string) { buf = append(buf, s...) }
	offsets := map[int]int{}

	appendStr("%PDF-1.4\n")

	// Object 1: catalog. Object 2: pages tree (kids filled later).
	kids := ""
	for i := range pagesText {
		kids += fmt.Sprintf("%d 0 R ", 3+2*i)
	}
	write := func(num int, body string) {
		offsets[num] = len(buf)
		appendStr(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", num, body))
	}

	write(1, "<< /Type /Catalog /Pages 2 0 R >>")
	write(2, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids, len(pagesText)))
	for i, text := range pagesText {
		pageNum := 3 + 2*i
		contNum := pageNum + 1
		content := fmt.Sprintf("BT /F1 24 Tf 72 720 Td (%s) Tj ET", text)
		write(pageNum, fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /Font << /F1 100 0 R >> >> >>", contNum))
		write(contNum, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}
	write(100, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	xrefAt := len(buf)
	appendStr(fmt.Sprintf("xref\n0 %d\n", 101))
	appendStr("0000000000 65535 f \n")
	for i := 1; i <= 100; i++ {
		if off, ok := offsets[i]; ok {
			appendStr(fmt.Sprintf("%010d 00000 n \n", off))
		} else {
			appendStr("0000000000 65535 f \n")
		}
	}
	appendStr(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", 101, xrefAt))
	return buf
}

// genTestPDF writes a generated PDF to the test's temp dir and returns its
// path. Each page gets one line of text: page-1, page-2, ...
func genTestPDF(t *testing.T, pages int) string {
	t.Helper()
	texts := make([]string, pages)
	for i := range texts {
		texts[i] = fmt.Sprintf("page-%d", i+1)
	}
	path := filepath.Join(t.TempDir(), "generated.pdf")
	if err := os.WriteFile(path, minimalPDF(texts), 0o644); err != nil {
		t.Fatalf("write test pdf: %v", err)
	}
	return path
}
