// save_test.go covers the save wiring (save.go): the dirty flag's life
// cycle around a mutation, the same-path save through the toolbar
// button (verified by reopening the file on disk), Save-As leaving the
// original untouched, the tab label's bullet, and the close-with-
// unsaved notice.
package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"gopdf/internal/pdf"
)

// createSquareViaUI drives the square drag flow (annotate.go) and waits
// for the mutation to land (Dirty set), returning when the tab reports
// unsaved edits.
func createSquareViaUI(t *testing.T, tt *ui.Tester, tab *Tab) {
	t.Helper()
	pumpToPage0(t, tt)
	if err := tt.Click("Square"); err != nil {
		t.Fatalf("square button: %v", err)
	}
	tt.Press(600, 400)
	tt.Move(700, 500)
	tt.Release(700, 500)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !tab.Dirty {
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}
	if !tab.Dirty {
		t.Fatal("the mutation never marked the tab dirty")
	}
}

// reopenAnnots opens path independently and returns its page-0 annots.
func reopenAnnots(t *testing.T, path string) []pdf.PageAnnot {
	t.Helper()
	d, err := pdf.Open(path)
	if err != nil {
		t.Fatalf("reopen %s: %v", path, err)
	}
	t.Cleanup(d.Close)
	ch := make(chan annotsListReply, 1)
	d.AnnotsAsync(0, func(as []pdf.PageAnnot, err error) { ch <- annotsListReply{as, err} })
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("reopen annots: %v", r.err)
		}
		return r.annots
	case <-time.After(5 * time.Second):
		t.Fatal("reopen annots timed out")
		return nil
	}
}

// TestSaveSamePathClearsDirty: a UI-created square marks the tab dirty,
// the toolbar's Save writes it, the bullet goes, and the file on disk
// carries the annot.
func TestSaveSamePathClearsDirty(t *testing.T) {
	s := reproState(t, 2)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	createSquareViaUI(t, tt, tab)

	if lbl := tabLabel(tab); !strings.HasSuffix(lbl, " •") {
		t.Fatalf("dirty label %q lacks the bullet", lbl)
	}
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("save button: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && tab.Dirty {
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}
	if tab.Dirty {
		t.Fatal("a successful save did not clear the dirty flag")
	}
	if lbl := tabLabel(tab); strings.Contains(lbl, "•") {
		t.Fatalf("clean label %q keeps the bullet", lbl)
	}
	if as := reopenAnnots(t, tab.Path); len(as) != 1 {
		t.Fatalf("saved file annots: %d, want 1", len(as))
	}
}

// TestSaveTabLabelBullet: the bullet is a pure function of Dirty; plain
// reads never show it.
func TestSaveTabLabelBullet(t *testing.T) {
	s := reproState(t, 1)
	tab := s.Active()
	if got := tabLabel(tab); got != tab.Title {
		t.Fatalf("clean label %q, want the bare title %q", got, tab.Title)
	}
	tab.Dirty = true
	if got := tabLabel(tab); got != tab.Title+" •" {
		t.Fatalf("dirty label %q", got)
	}
}

// TestSaveAsLeavesOriginalUntouched: saving to a foreign path persists
// the edits there while the original file keeps its original bytes (no
// annots), and the notice names the new file.
func TestSaveAsLeavesOriginalUntouched(t *testing.T) {
	s := reproState(t, 2)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	createSquareViaUI(t, tt, tab)

	other := filepath.Join(t.TempDir(), "copy.pdf")
	s.saveTabAs(tab, other)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && tab.Dirty {
		tt.Frame()
		time.Sleep(20 * time.Millisecond)
	}
	if tab.Dirty {
		t.Fatal("save-as did not clear the dirty flag")
	}
	if !strings.Contains(s.StatusNotice, "copy.pdf") {
		t.Fatalf("notice %q does not name the new file", s.StatusNotice)
	}
	if as := reopenAnnots(t, other); len(as) != 1 {
		t.Fatalf("copy annots: %d, want 1", len(as))
	}
	if as := reopenAnnots(t, tab.Path); len(as) != 0 {
		t.Fatalf("original file gained annots through save-as: %+v", as)
	}
}

// TestCloseDirtyTabNotices: closing a tab with unsaved edits closes
// without saving and says so in the status bar.
func TestCloseDirtyTabNotices(t *testing.T) {
	s := reproState(t, 1)
	tt := ui.NewTester(s.View, 1200, 800)
	tab := s.Active()
	createSquareViaUI(t, tt, tab)

	s.CloseTab(tab.ID)
	if !strings.Contains(s.StatusNotice, "closed with unsaved edits") {
		t.Fatalf("notice %q does not report the unsaved close", s.StatusNotice)
	}
	// The file on disk never saw the edit.
	if as := reopenAnnots(t, tab.Path); len(as) != 0 {
		t.Fatalf("closing wrote annots to disk: %+v", as)
	}
}
