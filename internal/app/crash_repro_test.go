// crash_repro_test.go reproduces a user-reported crash: "click the
// thumbnail button to switch, it crashed". Two interpretations, both driven
// against a REAL document so the async render pipeline is live:
//
//	A. switch the sidebar panel Outline→Thumbnails after navigating pages
//	   (the ScrollIntoView-during-build path)
//	B. click a thumbnail to switch pages once thumbnails have rendered
package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// crashPump advances the tester until want returns true or d elapses,
// giving doc-worker render callbacks time to land between frames.
func crashPump(t *testing.T, tt *ui.Tester, d time.Duration, want func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		tt.Frame()
		if want() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return want()
}

func reproState(t *testing.T, pages int) *AppState {
	t.Helper()
	path := genTestPDF(t, pages)
	s := NewAppState()
	tab, err := s.OpenTab(path)
	if err != nil {
		t.Fatalf("OpenTab: %v", err)
	}
	t.Cleanup(func() { s.CloseTab(tab.ID) })
	return s
}

// TestCrashSwitchPanelAfterNavigate: thumbnails → outline → navigate →
// back to thumbnails (panel switch while CurPage changed).
func TestCrashSwitchPanelAfterNavigate(t *testing.T) {
	s := reproState(t, 12)
	tt := ui.NewTester(s.View, 1200, 800)

	// Thumbnails show first (default panel); let renders land.
	crashPump(t, tt, 2*time.Second, func() bool {
		return s.Active().Cache.Len() > 0
	})

	if err := tt.Click("Outline"); err != nil {
		t.Fatalf("click Outline: %v", err)
	}
	tt.Frame()
	// Navigate away while the outline panel is showing.
	s.Active().Navigate(9, 0)
	tt.Frame()

	if err := tt.Click("Thumbnails"); err != nil {
		t.Fatalf("click Thumbnails: %v", err)
	}
	// The crash frame: builds the thumbnail list with a changed CurPage.
	crashPump(t, tt, 2*time.Second, func() bool { return false })
}

// TestCrashThumbnailClickSwitch: click a thumbnail row to switch pages
// after real thumbnails have rendered.
func TestCrashThumbnailClickSwitch(t *testing.T) {
	s := reproState(t, 12)
	tt := ui.NewTester(s.View, 1200, 800)

	if ok := crashPump(t, tt, 3*time.Second, func() bool {
		return s.Active().Cache.Len() >= 3
	}); !ok {
		t.Fatalf("thumbnails did not render (cache=%d)", s.Active().Cache.Len())
	}

	if err := tt.Click("Page 5"); err != nil {
		t.Fatalf("click Page 5: %v", err)
	}
	if s.Active().CurPage != 4 {
		t.Fatalf("CurPage = %d, want 4", s.Active().CurPage)
	}
	crashPump(t, tt, 2*time.Second, func() bool { return false })
}

// TestCrashRapidPanelToggling: switch panels quickly while renders land —
// races the in-flight RenderAsync callbacks against panel rebuilds.
func TestCrashRapidPanelToggling(t *testing.T) {
	s := reproState(t, 20)
	tt := ui.NewTester(s.View, 1200, 800)
	for i := 0; i < 12; i++ {
		if i%2 == 0 {
			tt.Click("Outline")
		} else {
			tt.Click("Thumbnails")
		}
		tt.Frame()
	}
	crashPump(t, tt, 2*time.Second, func() bool { return false })
}
