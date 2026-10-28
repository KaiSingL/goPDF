// docprops_test.go drives the Document Properties dialog (docprops.go) in
// ui.NewTester: the fields of a real generated PDF (genTestPDF,
// gen_test.go), the Close button, Modal's Escape dismissal, and the
// no-document guard (launcher tab only).
package app

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDocPropsShowsFields(t *testing.T) {
	s := NewAppState()
	path := genTestPDF(t, 2)
	tab, err := s.OpenTab(path)
	if err != nil {
		t.Fatalf("open test pdf: %v", err)
	}
	t.Cleanup(func() { s.CloseTab(tab.ID) }) // closes the Doc: frees the file for TempDir cleanup
	// Diagnostic: what Meta() actually carries on a generated PDF.
	for k, v := range s.Active().Doc.Meta() {
		if v != "" {
			t.Logf("meta %s = %q", k, v)
		}
	}

	s.ShowProperties = true
	tt := ui.NewTester(s.View, 900, 640)
	tt.Frame()

	for _, want := range []string{
		"Document Properties",
		"Pages",
		filepath.Base(path), // the header's file name
		"612 × 792 pt",      // genTestPDF pages are US Letter
	} {
		if !tt.HasText(want) {
			t.Fatalf("properties dialog missing %q: %q", want, tt.Texts())
		}
	}

	// The Close button clears the flag.
	if err := tt.Click("Close"); err != nil {
		t.Fatalf("click Close: %v", err)
	}
	tt.Frame()
	if s.ShowProperties {
		t.Errorf("Close left the dialog open")
	}

	// Reopened, Escape dismisses the modal (ui.Modal's own handling).
	s.ShowProperties = true
	tt.Frame()
	if !tt.HasText("Document Properties") {
		t.Fatalf("dialog did not reopen: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if s.ShowProperties {
		t.Errorf("Escape left the dialog open")
	}
}

func TestDocPropsWithoutDocument(t *testing.T) {
	s := NewAppState()
	s.NewEmptyTab() // launcher tab only: no document behind it
	s.ShowProperties = true
	tt := ui.NewTester(s.View, 900, 640)
	tt.Frame()
	tt.Frame() // an extra frame after the guard has dropped the flag
	if tt.HasText("Document Properties") {
		t.Errorf("properties dialog built without a document: %q", tt.Texts())
	}
	if s.ShowProperties {
		t.Errorf("flag not cleared without a document")
	}
}
