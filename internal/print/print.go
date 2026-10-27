// Package print implements Windows printing for GoPDF: the print dialog
// (comdlg32 PrintDlgW with a returned device context), page rendering
// through the existing pdf pipeline at the printer's resolution, and GDI
// output of each page (StretchDIBits with a top-down 32bpp DIB).
//
// MyGo ships no printing API (verified against the pinned v0.6.4 source),
// so this is pure syscall — no cgo. The dialog blocks, so Print runs on a
// goroutine of its own.
package print

import (
	"fmt"
	"image"
	"strings"
	"sync"

	"gopdf/internal/pdf"
)

// Options describe one print request.
type Options struct {
	Doc          *pdf.Doc
	From, To     int // inclusive page range, 0-based
	Copies       int // handled by the driver via the dialog
	DocumentName string
}

// busy prevents two dialogs at once (menu + Ctrl+P racing).
var busy sync.Mutex

// Print shows the print dialog and prints the range the user chose. It
// returns (printed, err); printed=false with a nil error means the user
// canceled. Call from a goroutine — the dialog blocks.
func Print(doc *pdf.Doc, name string) (bool, error) {
	if doc == nil || doc.NumPages() == 0 {
		return false, fmt.Errorf("print: nothing to print")
	}
	busy.Lock()
	defer busy.Unlock()

	dlg, hdc, ok, err := showPrintDialog(doc.NumPages(), name)
	if err != nil {
		return false, err
	}
	if !ok || hdc == 0 {
		return false, nil // canceled
	}
	defer deleteDC(hdc)

	opts := Options{
		Doc:          doc,
		From:         dlg.fromPage,
		To:           dlg.toPage,
		Copies:       dlg.copies,
		DocumentName: name,
	}
	if err := printPages(hdc, &opts); err != nil {
		return false, err
	}
	return true, nil
}

// printPages renders each page in the range fit into the printer's
// printable area and sends it as one DIB.
func printPages(hdc uintptr, o *Options) error {
	printW, printH := deviceCaps(hdc, HORZRES), deviceCaps(hdc, VERTRES)
	if printW <= 0 || printH <= 0 {
		return fmt.Errorf("print: printer reports no printable area")
	}

	if err := startDoc(hdc, o.DocumentName); err != nil {
		return err
	}
	for page := o.From; page <= o.To; page++ {
		pw, ph := o.Doc.PageSize(page) // points, unrotated
		if pw <= 0 || ph <= 0 {
			continue
		}
		// Fit the page into the printable area, keeping aspect, centered.
		scale := min(float64(printW)/pw, float64(printH)/ph)
		dstW := int(pw * scale)
		dstH := int(ph * scale)
		dstX := (printW - dstW) / 2
		dstY := (printH - dstH) / 2

		img, err := renderSync(o.Doc, page, 72*scale)
		if err != nil {
			return fmt.Errorf("print: page %d: %w", page+1, err)
		}
		if err := startPage(hdc); err != nil {
			return err
		}
		if err := stretchDIBits(hdc, img, dstX, dstY, dstW, dstH); err != nil {
			_ = endPage(hdc)
			return err
		}
		if err := endPage(hdc); err != nil {
			return err
		}
	}
	return endDoc(hdc)
}

// renderSync renders one page through the doc's worker and waits for it.
func renderSync(doc *pdf.Doc, page int, dpi float64) (*image.RGBA, error) {
	ch := make(chan struct{})
	var img *image.RGBA
	var err error
	doc.RenderAsync(pdf.RenderReq{Page: page, DPI: int(dpi), Kind: pdf.KindPage},
		func(im *image.RGBA, e error) {
			img, err = im, e
			close(ch)
		})
	<-ch
	return img, err
}

// swapToBGRA reorders fitz's RGBA pixels into the BGRA a 32bpp BI_RGB DIB
// expects, and returns the underlying byte buffer.
func swapToBGRA(img *image.RGBA) []byte {
	b := img.Pix
	for i := 0; i < len(b); i += 4 {
		b[i], b[i+2] = b[i+2], b[i] // R <-> B
	}
	return b
}

// pageLabel builds the document name shown in the printer queue.
func pageLabel(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "GoPDF Document"
	}
	return name
}
