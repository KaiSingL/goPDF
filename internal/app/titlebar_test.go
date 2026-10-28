// titlebar_test.go drives the integrated title bar (titlebar.go) in
// ui.NewTester: its composition (the optional app icon, the three menus)
// and its menu wiring. The Tester runs menu buttons headlessly for
// real — Click opens the popup, Menu lists its items, ChooseMenuItem walks
// submenus and reports the choice through the same Chosen path the window
// uses — so the assertions cover the actions, not just the labels. What
// headless cannot reach is the drag region itself (host startDrag /
// titleBarDoubleClicked are window-host calls), covered by review only.
package app

import (
	"bytes"
	"image"
	"image/png"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// titleView shows only the title bar.
func titleView(s *AppState) func(*ui.Context) {
	return func(c *ui.Context) { titleBarView(c, s) }
}

// winButtons is the room the window controls take on Windows with
// TitleBarHidden (internal/fake reports Right: 138): the bar must clear
// it, which the padding-only assertions here take on faith — the row's
// own height and padding are view-internal.
var winButtons = ui.TitleBar{Height: 40, Right: 138}

func TestTitleBarComposition(t *testing.T) {
	s := chromeState(1)
	tt := ui.NewTester(titleView(s), 900, 48)
	tt.SetTitleBar(winButtons)

	for _, want := range []string{"File", "View", "Go"} {
		if !tt.HasText(want) {
			t.Errorf("title bar missing %q; texts %q", want, tt.Texts())
		}
	}

	// With an icon set (main decodes resources/icon.png), the bar builds
	// the image ahead of the menus.
	pngBytes := &bytes.Buffer{}
	if err := png.Encode(pngBytes, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	bm, err := ui.DecodeBitmap(pngBytes.Bytes())
	if err != nil {
		t.Fatalf("DecodeBitmap: %v", err)
	}
	SetAppIcon(bm)
	t.Cleanup(func() { SetAppIcon(nil) })
	tt.Frame()
	for _, want := range []string{"File", "View", "Go"} {
		if !tt.HasText(want) {
			t.Errorf("icon pass lost %q; texts %q", want, tt.Texts())
		}
	}
}

func TestTitleBarFileMenu(t *testing.T) {
	s := chromeState(1)
	s.Recents = []string{`C:\tmp\newest.pdf`, `C:\tmp\older.pdf`}
	tt := ui.NewTester(titleView(s), 900, 48)
	tt.SetTitleBar(winButtons)

	// The File menu carries the native menu bar's items, separators
	// included.
	if err := tt.Click("File"); err != nil {
		t.Fatal(err)
	}
	want := []string{"Open…", "Open Recent", "-", "Print…", "Document Properties",
		"-", "Reopen Closed Tab", "Close Tab", "-", "Quit"}
	if got := tt.Menu(); !slices.Equal(got, want) {
		t.Fatalf("File menu = %q, want %q", got, want)
	}

	// Document Properties opens the dialog for an active tab.
	if err := tt.ChooseMenuItem("Document Properties"); err != nil {
		t.Fatal(err)
	}
	if !s.ShowProperties {
		t.Fatal("choosing Document Properties did not set ShowProperties")
	}

	// The Open Recent submenu lists the recents by file name (each
	// ChooseMenuItem closes the popup, so the menu reopens per choice);
	// it exists even when empty, holding a disabled placeholder.
	for _, name := range []string{"newest.pdf", "older.pdf"} {
		if err := tt.Click("File"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem("Open Recent", name); err != nil {
			t.Errorf("Open Recent > %s: %v", name, err)
		}
	}
	if err := tt.Click("File"); err != nil {
		t.Fatal(err)
	}
	s.Recents = nil
	tt.Frame()
	if err := tt.ChooseMenuItem("Open Recent", "No Recent Documents"); err == nil {
		t.Error("the empty-recents placeholder is not disabled")
	}
	tt.CloseMenu()
}

func TestTitleBarViewMenu(t *testing.T) {
	s := chromeState(1)
	was := s.ShowSidebar
	tt := ui.NewTester(titleView(s), 900, 48)
	tt.SetTitleBar(winButtons)

	if err := tt.Click("View"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Zoom In", "Zoom Out", "Reset Zoom", "Fit Width",
		"Fit Page", "Rotate Clockwise", "Toggle Sidebar", "Dark Mode"} {
		if !slices.Contains(tt.Menu(), want) {
			t.Errorf("View menu missing %q; items %q", want, tt.Menu())
		}
	}
	if err := tt.ChooseMenuItem("Toggle Sidebar"); err != nil {
		t.Fatal(err)
	}
	if s.ShowSidebar == was {
		t.Fatalf("Toggle Sidebar left ShowSidebar at %v", s.ShowSidebar)
	}
}

func TestTitleBarGoMenu(t *testing.T) {
	s := chromeState(1)
	tab := s.Tabs[0] // 5 pages, like every chromeState tab
	tt := ui.NewTester(titleView(s), 900, 48)
	tt.SetTitleBar(winButtons)

	if err := tt.Click("Go"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Next Page", "Previous Page", "First Page",
		"Last Page", "Back", "Forward"} {
		if !slices.Contains(tt.Menu(), want) {
			t.Errorf("Go menu missing %q; items %q", want, tt.Menu())
		}
	}

	// The jumps run the same guarded actions as the shortcuts.
	if err := tt.ChooseMenuItem("Last Page"); err != nil {
		t.Fatal(err)
	}
	if tab.CurPage != 4 {
		t.Fatalf("after Last Page: CurPage = %d, want 4", tab.CurPage)
	}
	if err := tt.Click("Go"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("First Page"); err != nil {
		t.Fatal(err)
	}
	if tab.CurPage != 0 {
		t.Fatalf("after First Page: CurPage = %d, want 0", tab.CurPage)
	}

	// Without an active tab the navigation items are no-ops, not panics
	// (the withActive guard).
	s.ActiveTab = -1
	if err := tt.Click("Go"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Next Page"); err != nil {
		t.Fatal(err)
	}
	tt.CloseMenu()
}

// TestTitleBarInRootView checks the composition order: the real root view
// builds the title bar (its menus) before everything else.
func TestTitleBarInRootView(t *testing.T) {
	s := chromeState(1)
	tt := ui.NewTester(s.View, 1000, 700)
	for _, want := range []string{"File", "View", "Go"} {
		if !tt.HasText(want) {
			t.Errorf("root view missing the title bar's %q menu; texts %q", want, tt.Texts())
		}
	}
}
