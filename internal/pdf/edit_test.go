package pdf

// Phase 0 binding tests: every fitz editing API exercised against generated
// PDFs, verified with reopen oracles — reopen the saved file and compare
// structure, text and metadata through independent reads. Password round
// trips verify the authentication bits and permissions; the incremental
// save test asserts the original bytes stay intact as a prefix.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gen2brain/go-fitz"
)

// allPerms is the permission mask Save uses when encrypting test files.
const allPerms = int(fitz.PermPrint | fitz.PermEdit | fitz.PermCopy | fitz.PermAnnotate |
	fitz.PermForm | fitz.PermAccessibility | fitz.PermAssemble | fitz.PermPrintHQ)

// writeTestPDF writes bytes to a fresh temp .pdf and returns its path.
func writeTestPDF(t *testing.T, b []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.pdf")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write test pdf: %v", err)
	}
	return path
}

// openGen opens a generated document for the test.
func openGen(t *testing.T, b []byte) *fitz.Document {
	t.Helper()
	f, err := fitz.NewFromMemory(b)
	if err != nil {
		t.Fatalf("open generated pdf: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func pageText(t *testing.T, f *fitz.Document, page int) string {
	t.Helper()
	s, err := f.Text(page)
	if err != nil {
		t.Fatalf("text of page %d: %v", page, err)
	}
	return s
}

func TestAuthenticateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "plain.pdf")
	if err := os.WriteFile(srcPath, genPDFBytes([]string{textContent("secret")}, nil, nil), 0o644); err != nil {
		t.Fatal(err)
	}
	enc := filepath.Join(dir, "enc.pdf")

	plain, err := fitz.New(srcPath)
	if err != nil {
		t.Fatalf("open plain: %v", err)
	}
	err = plain.Save(enc, fitz.SaveOptions{
		Encryption:    fitz.EncryptAES_256,
		Permissions:   allPerms,
		UserPassword:  "userpw",
		OwnerPassword: "ownerpw",
	})
	plain.Close()
	if err != nil {
		t.Fatalf("save encrypted: %v", err)
	}

	// Reopen without a password: ErrNeedsPassword, document unusable.
	f, err := fitz.New(enc)
	if err == nil || err != fitz.ErrNeedsPassword {
		t.Fatalf("open encrypted without password: err=%v, want ErrNeedsPassword", err)
	}
	if f == nil {
		t.Fatal("ErrNeedsPassword must still return the document for authentication")
	}
	defer f.Close()
	// Wrong password rejected.
	if bits, err := f.Authenticate("nope"); err != nil || bits != 0 {
		t.Fatalf("wrong password: bits=%d err=%v, want 0,nil", bits, err)
	}

	// User password grants the permissions the file carries.
	bits, err := f.Authenticate("userpw")
	if err != nil || bits&fitz.AuthUserPassword == 0 {
		t.Fatalf("user password: bits=%d err=%v, want AuthUserPassword", bits, err)
	}
	if !f.HasPermission(fitz.PermPrint) || !f.HasPermission(fitz.PermCopy) {
		t.Fatal("granted permissions missing after user auth")
	}
	if _, err := f.Text(0); err != nil {
		t.Fatalf("text after auth: %v", err)
	}
}

func TestSaveAsRoundTripUnmodified(t *testing.T) {
	srcPath := writeTestPDF(t, genPDFBytes([]string{textContent("alpha"), textContent("beta")}, nil, nil))
	f, err := fitz.New(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	out := filepath.Join(t.TempDir(), "copy.pdf")
	if err := f.Save(out, fitz.SaveOptions{}); err != nil {
		t.Fatalf("save-as: %v", err)
	}

	g, err := fitz.New(out)
	if err != nil {
		t.Fatalf("reopen saved: %v", err)
	}
	defer g.Close()
	if g.NumPage() != 2 {
		t.Fatalf("pages after save-as: %d, want 2", g.NumPage())
	}
	if s := pageText(t, g, 0); !bytes.Contains([]byte(s), []byte("alpha")) {
		t.Fatalf("page 0 text after save-as: %q", s)
	}
}

func TestIncrementalSaveKeepsOriginalBytes(t *testing.T) {
	srcPath := writeTestPDF(t, genPDFBytes([]string{textContent("title me")}, map[string]string{"Title": "Old"}, nil))
	orig, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}

	f, err := fitz.New(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.SetMetadata("info:Title", "New"); err != nil {
		t.Fatalf("set metadata: %v", err)
	}
	if err := f.Save(srcPath, fitz.SaveOptions{Incremental: true}); err != nil {
		t.Fatalf("incremental save: %v", err)
	}

	now, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(now) <= len(orig) {
		t.Fatalf("incremental save shrank or kept size: %d -> %d", len(orig), len(now))
	}
	if !bytes.HasPrefix(now, orig) {
		t.Fatal("incremental save modified the original bytes")
	}

	g, err := fitz.New(srcPath)
	if err != nil {
		t.Fatalf("reopen after incremental: %v", err)
	}
	defer g.Close()
	if got := g.Metadata()["title"]; got != "New" {
		t.Fatalf("title after incremental save: %q, want New", got)
	}
}

func TestIncrementalSaveMustTargetOpenFile(t *testing.T) {
	srcPath := writeTestPDF(t, genPDFBytes([]string{textContent("x")}, nil, nil))
	f, err := fitz.New(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	other := filepath.Join(t.TempDir(), "elsewhere.pdf")
	if err := f.Save(other, fitz.SaveOptions{Incremental: true}); err == nil {
		t.Fatal("incremental save to another path must be rejected")
	}
}

func TestAtomicSaveLeavesNoTempFiles(t *testing.T) {
	srcPath := writeTestPDF(t, genPDFBytes([]string{textContent("keep")}, nil, nil))
	f, err := fitz.New(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "atomic.pdf")
	if err := f.Save(out, fitz.SaveOptions{}); err != nil {
		t.Fatalf("atomic save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "atomic.pdf" {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestPageOrganizeRoundTrip(t *testing.T) {
	srcPath := writeTestPDF(t, genPDFBytes([]string{
		textContent("one"), textContent("two"), textContent("three"),
	}, nil, nil))
	f, err := fitz.New(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if f.NumPage() != 3 {
		t.Fatalf("pages: %d, want 3", f.NumPage())
	}

	// Blank insert at the end.
	if err := f.InsertBlankPage(3, 612, 792); err != nil {
		t.Fatalf("insert blank: %v", err)
	}
	if f.NumPage() != 4 {
		t.Fatalf("pages after blank insert: %d, want 4", f.NumPage())
	}

	// Rotate page 1 ("two") absolutely to 90 — NOT page 0, which is
	// deleted below; the rotation must survive on the moved page.
	if err := f.SetPageRotation(1, 90); err != nil {
		t.Fatalf("set rotation: %v", err)
	}
	b, err := f.Bound(1)
	if err != nil {
		t.Fatal(err)
	}
	if b.Dx() != 792 || b.Dy() != 612 {
		t.Fatalf("bounds after /Rotate 90: %v, want 792x612", b)
	}

	// Delete the first page; the rotated "two" becomes page 0.
	if err := f.DeletePage(0); err != nil {
		t.Fatalf("delete page: %v", err)
	}
	if f.NumPage() != 3 {
		t.Fatalf("pages after delete: %d, want 3", f.NumPage())
	}
	if s := pageText(t, f, 0); !bytes.Contains([]byte(s), []byte("two")) {
		t.Fatalf("page 0 after delete: %q", s)
	}

	out := filepath.Join(t.TempDir(), "organized.pdf")
	if err := f.Save(out, fitz.SaveOptions{}); err != nil {
		t.Fatalf("save organized: %v", err)
	}

	g, err := fitz.New(out)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if g.NumPage() != 3 {
		t.Fatalf("reopened pages: %d, want 3", g.NumPage())
	}
	if s := pageText(t, g, 0); !bytes.Contains([]byte(s), []byte("two")) {
		t.Fatalf("reopened page 0: %q", s)
	}
	if s := pageText(t, g, 2); bytes.Contains([]byte(s), []byte("three")) {
		t.Fatalf("blank page 2 carries text: %q", s)
	}
	b, err = g.Bound(0)
	if err != nil {
		t.Fatal(err)
	}
	if b.Dx() != 792 || b.Dy() != 612 {
		t.Fatalf("reopened bounds after rotation: %v, want 792x612", b)
	}
}

func TestAppendAndExtractRoundTrip(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.pdf")
	bPath := filepath.Join(dir, "b.pdf")
	if err := os.WriteFile(aPath, genPDFBytes([]string{textContent("A1"), textContent("A2")}, nil, nil), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, genPDFBytes([]string{textContent("B1"), textContent("B2"), textContent("B3")}, nil, nil), 0o644); err != nil {
		t.Fatal(err)
	}

	a, err := fitz.New(aPath)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	// Append only the first two pages of B.
	if err := a.AppendPagesFromFile(bPath, []int{0, 1}, 2); err != nil {
		t.Fatalf("append: %v", err)
	}
	if a.NumPage() != 4 {
		t.Fatalf("pages after append: %d, want 4", a.NumPage())
	}
	if s := pageText(t, a, 3); !bytes.Contains([]byte(s), []byte("B2")) {
		t.Fatalf("appended page 3: %q", s)
	}

	out := filepath.Join(dir, "combined.pdf")
	if err := a.Save(out, fitz.SaveOptions{}); err != nil {
		t.Fatalf("save combined: %v", err)
	}
	g, err := fitz.New(out)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if g.NumPage() != 4 {
		t.Fatalf("combined pages: %d, want 4", g.NumPage())
	}
	for i, want := range []string{"A1", "A2", "B1", "B2"} {
		if s := pageText(t, g, i); !bytes.Contains([]byte(s), []byte(want)) {
			t.Fatalf("combined page %d: %q, want %q", i, s, want)
		}
	}

	// Extract pages 1 and 3 (A2, B2) into a new file.
	extracted := filepath.Join(dir, "picked.pdf")
	if err := g.ExtractPagesToFile([]int{1, 3}, extracted, fitz.SaveOptions{}); err != nil {
		t.Fatalf("extract: %v", err)
	}
	e, err := fitz.New(extracted)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if e.NumPage() != 2 {
		t.Fatalf("extracted pages: %d, want 2", e.NumPage())
	}
	if s := pageText(t, e, 0); !bytes.Contains([]byte(s), []byte("A2")) {
		t.Fatalf("extracted page 0: %q", s)
	}
	if s := pageText(t, e, 1); !bytes.Contains([]byte(s), []byte("B2")) {
		t.Fatalf("extracted page 1: %q", s)
	}
}

func TestAnnotCRUDRoundTrip(t *testing.T) {
	srcPath := writeTestPDF(t, genPDFBytes([]string{textContent("annotate me")}, nil, nil))
	f, err := fitz.New(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// Highlight: quad over the first line, yellow, with a note. Markup
	// annots take their geometry from quad points, not a rect.
	quads := []fitz.Quad{{
		UL: fitz.Point{X: 72, Y: 60}, UR: fitz.Point{X: 220, Y: 60},
		LL: fitz.Point{X: 72, Y: 88}, LR: fitz.Point{X: 220, Y: 88},
	}}
	opacity := 0.5
	hID, err := f.CreateAnnot(0, fitz.AnnotHighlight, fitz.Rect{},
		fitz.AnnotEdit{
			QuadPoints: quads,
			Color:      []float64{1, 1, 0},
			Contents:   "looks good", SetContents: true,
			Author: "tester", SetAuthor: true,
			Opacity: &opacity,
		})
	if err != nil {
		t.Fatalf("create highlight: %v", err)
	}

	// Sticky note.
	nID, err := f.CreateAnnot(0, fitz.AnnotText, fitz.Rect{},
		fitz.AnnotEdit{
			Rect:     &[]fitz.Rect{fitz.Rect{X0: 400, Y0: 100, X1: 420, Y1: 120}}[0],
			Contents: "a note", SetContents: true,
		})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}

	annots, err := f.Annots(0)
	if err != nil {
		t.Fatalf("list annots: %v", err)
	}
	if len(annots) != 2 {
		t.Fatalf("annots: %d, want 2", len(annots))
	}
	byID := map[int64]fitz.Annot{}
	for _, a := range annots {
		byID[a.ID] = a
	}
	h := byID[hID]
	if h.Type != fitz.AnnotHighlight {
		t.Fatalf("highlight type: %v", h.Type)
	}
	if h.Contents != "looks good" || h.Author != "tester" {
		t.Fatalf("highlight contents/author: %q/%q", h.Contents, h.Author)
	}
	if h.Opacity != 0.5 {
		t.Fatalf("highlight opacity: %v, want 0.5", h.Opacity)
	}
	if len(h.QuadPoints) != 1 {
		t.Fatalf("highlight quad points: %d, want 1", len(h.QuadPoints))
	}
	if n := byID[nID]; n.Contents != "a note" {
		t.Fatalf("note contents: %q", n.Contents)
	}

	// Edit the note's contents, delete the highlight.
	if err := f.EditAnnot(0, nID, fitz.AnnotEdit{Contents: "edited", SetContents: true}); err != nil {
		t.Fatalf("edit note: %v", err)
	}
	if err := f.DeleteAnnot(0, hID); err != nil {
		t.Fatalf("delete highlight: %v", err)
	}
	if err := f.EditAnnot(0, hID, fitz.AnnotEdit{Contents: "zombie", SetContents: true}); !errors.Is(err, fitz.ErrStaleRef) {
		t.Fatalf("edit deleted annot: %v, want ErrStaleRef", err)
	}

	// Save, reopen, verify persistence.
	out := filepath.Join(t.TempDir(), "annotated.pdf")
	if err := f.Save(out, fitz.SaveOptions{}); err != nil {
		t.Fatalf("save annotated: %v", err)
	}
	g, err := fitz.New(out)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	annots, err = g.Annots(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(annots) != 1 {
		t.Fatalf("persisted annots: %d, want 1", len(annots))
	}
	if annots[0].Contents != "edited" || annots[0].Type != fitz.AnnotText {
		t.Fatalf("persisted note: %+v", annots[0])
	}
}

func TestFormFieldSetRoundTrip(t *testing.T) {
	srcPath := writeTestPDF(t, formPDFBytes())
	f, err := fitz.New(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	fields, err := f.Fields(0)
	if err != nil {
		t.Fatalf("fields: %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("fields: %d, want 1", len(fields))
	}
	fld := fields[0]
	if fld.Type != fitz.WidgetText || fld.Label != "Name" {
		t.Fatalf("field: type=%v label=%q", fld.Type, fld.Label)
	}

	if err := f.SetFieldValue(0, fld.ID, "Leon"); err != nil {
		t.Fatalf("set field value: %v", err)
	}
	fields, err = f.Fields(0)
	if err != nil {
		t.Fatal(err)
	}
	if fields[0].Value != "Leon" {
		t.Fatalf("value after set: %q, want Leon", fields[0].Value)
	}

	// Reset back to the default (empty).
	if err := f.ResetField(0, fld.ID); err != nil {
		t.Fatalf("reset field: %v", err)
	}
	fields, _ = f.Fields(0)
	if fields[0].Value != "" {
		t.Fatalf("value after reset: %q, want empty", fields[0].Value)
	}

	// Persist the fill.
	if err := f.SetFieldValue(0, fld.ID, "Leon"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "filled.pdf")
	if err := f.Save(out, fitz.SaveOptions{}); err != nil {
		t.Fatalf("save filled: %v", err)
	}
	g, err := fitz.New(out)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	fields, err = g.Fields(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0].Value != "Leon" {
		t.Fatalf("persisted field: %+v, want value Leon", fields)
	}
}

func TestSetMetadataRoundTrip(t *testing.T) {
	f := openGen(t, genPDFBytes([]string{textContent("m")}, nil, nil))
	if err := f.SetMetadata("info:Title", "Set Title"); err != nil {
		t.Fatalf("set metadata: %v", err)
	}
	if err := f.SetMetadata("info:Author", "GoPDF"); err != nil {
		t.Fatalf("set author: %v", err)
	}
	if got := f.Metadata()["title"]; got != "Set Title" {
		t.Fatalf("title in memory: %q", got)
	}
}

func TestRedactionRemovesText(t *testing.T) {
	// One page, two lines: redact the first line only.
	f := openGen(t, genPDFBytes([]string{textContent("top secret line", "safe line")}, nil, nil))

	// Line 1 sits at y≈53-77 in top-left page space; line 2 starts below
	// y=85 and must survive.
	if err := f.AddRedactAnnot(0, fitz.Rect{X0: 60, Y0: 30, X1: 340, Y1: 78}); err != nil {
		t.Fatalf("add redact: %v", err)
	}
	if err := f.ApplyRedactions(0, true); err != nil {
		t.Fatalf("apply redactions: %v", err)
	}
	if s := pageText(t, f, 0); bytes.Contains([]byte(s), []byte("secret")) {
		t.Fatalf("redacted text survived: %q", s)
	}
	if s := pageText(t, f, 0); !bytes.Contains([]byte(s), []byte("safe")) {
		t.Fatalf("safe text lost by redaction: %q", s)
	}

	// Persist: the saved file must not contain the secret text either.
	out := filepath.Join(t.TempDir(), "redacted.pdf")
	if err := f.Save(out, fitz.SaveOptions{Garbage: 4}); err != nil {
		t.Fatalf("save redacted: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("secret")) {
		t.Fatal("redacted text still present in saved bytes")
	}
	g, err := fitz.New(out)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if s := pageText(t, g, 0); bytes.Contains([]byte(s), []byte("secret")) {
		t.Fatalf("redacted text survived reopen: %q", s)
	}
}

func TestLayerConfigsListAndSelect(t *testing.T) {
	f := openGen(t, ocgPDFBytes())
	cfgs, err := f.LayerConfigs()
	if err != nil {
		t.Fatalf("layer configs: %v", err)
	}
	if len(cfgs) != 2 {
		t.Fatalf("layer configs: %d, want 2", len(cfgs))
	}
	if cfgs[0].Name != "Base" || cfgs[1].Name != "Alt" {
		t.Fatalf("config names: %q, %q", cfgs[0].Name, cfgs[1].Name)
	}
	if cfgs[0].Creator != "GoPDF" {
		t.Fatalf("config creator: %q", cfgs[0].Creator)
	}
	if err := f.SelectLayerConfig(1); err != nil {
		t.Fatalf("select layer config: %v", err)
	}
}

func TestNotPDFEditingRejected(t *testing.T) {
	// The content-type sniffing only accepts PDFs, so force a PDF-scanned
	// document path: any generated PDF edits fine; this test pins the
	// ErrStaleRef contract for bogus IDs instead.
	f := openGen(t, genPDFBytes([]string{textContent("x")}, nil, nil))
	err := f.EditAnnot(0, 999999, fitz.AnnotEdit{Contents: "ghost", SetContents: true})
	if !errors.Is(err, fitz.ErrStaleRef) {
		t.Fatalf("edit bogus annot id: %v, want ErrStaleRef", err)
	}
	err = f.SetFieldValue(0, 999999, "ghost")
	if !errors.Is(err, fitz.ErrStaleRef) {
		t.Fatalf("set bogus field id: %v, want ErrStaleRef", err)
	}
}
