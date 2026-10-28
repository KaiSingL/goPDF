// print_ui_test.go covers the print surfaces that are testable
// headless: the toolbar button's presence and the failure alert. The
// button itself is never CLICKED here — PrintActive opens the real
// system print dialog on a worker goroutine, which would block on (and
// pop up over) the test machine.
package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestToolbarPrintButton: the right section carries a Print button,
// wired for keyboard/a11y like its siblings (Find is by accessible
// label).
func TestToolbarPrintButton(t *testing.T) {
	s := chromeState(1)
	tt := ui.NewTester(toolbarView(s), 1200, 120)
	if _, ok := tt.Find("Print"); !ok {
		t.Fatalf("print button missing from the toolbar; texts %q", tt.Texts())
	}
	if _, ok := tt.Find("Comment"); ok {
		t.Errorf("the dead Comment button is back; texts %q", tt.Texts())
	}
}

// TestPrintAlert: a failed print opens an alert naming the cause; OK
// closes it.
func TestPrintAlert(t *testing.T) {
	s := chromeState(1)
	s.ShowPrintError = true
	s.PrintError = "print: the print dialog failed to open (CommDlgExtendedError 0x2)"
	tt := ui.NewTester(s.View, 900, 600)
	tt.Frame()

	if !tt.HasText("CommDlgExtendedError") {
		t.Fatalf("alert missing the error text; texts %q", tt.Texts())
	}
	if err := tt.Click("OK"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s.ShowPrintError {
		t.Fatal("OK did not close the print alert")
	}
}
