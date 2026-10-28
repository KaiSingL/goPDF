// gen_cropped_test.go builds a PDF whose MediaBox starts at (50,50), the
// shape that shifts every page-space coordinate — highlights, spans — by
// the box origin when a reader forgets to subtract it.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// genCroppedTestPDF writes a one-page PDF, MediaBox [50 50 662 842] (US
// Letter offset by 50,50), with the word "cropmark" at text position
// (72,750) in user space. Returns the path.
func genCroppedTestPDF(t *testing.T) string {
	t.Helper()
	var buf []byte
	appendStr := func(s string) { buf = append(buf, s...) }
	offsets := map[int]int{}
	write := func(num int, body string) {
		offsets[num] = len(buf)
		appendStr(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", num, body))
	}
	appendStr("%PDF-1.5\n")
	write(1, "<< /Type /Catalog /Pages 2 0 R >>")
	write(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	content := "BT /F1 24 Tf 72 750 Td (cropmark) Tj ET"
	write(3, "<< /Type /Page /Parent 2 0 R /MediaBox [50 50 662 842] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")
	write(4, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	write(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	xref := len(buf)
	appendStr("xref\n0 6\n0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		appendStr(fmt.Sprintf("%010d 00000 n \n", offsets[i]))
	}
	appendStr(fmt.Sprintf("trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref))

	dir := t.TempDir()
	path := filepath.Join(dir, "cropped.pdf")
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
