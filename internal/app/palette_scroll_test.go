// palette_scroll_test.go is the red/green loop for the palette results
// list not scrolling: with more rows than fit, a wheel over the list must
// bring later rows into view (virtualization builds only visible rows, so
// which labels exist in the tree is the observable scroll offset).
package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestPaletteWheelScrolls wheels the results list and expects rows beyond
// the first screen of results to appear.
func TestPaletteWheelScrolls(t *testing.T) {
	s := NewAppState()
	for i := 1; i <= 24; i++ {
		s.Recents = append(s.Recents, fmt.Sprintf(`C:\tmp\recent-%02d.pdf`, i))
	}
	tt := ui.NewTester(s.View, 1200, 800)
	tt.Frame()

	tt.Key(ui.Ctrl, ui.KeyK)
	tt.Frame()
	if !s.ShowPalette {
		t.Fatal("Ctrl+K did not open the palette")
	}

	// Rows are ~44 DIPs tall in a ~372 DIP list: roughly the first 8
	// results fit. The last seeded recent must NOT be built yet.
	if tt.HasText("recent-24.pdf") {
		t.Log("all rows already built — list is not virtualizing (or is tiny)")
	}
	if !tt.HasText("recent-01.pdf") {
		t.Fatalf("first recent not shown before scrolling: %q", tt.Texts())
	}

	// Wheel down hard over the middle of the palette list. Wheel scrolls
	// are clock-driven glides: Advance moves them (as viewer_test does).
	for i := 0; i < 12; i++ {
		tt.Wheel(600, 430, 0, 120)
		tt.Advance(100 * time.Millisecond)
		tt.Frame()
	}
	tt.Advance(time.Second)
	tt.Frame()

	if !tt.HasText("recent-24.pdf") {
		t.Fatalf("wheel did not scroll the results: last rows never built; texts=%q", tt.Texts())
	}
	// Negative check must use a palette-only string: the sidebar's recents
	// list behind the modal also contains "recent-01.pdf", but only the
	// palette shows the full detail path.
	if tt.HasText(`C:\tmp\recent-01.pdf`) {
		t.Fatalf("wheel did not scroll: first row still in view; texts=%q", tt.Texts())
	}
}
