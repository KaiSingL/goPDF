// fixes_round5_test.go: the launcher/initial page shows recent documents
// with thumbnails, and the sidebar stays hidden for launcher tabs.
package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestLauncherRecentsGallery: recents render as cards on the initial page
// and launcher tabs; the first-page thumbnail arrives asynchronously.
func TestLauncherRecentsGallery(t *testing.T) {
	s := NewAppState()
	SetState(s) // OpenPath (the card's action) reaches state through it
	path := genTestPDF(t, 2)
	s.addRecent(path)
	tt := ui.NewTester(s.View, 1200, 800)
	tt.Frame()

	if !tt.HasText("Recent documents") {
		t.Fatalf("initial page has no recents section: %q", tt.Texts())
	}

	// The card's thumbnail renders through a transient doc; give it a beat.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tt.Frame()
		recentMu.Lock()
		bm, done := recentThumbs[path]
		recentMu.Unlock()
		if done && bm != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	recentMu.Lock()
	bm, done := recentThumbs[path]
	recentMu.Unlock()
	if !done || bm == nil {
		t.Fatal("recent thumbnail never rendered")
	}

	// Launcher tabs show the gallery too, and no sidebar.
	if err := tt.Click("New tab"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Recent documents") || !tt.HasText("Drop a PDF anywhere") {
		t.Fatalf("launcher tab missing recents/drop hint: %q", tt.Texts())
	}
	if tt.HasText("Thumbnails") {
		t.Fatalf("sidebar visible on a launcher tab: %q", tt.Texts())
	}

	// Clicking the card opens the document.
	if err := tt.Click("Open " + filepath.Base(path)); err != nil {
		t.Fatalf("click recent card: %v", err)
	}
	tt.Frame()
	if a := s.Active(); a == nil || a.IsEmpty() {
		t.Fatalf("recent card did not open the document (tabs=%d)", len(s.Tabs))
	}
	t.Cleanup(func() {
		for _, tab := range s.Tabs {
			if tab.Doc != nil {
				tab.Doc.Close()
			}
		}
	})
}
