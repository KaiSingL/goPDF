// present.go is presentation mode (issue 4): the active tab's document
// fills the window one page at a time, on a dark surround, driven by the
// keys and buttons a presenter holds. Entering (Ctrl+L, View ▸
// Presentation, the palette's Presentation command) swaps the whole
// window for the slide view; Esc or Ctrl+L again brings the reader back
// with its zoom and scroll exactly as they were.
//
// # Design
//
//   - Presentation is a view mode, not a parallel document state: it
//     renders Tab.CurPage through the reader's own pipeline
//     (pageBitmap: RenderAsync, the tab's PageCache, the shared bitmap
//     cache) at a page-fit zoom computed from the window size, and it
//     never writes the reader's zoom or scroll. The exit restore is
//     exact because there is nothing to undo; Tab.presentRestore puts
//     back what was saved at entry regardless, as a contract.
//   - Entering makes the window full screen when it can: mygo v0.6.4's
//     Window.SetFullScreen saves the window placement itself and
//     restores it when leaving (verified in the pinned source), so
//     AppState.PresentFullscreen records only that presentation was
//     the one who changed it, and exit leaves full screen again. A
//     window already full screen at entry, or no window at all
//     (headless tests), stays untouched.
//   - The reader's chrome, overlays and shortcuts do not build while
//     presenting, so nothing behind the slides takes a key or a click:
//     the annotation overlays (annotate.go) hide with the rest of the
//     chrome, no extra state needed. Any path that changes the active
//     tab (opening, selecting, closing the presenting tab) ends
//     presentation first, because the overlays those paths show need
//     the reader's chrome back.
//   - Slide navigation is silent (Tab.SetCurrentPage, no history): a
//     deck must not flood the reader's back stack, and on exit the
//     viewer follows CurPage to the last shown slide by itself.
package app

import (
	"strconv"

	"github.com/egoist/mygo/ui"
)

// The presentation surround: a fixed near-black whatever the app theme
// is (the point of the mode is dim chrome around a bright slide), and
// the muted tone of the placeholder and the typed-number HUD.
var (
	presentBg    = ui.RGBA(0x0a, 0x0a, 0x0c, 1)
	presentMuted = ui.RGBA(0xC8, 0xC8, 0xCE, 0.80)
)

// presentHud is the window-height slice reserved under the slide. It is
// part of the dark surround (never visible as such) and holds the
// typed-number readout; reserving it always keeps the slide's fit from
// shifting the moment digits are typed.
const presentHud float32 = 34

// maxPresentDigits caps the typed slide number; the row is enough for
// any document the reader opens.
const maxPresentDigits = 4

// presentDigits is the slide number being typed (digits then Enter).
// Package state, reset whenever presentation opens, turns or closes.
var presentDigits string

// presentRestore is the reader view of a tab as it was when that tab
// entered presentation, put back on exit.
type presentRestore struct {
	zoomMode ZoomMode
	zoom     float64
	scrollY  float32
}

// presentingTab returns the active tab when it is presenting, else nil.
// Only the active tab can carry the flag: entry requires it and every
// active-tab change ends presentation first.
func (s *AppState) presentingTab() *Tab {
	if t := s.Active(); t != nil && t.Presenting {
		return t
	}
	return nil
}

// StartPresenting enters presentation mode on the active tab. Nothing
// to show (no tab, a launcher tab, an empty document) is a no-op, as is
// entering twice. Call from the UI thread.
func (s *AppState) StartPresenting() {
	t := s.Active()
	if t == nil || t.Doc == nil || t.NumPages <= 0 || t.Presenting {
		return
	}
	// Overlays the reader builds must not linger under presentation
	// (presentView builds none of them): clear the flags now or they
	// would all resurface the moment presentation ends.
	s.ShowPalette = false
	s.ShowSearchPanel = false
	s.ShowGoTo = false
	t.Presenting = true
	t.presentRestore = &presentRestore{
		zoomMode: t.ZoomMode,
		zoom:     t.Zoom,
		scrollY:  viewerScroll.Y,
	}
	presentDigits = ""
	// Full screen, when the window exists and is not already in it.
	// SetFullScreen stores the placement itself; PresentFullscreen
	// remembers only that we are the one to undo it.
	if window != nil && !window.IsFullScreen() {
		window.SetFullScreen(true)
		s.PresentFullscreen = true
	}
}

// StopPresenting leaves presentation mode on the active tab; a no-op
// when it was not presenting. Call from the UI thread.
func (s *AppState) StopPresenting() {
	if s.presentingTab() == nil {
		return
	}
	s.endPresentation()
}

// TogglePresenting enters or leaves presentation mode (the Ctrl+L
// binding, the View menu and the palette all route through here).
func (s *AppState) TogglePresenting() {
	if s.presentingTab() != nil {
		s.StopPresenting()
		return
	}
	s.StartPresenting()
}

// endPresentation is the one exit path: it clears the flag on whichever
// tab carries it, restores that tab's saved reader view, and returns
// the window from full screen if presentation sent it there. Call from
// the UI thread.
func (s *AppState) endPresentation() {
	presentDigits = ""
	for _, t := range s.Tabs {
		if !t.Presenting {
			continue
		}
		t.Presenting = false
		if r := t.presentRestore; r != nil {
			t.presentRestore = nil
			t.ZoomMode = r.zoomMode
			t.Zoom = clampZoom(r.zoom)
			// A direct offset write: the scroll container applies (and
			// clamps) it when the reader builds again. When the slide
			// moved while presenting, the viewer's own CurPage jump
			// overrides this, landing on the new page's top.
			viewerScroll.Y = max(float32(0), r.scrollY)
		}
	}
	if s.PresentFullscreen {
		s.PresentFullscreen = false
		if window != nil {
			window.SetFullScreen(false)
		}
	}
}

// presentGoto shows slide n (0-based, clamped) and forgets any typed
// number. Silent on purpose: no history entries from a deck.
func presentGoto(t *Tab, n int) {
	presentDigits = ""
	t.SetCurrentPage(n)
}

// presentTurn moves delta slides forward (positive) or back.
func presentTurn(t *Tab, delta int) {
	presentGoto(t, t.CurPage+delta)
}

// presentFit computes the slide zoom: page-fit (whichever of width and
// height fit keeps the WHOLE page visible) against the window content
// minus the HUD slice, within the app's zoom limits.
func presentFit(t *Tab, viewW, viewH float64) float64 {
	pw, ph := t.PageSizePt(t.CurPage)
	if pw <= 0 || ph <= 0 || viewW <= 0 || viewH <= 0 {
		return clampZoom(t.Zoom)
	}
	return clampZoom(min(viewW/(pw*dipPerPt), viewH/(ph*dipPerPt)))
}

// presentView builds the whole window while the active tab presents:
// the clicker's shortcuts first (window scope, as the reader's root
// view declares its own), then the slide itself.
func presentView(c *ui.Context, s *AppState) {
	t := s.presentingTab()
	if t == nil {
		return
	}
	if t.Doc == nil || t.NumPages <= 0 { // defensive; entry guards prevent it
		s.endPresentation()
		return
	}
	presentShortcuts(c, s)

	// The render pipeline's staleness mirror, exactly as the viewer keeps
	// it each build (viewer.go): a Reload while presenting must not
	// strand the slide's renders as forever-stale. (Kept identical to the
	// viewer's code on purpose; presentation is a view mode.)
	st := viewerStateFor(t.ID)
	if st.gen != t.ReloadGen {
		st.gen = t.ReloadGen
		viewerMu.Lock()
		viewerGen[t.ID] = t.ReloadGen
		bmpClearTab(t.ID)
		inflightClearTab(t.ID)
		viewerMu.Unlock()
	}

	// Fit the slide to the window; re-render on resize rides the same
	// numbers: a new size is a new zoom, and pageBitmap requests the
	// matching DPI through the reader's own machinery.
	vw, vh := c.Size()
	z := presentFit(t, float64(vw), float64(vh)-float64(presentHud))
	pw, ph := t.PageSizePt(t.CurPage)
	w, h := float32(pw*dipPerPt*z), float32(ph*dipPerPt*z)
	bm := pageBitmap(t, t.CurPage, z, z)

	c.Key("gopdf-present")
	e := ui.Box(c).Fill().Background(presentBg).
		Draw(func(p *ui.Painter, _ ui.Rect) { deviceScale = p.Scale() }).
		HandleInput(func(ev ui.InputEvent) bool { return presentInput(s, vw, ev) })
	e.Children(func() {
		ui.Column(c).Fill().Children(func() {
			ui.Box(c).Grow(1) // above the slide
			ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
				if bm != nil {
					ui.Image(c, bm).Size(w, h).Fit(ui.FillBox)
					return
				}
				ui.Textf(c, "Page %d", t.CurPage+1).TextColor(presentMuted)
			})
			// The HUD slice: the typed slide number, right-aligned,
			// same dark as the surround when nothing is typed.
			ui.Row(c).FillWidth().Height(presentHud).Justify(ui.End).
				PaddingX(16).Children(func() {
				if presentDigits != "" {
					ui.Textf(c, "Go to page %s", presentDigits).
						FontSize(13).TextColor(presentMuted)
				}
			})
		})
	})
}

// presentShortcuts declares the presentation keyboard map. Handlers
// re-resolve the presenting tab at run time, as the reader's shortcuts
// re-resolve the active tab.
func presentShortcuts(c *ui.Context, s *AppState) {
	turn := func(delta int) func() {
		return func() {
			if t := s.presentingTab(); t != nil {
				presentTurn(t, delta)
			}
		}
	}

	// Exit: Esc, or the chord that entered.
	c.OnShortcut(0, ui.KeyEscape, func() { s.StopPresenting() })
	c.OnShortcut(ui.Ctrl, ui.KeyL, func() { s.StopPresenting() })

	// Forward: Right, PageDown, Space, Enter (Enter with a typed number
	// jumps instead, below). Back: Left, PageUp, Shift+Space,
	// Backspace.
	c.OnShortcut(0, ui.KeyRight, turn(+1))
	c.OnShortcut(0, ui.KeyPageDown, turn(+1))
	c.OnShortcut(0, ui.KeySpace, turn(+1))
	c.OnShortcut(ui.Shift, ui.KeySpace, turn(-1))
	c.OnShortcut(0, ui.KeyLeft, turn(-1))
	c.OnShortcut(0, ui.KeyPageUp, turn(-1))
	c.OnShortcut(0, ui.KeyBackspace, turn(-1))

	// First and last slide.
	c.OnShortcut(0, ui.KeyHome, func() {
		if t := s.presentingTab(); t != nil {
			presentGoto(t, 0)
		}
	})
	c.OnShortcut(0, ui.KeyEnd, func() {
		if t := s.presentingTab(); t != nil {
			presentGoto(t, t.NumPages-1)
		}
	})

	// Slide numbers: digits accumulate, then Enter jumps (a number out
	// of range clamps; an empty or zero buffer falls through to a plain
	// page turn). The number row only: mygo exposes no numpad keys.
	digit := func(d string) func() {
		return func() {
			if s.presentingTab() != nil && len(presentDigits) < maxPresentDigits {
				presentDigits += d
			}
		}
	}
	c.OnShortcut(0, ui.Key0, digit("0"))
	c.OnShortcut(0, ui.Key1, digit("1"))
	c.OnShortcut(0, ui.Key2, digit("2"))
	c.OnShortcut(0, ui.Key3, digit("3"))
	c.OnShortcut(0, ui.Key4, digit("4"))
	c.OnShortcut(0, ui.Key5, digit("5"))
	c.OnShortcut(0, ui.Key6, digit("6"))
	c.OnShortcut(0, ui.Key7, digit("7"))
	c.OnShortcut(0, ui.Key8, digit("8"))
	c.OnShortcut(0, ui.Key9, digit("9"))
	c.OnShortcut(0, ui.KeyEnter, func() {
		t := s.presentingTab()
		if t == nil {
			return
		}
		if n, err := strconv.Atoi(presentDigits); err == nil && n >= 1 {
			presentGoto(t, n-1)
			return
		}
		presentTurn(t, +1) // no valid number buffered: Enter advances
	})
}

// presentInput takes the pointer for the slides: a plain click on the
// right half advances and on the left half goes back (presenter style),
// a plain wheel notch turns with its direction. Zoom input (Ctrl+wheel,
// pinches) is consumed and ignored: slides do not zoom, and the reader
// behind them must not either.
func presentInput(s *AppState, viewW float32, ev ui.InputEvent) bool {
	t := s.presentingTab()
	if t == nil {
		return false
	}
	switch {
	case ev.Kind == ui.InputPointerDown && ev.Button == 0 && ev.Mods == 0:
		if ev.X*2 >= viewW {
			presentTurn(t, +1)
		} else {
			presentTurn(t, -1)
		}
		return true
	case ev.Kind == ui.InputScroll && ev.Mods&ui.Ctrl == 0:
		if ev.DY > 0 {
			presentTurn(t, +1)
		} else if ev.DY < 0 {
			presentTurn(t, -1)
		}
		return true
	case ev.Kind == ui.InputScroll || ev.Kind == ui.InputZoom:
		return true // zoom passthrough, excluded on purpose
	}
	return false
}
