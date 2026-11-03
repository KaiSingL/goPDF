//go:build !cgo || nocgo

package fitz

// purego builds cannot reach MuPDF's pdf_* layer (the editing surface
// needs the static MuPDF library that only cgo links). Every editing
// method returns ErrNeedsCGO; the read-only API keeps working.

import "fmt"

func editingUnsupported(op string) error {
	return fmt.Errorf("fitz: %s: %w", op, ErrNeedsCGO)
}

func (f *Document) NeedsPassword() bool {
	f.mtx.Lock()
	defer f.mtx.Unlock()
	return int(fzNeedsPassword(f.ctx, f.doc)) != 0
}

func (f *Document) Authenticate(password string) (int, error) {
	return 0, editingUnsupported("authenticate")
}

func (f *Document) HasPermission(p Permission) bool { return false }
func (f *Document) Save(path string, opts SaveOptions) error {
	return editingUnsupported("save")
}
func (f *Document) InsertBlankPage(at int, w, h float64) error {
	return editingUnsupported("insert blank page")
}
func (f *Document) DeletePage(n int) error               { return editingUnsupported("delete page") }
func (f *Document) DeletePageRange(start, end int) error { return editingUnsupported("delete pages") }
func (f *Document) SetPageRotation(n, rot int) error     { return editingUnsupported("rotate page") }
func (f *Document) AppendPagesFromFile(srcPath string, pages []int, at int) error {
	return editingUnsupported("append pages")
}
func (f *Document) ExtractPagesToFile(pages []int, path string, opts SaveOptions) error {
	return editingUnsupported("extract pages")
}
func (f *Document) Annots(pageNumber int) ([]Annot, error) { return nil, editingUnsupported("annots") }
func (f *Document) CreateAnnot(pageNumber int, t AnnotType, rect Rect, e AnnotEdit) (int64, error) {
	return 0, editingUnsupported("create annot")
}
func (f *Document) EditAnnot(pageNumber int, id int64, e AnnotEdit) error {
	return editingUnsupported("edit annot")
}
func (f *Document) DeleteAnnot(pageNumber int, id int64) error {
	return editingUnsupported("delete annot")
}
func (f *Document) RefreshAnnots(pageNumber int) error     { return editingUnsupported("refresh annots") }
func (f *Document) Fields(pageNumber int) ([]Field, error) { return nil, editingUnsupported("fields") }
func (f *Document) SetFieldValue(pageNumber int, id int64, value string) error {
	return editingUnsupported("set field value")
}
func (f *Document) ResetField(pageNumber int, id int64) error {
	return editingUnsupported("reset field")
}
func (f *Document) SetMetadata(key, value string) error { return editingUnsupported("set metadata") }
func (f *Document) AddRedactAnnot(pageNumber int, rect Rect) error {
	return editingUnsupported("add redact annot")
}
func (f *Document) ApplyRedactions(pageNumber int, blackBoxes bool) error {
	return editingUnsupported("apply redactions")
}
func (f *Document) LayerConfigs() ([]LayerConfig, error) {
	return nil, editingUnsupported("layer configs")
}
func (f *Document) SelectLayerConfig(i int) error { return editingUnsupported("select layer config") }
