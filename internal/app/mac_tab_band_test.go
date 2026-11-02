// mac_tab_band_test.go pins the macOS merged band: the tab strip lives
// in the title-bar band — tabs and the "+" button present, none of the
// Windows inline menus, and no separate title band row above the strip
// (there is no icon and no document title; the system menu bar owns the
// menus, menu.go).
package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// macLights is the room the macOS traffic lights take at the band's
// left edge (TitleBarHiddenInset), mirroring winButtons of
// titlebar_test.go for the other platform.
var macLights = ui.TitleBar{Height: 40, Left: 70}

func TestMacTabBandShowsStrip(t *testing.T) {
	s := chromeState(1)
	tt := ui.NewTester(func(c *ui.Context) { macTabBar(c, s) }, 900, 48)
	tt.SetTitleBar(macLights)
	tt.Frame()

	if !tt.HasText("doc1.pdf") {
		t.Fatalf("mac band shows no tab; texts %q", tt.Texts())
	}
	if !tt.HasText("New tab") {
		t.Fatalf("mac band lost the + button; texts %q", tt.Texts())
	}
	for _, leaked := range []string{"File", "View", "Go"} {
		if tt.HasText(leaked) {
			t.Fatalf("inline menu %q leaked into the mac band; texts %q", leaked, tt.Texts())
		}
	}
}

func TestMacBandNewTabClick(t *testing.T) {
	s := chromeState(1)
	tt := ui.NewTester(func(c *ui.Context) { macTabBar(c, s) }, 900, 48)
	tt.SetTitleBar(macLights)
	tt.Frame()

	if err := tt.Click("New tab"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// The "+" button opened a launcher tab: two tabs now, the new one
	// active (the launcher takes the focus, as from the toolbar).
	if len(s.Tabs) != 2 {
		t.Fatalf("after New tab: %d tabs, want 2", len(s.Tabs))
	}
}

func TestRootViewMergedBand(t *testing.T) {
	old := mergedTabBand
	mergedTabBand = true
	t.Cleanup(func() { mergedTabBand = old })
	s := chromeState(1)
	tt := ui.NewTester(s.View, 1000, 700)

	// One row: the strip is there, the Windows title band's contents
	// are not — no divider-eaten icon, no File/View/Go, no centered
	// document title beyond the tab's own.
	if !tt.HasText("doc1.pdf") || !tt.HasText("New tab") {
		t.Fatalf("root view lost the tab strip; texts %q", tt.Texts())
	}
	for _, leaked := range []string{"File", "View", "Go", "GoPDF"} {
		if tt.HasText(leaked) {
			t.Fatalf("title band content %q leaked into the merged band; texts %q", leaked, tt.Texts())
		}
	}
}
