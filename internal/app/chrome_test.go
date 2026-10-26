// chrome_test.go drives the four chrome modules (tabs, toolbar, sidebar,
// status bar) in ui.NewTester without a window. Tabs are built with nil
// Doc on purpose: every chrome component must be nil-safe. Real-PDF
// thumbnail rendering needs gen_test.go (Agent B) and is skipped there.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
	"gopdf/internal/pdf"
)

// chromeTabSeq gives every test tab a unique ID: the chrome modules keep
// per-tab caches keyed by tab ID (searchQueries, thumbLists, …) that live
// for the whole test binary, so tests must not share IDs.
var chromeTabSeq atomic.Uint64

// chromeState builds an AppState with n doc-less tabs of 5 pages.
func chromeState(n int) *AppState {
	s := NewAppState()
	for i := 1; i <= n; i++ {
		k := chromeTabSeq.Add(1)
		s.Tabs = append(s.Tabs, &Tab{
			ID:    fmt.Sprintf("tab-%d", k),
			Path:  fmt.Sprintf("C:\\tmp\\doc%d.pdf", k),
			Title: fmt.Sprintf("doc%d.pdf", i),
			// Doc nil: components must not panic without a document.
			NumPages: 5,
			Zoom:     1.0,
			ZoomMode: ZoomFitWidth,
		})
	}
	if n > 0 {
		s.ActiveTab = 0
	}
	return s
}

// miniPDF is a two-page document with no xref table; MuPDF repairs it on
// open. state.CloseTab calls Doc.Close unconditionally, so the close-path
// test needs tabs whose Doc is real (reported as a state.go contract gap).
const miniPDF = `%PDF-1.4
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >> endobj
3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >> endobj
4 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >> endobj
trailer << /Root 1 0 R /Size 5 >>
%%EOF`

// chromeDocTabs builds an AppState of n tabs sharing one real document.
// It returns the document path the tabs carry.
func chromeDocTabs(t *testing.T, n int) (*AppState, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tiny.pdf")
	if err := os.WriteFile(path, []byte(miniPDF), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := pdf.Open(path)
	if err != nil {
		t.Fatalf("pdf.Open: %v", err)
	}
	t.Cleanup(doc.Close)

	s := NewAppState()
	for i := 1; i <= n; i++ {
		s.Tabs = append(s.Tabs, &Tab{
			ID:       fmt.Sprintf("tab-%d", chromeTabSeq.Add(1)),
			Path:     doc.Path(),
			Title:    fmt.Sprintf("doc%d.pdf", i),
			Doc:      doc, // shared; Close is idempotent
			NumPages: doc.NumPages(),
			Zoom:     1.0,
			ZoomMode: ZoomFitWidth,
		})
	}
	if n > 0 {
		s.ActiveTab = 0
	}
	return s, doc.Path()
}

// tabView shows only the tab strip.
func tabView(s *AppState) func(*ui.Context) {
	return func(c *ui.Context) { tabBar(c, s) }
}

// toolbarView shows only the second bar.
func toolbarView(s *AppState) func(*ui.Context) {
	return func(c *ui.Context) { toolbar(c, s) }
}

func TestTabStripSelectAndClose(t *testing.T) {
	// state.CloseTab closes Doc unconditionally (frozen file), so tabs
	// carrying a real document exercise the close path.
	s, docPath := chromeDocTabs(t, 2)
	tt := ui.NewTester(tabView(s), 800, 200)

	// Clicking a tab selects it.
	if err := tt.Click("doc2.pdf"); err != nil {
		t.Fatal(err)
	}
	if s.ActiveTab != 1 {
		t.Fatalf("ActiveTab = %d, want 1", s.ActiveTab)
	}

	// The close button removes the tab and records its path.
	if err := tt.Click("Close doc2.pdf"); err != nil {
		t.Fatal(err)
	}
	if len(s.Tabs) != 1 || s.Tabs[0].Title != "doc1.pdf" {
		t.Fatalf("tabs after close: %v", s.Tabs)
	}
	if len(s.ClosedPaths) != 1 || s.ClosedPaths[0] != docPath {
		t.Fatalf("ClosedPaths = %v, want [%s]", s.ClosedPaths, docPath)
	}
	if s.ActiveTab != 0 {
		t.Fatalf("ActiveTab = %d, want 0", s.ActiveTab)
	}

	// Closing the last tab empties the strip.
	if err := tt.Click("Close doc1.pdf"); err != nil {
		t.Fatal(err)
	}
	if len(s.Tabs) != 0 || s.ActiveTab != -1 {
		t.Fatalf("after last close: %d tabs, ActiveTab %d", len(s.Tabs), s.ActiveTab)
	}
	if tt.HasText("doc1.pdf") {
		t.Errorf("strip still shows a closed tab")
	}
}

func TestToolbarHistoryAndZoom(t *testing.T) {
	s := chromeState(1)
	tab := s.Tabs[0]
	tt := ui.NewTester(toolbarView(s), 1200, 120)

	// Back/forward start disabled: clicking is a no-op, not a panic.
	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	if tab.CurPage != 0 || len(tab.History) != 0 {
		t.Fatalf("disabled Back changed state: page %d, history %v", tab.CurPage, tab.History)
	}

	// A jump enables Back; it returns to the old page and arms Forward.
	tab.Navigate(3, 0)
	tt.Frame()
	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	if tab.CurPage != 0 || len(tab.Future) != 1 {
		t.Fatalf("after Back: page %d, future %v", tab.CurPage, tab.Future)
	}
	if err := tt.Click("Forward"); err != nil {
		t.Fatal(err)
	}
	if tab.CurPage != 3 {
		t.Fatalf("after Forward: page %d, want 3", tab.CurPage)
	}

	// Zoom in steps and switches to custom; the percent resets to 100%.
	if err := tt.Click("Zoom in"); err != nil {
		t.Fatal(err)
	}
	if tab.ZoomMode != ZoomCustom || tab.Zoom < 1.19 || tab.Zoom > 1.21 {
		t.Fatalf("after Zoom in: mode %d zoom %v", tab.ZoomMode, tab.Zoom)
	}
	if err := tt.Click("120%"); err != nil {
		t.Fatal(err)
	}
	if tab.Zoom != 1.0 {
		t.Fatalf("after reset: zoom %v, want 1", tab.Zoom)
	}
	if err := tt.Click("Zoom out"); err != nil {
		t.Fatal(err)
	}
	if tab.Zoom < 0.82 || tab.Zoom > 0.84 {
		t.Fatalf("after Zoom out: zoom %v", tab.Zoom)
	}

	// Reload clears the image cache and bumps the generation.
	gen := tab.ReloadGen
	if err := tt.Click("Reload"); err != nil {
		t.Fatal(err)
	}
	if tab.ReloadGen != gen+1 {
		t.Fatalf("ReloadGen = %d, want %d", tab.ReloadGen, gen+1)
	}

	// The segmented fit control writes ZoomMode.
	if err := tt.Click("Fit Page"); err != nil {
		t.Fatal(err)
	}
	if tab.ZoomMode != ZoomFitPage {
		t.Fatalf("ZoomMode = %d, want ZoomFitPage", tab.ZoomMode)
	}
}

func TestToolbarSearchWithoutDoc(t *testing.T) {
	s := chromeState(1)
	tab := s.Tabs[0]
	tt := ui.NewTester(toolbarView(s), 1000, 120)

	// Typing runs (and, with no doc, immediately settles) the search.
	if err := tt.Click("Search document"); err != nil {
		t.Fatal(err)
	}
	tt.Type("doc")
	if tab.Search.Query != "doc" {
		t.Fatalf("Query = %q, want %q", tab.Search.Query, "doc")
	}
	if tab.Search.Running {
		t.Errorf("search still Running without a document")
	}

	// Enter with the same query and no hits neither steps nor panics.
	tt.Key(0, ui.KeyEnter)
	if tab.CurPage != 0 {
		t.Errorf("CurPage = %d, want 0", tab.CurPage)
	}

	// FocusBind: a request token stored in SearchFocus (as Ctrl+F would)
	// focuses the field, which then types. (The input's text itself is
	// painted, not a Text node, so it never shows in Texts() — assert on
	// the committed query instead.)
	SearchFocus = "search-test-1"
	tt.Frame()
	tt.Type("x")
	if tab.Search.Query != "docx" {
		t.Errorf("Query = %q, want %q (field did not take focus)", tab.Search.Query, "docx")
	}
	SearchFocus = ""
}

func TestSidebarThumbnailsAndOutline(t *testing.T) {
	s := chromeState(1)
	tab := s.Tabs[0]
	tab.Outline = []pdf.OutlineNode{
		{Title: "Chapter 1", Page: 1, Children: []pdf.OutlineNode{
			{Title: "Section 1.1", Page: 2},
		}},
		{Title: "Dead link", Page: -1},
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().Children(func() { sidebar(c, s) })
	}, 700, 900)

	// Thumbnails: every page shows as a row; clicking one navigates.
	for p := 1; p <= 5; p++ {
		if !tt.HasText(fmt.Sprintf("Page %d", p)) {
			t.Fatalf("thumbnail row %d missing; texts %q", p, tt.Texts())
		}
	}
	if err := tt.Click("Page 3"); err != nil {
		t.Fatal(err)
	}
	if tab.CurPage != 2 {
		t.Fatalf("CurPage = %d, want 2", tab.CurPage)
	}

	// The segmented switch flips the panel.
	if err := tt.Click("Outline"); err != nil {
		t.Fatal(err)
	}
	if s.SidebarTab != SidebarOutline {
		t.Fatalf("SidebarTab = %d, want SidebarOutline", s.SidebarTab)
	}
	if tt.HasText("No outline") {
		t.Errorf("outline present but the panel says No outline")
	}

	// Clicking an entry navigates; -1 pages are dead.
	if err := tt.Click("Chapter 1"); err != nil {
		t.Fatal(err)
	}
	if tab.CurPage != 1 {
		t.Fatalf("CurPage = %d, want 1", tab.CurPage)
	}
	if err := tt.Click("Dead link"); err != nil {
		t.Fatal(err)
	}
	if tab.CurPage != 1 {
		t.Fatalf("dead outline link navigated to %d", tab.CurPage)
	}

	// A tab without an outline shows the muted placeholder.
	if err := tt.Click("Thumbnails"); err != nil {
		t.Fatal(err)
	}
	s.Tabs[0].Outline = nil
	tt.Frame()
	if err := tt.Click("Outline"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("No outline") {
		t.Errorf("missing \"No outline\" placeholder; texts %q", tt.Texts())
	}
}

func TestSidebarWithoutDoc(t *testing.T) {
	s := chromeState(1)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().Children(func() { sidebar(c, s) })
	}, 700, 600)
	if !tt.HasText("Page 1") || !tt.HasText("Page 5") {
		t.Fatalf("placeholder rows missing; texts %q", tt.Texts())
	}
	// Clicking a placeholder row navigates within the (unrendered) doc.
	if err := tt.Click("Page 2"); err != nil {
		t.Fatal(err)
	}
	if s.Tabs[0].CurPage != 1 {
		t.Fatalf("CurPage = %d, want 1", s.Tabs[0].CurPage)
	}
}

func TestStatusBarPageInput(t *testing.T) {
	s := chromeState(1)
	tab := s.Tabs[0]
	tt := ui.NewTester(func(c *ui.Context) { statusBar(c, s) }, 700, 80)

	if !tt.HasText("Page 1 of 5") {
		t.Fatalf("status text missing; texts %q", tt.Texts())
	}

	// A valid number navigates (1-based).
	if err := tt.Click("Go to page"); err != nil {
		t.Fatal(err)
	}
	tt.Type("3")
	tt.Key(0, ui.KeyEnter)
	if tab.CurPage != 2 {
		t.Fatalf("CurPage = %d, want 2", tab.CurPage)
	}
	if !tt.HasText("Page 3 of 5") {
		t.Errorf("status not updated; texts %q", tt.Texts())
	}

	// Garbage resets the display without navigating.
	if err := tt.Click("Go to page"); err != nil {
		t.Fatal(err)
	}
	tt.Type("zz")
	tt.Key(0, ui.KeyEnter)
	if tab.CurPage != 2 {
		t.Fatalf("garbage input navigated to %d", tab.CurPage)
	}

	// Out-of-range numbers clamp.
	if err := tt.Click("Go to page"); err != nil {
		t.Fatal(err)
	}
	tt.Type("99")
	tt.Key(0, ui.KeyEnter)
	if tab.CurPage != 4 {
		t.Fatalf("CurPage = %d, want 4 (clamped)", tab.CurPage)
	}
	// The theme toggle button builds; clicking it is NOT exercised here:
	// toggleTheme (Agent D) applies the theme through the app main loop,
	// which headless tests do not run (it blocks there).
	if _, ok := tt.Find("Toggle dark mode"); !ok {
		t.Errorf("dark-mode toggle missing")
	}
}

func TestChromeEmptyState(t *testing.T) {
	s := chromeState(0)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			tabBar(c, s)
			toolbar(c, s)
			if s.ShowSidebar {
				sidebar(c, s)
			}
			statusBar(c, s)
		})
	}, 900, 600)

	if !tt.HasText("GoPDF") {
		t.Errorf("empty status bar missing the app name; texts %q", tt.Texts())
	}
	if !tt.HasText("No document open") {
		t.Errorf("empty sidebar missing its placeholder; texts %q", tt.Texts())
	}
}
