// toolbar_test.go covers the four-section toolbar layout: the new
// page-turn section (buttons + "current / total" indicator) and the
// search button's behavior in its new right-side home.
package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestToolbarPageSection: the indicator follows the page, Previous page
// is a no-op on the first page, Next page a no-op on the last.
func TestToolbarPageSection(t *testing.T) {
	s := chromeState(1) // one tab, 5 pages, no Doc: pure UI
	tab := s.Tabs[0]
	tt := ui.NewTester(toolbarView(s), 1200, 120)

	if !tt.HasText("1 / 5") {
		t.Fatalf("page indicator missing; texts %q", tt.Texts())
	}
	if err := tt.Click("Previous page"); err != nil { // disabled on page 0
		t.Fatal(err)
	}
	if tab.CurPage != 0 {
		t.Fatalf("disabled Previous page moved to %d", tab.CurPage)
	}

	if err := tt.Click("Next page"); err != nil {
		t.Fatal(err)
	}
	if tab.CurPage != 1 {
		t.Fatalf("after Next page: %d, want 1", tab.CurPage)
	}
	tt.Frame()
	if !tt.HasText("2 / 5") {
		t.Fatalf("indicator did not follow the page; texts %q", tt.Texts())
	}

	tab.Navigate(4, 0) // last page
	tt.Frame()
	if !tt.HasText("5 / 5") {
		t.Fatalf("indicator on the last page; texts %q", tt.Texts())
	}
	if err := tt.Click("Next page"); err != nil { // disabled on the last page
		t.Fatal(err)
	}
	if tab.CurPage != 4 {
		t.Fatalf("disabled Next page moved to %d", tab.CurPage)
	}
	if err := tt.Click("Previous page"); err != nil {
		t.Fatal(err)
	}
	if tab.CurPage != 3 {
		t.Fatalf("after Previous page: %d, want 3", tab.CurPage)
	}
}

// TestToolbarSearchOnTheRight: search moved to the right section — it
// still toggles the results panel, alongside the new page section.
func TestToolbarSearchOnTheRight(t *testing.T) {
	s := chromeState(1)
	tt := ui.NewTester(toolbarView(s), 1200, 120)
	if s.ShowSearchPanel {
		t.Fatal("panel should start hidden")
	}
	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	if !s.ShowSearchPanel {
		t.Fatal("Search no longer toggles the results panel")
	}
	if !tt.HasText("1 / 5") {
		t.Fatalf("page indicator missing; texts %q", tt.Texts())
	}
}
