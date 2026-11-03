package pdf

// Test-only generators for PDFs with interactive features: an AcroForm text
// field and optional-content (layer) configurations. Same hand-written PDF
// 1.4 style as genPDFBytes.

import "fmt"

// formPDFBytes builds a one-page PDF with a single text field named
// "Name", empty value, on the AcroForm. The widget sits at
// (72,700)-(300,720) in user space.
func formPDFBytes() []byte {
	f := &pdfFile{}
	// 1 catalog, 2 pages, 3 page, 4 contents, 5 font, 6 widget, 7 acroform
	f.obj(1, "<< /Type /Catalog /Pages 2 0 R /AcroForm 7 0 R >>")
	f.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	content := textContent("Form test page")
	f.obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R "+
		"/Resources << /Font << /F1 5 0 R >> >> /Annots [6 0 R] >>")
	f.obj(4, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	f.obj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	f.obj(6, "<< /Type /Annot /Subtype /Widget /FT /Tx /T (Name) /V () "+
		"/Rect [72 700 300 720] /F 4 /P 3 0 R >>")
	f.obj(7, "<< /Fields [6 0 R] /DR << /Font << /Helv 5 0 R >> >> /DA (/Helv 0 Tf 0 g) >>")
	return f.finish("")
}

// ocgPDFBytes builds a one-page PDF with one optional-content group and two
// named layer configurations: "Base" (layer on) and "Alt" (layer off).
func ocgPDFBytes() []byte {
	f := &pdfFile{}
	// 1 catalog, 2 pages, 3 page, 4 contents, 5 font, 6 ocg, 7 config D,
	// 8 config Alt
	f.obj(1, "<< /Type /Catalog /Pages 2 0 R /OCProperties << /OCs [6 0 R] "+
		"/D 7 0 R /Configs [7 0 R 8 0 R] >> >>")
	f.obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	content := "/OC /OC1 BDC " + textContent("Layered content") + " EMC"
	f.obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R "+
		"/Resources << /Font << /F1 5 0 R >> /Properties << /OC1 6 0 R >> >> >>")
	f.obj(4, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	f.obj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	f.obj(6, "<< /Type /OCG /Name (LayerA) >>")
	f.obj(7, "<< /Name (Base) /Creator (GoPDF) /ON [6 0 R] >>")
	f.obj(8, "<< /Name (Alt) /Creator (GoPDF) /OFF [6 0 R] >>")
	return f.finish("")
}
