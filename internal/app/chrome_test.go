// chrome_test.go drives the four chrome modules (tabs, toolbar, sidebar,
// status bar) in ui.NewTester without a window, plus the search panel's
// flow (searchpanel.go: toggle → type → Enter → hits → step → Escape).
// Tabs are built with nil Doc on purpose: every chrome component must be
// nil-safe. Real-PDF flows use genTestPDF (gen_test.go).
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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
	tt := ui.NewTester(toolbarView(s), 1000, 120)

	// The toolbar carries only the search ICON now (the field moved into
	// the panel): clicking toggles the flag, no field is built here.
	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	if !s.ShowSearchPanel {
		t.Fatalf("Search icon did not set ShowSearchPanel")
	}
	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	if s.ShowSearchPanel {
		t.Fatalf("second click did not clear ShowSearchPanel")
	}
	tt.Frame() // extra frames while elements come and go
	tt.Frame()
	if tt.HasText("Search document") {
		t.Errorf("toolbar still builds a search field; texts %q", tt.Texts())
	}
}

// freshSearchState drops the search panel's per-tab caches for tab: the
// maps in searchpanel.go live for the whole test binary, and tab IDs from
// OpenTab (tabSeq) and chromeState (chromeTabSeq) are numbered by
// different counters, so a recycled "tab-N" must not inherit an earlier
// test's field text or list scroll.
func freshSearchState(tab *Tab) {
	delete(searchQueries, tab.ID)
	delete(searchLists, tab.ID)
	delete(searchScrolled, tab.ID)
}

// panelShell is the search tests' harness view: the shortcuts, the
// toolbar, and a content row holding the search panel exactly as view.go
// composes it (divider, then panel) — without the viewer, whose page
// renders only add worker traffic these assertions don't need.
func panelShell(s *AppState) func(*ui.Context) {
	return func(c *ui.Context) {
		shortcuts(c, s)
		ui.Column(c).Fill().Children(func() {
			toolbar(c, s)
			ui.Row(c).Fill().Grow(1).Children(func() {
				if s.ShowSearchPanel && s.Active() != nil {
					ui.Divider(c)
					searchPanel(c, s)
				}
			})
		})
	}
}

// buildIndex waits, synchronized, for the tab's text index: IndexAsync's
// first callback runs on the doc worker goroutine, but later ones run
// inline in the caller — so once built, panel searches complete inside
// the very frame that submits them (deterministic, and race-free under
// -race: the channel receive orders the worker's writes before the test's
// reads).
func buildIndex(t *testing.T, tab *Tab) {
	t.Helper()
	done := make(chan struct{})
	tab.Doc.IndexAsync(func(*pdf.Index, error) { close(done) })
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("index build timed out")
	}
}

func TestSearchPanelFlow(t *testing.T) {
	s := NewAppState()
	tab, err := s.OpenTab(genTestPDF(t, 3)) // pages carry "page-1"…"page-3"
	if err != nil {
		t.Fatalf("open generated pdf: %v", err)
	}
	t.Cleanup(func() { s.CloseTab(tab.ID) })
	freshSearchState(tab)
	buildIndex(t, tab)

	tab.Navigate(2, 0) // away from the first hit, so the jump is visible
	tt := ui.NewTester(panelShell(s), 1000, 700)

	// Hidden until toggled: no panel, no field, no empty-state hint.
	if tt.HasText("Search document") || tt.HasText("Type and press Enter") {
		t.Fatalf("panel visible before the toggle; texts %q", tt.Texts())
	}

	// The toolbar search icon opens the panel.
	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	if !s.ShowSearchPanel || !tt.HasText("Type and press Enter") {
		t.Fatalf("panel did not open: flag %v, texts %q", s.ShowSearchPanel, tt.Texts())
	}

	// Typing must NOT search: the field edits its own storage; the
	// committed query stays empty until Enter.
	if err := tt.Click("Search document"); err != nil {
		t.Fatal(err)
	}
	tt.Type("page-")
	tt.Frame()
	if tab.Search.Query != "" || tab.Search.Running || tab.Search.Hits != nil {
		t.Fatalf("typing auto-ran the search: query %q running %v hits %v",
			tab.Search.Query, tab.Search.Running, tab.Search.Hits)
	}

	// Enter runs the search; with the index pre-built the hits arrive in
	// the same pass and the tab jumps to the first hit.
	tt.Key(0, ui.KeyEnter)
	if tab.Search.Query != "page-" || tab.Search.Running {
		t.Fatalf("after Enter: query %q running %v", tab.Search.Query, tab.Search.Running)
	}
	if len(tab.Search.Hits) != 3 {
		t.Fatalf("hits = %d, want 3 (one per page)", len(tab.Search.Hits))
	}
	if tab.CurPage != 0 || tab.Search.Current != 0 {
		t.Fatalf("after Enter: page %d current %d, want 0/0", tab.CurPage, tab.Search.Current)
	}
	if !tt.HasText("3 results") || !tt.HasText("1 of 3") {
		t.Errorf("counter row missing; texts %q", tt.Texts())
	}
	for p := 1; p <= 3; p++ {
		if !tt.HasText(fmt.Sprintf("Page %d", p)) {
			t.Errorf("hit row for page %d missing; texts %q", p, tt.Texts())
		}
		if !tt.HasText(fmt.Sprintf("page-%d", p)) { // the snippet text
			t.Errorf("snippet for page %d missing; texts %q", p, tt.Texts())
		}
	}

	// Escape right away clears the query — the field kept focus through
	// the submit — leaving the panel open on its empty state.
	tt.Key(0, ui.KeyEscape)
	if tab.Search.Query != "" || tab.Search.Hits != nil {
		t.Fatalf("Escape did not clear: query %q hits %v", tab.Search.Query, tab.Search.Hits)
	}
	if !s.ShowSearchPanel {
		t.Fatalf("clearing Escape also closed the panel")
	}
	if !tt.HasText("Type and press Enter") {
		t.Errorf("cleared panel lost its empty state; texts %q", tt.Texts())
	}

	// Searching again works from the cleared panel. The clear dropped the
	// field's focus (as after any external edit), so click back in first.
	if err := tt.Click("Search document"); err != nil {
		t.Fatal(err)
	}
	tt.Type("page-")
	tt.Key(0, ui.KeyEnter)
	if tab.Search.Query != "page-" || len(tab.Search.Hits) != 3 {
		t.Fatalf("re-search failed: query %q hits %d", tab.Search.Query, len(tab.Search.Hits))
	}
	if tab.Search.Current != 0 || tab.CurPage != 0 {
		t.Fatalf("after re-search: current %d page %d, want 0/0", tab.Search.Current, tab.CurPage)
	}

	// The steppers walk the hits and navigate the tab.
	if err := tt.Click("Next hit"); err != nil {
		t.Fatal(err)
	}
	if tab.Search.Current != 1 || tab.CurPage != 1 {
		t.Fatalf("after Next: current %d page %d, want 1/1", tab.Search.Current, tab.CurPage)
	}
	if !tt.HasText("2 of 3") {
		t.Errorf("position not updated; texts %q", tt.Texts())
	}
	if err := tt.Click("Previous hit"); err != nil {
		t.Fatal(err)
	}
	if tab.Search.Current != 0 || tab.CurPage != 0 {
		t.Fatalf("after Prev: current %d page %d, want 0/0", tab.Search.Current, tab.CurPage)
	}

	// Clicking a result row selects it and jumps to its page.
	if err := tt.Click("Page 3"); err != nil {
		t.Fatal(err)
	}
	if tab.Search.Current != 2 || tab.CurPage != 2 {
		t.Fatalf("after row click: current %d page %d, want 2/2", tab.Search.Current, tab.CurPage)
	}

	// Focus now sits on the clicked row, so the field does not take this
	// Escape: the window binding closes the panel (shortcuts.go).
	tt.Key(0, ui.KeyEscape)
	if s.ShowSearchPanel || tt.HasText("Search document") {
		t.Fatalf("Escape did not close the panel; texts %q", tt.Texts())
	}

	// The toolbar toggle reopens the panel — with the previous search
	// intact, since the query and hits live in the tab, not the panel —
	// and closes it again.
	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	if !s.ShowSearchPanel || !tt.HasText("Search document") || !tt.HasText("3 results") {
		t.Fatalf("panel did not reopen with its results: flag %v, texts %q", s.ShowSearchPanel, tt.Texts())
	}
	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	if s.ShowSearchPanel {
		t.Fatalf("second toggle did not close the panel")
	}
	tt.Frame()
	tt.Frame()
}

func TestSearchPanelWithoutDoc(t *testing.T) {
	s := chromeState(1)
	tab := s.Tabs[0]
	freshSearchState(tab)
	tt := ui.NewTester(panelShell(s), 1000, 700)

	// Open the panel, type, Enter: without a document the search settles
	// immediately (nothing to index) and no hit steps anywhere.
	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Search document"); err != nil {
		t.Fatal(err)
	}
	tt.Type("doc")
	tt.Key(0, ui.KeyEnter)
	if tab.Search.Query != "doc" {
		t.Fatalf("Query = %q, want %q", tab.Search.Query, "doc")
	}
	if tab.Search.Running {
		t.Errorf("search still Running without a document")
	}
	if !tt.HasText("No results") || !tt.HasText("0 results") {
		t.Errorf("zero-hit search missing its placeholders; texts %q", tt.Texts())
	}

	// Enter with the same query and no hits neither steps nor panics.
	tt.Key(0, ui.KeyEnter)
	if tab.CurPage != 0 {
		t.Errorf("CurPage = %d, want 0", tab.CurPage)
	}

	// FocusBind: a request token stored in SearchFocus (as Ctrl+F would)
	// focuses the panel's field, which then types. (The input's text
	// itself is painted, not a Text node, so it never shows in Texts() —
	// assert on the committed query instead.)
	SearchFocus = "search-test-1"
	tt.Frame()
	tt.Type("x")
	tt.Key(0, ui.KeyEnter)
	if tab.Search.Query != "docx" {
		t.Errorf("Query = %q, want %q (field did not take focus)", tab.Search.Query, "docx")
	}
	SearchFocus = ""

	// Escape with text in the field clears it; a second Escape (empty
	// field) closes the panel.
	tt.Key(0, ui.KeyEscape)
	if tab.Search.Query != "" {
		t.Errorf("Query = %q after the clearing Escape", tab.Search.Query)
	}
	if !s.ShowSearchPanel {
		t.Errorf("clearing Escape closed the panel")
	}
	tt.Key(0, ui.KeyEscape)
	if s.ShowSearchPanel {
		t.Errorf("second Escape did not close the panel")
	}
	tt.Frame()
	tt.Frame()
}

func TestSearchPanelInViewAndShortcut(t *testing.T) {
	s := NewAppState()
	s.ShowSidebar = false
	tab, err := s.OpenTab(genTestPDF(t, 1))
	if err != nil {
		t.Fatalf("open generated pdf: %v", err)
	}
	t.Cleanup(func() { s.CloseTab(tab.ID) })
	freshSearchState(tab)
	buildIndex(t, tab)

	tt := ui.NewTester(s.View, 900, 640) // the real root view
	tt.Frame()
	if tt.HasText("Search document") {
		t.Fatalf("panel visible before Ctrl+F; texts %q", tt.Texts())
	}

	// Ctrl+F opens the panel AND focuses its field with a fresh token, so
	// typing lands without a click.
	tt.Key(ui.Ctrl, ui.KeyF)
	if !s.ShowSearchPanel || !tt.HasText("Type and press Enter") {
		t.Fatalf("Ctrl+F did not open the panel: flag %v, texts %q", s.ShowSearchPanel, tt.Texts())
	}
	tt.Type("page-")
	tt.Key(0, ui.KeyEnter)
	if tab.Search.Query != "page-" || len(tab.Search.Hits) != 1 {
		t.Fatalf("focused-field search failed: query %q hits %d", tab.Search.Query, len(tab.Search.Hits))
	}
	if tab.CurPage != 0 {
		t.Errorf("CurPage = %d, want 0", tab.CurPage)
	}

	// The focused field takes the first Escape (clears); the next one
	// goes to the window binding and closes the panel.
	tt.Key(0, ui.KeyEscape)
	if tab.Search.Query != "" {
		t.Errorf("Query = %q after the clearing Escape", tab.Search.Query)
	}
	tt.Key(0, ui.KeyEscape)
	if s.ShowSearchPanel || tt.HasText("Search document") {
		t.Errorf("Escape did not close the panel; texts %q", tt.Texts())
	}
	tt.Frame()
	tt.Frame()
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
