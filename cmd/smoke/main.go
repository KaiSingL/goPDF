// Command smoke verifies the cgo + go-fitz + mingw toolchain end to end:
// it generates a small two-page PDF, opens it with MuPDF, renders a page,
// extracts text, and prints timings. It doubles as a perf probe later.
package main

import (
	"fmt"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/gen2brain/go-fitz"
)

// minimalPDF builds a valid little PDF with one line of text per page.
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

func main() {
	dir, err := os.MkdirTemp("", "smoke")
	if err != nil {
		log.Fatal(err)
	}
	path := filepath.Join(dir, "smoke.pdf")
	if err := os.WriteFile(path, minimalPDF([]string{"Hello modern-pdf", "Second page: The quick brown fox"}), 0o644); err != nil {
		log.Fatal(err)
	}

	t0 := time.Now()
	doc, err := fitz.New(path)
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	fmt.Printf("open:    %v (%d pages)\n", time.Since(t0), doc.NumPage())

	t1 := time.Now()
	img, err := doc.ImageDPI(0, 150)
	if err != nil {
		log.Fatalf("render: %v", err)
	}
	fmt.Printf("render:  %v (%dx%d px)\n", time.Since(t1), img.Rect.Dx(), img.Rect.Dy())

	t2 := time.Now()
	text, err := doc.Text(0)
	if err != nil {
		log.Fatalf("text: %v", err)
	}
	fmt.Printf("text:    %v %q\n", time.Since(t2), text)

	toc, err := doc.ToC()
	if err != nil && err != fitz.ErrLoadOutline {
		log.Fatalf("toc: %v", err)
	}
	// NB: fitz returns ErrLoadOutline for documents that simply have no
	// outline; treat it as an empty table of contents.
	fmt.Printf("toc:     %d entries (err=%v)\n", len(toc), err)

	out := filepath.Join("build", "smoke-page0.png")
	if err := os.MkdirAll("build", 0o755); err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(out)
	if err != nil {
		log.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
	f.Close()
	fmt.Println("wrote", out)

	if err := doc.Close(); err != nil {
		log.Fatal(err)
	}
	fmt.Println("SMOKE OK")
}
