// ui_polish_test.go locks in the chrome polish round: the sidebar hides
// when no document is open, and the outline highlight picks the section
// the reader is actually in.
package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"gopdf/internal/pdf"
)

// TestSidebarHiddenWithoutDocument: the sidebar (and its panel switch)
// only show while a document is open.
func TestSidebarHiddenWithoutDocument(t *testing.T) {
	s := NewAppState() // ShowSidebar defaults true, no tabs
	tt := ui.NewTester(s.View, 1200, 800)
	if tt.HasText("Thumbnails") || tt.HasText("No document open") {
		t.Errorf("sidebar content visible without a document: %q", tt.Texts())
	}

	path := genTestPDF(t, 3)
	tab, err := s.OpenTab(path)
	if err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Thumbnails") {
		t.Errorf("sidebar switch missing with a document open: %q", tt.Texts())
	}

	s.CloseTab(tab.ID)
	tt.Frame()
	if tt.HasText("Thumbnails") {
		t.Errorf("sidebar switch visible after closing the last tab: %q", tt.Texts())
	}
}

// TestCurrentOutlinePath: the highlighted outline entry is the last entry
// in document order whose page is at or before the current page; dead
// entries (-1) never count.
func TestCurrentOutlinePath(t *testing.T) {
	tt := &Tab{CurPage: 0, Outline: []pdf.OutlineNode{
		{Title: "Chapter 1", Page: 0, Children: []pdf.OutlineNode{
			{Title: "Section 1.1", Page: 0},
			{Title: "Section 1.2", Page: 2},
		}},
		{Title: "Chapter 2", Page: 5, Children: []pdf.OutlineNode{
			{Title: "Dead link", Page: -1},
			{Title: "Section 2.1", Page: 6},
		}},
	}}
	cases := []struct {
		page int
		want string
	}{
		{0, "o/0/0"},  // inside Section 1.1
		{1, "o/0/0"},  // still Section 1.1 until 1.2 starts
		{2, "o/0/1"},  // Section 1.2
		{4, "o/0/1"},  // last section that started
		{5, "o/1"},    // Chapter 2 (before any live child)
		{6, "o/1/1"},  // Section 2.1 (dead sibling skipped)
		{99, "o/1/1"}, // past the end: last section
	}
	for _, tc := range cases {
		tt.CurPage = tc.page
		if got := currentOutlinePath(tt); got != tc.want {
			t.Errorf("CurPage %d: currentOutlinePath = %q, want %q", tc.page, got, tc.want)
		}
	}
}
