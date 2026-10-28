// annot_test.go verifies that annotations RENDER: the patched go-fitz
// fork runs fz_run_page (contents + annotations) for image renders, so a
// page's /Annots — here a red /Square — must show up in the pixels.
package pdf

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// annotatedPDF builds a one-page PDF whose page carries a red Square
// annotation over [100 400 300 600] (PDF points, bottom-left origin).
func annotatedPDF(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
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
	content := "1 1 1 rg 0 0 612 792 re f" // white background fill
	write(3, `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Annots [5 0 R] /Resources << >> >>`)
	write(4, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	write(5, "<< /Type /Annot /Subtype /Square /Rect [100 400 300 600] /C [1 0 0] /F 4 /BS << /W 4 >> >>")
	xref := len(buf)
	appendStr("xref\n0 6\n0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		appendStr(fmt.Sprintf("%010d 00000 n \n", offsets[i]))
	}
	appendStr(fmt.Sprintf("trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref))
	path := filepath.Join(dir, "annot.pdf")
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestAnnotationsRender: the square's red border must appear in the
// rendered page (run_page_full), where run_page_contents showed none.
func TestAnnotationsRender(t *testing.T) {
	doc, err := Open(annotatedPDF(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer doc.Close()

	imgCh := make(chan *image.RGBA, 1)
	errCh := make(chan error, 1)
	doc.RenderAsync(RenderReq{Page: 0, DPI: 72, Kind: KindPage},
		func(img *image.RGBA, err error) {
			if err != nil {
				errCh <- err
				return
			}
			imgCh <- img
		})
	select {
	case img := <-imgCh:
		pix, stride := img.Pix, img.Stride
		red := 0
		for y := 0; y < img.Bounds().Dy(); y++ {
			row := y * stride
			for x := 0; x < img.Bounds().Dx(); x++ {
				o := row + x*4
				if pix[o] > 180 && pix[o+1] < 90 && pix[o+2] < 90 {
					red++
				}
			}
		}
		// The border is 4pt wide around a 200x200pt square at 72dpi:
		// hundreds of red pixels. Zero means annotations did not render.
		if red < 100 {
			t.Fatalf("annotation not rendered: only %d red pixels", red)
		}
		t.Logf("annotation rendered: %d red pixels", red)
	case err := <-errCh:
		t.Fatalf("render: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("render did not complete")
	}
}
