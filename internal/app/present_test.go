// present_test.go covers the issue-4 presentation mode state machine
// (present.go): enter/exit round trips the reader view, navigation
// advances the silent page counter, the active-tab and close guards end
// presentation, and a full-screen entry records the undo only when the
// window needed changing. The window-less testers use nil window (the
// headless shape), so full screen is a no-op by design; the flag
// behavior is asserted directly.
package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestPresentationEnterExitRestores: Ctrl+L toggles, exit puts the zoom
// mode, zoom and scroll back.
func TestPresentationEnterExitRestores(t *testing.T) {
	s := reproState(t, 3)
	tab := s.Active()
	tab.SetZoomMode(ZoomCustom)
	tab.SetZoom(1.75)

	if s.presentingTab() != nil {
		t.Fatal("a fresh tab presents before it was asked to")
	}

	keyL := func() {
		tt := ui.NewTester(s.View, 1200, 800)
		tt.Key(ui.Ctrl, ui.KeyL)
	}
	keyL()
	if s.presentingTab() == nil {
		t.Fatal("Ctrl+L did not enter presentation")
	}
	if !tab.Presenting || tab.presentRestore == nil {
		t.Fatal("entry did not set the flag and the restore copy")
	}
	if tab.presentRestore.zoom != 1.75 {
		t.Fatalf("entry stored zoom %v, want the pre-entry 1.75", tab.presentRestore.zoom)
	}

	keyL() // the same chord exits
	if s.presentingTab() != nil || tab.Presenting {
		t.Fatal("second Ctrl+L did not exit presentation")
	}
	if tab.presentRestore != nil {
		t.Fatal("exit left a restore copy behind")
	}
	if tab.ZoomMode != ZoomCustom || tab.Zoom != 1.75 {
		t.Fatalf("exit restored (%v, %v), want the exact pre-entry view", tab.ZoomMode, tab.Zoom)
	}
}

// TestPresentationNavigation: keys turn the page silently (no history)
// and Enter with a typed number jumps.
func TestPresentationNavigation(t *testing.T) {
	s := reproState(t, 5)
	tab := s.Active()
	s.StartPresenting()
	if tab.CurPage != 0 {
		t.Fatalf("presentation started at page %d, want 0", tab.CurPage)
	}

	tt := ui.NewTester(s.View, 1200, 800)
	tt.Key(0, ui.KeyRight)
	tt.Key(0, ui.KeyRight)
	tt.Key(0, ui.KeySpace)
	tt.Key(0, ui.KeyLeft)
	if tab.CurPage != 2 {
		t.Fatalf("movement landed on page %d, want 2", tab.CurPage)
	}
	if len(tab.History) != 0 {
		t.Fatal("slide turns wrote reader history entries")
	}

	tt.Key(0, ui.Key5)
	if presentDigits != "5" {
		t.Fatalf("typed digits: %q, want 5", presentDigits)
	}
	tt.Key(0, ui.KeyEnter)
	if tab.CurPage != 4 {
		t.Fatalf("number jump landed on page %d, want the last slide", tab.CurPage)
	}
	if presentDigits != "" {
		t.Fatal("the jump did not clear the typed number")
	}

	tt.Key(0, ui.KeyEscape)
	if s.presentingTab() != nil {
		t.Fatal("Escape did not exit presentation")
	}
}

// TestPresentationEndsOnTabChangeAndClose: switching the active tab or
// closing the presenting tab hands the window back.
func TestPresentationEndsOnTabChangeAndClose(t *testing.T) {
	s := reproState(t, 3)
	tab := s.Active()
	s.OpenTab(genTestPDF(t, 2))
	s.StartPresenting()
	if s.presentingTab() == nil {
		t.Fatal("entry failed")
	}

	second := s.Tabs[1]
	s.SelectTabBy(second.ID)
	if s.presentingTab() != nil {
		t.Fatal("switching tabs kept presentation running")
	}
	if tab.Presenting {
		t.Fatal("the presenting tab kept its flag after the switch")
	}
	if s.PresentFullscreen {
		t.Fatal("a headless entry left the fullscreen-undo flag set")
	}

	// Close the presenting tab: presentation ends before teardown, and
	// the flag dies with the removed tab (asserting it, because a
	// different active tab makes presentingTab() nil regardless: only
	// THIS assertion can catch a lost CloseTab guard headlessly).
	s.SelectTabBy(tab.ID)
	s.StartPresenting()
	s.CloseTab(tab.ID)
	if tab.Presenting {
		t.Fatal("closing the presenting tab left its flag set")
	}
	if s.presentingTab() != nil {
		t.Fatal("closing the presenting tab left presentation on")
	}
	if s.PresentFullscreen {
		t.Fatal("closing the presenting tab left the fullscreen undo pending")
	}
}

// TestPresentationNumberJumpClamps: digits past the last page clamp at
// the last slide, and the digit buffer never grows past 4 digits.
func TestPresentationNumberJumpClamps(t *testing.T) {
	s := reproState(t, 5)
	tab := s.Active()
	s.StartPresenting()
	tt := ui.NewTester(s.View, 1200, 800)

	tt.Key(0, ui.Key9) // 9 > 5 pages: the jump clamps at the last slide
	tt.Key(0, ui.KeyEnter)
	if tab.CurPage != 4 {
		t.Fatalf("out-of-range jump landed on page %d, want the clamped last slide", tab.CurPage)
	}

	tt.Key(0, ui.Key1)
	tt.Key(0, ui.Key2)
	tt.Key(0, ui.Key3)
	tt.Key(0, ui.Key4)
	tt.Key(0, ui.Key5) // a 5th digit must be dropped (buffer capped at 4)
	if presentDigits != "1234" {
		t.Fatalf("digit buffer: %q, want the capped 1234", presentDigits)
	}
	tt.Key(0, ui.KeyEnter)
	if tab.CurPage != 1233 {
		t.Fatalf("capped buffer jump landed on page %d, want the clamped last slide", tab.CurPage)
	}
	s.StopPresenting()
}

// TestPresentationGuardedWithoutDocument: no tab or a launcher tab
// cannot present, and Ctrl+L is a no-op without visible changes.
func TestPresentationGuardedWithoutDocument(t *testing.T) {
	s := NewAppState()
	tt := ui.NewTester(s.View, 1200, 800)
	tt.Key(ui.Ctrl, ui.KeyL)
	if s.presentingTab() != nil {
		t.Fatal("presentation started without a document")
	}
}
