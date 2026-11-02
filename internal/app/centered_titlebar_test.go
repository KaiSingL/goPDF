// centered_titlebar_test.go pins the macOS title-bar band: the active
// tab's document title centered in the band, the app name while the
// launcher tab shows, and none of the Windows inline menus leaking in —
// on macOS the system menu bar owns File / View / Go (menu.go).
package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// macLights is the room the macOS traffic lights take at the band's
// left edge (TitleBarHiddenInset), mirroring winButtons of
// titlebar_test.go for the other platform.
var macLights = ui.TitleBar{Height: 40, Left: 70}

func TestCenteredTitleBarShowsDocumentTitle(t *testing.T) {
	s := chromeState(1)
	tt := ui.NewTester(func(c *ui.Context) { centeredTitleBar(c, s) }, 900, 48)
	tt.SetTitleBar(macLights)
	tt.Frame()

	if !tt.HasText("doc1.pdf") {
		t.Fatalf("mac band shows no document title; texts %q", tt.Texts())
	}
	for _, leaked := range []string{"File", "View", "Go"} {
		if tt.HasText(leaked) {
			t.Fatalf("inline menu %q leaked into the mac band; texts %q", leaked, tt.Texts())
		}
	}
}

func TestCenteredTitleBarAppNameWithoutTab(t *testing.T) {
	s := NewAppState() // the launcher tab: no documents open
	tt := ui.NewTester(func(c *ui.Context) { centeredTitleBar(c, s) }, 900, 48)
	tt.SetTitleBar(macLights)
	tt.Frame()

	if !tt.HasText("GoPDF") {
		t.Fatalf("empty mac band shows no app name; texts %q", tt.Texts())
	}
}

func TestWindowTitleFollowsActiveTab(t *testing.T) {
	s := chromeState(2)
	s.ActiveTab = 1
	if got, want := windowTitle(s), "doc2.pdf"; got != want {
		t.Fatalf("windowTitle = %q, want %q", got, want)
	}
	s.ActiveTab = -1 // nothing active: the launcher tab
	if got, want := windowTitle(s), "GoPDF"; got != want {
		t.Fatalf("windowTitle without a tab = %q, want %q", got, want)
	}
}
